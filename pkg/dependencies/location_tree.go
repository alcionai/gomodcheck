package dependencies

type LocationTree interface {
	ParentPackage() string
	OriginalLocation() FileLocation
	EffectiveLocation() FileLocation

	Ancestor() LocationTree
}

type FileLocation struct {
	Row int
	Col int
}

type dependencyLocationTree struct {
	// parentModVersion is the string representation of the parent module,
	// including the package path and version number.
	parentModVersion string

	// original holds the line number and column inthe line in the parent
	// gomodfile this dependency was originally added at.
	original FileLocation

	// replace holds the line number and column inthe line in the parent
	// gomodfile this dependency was was replaced at.
	replace FileLocation

	// ancestor denotes a previous file location that may help add more context.
	// For example, if a replace directive is included because of another replace
	// directive this can help track it down by showing the full lineage of
	// replace directives.
	ancestor LocationTree
}

func (d dependencyLocationTree) ParentPackage() string {
	return d.parentModVersion
}

func (d dependencyLocationTree) OriginalLocation() FileLocation {
	return d.original
}

func (d dependencyLocationTree) EffectiveLocation() FileLocation {
	if d.replace.Row != 0 {
		return d.replace
	}

	return d.OriginalLocation()
}

func (d dependencyLocationTree) Ancestor() LocationTree {
	return d.ancestor
}
