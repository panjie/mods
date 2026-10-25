package commandanalysis

import "context"

// Dependencies exposes only read-only host operations. No interface accepts an
// executable command. Nil fields select bounded production implementations.
type Dependencies struct {
	UntrustedProgramDirs []string
	Environment          func() []string
	Paths                PathResolver
	Git                  GitResolver
	PowerShell           PowerShellParser
	Automatics           AutomaticResolver
}

type PathResolver interface {
	Resolve(context.Context, string) (string, error)
	RepositoryDirs(context.Context, string) ([]string, error)
}

type GitQueryKind uint8

const (
	GitBranch GitQueryKind = iota
	GitBranchPushRemote
	GitPushDefault
	GitBranchRemote
	GitRemotes
	GitURLRewrites
	GitPushURLs
	GitExecutable
)

type GitQuery struct {
	Kind                 GitQueryKind
	Cwd, Name            string
	Environment          []string
	UntrustedProgramDirs []string
}

type GitResolver interface {
	Query(context.Context, GitQuery) (string, error)
}

// Parse returns bounded protocol JSON, never output from executing source.
type PowerShellParser interface {
	Parse(context.Context, string) ([]byte, error)
}

// AutomaticResolver accepts only validated HOME/PROFILE names and properties.
type AutomaticResolver interface {
	Resolve(context.Context, string, string, []string) (string, error)
}

type Settings struct {
	WorkingDir       string
	ReadOnlyCommands []string
}

// CreationParentResolver optionally proves the existing ancestor whose entries
// may be created by mkdir -p. A resolver without this capability cannot make
// directory creation complete.
type CreationParentResolver interface {
	CreationParent(context.Context, string) (string, error)
}
