# gomodcheck

gomodcheck is a CLI linter that helps ensure module versions between a project
and its dependencies stay in sync.

## Why should I use gomodcheck?

Sometimes projects have dependencies that need to be kept at certain versions
because they're also used as a dependency of another dependency in the project.
Although golang's module ecosystem and tools like `go mod` usually make managing
deps easy, but there are times where it can become error prone to determine if a
module is at the correct version or not. Two examples of this are modules that a
dependency replaces and modules that are used directly in both a dependency and
the project consuming the dependency.

In the former case, golang makes no attempt to process honor dependency replace
directives in the project that consumes said dependency. This can cause
unexpected build failures or unexpected behavior if not carefully managed.

In the second case, it can be difficult to determine when it's alright to
upgrade a dependency without manually checking the version in both projects.
This problem may be worsen by tools like dependabot as they'll try to
automatically update direct dependencies for a project.

gomodcheck is meant to automate comparing module versions between projects by
providing users with a CLI tool that understands modfiles and can find
dependencies and their versions. By reading modfiles for the project it's run on
and the dependencies of the project gomodcheck can quickly find differences and
report them to developers.

## Installing

gomodcheck is provided as a go module and can be installed by running
`go install github.com/ashmrtn/gomodcheck@latest`

## Running

gomodcheck is a CLI tool that follows POSIX standards for flags (i.e. use
`--flag-name` for long form of flag) but allows specifying package sets like
`go build` does. However, getting accurate output from gomodcheck requires
downloading package info for dependencies of the project to check.

The easiest and fastest way to run gomodcheck on all modfiles in a project is:
1. `go install github.com/ashmrtn/gomodcheck@latest`
1. `git clone <project to check>`
1. `cd <project to check>`
1. `go mod tidy`
1. `gomodcheck <flags> ./...`

Only modules that appear in both the linted project and the dependency (or
dependencies) specified by flags will generate lint errors. If a module appears
only as a dependency of a dependency then no errors will be output.

### Flags

gomod check current supports two different ways of checking module versions:
explicit module checking with the `--match-dep` flag and checking all replaced
modules with the `--match-replaces` flag.

To make discussing this a little easier, I'll use the term _dependency_ to mean
a module included in the project being linted that contains another module we
want to match the version of. I'll use the term _target dependency_ to mean the
module that appears in the gomodfile of both the project being linted and a
dependency where the version declared in the dependency should be used.

#### `--match-replaces`

The `--match-replaces <dependency>` flag tells gomodcheck to resolve every
replace directive in the dependency and ensure that if the project contains any
of the modules in the replace statement, make sure those also resolve to the
same version as the replace directive. Pass this flag multiple times to check
the replace directives of multiple dependencies.

To give an example, given the gomod files below, if a developer wanted to ensure
gomodcheck also replaced `github.com/ashmrtn/foo` with `github.com/ashmrtn/bar`
until some bugfixes merged into the upstream `github.com/ashmrtn/foo` repo
they'd add the flag `--check-replaces github.com/ashmrtn/example-project`.

```
====== gomodcheck go.mod ======
module github.com/ashmrtn/gomodcheck

go 1.21

require (
  github.com/ashmrtn/example-project v0.0.1
  golang.org/x/tools v0.17.0
)

require (
  github.com/ashmrtn/foo v0.0.1 // indirect
)

====== example-project go.mod ======
module github.com/ashmrtn/example-project

go 1.21

// TODO: Replace when bugfix in repo bar merges upstream into repo foo.
replace github.com/ashmrtn/foo => github.com/ashmrtn/bar 

require github.com/ashmrtn/foo v0.0.1
```

#### `--match-dep`

The `--match-dep <dependency>:<target dependency>` flag allows developers to
match against the target dependency version specified in the dependency. The
input format of this The `--match-dep` flag can be specified multiple times if
multiple (dependency, target dependency) pairs need to be checked. However, each
target dependency can be paired with only a single dependency (e.x.
`--match-dep foo:bar --match-dep baz:bar` would result in an error).

For example, with the gomodfiles shown below, if a developer wanted to ensure
the version of `golang.org/x/tools` used in `github.com/ashmrtn/example-project`
was also used in `github.com/ashmrtn/gomodcheck` then they'd add the flag
`--match-dep github.com/ashmrtn/example-project:golang.org/x/tools`.

```
====== gomodcheck go.mod ======
module github.com/ashmrtn/gomodcheck

go 1.21

require (
  github.com/ashmrtn/example-project v0.0.1
  golang.org/x/tools v0.17.0
)

====== example-project go.mod ======
module github.com/ashmrtn/example-project

go 1.21

require golang.org/x/tools v0.16.0
```

When explicitly matching a module that also appears on the left-hand side of a
replace directive, the original, non-replaced module path (left-hand side)
should be passed to the `match-dep` flag.

### Usage tips

gomodcheck doesn't persist any information between runs. This means that there's
some cases where it can't report differences between module versions because of
the way the modfiles have changed. A good example of this is the removal of a
replace directive in a dependency when using the `--match-replaces` flag. If no
other flags are passed to gomodcheck, gomodcheck won't produce a warning even
though the target dependency versions no longer match.

To work around this situation, developers can add dependencies specified in
replace statements as a target dependency in a  `--match-dep` statements as
well. That way, when a dependency is removed from a replace directive it'll
still appear as a module to be checked. gomodcheck will then attempt to match
the (now unreplaced) version of the module in the dependency with the (still
replaced) version in the project being linted and will return an error.

## Limitations

gomodcheck is still very much a work in progress, so it has some limitations.
The most noticeable limitations at the moment are the following:
* doesn't recursively check versions of dependency. Instead only checks module
  versions in the project it's run on and any of it's direct dependencies
* doesn't persist data between runs so can't detect module version differences
  if a module is removed from a replace directive (see
  [Usage tips](#usage-tips))

In terms of code, gomodcheck is still in progress and could use some further
restructuring. Right now there's quite a bit of logic for actually checking
dependencies living in the code that interacts with the CLI package. Eventually
I'd like to move that elsewhere so that it's easier to build other front-ends
for gomodcheck.
