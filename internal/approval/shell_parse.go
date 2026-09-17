package approval

import (
	"bytes"
	"os"
	"strings"

	"mvdan.cc/sh/v3/syntax"
)

// POSIX shell syntax helpers shared by command assessment and path extraction.

// shellLeaf is a simple command used by the structural reviewability analysis.
type shellLeaf struct {
	call *syntax.CallExpr
}

func collectShellLeaves(stmt *syntax.Stmt, leaves *[]shellLeaf) bool {
	if stmt == nil || stmt.Cmd == nil {
		return false
	}
	if binary, ok := stmt.Cmd.(*syntax.BinaryCmd); ok {
		if stmt.Negated || stmt.Background || len(stmt.Redirs) > 0 {
			return false
		}
		return collectShellLeaves(binary.X, leaves) && collectShellLeaves(binary.Y, leaves)
	}
	call, ok := stmt.Cmd.(*syntax.CallExpr)
	if !ok {
		return false
	}
	if text, ok := printShellNode(stmt); !ok || text == "" {
		return false
	}
	*leaves = append(*leaves, shellLeaf{call: call})
	return true
}

func printShellNode(node syntax.Node) (string, bool) {
	var buf bytes.Buffer
	printer := syntax.NewPrinter(syntax.SingleLine(true))
	if err := printer.Print(&buf, node); err != nil {
		return "", false
	}
	return strings.TrimSpace(buf.String()), true
}

func normalizeShellCommandWithMode(command string, posix bool) string {
	command = strings.TrimSpace(command)
	if command == "" || !posix {
		return command
	}
	parser := syntax.NewParser(syntax.Variant(syntax.LangPOSIX))
	file, err := parser.Parse(strings.NewReader(command), "")
	if err != nil {
		return command
	}
	normalized, ok := printShellNode(file)
	if !ok {
		return command
	}
	return normalized
}

func shellNodeHasDynamicParts(node syntax.Node) bool {
	dynamic := false
	syntax.Walk(node, func(child syntax.Node) bool {
		switch child.(type) {
		case *syntax.ParamExp, *syntax.CmdSubst, *syntax.ArithmExp,
			*syntax.ProcSubst, *syntax.ExtGlob, *syntax.BraceExp:
			dynamic = true
			return false
		default:
			return !dynamic
		}
	})
	return dynamic
}

func staticShellWord(word *syntax.Word) (string, bool) {
	var result strings.Builder
	for _, part := range word.Parts {
		switch value := part.(type) {
		case *syntax.Lit:
			result.WriteString(value.Value)
		case *syntax.SglQuoted:
			result.WriteString(value.Value)
		case *syntax.DblQuoted:
			for _, quotedPart := range value.Parts {
				lit, ok := quotedPart.(*syntax.Lit)
				if !ok {
					return "", false
				}
				result.WriteString(lit.Value)
			}
		default:
			return "", false
		}
	}
	return result.String(), true
}

// accessShellWord resolves the narrow set of dynamic words whose value and
// filesystem scope are known to the approval layer. It is deliberately
// separate from staticShellWord so literal-only consumers never accept expansions.
func accessShellWord(word *syntax.Word) (string, bool) {
	if word == nil {
		return "", false
	}
	var result strings.Builder
	for _, part := range word.Parts {
		if !appendAccessWordPart(&result, part) {
			return "", false
		}
	}
	return result.String(), true
}

func appendAccessWordPart(result *strings.Builder, part syntax.WordPart) bool {
	switch value := part.(type) {
	case *syntax.Lit:
		result.WriteString(value.Value)
	case *syntax.SglQuoted:
		result.WriteString(value.Value)
	case *syntax.DblQuoted:
		for _, quotedPart := range value.Parts {
			if !appendAccessWordPart(result, quotedPart) {
				return false
			}
		}
	case *syntax.ParamExp:
		home, ok := simpleHomeExpansion(value)
		if !ok {
			return false
		}
		result.WriteString(home)
	default:
		return false
	}
	return true
}

func simpleHomeExpansion(exp *syntax.ParamExp) (string, bool) {
	if exp == nil || exp.Param == nil || exp.Param.Value != "HOME" ||
		exp.Flags != nil || exp.Excl || exp.Length || exp.Width || exp.IsSet ||
		exp.NestedParam != nil || exp.Index != nil || len(exp.Modifiers) > 0 ||
		exp.Slice != nil || exp.Repl != nil || exp.Names != 0 || exp.Exp != nil {
		return "", false
	}
	home, err := os.UserHomeDir()
	if err != nil || strings.TrimSpace(home) == "" {
		return "", false
	}
	return home, true
}

func shellWordsForAccess(words []*syntax.Word) []string {
	args := make([]string, 0, len(words))
	for _, word := range words {
		value, ok := accessShellWord(word)
		if !ok {
			return nil
		}
		args = append(args, value)
	}
	return args
}

