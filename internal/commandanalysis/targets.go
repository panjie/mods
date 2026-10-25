package commandanalysis

import (
	"path/filepath"
	"strings"

	"github.com/panjie/mods/internal/pathutil"
	"mvdan.cc/sh/v3/syntax"
)

// Targets describe evidence, not permissions. Candidate paths never prove an
// access mode; unresolved writes never establish complete target coverage.
type TargetKind uint8

const (
	TargetFilesystem TargetKind = iota + 1
	TargetOrigin
	TargetProvider
)

type TargetOperation uint8

const (
	OperationRead TargetOperation = iota + 1
	OperationFollowLeaf
	OperationCreateEntry
	OperationRemoveEntry
	OperationRenameEntry
	OperationDirectory
	OperationDestination // destination type not modeled yet; never complete
)

type TargetState uint8

const (
	TargetUnresolved TargetState = iota
	TargetResolved
)

type Target struct {
	NodeID      int
	Kind        TargetKind
	Role        TargetRole
	Operation   TargetOperation
	Raw         string
	LexicalPath string
	Path        string
	Dir         string
	State       TargetState
	Literal     bool
}

func targetFromInvocation(t InvocationTarget) Target {
	op := t.Operation
	if op == 0 {
		if t.Role == TargetRead {
			op = OperationRead
		} else {
			op = OperationFollowLeaf
		}
	}
	return Target{Kind: TargetFilesystem, Role: t.Role, Operation: op, Raw: t.Path, Literal: t.Literal}
}
func appendInvocationTargets(result *CommandAssessment, f *InvocationFacts) {
	for _, t := range f.Targets {
		if f.ReadOnly && t.Role == TargetWrite {
			t.Role = TargetRead
			t.Operation = OperationRead
		}
		result.Targets = append(result.Targets, targetFromInvocation(t))
	}
	if f.Git != nil {
		role := TargetWrite
		if f.ReadOnly {
			role = TargetRead
		}
		appendDirectoryTargets(result, f.Git.KnownDirs, role)
	}
}
func appendPOSIXTargets(result *CommandAssessment, file *syntax.File, session *semanticSession) {
	syntax.Walk(file, func(n syntax.Node) bool {
		switch n := n.(type) {
		case *syntax.CallExpr:
			if len(n.Args) > 0 {
				start := len(result.Targets)
				appendInvocationTargets(result, session.posix(n))
				for i := start; i < len(result.Targets); i++ {
					result.Targets[i].NodeID = session.nodeIDs[n]
					for _, arg := range n.Args[1:] {
						if value, ok := staticShellWord(arg); ok && value == result.Targets[i].Raw && quotedLiteralWord(arg) {
							result.Targets[i].Literal = true
						}
					}
				}
			}

		case *syntax.Redirect:
			role, op := TargetRead, OperationRead
			if redirectionWritesPersistent(n) {
				role, op = TargetWrite, OperationFollowLeaf
			} else if n.Op != syntax.RdrIn {
				return true
			}
			words := coverageWords([]*syntax.Word{n.Word})
			if len(words) != 1 && quotedLiteralWord(n.Word) {
				if value, ok := staticShellWord(n.Word); ok {
					words = []string{value}
				}
			}
			if len(words) == 1 {
				_, static := staticShellWord(n.Word)
				result.Targets = append(result.Targets, Target{NodeID: session.nodeIDs[n], Kind: TargetFilesystem, Role: role, Operation: op, Raw: words[0], Literal: static && quotedLiteralWord(n.Word)})
			}
		}
		return true
	})
}
func appendDirectoryTargets(result *CommandAssessment, dirs []string, role TargetRole) {
	for _, dir := range dirs {
		result.Targets = append(result.Targets, Target{Kind: TargetFilesystem, Role: role, Operation: OperationDirectory, Raw: dir})
	}
}
func appendOriginTargets(result *CommandAssessment, origins []string, role TargetRole) {
	for _, origin := range NormalizeRemoteOrigins(origins) {
		result.Targets = append(result.Targets, Target{Kind: TargetOrigin, Role: role, Raw: origin, Path: origin, State: TargetResolved})
	}
}

