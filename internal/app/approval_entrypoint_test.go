package app

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/panjie/mods/internal/approval"
	"github.com/panjie/mods/internal/proto"
	toolregistry "github.com/panjie/mods/internal/tools"
	"github.com/stretchr/testify/require"
)

// Exercise the production gate with already assessed targets. In particular,
// Windows path matching must not depend on starting a PowerShell host: parsing
// is covered separately by the shell assessment conformance tests.
func TestRequestApprovalSavedDirectoryTargets(t *testing.T) {
	root := t.TempDir()
	allowed := filepath.Join(root, "allowed")
	other := filepath.Join(root, "other")
	scope := WorkingDirScope(root)
	home, err := os.UserHomeDir()
	require.NoError(t, err)
	cases := []struct {
		name           string
		paths, targets []string
		originalCwd    string
		want           bool
	}{
		{name: "same directory", paths: []string{allowed}, targets: []string{allowed}, want: true},
		{name: "subdirectory", paths: []string{allowed}, targets: []string{filepath.Join(allowed, "sub")}, want: true},
		{name: "outside directory", paths: []string{allowed}, targets: []string{other}},
		{name: "sibling prefix", paths: []string{allowed}, targets: []string{allowed + "2"}},
		{name: "all targets covered", paths: []string{allowed, other}, targets: []string{allowed, other}, want: true},
		{name: "one target uncovered", paths: []string{allowed}, targets: []string{allowed, other}},
		{name: "unknown target", paths: []string{allowed}},
		{name: "changed cwd", paths: []string{allowed}, targets: []string{allowed}, originalCwd: other, want: true},
		{name: "legacy relative path", paths: []string{"allowed"}, targets: []string{allowed}, originalCwd: root, want: true},
		{name: "legacy glob", paths: []string{filepath.Join(allowed, "*")}, targets: []string{allowed}, want: true},
		{name: "legacy glob sibling", paths: []string{filepath.Join(allowed, "*")}, targets: []string{allowed + "2"}},
		{name: "legacy home path", paths: []string{"~/.config"}, targets: []string{filepath.Join(home, ".config", "mods")}, want: true},
		{name: "legacy home sibling", paths: []string{"~/.config"}, targets: []string{filepath.Join(home, ".ssh")}},
		{name: "windows case insensitive", paths: []string{`C:\Users`}, targets: []string{`c:\users\project`}, want: true},
		{name: "windows sibling prefix", paths: []string{`C:\Users`}, targets: []string{`C:\Users2`}},
		{name: "windows drive root", paths: []string{`C:\`}, targets: []string{`C:\project`}, want: true},
		{name: "windows legacy glob", paths: []string{`C:\Users\Test\Downloads\*`}, targets: []string{`C:\Users\Test\Downloads`}, want: true},
		{name: "windows legacy glob sibling", paths: []string{`C:\Users\Test\Downloads\*`}, targets: []string{`C:\Users\Test\Downloads2`}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := &toolReviewer{reviewMode: ReviewAuto, scope: scope, reviewAvailabilityKnown: true}
			rule := Rule{Type: approval.DirAllow, Mode: AccessWrite, Paths: tc.paths}
			if tc.originalCwd != "" {
				rule.ScopeKind = approval.ScopeWorkingDir
				rule.ScopeValue = tc.originalCwd
			}
			r.rules.Add(rule)
			var trace approvalTrace
			err := r.requestApproval(reviewerDeps{
				ctx: context.Background(), accessIntent: AccessIntent{Class: AccessWrite, Dirs: tc.targets},
				// A separate temp root prevents the automatic temporary-write exception
				// from masking missing rule coverage in these synthetic fixtures.
				safeDirs: []string{t.TempDir()}, onDecision: func(got approvalTrace) { trace = got },
			}, "fs_write_file", []byte(`{"path":"target","content":"x"}`))
			if tc.want {
				require.NoError(t, err)
				require.Equal(t, "saved rule", trace.Source)
			} else {
				require.ErrorIs(t, err, errReviewUnavailable)
			}
		})
	}
}

func TestRequestApprovalIgnoresLegacyPermissions(t *testing.T) {
	root := t.TempDir()
	scope := WorkingDirScope(root)
	rules := []Rule{
		{Type: approval.EditAll, Tool: "file_edit"},
		{Type: approval.ToolAll, Tool: "mcp_update"},
		{Type: approval.ShellPrefix, Tool: "shell_run", Pattern: "rm *"},
		{Type: approval.ShellExact, Tool: "shell_run", Pattern: "rm target"},
		{Type: approval.DirAllow, Paths: []string{root}},
		{Type: approval.DirAllow, Paths: []string{root}, Mode: AccessRead},
		{Type: approval.RemoteAllow, Origins: []string{"https://api.example.com"}},
		{Type: approval.RemoteAllow, Origins: []string{"https://api.example.com"}, Mode: AccessRead},
	}
	for _, scoped := range []bool{false, true} {
		for _, rule := range rules {
			if scoped {
				rule.ScopeKind = scope.Kind
				rule.ScopeValue = scope.Value
			}
			t.Run(rule.String()+"/"+string(rule.ScopeKind), func(t *testing.T) {
				r := &toolReviewer{reviewMode: ReviewAuto, scope: scope, reviewAvailabilityKnown: true}
				r.rules.Add(rule)
				for _, name := range []string{"fs_write_file", "shell_run", "powershell_run", "process_run", "mcp_update"} {
					for _, intent := range []AccessIntent{
						{Class: AccessWrite, Dirs: []string{root}},
						{Class: AccessWrite, RemoteOrigins: []string{"https://api.example.com"}},
						{Class: AccessWrite},
					} {
						err := r.requestApproval(reviewerDeps{ctx: context.Background(), accessIntent: intent, safeDirs: []string{t.TempDir()}}, name, []byte(`{"command":"rm target"}`))
						require.ErrorIs(t, err, errReviewUnavailable, name)
					}
				}
			})
		}
	}
}

func TestRequestApprovalTargetRulesRespectModes(t *testing.T) {
	root := t.TempDir()
	scope := WorkingDirScope(root)
	for _, mode := range []ReviewMode{ReviewAuto, ReviewAlways, ReviewNever} {
		t.Run(string(mode), func(t *testing.T) {
			r := &toolReviewer{reviewMode: mode, scope: scope, reviewAvailabilityKnown: true}
			r.rules.Add(RulesForDirs([]string{root}, scope, AccessWrite)...)
			r.rules.Add(RulesForRemoteOrigins([]string{"https://api.example.com"})...)
			for _, tc := range []struct {
				name     string
				intent   AccessIntent
				reusable bool
			}{
				{"local write", AccessIntent{Class: AccessWrite, Dirs: []string{root}}, true},
				{"remote write", AccessIntent{Class: AccessWrite, RemoteOrigins: []string{"https://api.example.com"}}, true},
				{"mixed writes", AccessIntent{WriteDirs: []string{root}, WriteOrigins: []string{"https://api.example.com"}}, true},
				{"different remote origin", AccessIntent{Class: AccessWrite, RemoteOrigins: []string{"https://other.example.com"}}, false},
				{"unresolved write", AccessIntent{Class: AccessWrite, Dirs: []string{root}, UnresolvedPaths: []string{"$TARGET"}}, false},
				{"provider write", AccessIntent{Class: AccessWrite, Dirs: []string{root}, ProviderWriteTargets: []string{`HKCU:\Software\Test`}}, false},
				{"unknown write", AccessIntent{Class: AccessWrite}, false},
			} {
				t.Run(tc.name, func(t *testing.T) {
					err := r.requestApproval(reviewerDeps{ctx: context.Background(), accessIntent: tc.intent, safeDirs: []string{t.TempDir()}}, "mcp_update", nil)
					if mode == ReviewNever || mode == ReviewAuto && tc.reusable {
						require.NoError(t, err)
					} else {
						require.ErrorIs(t, err, errReviewUnavailable)
					}
				})
			}
			// Proven reads do not depend on saved write permissions, even in always.
			require.NoError(t, r.requestApproval(reviewerDeps{ctx: context.Background(), accessIntent: AccessIntent{Class: AccessRead, UnresolvedPaths: []string{"$FILE"}}}, "fs_read_file", nil))
		})
	}
}

// Verify the gate is actually wired into tool dispatch, including legacy tool
// names whose old RuleSet.Allows implementation used broader permissions.
func TestToolCallerUsesTargetApproval(t *testing.T) {
	cwd, err := os.Getwd()
	require.NoError(t, err)
	for _, name := range []string{"fs_write_file", "mcp_update"} {
		for _, tc := range []struct {
			name              string
			mode              ReviewMode
			targetRule, allow bool
		}{
			{"legacy permission", ReviewAuto, false, false},
			{"approved target", ReviewAuto, true, true},
			{"always ignores target rule", ReviewAlways, true, false},
			{"never bypasses review", ReviewNever, false, true},
		} {
			t.Run(name+"/"+tc.name, func(t *testing.T) {
				cfg := defaultConfig()
				cfg.WorkingDir = cwd
				cfg.ReviewMode = tc.mode
				cfg.InteractiveTTYAvailable = false
				m := &Mods{Config: &cfg, ctx: context.Background(), reviewer: newToolReviewer(&cfg)}
				m.reviewer.rules.Add(Rule{Type: approval.EditAll, Tool: "file_edit", ScopeKind: approval.ScopeWorkingDir, ScopeValue: cwd}, Rule{Type: approval.ToolAll, Tool: name, ScopeKind: approval.ScopeWorkingDir, ScopeValue: cwd})
				if tc.targetRule {
					m.reviewer.rules.Add(RulesForDirs([]string{cwd}, m.reviewer.scope, AccessWrite)...)
				}
				registry := toolregistry.NewRegistry()
				called := false
				require.NoError(t, registry.Register(toolregistry.Tool{
					Spec: proto.ToolSpec{Name: name}, Capabilities: toolregistry.ToolCapabilities{Mutable: true},
					IntentExtractor: func(json.RawMessage) approval.AccessIntent {
						return AccessIntent{Class: AccessWrite, Dirs: []string{cwd}}
					},
					Call: func(context.Context, json.RawMessage) (string, error) { called = true; return "ok", nil },
				}))
				_, err := m.toolCaller(registry, &cfg)(proto.ToolCallRequest{Name: name, Arguments: []byte(`{}`)})
				if tc.allow {
					require.NoError(t, err)
				} else {
					require.ErrorIs(t, err, errReviewUnavailable)
				}
				require.Equal(t, tc.allow, called)
			})
		}
	}
}
