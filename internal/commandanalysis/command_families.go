package commandanalysis

// Command families used by the structural reviewability heuristic to identify
// operands that may themselves invoke a command. They do not grant permissions.
var exactShellCommands = map[string]bool{
	"awk": true, "bash": true, "dash": true, "doas": true, "env": true,
	"eval": true, "exec": true, "find": true, "flock": true, "ionice": true,
	"ksh": true, "node": true, "perl": true, "python": true, "python3": true,
	"ruby": true, "sed": true, "setsid": true, "sh": true, "sudo": true,
	"tee": true, "watch": true, "xargs": true, "zsh": true,
}

// subcommandShellCommands have a well-known subcommand structure.
var subcommandShellCommands = map[string]bool{
	"bun": true, "cargo": true, "docker": true, "gh": true, "git": true,
	"go": true, "helm": true, "kubectl": true, "npm": true, "pnpm": true,
	"yarn": true,
}

// flagPrefixShellCommands accept flags before the path operand.
var flagPrefixShellCommands = map[string]bool{
	"chmod": true, "chown": true, "cp": true, "mkdir": true, "mv": true,
	"rm": true, "rmdir": true, "touch": true,
}