// scalarSubstPlaceholder stands in for an allowlisted scalar command
// substitution while reducing a word to its deterministic directory scope.
// It contains no path separator, "$", or "%", so parent-directory reduction
// treats it as an ordinary final path segment.
const scalarSubstPlaceholder = "0"

// accessShellWordConfined renders word like accessShellWord, except that an
// allowlisted scalar command substitution renders as a placeholder segment
// (the substitution cannot introduce a path separator or "..", so the word's
// directory scope stays deterministic). ok is false when the word contains
// any other runtime expansion.
func accessShellWordConfined(word *syntax.Word, confinementOK bool) (string, bool) {
	if word == nil {
		return "", false
	}
	var result strings.Builder
	for _, part := range word.Parts {
		if !appendAccessWordPartConfined(&result, part, confinementOK) {
			return "", false
		}
	}
	return result.String(), true
}

func appendAccessWordPartConfined(result *strings.Builder, part syntax.WordPart, confinementOK bool) bool {
	switch value := part.(type) {
	case *syntax.DblQuoted:
		for _, quotedPart := range value.Parts {
			if !appendAccessWordPartConfined(result, quotedPart, confinementOK) {
				return false
			}
		}
		return true
	case *syntax.CmdSubst:
		if confinementOK && cmdSubstScalarConfined(value) {
			result.WriteString(scalarSubstPlaceholder)
			return true
		}
		return false
	default:
		return appendAccessWordPart(result, part)
	}
}

// shellWordsForAccessConfined resolves a call's words with scalar-confined
// command substitutions materialized. Any word carrying a different runtime
// expansion still rejects the whole invocation, because dropping or
// misplacing a dynamic operand would corrupt position-based target
// extraction (cp/mv destinations, rm operands).
func shellWordsForAccessConfined(words []*syntax.Word, confinementOK bool) []string {
	args := make([]string, 0, len(words))
	for _, word := range words {
		value, ok := accessShellWordConfined(word, confinementOK)
		if !ok {
			return nil
		}
		args = append(args, value)
	}
	return args
}

// StaticPOSIXLiteralArgs returns statically known argument values from every
// simple command in a POSIX shell expression. Quoting is removed by the AST,
// while parameter expansions and other dynamic words are omitted. Callers use
// this to recover quoted path arguments without treating quoted awk/sed
// programs as raw shell text.
func StaticPOSIXLiteralArgs(command string) []string {
	parser := syntax.NewParser(syntax.Variant(syntax.LangPOSIX))
	file, err := parser.Parse(strings.NewReader(command), "")
	if err != nil {
		return nil
	}
	var args []string
	syntax.Walk(file, func(node syntax.Node) bool {
		call, ok := node.(*syntax.CallExpr)
		if !ok {
			return true
		}
		for _, word := range call.Args {
			if value, ok := staticShellWord(word); ok && value != "" {
				args = append(args, value)
			}
		}
		return true
	})
	return args
}

// POSIXHasUnquotedBareHomeArg reports whether a simple command contains an
// unquoted, standalone "~" argument. POSIX shells expand that form using the
// child process's HOME, while quoted tildes and tildes embedded in another
// word remain literal. Keeping this distinction in the AST layer prevents the
// approval classifier from guessing a concrete home directory.
func POSIXHasUnquotedBareHomeArg(command string) bool {
	parser := syntax.NewParser(syntax.Variant(syntax.LangPOSIX))
	file, err := parser.Parse(strings.NewReader(command), "")
	if err != nil {
		return false
	}
	found := false
	syntax.Walk(file, func(node syntax.Node) bool {
		if found {
			return false
		}
		call, ok := node.(*syntax.CallExpr)
		if !ok || len(call.Args) < 2 {
			return true
		}
		for _, word := range call.Args[1:] {
			if len(word.Parts) != 1 {
				continue
			}
			lit, ok := word.Parts[0].(*syntax.Lit)
			if ok && lit.Value == "~" {
				found = true
				return false
			}
		}
		return true
	})
	return found
}

func redirectionWrites(op syntax.RedirOperator) bool {
	switch op {
	case syntax.RdrOut, syntax.AppOut, syntax.RdrClob, syntax.RdrAll, syntax.AppAll, syntax.RdrInOut:
		return true
	default:
		return false
	}
}

func redirectionWritesPersistent(redir *syntax.Redirect) bool {
	if redir == nil || !redirectionWrites(redir.Op) {
		return false
	}
	target, ok := staticShellWord(redir.Word)
	if ok && isNullRedirectionTarget(target) {
		return false
	}
	return true
}

func isNullRedirectionTarget(target string) bool {
	target = strings.TrimSpace(target)
	return target == "/dev/null" || strings.EqualFold(target, "NUL")
}
