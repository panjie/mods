package commandanalysis

import "strings"

// The first output binding deliberately accepts one scalar path only. Exact
// parameter names prevent unknown/abbreviated options from hiding another path.
// General arrays, splats, expressions, dynamic parameters and providers are
// never inferred from flattened argv.
func bindPowerShellOutput(name string, args []string) (path string, literal, ok bool) {
	seen := map[string]bool{}
	position := 0
	for i := 0; i < len(args); i++ {
		arg := args[i]
		parameter := ""
		if strings.HasPrefix(arg, "-") {
			parameter = strings.ToLower(strings.TrimPrefix(arg, "-"))
			if seen[parameter] {
				return "", false, false
			}
			seen[parameter] = true
			switch parameter {
			case "force", "nonewline":
				continue
			case "append", "noclobber":
				if name != "out-file" {
					return "", false, false
				}
				continue
			case "passthru":
				if name == "out-file" {
					return "", false, false
				}
				continue
			case "literalpath", "path", "filepath", "value", "encoding", "width", "inputobject":
				if parameter == "filepath" && name != "out-file" || parameter == "path" && name == "out-file" || parameter == "value" && name == "out-file" || (parameter == "width" || parameter == "inputobject") && name != "out-file" {
					return "", false, false
				}
				i++
				if i == len(args) {
					return "", false, false
				}
				arg = args[i]
			default:
				return "", false, false
			}
		} else {
			position++
			if position == 1 && path == "" {
				parameter = "path"
			} else if position <= 2 && name != "out-file" && !seen["value"] {
				parameter = "value"
				seen["value"] = true
			} else {
				return "", false, false
			}
		}
		if !powerShellScalarLiteral(arg) {
			return "", false, false
		}
		if parameter == "path" || parameter == "literalpath" || parameter == "filepath" {
			if path != "" {
				return "", false, false
			}
			raw := trimPowerShellLiteral(arg)
			if raw == "" || IsPowerShellProviderPath(raw) || parameter != "literalpath" && strings.ContainsAny(raw, "*?[]") {
				return "", false, false
			}
			path = arg
			literal = parameter == "literalpath"
		}
	}
	return path, literal, path != ""
}

func powerShellScalarLiteral(raw string) bool {
	if raw == "" {
		return false
	}
	single, double := powerShellArgQuoting(raw)
	value := trimPowerShellLiteral(raw)
	// Escaped/embedded quotes need a decoded AST value, not string trimming.
	if strings.ContainsAny(value, "'\"`") {
		return false
	}
	if single {
		return true
	}
	if strings.Contains(value, "$") {
		return false
	}
	return double || !strings.ContainsAny(value, "@,(){};|&\r\n")
}

// A legacy FileRedirection label alone is insufficient. Require a one-to-one
// mapping to literal AST nodes with a known command owner and stream.
func powerShellRedirectionTargets(ir *psBridgeIR) ([]Target, bool) {
	nodes := map[int]psSyntaxNode{}
	count := 0
	for _, n := range ir.Nodes {
		nodes[n.ID] = n
		if n.Kind == "FileRedirectionAst" {
			count++
		}
	}
	labeled := false
	for _, kind := range ir.Redirects {
		if kind == "FileRedirection" {
			labeled = true
		}
	}
	if !labeled && count == 0 && len(ir.Redirections) == 0 {
		return nil, true
	}
	if !labeled || count == 0 || count != len(ir.Redirections) || ir.HasAssignment || ir.HasControlFlow || ir.HasScriptBlock || ir.HasStopParsing {
		return nil, false
	}
	seen := map[int]bool{}
	var targets []Target
	for _, r := range ir.Redirections {
		n := nodes[r.NodeID]
		owner := nodes[r.Owner]
		raw := trimPowerShellLiteral(r.Target)
		if seen[r.NodeID] || n.Kind != "FileRedirectionAst" || n.Parent != r.Owner || owner.Kind != "CommandAst" || !r.Literal || r.Stream < 0 || r.Stream > 6 || !powerShellScalarLiteral(r.Target) || raw == "" || strings.ContainsAny(raw, "*?[]") || IsPowerShellProviderPath(raw) {
			return nil, false
		}
		seen[r.NodeID] = true
		targets = append(targets, Target{NodeID: r.NodeID, Kind: TargetFilesystem, Role: TargetWrite, Operation: OperationFollowLeaf, Raw: r.Target, Literal: true})
	}
	return targets, true
}