// Only operation-specific leaves feed authorization. KnownDirs, cwd and
// literal fallback are deliberately not consulted here.
func (m *analysisRun) resolveTargets(result CommandAssessment, cwd string, flavor pathutil.Flavor, literal, expand bool) CommandAssessment {
	var targets []Target
	seen := map[Target]bool{}
	for _, t := range result.Targets {
		targetLiteral := literal || t.Literal
		if t.Kind != TargetFilesystem {
			if !seen[t] {
				targets = append(targets, t)
				seen[t] = true
			}
			continue
		}
		raw := t.Raw
		// POSIX AST words and process argv are already decoded. Only the
		// PowerShell IR retains syntactic quotes around a literal operand.
		if !literal && flavor == pathutil.FlavorPowerShell {
			raw = trimPowerShellLiteral(raw)
		}
		unsafePath := unsafeTargetNormalization(raw) || unsafeTargetNormalization(cwd)
		if raw == "" {
			if t.Role == TargetWrite {
				result.AddGap(GapWriteTargets)
			}
			continue
		}
		if !targetLiteral && expand && strings.Contains(raw, "$") {
			opts := m.options(cwd, flavor)
			if !m.shadowed("PWD", flavor) {
				opts.Env["PWD"] = cwd
			} else {
				delete(opts.Env, "PWD")
			}
			// ExpandEnvPath cleans its inputs; do not let an unsafe
			// environment value lose its significant components first.
			for name, value := range opts.Env {
				if unsafeTargetNormalization(value) {
					delete(opts.Env, name)
				}
			}
			if value, ok := pathutil.ExpandEnvPath(raw, opts); ok {
				raw = value
			}
		}
		if !literal && flavor == pathutil.FlavorPowerShell {
			if value, ok := PowerShellFilesystemProviderPath(raw); ok {
				raw = value
			} else if IsPowerShellProviderPath(raw) {
				t.Kind = TargetProvider
				t.Path = raw
				t.State = TargetResolved
				targets = append(targets, t)
				continue
			}
		}
		// Normalization must not erase filesystem-significant components.
		// Until these forms have an exact resolver, writes require review.
		unresolved := !targetLiteral && strings.ContainsAny(raw, "$`*?[]")
		if t.Role == TargetWrite && (unsafePath || unsafeTargetNormalization(raw)) {
			unresolved = true
		}
		if t.Role == TargetWrite && (t.Operation == OperationRemoveEntry || t.Operation == OperationRenameEntry) && (strings.HasSuffix(raw, "/") || strings.HasSuffix(raw, `\`) || filepath.Base(raw) == "." || filepath.Base(raw) == "..") {
			unresolved = true
		}
		if unresolved {
			if t.Role == TargetWrite {
				result.AddGap(GapWriteTargets)
			}
		} else {
			path := pathutil.NormalizeShellPath(raw, m.options(cwd, flavor))
			if targetLiteral {
				path = normalizeLiteralProcessPath(raw, cwd, flavor)
			}
			t.LexicalPath = path
			if filepath.IsAbs(path) {
				if t.Role != TargetWrite {
					// Reads need no write authority. Candidate normalization is display-only
					// and performs no host query or inferred write classification.
					t.Path = path
					t.Dir = filepath.Dir(path)
					t.State = TargetResolved
					if t.Operation == OperationDirectory {
						t.Dir = path
					}
				} else {
					query := path
					entry := t.Operation == OperationCreateEntry || t.Operation == OperationRemoveEntry || t.Operation == OperationRenameEntry
					if entry {
						query = filepath.Dir(path)
					}
					real, err := m.resolvePath(query)
					if err == nil && filepath.IsAbs(real) && !unsafeTargetNormalization(real) {
						t.Path = real
						t.Dir = filepath.Dir(real)
						t.State = TargetResolved
						if entry {
							t.Path = filepath.Join(real, filepath.Base(path))
							t.Dir = real
						}
						if t.Operation == OperationDirectory {
							t.Dir = real
						}
						if t.Operation == OperationCreateEntry {
							parent, err := m.creationParent(path)
							if err != nil || !filepath.IsAbs(parent) || unsafeTargetNormalization(parent) {
								result.AddGap(GapWriteTargets)
								t.State = TargetUnresolved
							} else {
								t.Dir = parent
							}
						}
						if t.Operation == OperationDestination {
							result.AddGap(GapWriteTargets)
							t.State = TargetUnresolved
						}
						if real != query {
							result.KnownDirs = appendMissingShellDirs(result.KnownDirs, []string{t.Dir})
						}
					} else {
						result.AddGap(GapWriteTargets)
					}
				}
			} else if t.Role == TargetWrite {
				result.AddGap(GapWriteTargets)
			}
		}
		if !seen[t] {
			targets = append(targets, t)
			seen[t] = true
		}
	}
	result.Targets = targets
	return result
}

func quotedLiteralWord(word *syntax.Word) bool {
	if word == nil || len(word.Parts) != 1 {
		return false
	}
	switch word.Parts[0].(type) {
	case *syntax.SglQuoted, *syntax.DblQuoted:
		return true
	}
	return false
}

// unsafeTargetNormalization identifies paths that the lexical path helpers
// cannot safely canonicalize for authorization. In particular, link/.. must
// traverse the link before applying .., and whitespace is part of a filename.
// Check both separators conservatively because these helpers support Windows
// paths even when analysis runs on another platform.
func unsafeTargetNormalization(value string) bool {
	for _, part := range strings.FieldsFunc(value, func(r rune) bool { return r == '/' || r == '\\' }) {
		if part == ".." || part != strings.TrimSpace(part) {
			return true
		}
	}
	return false
}
