package commandanalysis

import "strings"

// Audited subset of https://man.archlinux.org/man/pacman.8.en . In particular,
// -S alone, refresh, downloads, config overrides, and abbreviations are not reads.
func readPacman(_ *commandEntry, args []string, _ ReadOnlyCommandPolicy, _ *catalogEvaluation) bool {
	if len(args) == 1 && (args[0] == "--help" || args[0] == "-h" || args[0] == "--version" || args[0] == "-V") {
		return true
	}
	mode := byte(0)
	var flags strings.Builder
	ended := false
	longs := map[string]string{"--query": "Q", "--sync": "S", "--info": "i", "--search": "s", "--list": "l", "--groups": "g", "--quiet": "q", "--deps": "d", "--explicit": "e", "--foreign": "m", "--native": "n", "--unrequired": "t", "--upgrades": "u", "--owns": "o", "--check": "k", "--changelog": "c"}
	for _, arg := range args {
		if arg == "--" && !ended {
			ended = true
			continue
		}
		if ended || !strings.HasPrefix(arg, "-") {
			continue
		}
		if arg == "-" {
			return false
		}
		options := strings.TrimPrefix(arg, "-")
		if strings.HasPrefix(arg, "--") {
			var ok bool
			options, ok = longs[arg]
			if !ok {
				return false
			}
		}
		for i := 0; i < len(options); i++ {
			c := options[i]
			if c == 'Q' || c == 'S' {
				if mode != 0 {
					return false
				}
				mode = c
			} else {
				flags.WriteByte(c)
			}
		}
	}
	allowed := "islgqdemntuokc"
	if mode == 'S' {
		allowed = "islgq"
		if !strings.ContainsAny(flags.String(), "islg") {
			return false
		}
	} else if mode != 'Q' {
		return false
	}
	for _, c := range flags.String() {
		if !strings.ContainsRune(allowed, c) {
			return false
		}
	}
	return true
}
