package cmd

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/pkg/errors"
	"github.com/spf13/cobra"
	"golang.org/x/exp/slices"
	"golang.org/x/tools/go/packages"

	"github.com/ashmrtn/gomodcheck/pkg/dependencies"
)

type modCheckCommand struct {
	// chekcReplacePackages contains the set of package paths to parse for replace
	// statements. The replaces deps in those statements are then compared to the
	// deps in the package this command is run on.
	checkReplacePackages []string

	// rawMatchDeps contains the unparsed set of <package path>:<dep path> to
	// parse. The dep version defined in each of these must match the deps of the
	// same name in the package this command is run on.
	rawMatchDeps []string

	// parsedMatchDeps is populated from the info in rawMatchDeps. It goes from
	// <package path> -> set <dep path> where the package path is the path that
	// will appear in this package's mod file and the dep path is the package path
	// of the dependency the matching should be done on.
	parsedMatchDeps map[string]map[string]struct{}

	// projectDeps contains all dependency sets loaded from gomodfiles in this
	// project.
	projectDeps []dependencies.PackageDependencies

	// depDeps contains package path -> dependency sets. The package path is used
	// instead of the gomodfile path so that we can determine which dependencies
	// to check.
	depDeps map[string]dependencies.PackageDependencies

	// allLoadedDeps contains the dependency sets that has been created reading
	// modfiles. It maps from the path of the gomodfile to the dependencies read
	// from the gomodfile.
	allLoadedDeps map[string]dependencies.PackageDependencies
}

func (c *modCheckCommand) parseAndVerifyMatchDeps() error {
	// Split the input into two parts, the package to check the dep version in and
	// the dep name that we're checking the version of.
	for _, input := range c.rawMatchDeps {
		parts := strings.Split(input, ":")

		switch {
		case len(parts) != 2:
			return errors.Errorf("unexpected dep match input: %s", input)

		case len(parts[0]) == 0, len(parts[1]) == 0:
			return errors.Errorf(
				"empty package path in dep match input: %s",
				input,
			)
		}

		if c.parsedMatchDeps[parts[0]] == nil {
			c.parsedMatchDeps[parts[0]] = map[string]struct{}{}
		}

		c.parsedMatchDeps[parts[0]][parts[1]] = struct{}{}
	}

	// Make sure that each dep we're checking the version of only appears once.
	validateTmp := make(map[string]string, len(c.parsedMatchDeps))

	for packageName, depSet := range c.parsedMatchDeps {
		for dep := range depSet {
			// We've already been asked to check the version of this dep by sourcing
			// the version from a different package. Return an error.
			if otherPackageName, ok := validateTmp[dep]; ok {
				return errors.Errorf(
					"dep %s being sourced from multiple packages: %s and %s",
					dep,
					otherPackageName,
					packageName,
				)
			}

			validateTmp[dep] = packageName
		}
	}

	return nil
}

func (c *modCheckCommand) getOrLoadPackageDeps(
	pkg *packages.Package,
	dep dependencies.Dependency,
) (dependencies.PackageDependencies, bool, error) {
	if pkg.Module == nil {
		return nil, false, nil
	}

	modFilePath := pkg.Module.GoMod

	if pkg.Module.Replace != nil {
		modFilePath = pkg.Module.Replace.GoMod
	}

	// No gomodfile specified, check the next package.
	if len(modFilePath) == 0 {
		return nil, false, nil
	}

	// We've already loaded info for this particular gomodfile. No need to load
	// it again so continue on.
	if deps := c.allLoadedDeps[modFilePath]; deps != nil {
		return deps, false, nil
	}

	// We actually need to go load data.
	deps, err := dependencies.NewProjectDependenciesFromModfile(dep, modFilePath)
	if err != nil {
		return nil, false, errors.Wrapf(
			err,
			"loading dependency info for: %s",
			modFilePath,
		)
	}

	c.allLoadedDeps[modFilePath] = deps

	return deps, true, nil
}

func (c *modCheckCommand) readDepMappings(
	ctx context.Context,
	packagePath string,
) error {
	cfg := &packages.Config{
		Context: ctx,
		Mode:    packages.NeedName | packages.NeedImports | packages.NeedModule,
	}

	pkgs, err := packages.Load(cfg, packagePath)
	if err != nil {
		return errors.Wrap(err, "getting packages")
	}

	for _, pkg := range pkgs {
		pkgDepSet, freshLoad, err := c.getOrLoadPackageDeps(pkg, nil)
		if err != nil {
			return errors.Wrap(err, "loading project deps")
		} else if freshLoad {
			c.projectDeps = append(c.projectDeps, pkgDepSet)
		}

		// Go through the imports in this package. If any of them are in the list of
		// packages that we're going to compare against load them as well.
		for _, importPkg := range pkg.Imports {
			var importPkgPath string

			if importPkg.Module != nil {
				importPkgPath = importPkg.Module.Path
			}

			// If the package backing this import isn't one of the ones we're going to
			// check against then don't bother loading it.
			if _, ok := c.parsedMatchDeps[importPkgPath]; !ok &&
				!slices.Contains(c.checkReplacePackages, importPkgPath) {
				continue
			}

			// Pull the dep info on this package that we've already parsed from the
			// current gomodfile. This allows us to build a full lineage of file
			// locations.
			importDep := pkgDepSet.GetDep(importPkgPath)

			if deps, freshLoad, err := c.getOrLoadPackageDeps(
				importPkg,
				importDep,
			); err != nil {
				return errors.Wrapf(
					err,
					"loading deps for dependency %s",
					importPkgPath,
				)
			} else if freshLoad {
				c.depDeps[importPkgPath] = deps
			}
		}
	}

	return nil
}

