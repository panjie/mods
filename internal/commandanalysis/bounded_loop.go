package commandanalysis

import (
	"strings"

	"mvdan.cc/sh/v3/syntax"
)

const maxBoundedLoopItems = 32
const maxBoundedLoopWork = 256

type boundedLoopProof struct {
	read    bool
	targets []Target
	reason  string
}

// Loop proofs are deliberately read-only. They never create write coverage or
// trust evidence. Unknown siblings, assignments and environment-changing
// builtins disable expansion; the original AST remains authoritative.
func boundedLoopContextSafe(file *syntax.File) bool {
	if file == nil {
		return false
	}
	safe := true
	syntax.Walk(file, func(n syntax.Node) bool {
		if !safe {
			return false
		}
		switch n := n.(type) {
		case *syntax.Assign, *syntax.FuncDecl, *syntax.DeclClause, *syntax.CmdSubst, *syntax.ProcSubst, *syntax.ArithmExp:
			safe = false
		case *syntax.Redirect:
			if !boundedLoopRedirect(n) {
				safe = false
			}
		case *syntax.ForClause:
			it, ok := n.Loop.(*syntax.WordIter)
			if !ok || !safeLoopVariable(it.Name.Value) {
				safe = false
			}
		case *syntax.CallExpr:
			if len(n.Args) == 0 {
				safe = false
				break
			}
			name, ok := staticShellWord(n.Args[0])
			e := commandCatalog.lookup(nativeSpace, name)
			if !ok || !isBarePOSIXCommand(name) || posixBindingCommandNames[name] || e == nil || e.Read == nil {
				safe = false
				break
			}
			// Dual-use commands are safe siblings only when this invocation is
			// itself read-only. Loop bodies are still rebound and checked below.
			if (e.Write != nil || e.Risk != nil) && !catalogTokens(shellWordsForAccess(n.Args), nativeSpace, ReadOnlyCommandPolicy{}).BuiltinRead {
				safe = false
			}
		}
		return safe
	})
	return safe
}

func safeLoopVariable(name string) bool {
	// Uppercase names include PATH, IFS, ENV and loader controls. Do not bind
	// execution-environment variables as if they were ordinary local operands.
	if name == "" {
		return false
	}
	for _, c := range name {
		if !(c >= 'a' && c <= 'z' || c == '_' || c >= '0' && c <= '9') {
			return false
		}
	}
	return true
}

func (s *semanticSession) boundedLoop(loop *syntax.ForClause) *boundedLoopProof {
	if s == nil {
		return &boundedLoopProof{}
	}
	if p, ok := s.loops[loop]; ok {
		return p
	}
	p := &boundedLoopProof{reason: "unsupported execution context or loop form"}
	if s.loops == nil {
		s.loops = map[*syntax.ForClause]*boundedLoopProof{}
	}
	s.loops[loop] = p
	it, ok := loop.Loop.(*syntax.WordIter)
	if !s.loopContextSafe || !ok || loop.Select || !safeLoopVariable(it.Name.Value) {
		return p
	}
	if len(it.Items) == 0 || len(it.Items) > maxBoundedLoopItems {
		p.reason = "literal iteration count is absent or exceeds limit"
		return p
	}
	var targets []Target
	for _, item := range it.Items {
		value, ok := boundLoopWord(item, "", "", false)
		if !ok {
			p.reason = "iteration list contains an unresolved expansion"
			return p
		}
		for _, stmt := range loop.Do {
			if !s.boundLoopStmt(stmt, it.Name.Value, value, &targets, &p.reason) {
				return p
			}
		}
	}
	p.reason = ""
	p.read = true
	p.targets = targets
	return p
}

