package approval

import (
	"bytes"
	"strings"

	"mvdan.cc/sh/v3/syntax"
)

// POSIX shell parsing built on mvdan.cc/sh. Used (a) to derive stable
// approval rules from a command (via ruleForShellLeaf/ruleFromTokens)
// and (b) by the writable-directory extractor in writable_dirs.go.
//
// The simple tokenizer in simple_tokenize.go is the fallback path for
// Windows / PowerShell commands.

type shellLeaf struct {
	text  string
	call  *syntax.CallExpr
	exact bool
}

func parseShellLeaves(command string) ([]shellLeaf, bool) {
	parser := syntax.NewParser(syntax.Variant(syntax.LangPOSIX))
	file, err := parser.Parse(strings.NewReader(command), "")
	if err != nil {
		return nil, false
	}
	var leaves []shellLeaf
	for _, stmt := range file.Stmts {
		if !collectShellLeaves(stmt, &leaves) {
			return nil, false
		}
	}
	return leaves, true
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
	text, ok := printShellNode(stmt)
	if !ok || text == "" {
		return false
	}
	exact := stmt.Negated || stmt.Background || len(stmt.Redirs) > 0 || len(call.Assigns) > 0
	if shellNodeHasDynamicParts(stmt) {
		exact = true
	}
	*leaves = append(*leaves, shellLeaf{
		text:  text,
		call:  call,
		exact: exact,
	})
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

func ruleForShellLeaf(tool string, leaf shellLeaf) Rule {
	if leaf.exact || leaf.call == nil || len(leaf.call.Args) == 0 {
		return shellExactRule(tool, leaf.text)
	}
	args := shellLeafArgs(leaf)
	if len(args) == 0 {
		return shellExactRule(tool, leaf.text)
	}
	return ruleFromTokens(tool, args, leaf.exact, leaf.text)
}

func shellLeafArgs(leaf shellLeaf) []string {
	if leaf.call == nil || len(leaf.call.Args) == 0 {
		return nil
	}
	args := make([]string, 0, len(leaf.call.Args))
	for _, word := range leaf.call.Args {
		value, ok := staticShellWord(word)
		if !ok {
			return nil
		}
		args = append(args, value)
	}
	return args
}

func ruleFromTokens(tool string, args []string, exact bool, originalText string) Rule {
	if exact || len(args) == 0 {
		return shellExactRule(tool, originalText)
	}
	if exactShellCommands[args[0]] {
		return shellExactRule(tool, originalText)
	}
	prefixLen := shellPrefixLength(args)
	if prefixLen <= 0 {
		return shellExactRule(tool, originalText)
	}
	if prefixLen >= len(args) &&
		!subcommandShellCommands[args[0]] &&
		!flagPrefixShellCommands[args[0]] {
		return shellExactRule(tool, originalText)
	}
	return Rule{
		Type:    ShellPrefix,
		Tool:    tool,
		Pattern: strings.Join(args[:prefixLen], " ") + " *",
	}
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

func shellWords(words []*syntax.Word) []string {
	args := make([]string, 0, len(words))
	for _, word := range words {
		value, ok := staticShellWord(word)
		if !ok {
			return nil
		}
		args = append(args, value)
	}
	return args
}

func redirectionWrites(op syntax.RedirOperator) bool {
	switch op {
	case syntax.RdrOut, syntax.AppOut, syntax.ClbOut, syntax.RdrAll, syntax.AppAll, syntax.RdrInOut:
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