type depError struct {
	wantVersion string
	gotVersion  string

	gotLoc  dependencies.LocationTree
	wantLoc dependencies.LocationTree
}

func (c modCheckCommand) findDepErrors() []depError {
	var (
		res []depError

		// Maps from package path -> dependencies.Dependency that needs to be
		// compared to the dependencies.Dependency in the main project.
		depsToCheck = map[string]dependencies.Dependency{}
	)

	for depPackage, matchDepSet := range c.parsedMatchDeps {
		depSet := c.depDeps[depPackage]
		if depSet == nil {
			// There either wasn't a gomodfile for this dep or the dep wasn't used by
			// an import of the main project.
			continue
		}

		for depPath := range matchDepSet {
			if dep := depSet.GetDep(depPath); dep != nil {
				depsToCheck[depPath] = dep
			}
		}
	}

	for _, depPackage := range c.checkReplacePackages {
		depSet := c.depDeps[depPackage]
		if depSet == nil {
			continue
		}

		for _, dep := range depSet.Replacements() {
			// TODO(ashmrtn): Make sure some other package doesn't also require this
			// dep be checked. We need to check this because we don't know upfront
			// what replace directives deps will have.
			depsToCheck[dep.OriginalVersion().Path] = dep
		}
	}

	for _, checkDep := range depsToCheck {
		for _, projectDepSet := range c.projectDeps {
			projectDep := projectDepSet.GetDep(checkDep.OriginalVersion().Path)
			if projectDep == nil {
				continue
			}

			wantVersion := checkDep.EffectiveVersion().String()
			gotVersion := projectDep.EffectiveVersion().String()

			if wantVersion != gotVersion {
				res = append(
					res,
					depError{
						wantVersion: wantVersion,
						gotVersion:  gotVersion,
						gotLoc:      projectDep.Location(),
						wantLoc:     checkDep.Location(),
					},
				)
			}
		}
	}

	return res
}

func ancestryToString(loc dependencies.LocationTree) string {
	var res string

	for loc != nil {
		res += fmt.Sprintf(
			"\t\toriginally included in modfile for module %s line %d, col %d",
			loc.ParentPackage(),
			loc.OriginalLocation().Row,
			loc.OriginalLocation().Col,
		)

		if loc.EffectiveLocation() != loc.OriginalLocation() {
			res += fmt.Sprintf(
				"\n\t\t\treplaced at line %d, col %d",
				loc.EffectiveLocation().Row,
				loc.EffectiveLocation().Col,
			)
		}

		res += "\n"

		loc = loc.Ancestor()
	}

	return res
}

func printFormattedErr(depErr depError) {
	msg := fmt.Sprintf(
		"Module mismatch: in modfile for module %s line %d, col %d: "+
			"have version %s but want version %s\n",
		depErr.gotLoc.ParentPackage(),
		depErr.gotLoc.EffectiveLocation().Row,
		depErr.gotLoc.EffectiveLocation().Col,
		depErr.gotVersion,
		depErr.wantVersion,
	)

	msg += "\tgot version:\n" + ancestryToString(depErr.gotLoc)
	msg += "\twant version:\n" + ancestryToString(depErr.wantLoc)

	fmt.Fprint(os.Stderr, msg)
}

func (c *modCheckCommand) run(ctx context.Context, packagePath string) error {
	if err := c.readDepMappings(ctx, packagePath); err != nil {
		return errors.Wrap(err, "reading dependency mappings")
	}

	depErrs := c.findDepErrors()

	for _, depErr := range depErrs {
		printFormattedErr(depErr)
	}

	if len(depErrs) > 0 {
		return errors.New("found dependency mismatches")
	}

	return nil
}

const (
	matchReplaceVarName = "match-replaces"
	matchDepVarName     = "match-dep"
)

func newModCheckCommand() *cobra.Command {
	// Create the struct that's going to do everything so we can use it's
	// variables as the location to place flag values.
	runCommand := &modCheckCommand{
		parsedMatchDeps: map[string]map[string]struct{}{},
		depDeps:         map[string]dependencies.PackageDependencies{},
		allLoadedDeps:   map[string]dependencies.PackageDependencies{},
	}

	// Setup cobra command struct.
	res := &cobra.Command{
		Use: "gomodcheck",
		Short: "gomodcheck is a CLI tool to help ensure package module versions " +
			"remain consistent across a project and its dependencies.",
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()

			if len(args) != 1 {
				return errors.Errorf("invalid required package specifier: %s", args)
			}

			if err := runCommand.parseAndVerifyMatchDeps(); err != nil {
				return errors.Wrap(err, "parsing flags")
			}

			// Don't print usage info after this point since flags have been verified.
			cmd.SilenceUsage = true

			return runCommand.run(ctx, args[0])
		},
	}

	// Add flags to the cobra command.
	flags := res.Flags()
	flags.StringSliceVar(
		&runCommand.checkReplacePackages,
		matchReplaceVarName,
		nil,
		"",
	)
	flags.StringSliceVar(
		&runCommand.rawMatchDeps,
		matchDepVarName,
		nil,
		"",
	)

	return res
}

func Execute() error {
	cmd := newModCheckCommand()
	return cmd.Execute()
}
