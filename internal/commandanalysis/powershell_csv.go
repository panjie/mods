package commandanalysis

import "strings"

// bindImportCSV accepts one literal filesystem input, with exact, scalar
// parameters. It deliberately excludes pipeline-bound paths, arrays, splats,
// expressions, provider paths, abbreviations and common variable parameters.
// This is a read proof only; the whole IR must still pass composition checks.
func bindImportCSV(args []string) (string, bool) {
	path := ""
	seen := map[string]bool{}
	for i := 0; i < len(args); i++ {
		arg := args[i]
		parameter := "path"
		if strings.HasPrefix(arg, "-") {
			parameter = strings.ToLower(strings.TrimPrefix(arg, "-"))
			if seen[parameter] {
				return "", false
			}
			seen[parameter] = true
			switch parameter {
			case "path", "literalpath", "delimiter", "encoding":
				i++
				if i == len(args) {
					return "", false
				}
				arg = args[i]
			default:
				return "", false
			}
		}
		if !powerShellScalarLiteral(arg) {
			return "", false
		}
		value := trimPowerShellLiteral(arg)
		switch parameter {
		case "path", "literalpath":
			if path != "" || value == "" || strings.HasPrefix(value, "-") || IsPowerShellProviderPath(value) || parameter == "path" && strings.ContainsAny(value, "*?[]") {
				return "", false
			}
			path = value
		case "delimiter":
			// PowerShell's delimiter is a single UTF-16 char, not an arbitrary
			// string or script-converted object.
			chars := []rune(value)
			if len(chars) != 1 || chars[0] > 0xffff {
				return "", false
			}
		case "encoding":
			switch strings.ToLower(value) {
			case "ascii", "bigendianunicode", "default", "oem", "unicode", "utf8", "utf7", "utf32":
			default:
				return "", false
			}
		}
	}
	return path, path != ""
}

func readPSImportCSV(_ *commandEntry, args []string, _ ReadOnlyCommandPolicy, _ *catalogEvaluation) bool {
	_, ok := bindImportCSV(args)
	return ok
}

func importCSVReadOperands(args []string) ([]string, bool) {
	path, ok := bindImportCSV(args)
	if !ok {
		return nil, false
	}
	return []string{path}, true
}