func (s *semanticSession) boundLoopStmt(stmt *syntax.Stmt, name, value string, targets *[]Target, reason *string) bool {
	fail := func(message string) bool { *reason = message; return false }
	s.loopWork++
	if s.loopWork > maxBoundedLoopWork || stmt == nil || stmt.Background || stmt.Coprocess {
		return fail("unsupported loop statement or expansion limit")
	}
	for _, r := range stmt.Redirs {
		if !boundedLoopRedirect(r) {
			return fail("unsupported loop statement or redirection")
		}
	}
	switch c := stmt.Cmd.(type) {
	case *syntax.BinaryCmd:
		return s.boundLoopStmt(c.X, name, value, targets, reason) && s.boundLoopStmt(c.Y, name, value, targets, reason)
	case *syntax.CallExpr:
		if len(c.Assigns) > 0 || len(c.Args) == 0 {
			return fail("unsupported loop statement or redirection")
		}
		command, ok := boundLoopWord(c.Args[0], "", "", false)
		if !ok || !isBarePOSIXCommand(command) {
			return fail("unsupported loop statement or redirection")
		}
		args := []string{command}
		for _, word := range c.Args[1:] {
			v, ok := boundLoopWord(word, name, value, false)
			if !ok {
				return fail("unresolved or unsupported argument in " + command)
			}
			args = append(args, v)
		}
		f := evaluateInvocation(invocationBinding{Name: command, Read: args, Write: args, Coverage: args, ExactRead: true}, nativeSpace, ReadOnlyCommandPolicy{})
		s.evaluations++
		if !f.BuiltinRead {
			return fail("command or arguments are not proven read-only: " + command)
		}
		for _, t := range f.Targets {
			if t.Role == TargetWrite {
				return fail("loop body contains a write target")
			}
			t.Literal = true
			*targets = append(*targets, targetFromInvocation(t))
		}
		return true
	default:
		return fail("unsupported loop statement or expansion limit")
	}
}

// Decode literals according to their quote context. Globbing, substitutions,
// and parameter operators never become literal argv by textual replacement.
func boundLoopWord(word *syntax.Word, name, value string, quoted bool) (string, bool) {
	if word == nil {
		return "", false
	}
	var b strings.Builder
	for _, part := range word.Parts {
		switch p := part.(type) {
		case *syntax.Lit:
			text, ok := decodeLoopLiteral(p.Value, quoted)
			if !ok {
				return "", false
			}
			b.WriteString(text)
		case *syntax.SglQuoted:
			if p.Dollar {
				return "", false
			}
			b.WriteString(p.Value)
		case *syntax.DblQuoted:
			if p.Dollar {
				return "", false
			}
			v, ok := boundLoopWord(&syntax.Word{Parts: p.Parts}, name, value, true)
			if !ok {
				return "", false
			}
			b.WriteString(v)
		case *syntax.ParamExp:
			if name == "" || p.Param == nil || p.Param.Value != name || p.Flags != nil || p.Excl || p.Length || p.Width || p.IsSet || p.NestedParam != nil || p.Index != nil || len(p.Modifiers) > 0 || p.Slice != nil || p.Repl != nil || p.Names != 0 || p.Exp != nil {
				return "", false
			}
			if !quoted && (value == "" || strings.ContainsAny(value, " \t\r\n*?[]")) {
				return "", false
			}
			b.WriteString(value)
		default:
			return "", false
		}
	}
	return b.String(), true
}

// Apply the same restriction to surrounding and sibling redirects: a read
// proof must not inherit a lossy null-device spelling or a non-FD duplication.
func boundedLoopRedirect(r *syntax.Redirect) bool {
	if r == nil || r.Word == nil || r.Hdoc != nil {
		return false
	}
	value, ok := boundLoopWord(r.Word, "", "", false)
	if !ok {
		return false
	}
	switch r.Op {
	case syntax.RdrIn:
		return true
	case syntax.RdrOut, syntax.AppOut, syntax.RdrClob:
		return value == "/dev/null"
	case syntax.DplOut, syntax.DplIn:
		return value == "0" || value == "1" || value == "2" || value == "-"
	default:
		return false
	}
}

// Double quotes remove backslashes only before $, `, ", backslash, and
// newline. Other backslashes are literal (e.g. a regex \.). Parameter and
// command substitutions are separate AST nodes, never decoded here.
func decodeLoopLiteral(value string, quoted bool) (string, bool) {
	if !quoted {
		if strings.ContainsAny(value, "\\`$*?[]~{}") {
			return "", false
		}
		return value, true
	}
	var b strings.Builder
	for i := 0; i < len(value); i++ {
		if value[i] != '\\' {
			b.WriteByte(value[i])
			continue
		}
		if i+1 == len(value) {
			return "", false
		}
		next := value[i+1]
		if strings.ContainsRune("$`\"\\\n", rune(next)) {
			i++
			if next != '\n' {
				b.WriteByte(next)
			}
		} else {
			b.WriteByte('\\')
		}
	}
	return b.String(), true
}
