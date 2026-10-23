package commandanalysis

// Dependencies contains host facts needed for executable trust checks. These
// directories do not grant write authority. Filesystem/env/Git query interfaces
// will replace the legacy direct host reads in phase 2.
type Dependencies struct {
	UntrustedProgramDirs []string
}

// Settings is copied by NewEngine; neither application config nor saved rules
// are visible to the analyzer.
type Settings struct {
	WorkingDir       string
	ReadOnlyCommands []string
}
