package dependencies

import (
	"os"

	"github.com/pkg/errors"
	"golang.org/x/mod/modfile"
	"golang.org/x/mod/module"
)

type PackageDependencies interface {
	Replacements() []Dependency
	GetDep(packagePath string) Dependency
}

type Dependency interface {
	// TODO(ashmrtn): Add another function that points to the line the replacement
	// was done at.
	OriginalVersion() module.Version
	EffectiveVersion() module.Version
	Location() LocationTree
}

type dependency struct {
	originalVersion  module.Version
	effectiveVersion module.Version

	location *dependencyLocationTree

	globalReplace bool
}

func (d dependency) OriginalVersion() module.Version {
	return d.originalVersion
}

func (d dependency) EffectiveVersion() module.Version {
	return d.effectiveVersion
}

func (d dependency) Location() LocationTree {
	if d.location == nil {
		return nil
	}

	return d.location
}

func (d *dependency) maybeUpdate(rep *modfile.Replace) (bool, error) {
	// Handle targetted replace directives. Either:
	//   * The replace directive isn't targetting this version so there's nothing
	//     to do
	//   * This dep has already been updated by a targetted replace directive and
	//     we should return an error
	//   * This dep can be updated, even if it was already updated by a global
	//     replace directive.
	if len(rep.Old.Version) > 0 {
		// Replace statment for a different module version, nothing to do.
		if d.OriginalVersion().Version != rep.Old.Version {
			return false, nil
		}

		if d.OriginalVersion() != d.EffectiveVersion() && !d.globalReplace {
			return false, errors.Errorf(
				"multiple version-specific replace directives for module %s",
				d.OriginalVersion().Path,
			)
		}

		d.effectiveVersion = rep.New
		d.location.replace.Row = rep.Syntax.Start.Line
		d.location.replace.Col = rep.Syntax.Start.LineRune
		d.globalReplace = false

		return true, nil
	}

	// Remainder of function deals with untargetted replace directives.
	if d.EffectiveVersion().Version != d.OriginalVersion().Version {
		if d.globalReplace {
			return false, errors.Errorf(
				"multiple non-version-specific replace directives for module %s",
				d.OriginalVersion().Path,
			)
		}

		// The module's already had it's version updated by a targetted replace
		// directive.
		return false, nil
	}

	d.effectiveVersion = rep.New
	d.location.replace.Row = rep.Syntax.Start.Line
	d.location.replace.Col = rep.Syntax.Start.LineRune
	d.globalReplace = true

	return true, nil
}

func readModFile(path string) (*modfile.File, error) {
	mod, err := os.ReadFile(path)
	if err != nil {
		return nil, errors.Wrap(err, "reading mod file")
	}

	f, err := modfile.Parse(path, mod, nil)
	if err != nil {
		return nil, errors.Wrap(err, "parsing mod file")
	}

	return f, nil
}

func NewProjectDependenciesFromModfile(
	parentModDecl Dependency,
	modFilePath string,
) (PackageDependencies, error) {
	modFile, err := readModFile(modFilePath)
	if err != nil {
		return nil, errors.WithStack(err)
	}

	res := &projectDependencies{
		allDependencies:    map[string]*dependency{},
		directDependencies: map[string]*dependency{},
		replacements:       map[string]*dependency{},
	}

	for _, req := range modFile.Require {
		if _, ok := res.allDependencies[req.Mod.Path]; ok {
			return nil, errors.Errorf("duplicate dependency %s", req.Mod.Path)
		}

		loc := &dependencyLocationTree{
			parentModVersion: modFile.Module.Mod.String(),
			original: FileLocation{
				Row: req.Syntax.Start.Line,
				Col: req.Syntax.Start.LineRune,
			},
		}

		if parentModDecl != nil {
			loc.ancestor = parentModDecl.Location()
		}

		dep := &dependency{
			originalVersion:  req.Mod,
			effectiveVersion: req.Mod,
			location:         loc,
		}

		res.allDependencies[req.Mod.Path] = dep

		if !req.Indirect {
			res.directDependencies[req.Mod.Path] = dep
		}
	}

	for _, rep := range modFile.Replace {
		if err := res.updateEffectiveVersion(rep); err != nil {
			return nil, errors.WithStack(err)
		}
	}

	return res, nil
}

type projectDependencies struct {
	// replacements contains package path -> dep info for all dependency that have
	// been updated by replace directives.
	replacements map[string]*dependency

	// directDependencies contains the package path -> dep info for direct
	// dependencies in this package.
	directDependencies map[string]*dependency

	// allDependencies contains the package path -> dep info for every dependency
	// in this package.
	allDependencies map[string]*dependency
}

func (p projectDependencies) GetDep(packagePath string) Dependency {
	// Use an if-block so it doesn't return a nil instance of the concrete type as
	// a non-nil interface result.
	if dep, ok := p.allDependencies[packagePath]; ok {
		return dep
	}

	return nil
}

func (p projectDependencies) Replacements() []Dependency {
	res := make([]Dependency, 0, len(p.replacements))

	for _, rep := range p.replacements {
		res = append(res, rep)
	}

	return res
}

func (p *projectDependencies) updateEffectiveVersion(
	rep *modfile.Replace,
) error {
	repPath := rep.Old.Path

	dep, ok := p.allDependencies[repPath]
	if !ok {
		// We don't have this dependency at all so there's nothing to update.
		return nil
	}

	if updated, err := dep.maybeUpdate(rep); err != nil {
		return errors.WithStack(err)
	} else if updated {
		p.replacements[repPath] = dep
	}

	return nil
}
