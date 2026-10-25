package commandanalysis

import (
	"encoding/json"
	"strings"

	"mvdan.cc/sh/v3/syntax"
)

// semanticSession lives for one analysis, including the trust-free evidence
// comparison and Git enumeration. Revalidation always creates a new session.
// Keys identify syntax nodes, not command names or results from earlier runs.
type semanticSession struct {
	context         ArgvStaticContext
	policy          ReadOnlyCommandPolicy
	confined        bool
	loopContextSafe bool
	loops           map[*syntax.ForClause]*boundedLoopProof
	loopWork        int
	posixFacts      map[*syntax.CallExpr]*InvocationFacts
	psFacts         map[string]*InvocationFacts
	byID            map[int]*InvocationFacts
	nodeIDs         map[syntax.Node]int
	evaluations     int
	gitChecked      map[*InvocationFacts]bool
}

func newSemanticSession(p *parsedCommand, policy ReadOnlyCommandPolicy) *semanticSession {
	policy.session = nil
	s := &semanticSession{policy: policy, posixFacts: map[*syntax.CallExpr]*InvocationFacts{}, psFacts: map[string]*InvocationFacts{}, byID: map[int]*InvocationFacts{}, gitChecked: map[*InvocationFacts]bool{}}
	if p != nil {
		s.nodeIDs = p.nodeIDs
		s.loops = map[*syntax.ForClause]*boundedLoopProof{}
		s.loopContextSafe = boundedLoopContextSafe(p.file)
		if p.file != nil {
			s.confined = posixFileAllowsScalarConfinement(p.file)
		}
	}
	return s
}
func (s *semanticSession) posix(call *syntax.CallExpr) *InvocationFacts {
	if f := s.posixFacts[call]; f != nil {
		return f
	}
	name := ""
	if len(call.Args) > 0 {
		name, _ = staticShellWord(call.Args[0])
	}
	read := shellWordsForAccess(call.Args)
	if len(read) > 0 {
		read[0] = name
	}
	if name == "find" {
		for i, word := range call.Args {
			if i < len(read) && len(word.Parts) == 1 {
				if lit, ok := word.Parts[0].(*syntax.Lit); ok && (lit.Value == `\(` || lit.Value == `\)`) {
					read[i] = lit.Value[1:]
				}
			}
		}
	}
	exactRead := false
	if entry := commandCatalog.lookup(nativeSpace, name); entry != nil && entry.LiteralRead {
		read = nil
		exactRead = true
		for _, word := range call.Args {
			value, ok := boundLoopWord(word, "", "", false)
			if !ok {
				read = nil
				exactRead = false
				break
			}
			read = append(read, value)
		}
	}
	f := evaluateInvocation(invocationBinding{Name: name, Read: read, ExactRead: exactRead, Write: shellWordsForAccessConfined(call.Args, s.confined), Coverage: coverageWords(call.Args)}, nativeSpace, s.policy)
	s.context.run.resolveTransferFacts(&f, s.context.Cwd, false)
	s.evaluations++
	s.posixFacts[call] = &f
	if id := s.nodeIDs[call]; id > 0 {
		s.byID[id] = &f
	}
	return &f
}
func (s *semanticSession) ps(inv psCommandInvocation) *InvocationFacts {
	// Older synthetic IR fixtures have no node IDs. Include argv as well as ID
	// so those fixtures cannot alias different invocations.
	keyBytes, _ := json.Marshal(struct {
		ID   int
		Name string
		Args []string
	}{inv.NodeID, inv.Name, inv.Args})
	key := string(keyBytes)
	if f := s.psFacts[key]; f != nil {
		return f
	}
	f := catalogTokens(append([]string{inv.Name}, inv.Args...), powershellSpace, s.policy)
	s.evaluations++
	s.psFacts[key] = &f
	if inv.NodeID > 0 {
		s.byID[inv.NodeID] = &f
	}
	return &f
}
func invocationRead(f *InvocationFacts, policy ReadOnlyCommandPolicy) (bool, string) {
	if len(policy.Commands) == 0 {
		return f.BuiltinRead, f.ReadReason
	}
	return f.ReadOnly, f.ReadReason
}
func (s *semanticSession) git(f *InvocationFacts, tokens []string, posix bool, context ArgvStaticContext) *CommandAssessment {
	if !s.gitChecked[f] {
		s.gitChecked[f] = true
		if result, handled := assessGitArgvStatic(tokens, posix, context); handled {
			f.Git = &result
		}
	}
	return f.Git
}
func appendWriteOperands(result *CommandAssessment, targets []writableTarget) {
	for _, target := range targets {
		if value := trimPowerShellLiteral(strings.TrimSpace(target.path)); value != "" {
			result.WritePaths = append(result.WritePaths, value)
		}
	}
}
