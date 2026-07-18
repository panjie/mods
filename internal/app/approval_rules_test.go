package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/panjie/mods/internal/approval"
	"github.com/panjie/mods/internal/proto"
	toolregistry "github.com/panjie/mods/internal/tools"
	"github.com/stretchr/testify/require"
)

var testApprovalScope = WorkspaceScope("/workspace")

func testConfigForWorkspace(workspace string) *Config {
	cfg := &Config{}
	cfg.BuiltinTools.Workspace = workspace
	return cfg
}

func testShellWorkspaceScope(t *testing.T) Scope {
	t.Helper()
	if runtime.GOOS == "windows" {
		home, err := os.UserHomeDir()
		require.NoError(t, err)
		require.NotEmpty(t, home)
		return WorkspaceScope(filepath.Join(home, "mods-test-workspace"))
	}
	return testApprovalScope
}

func scopedRule(rule ApprovalRule) ApprovalRule {
	rule.ScopeKind = testApprovalScope.Kind
	rule.ScopeValue = testApprovalScope.Value
	return rule
}

func scopedRules(rules []ApprovalRule) []ApprovalRule {
	result := make([]ApprovalRule, 0, len(rules))
	for _, rule := range rules {
		result = append(result, scopedRule(rule))
	}
	return result
}

func TestShellUnknownEffectPresentationSurvivesPrebuiltAccessIntent(t *testing.T) {
	oldInputTTY := IsInputTTY
	IsInputTTY = func() bool { return true }
	t.Cleanup(func() { IsInputTTY = oldInputTTY })

	workspaceScope := testShellWorkspaceScope(t)
	registry := testReviewRegistry(t)
	mods := &Mods{
		ctx:                 context.Background(),
		Config:              testConfigForWorkspace(workspaceScope.Value),
		currentToolRegistry: registry,
		shellAnalyzer: func(string, string) shellCommandAnalysis {
			return shellCommandAnalysis{
				NeedsReview:  true,
				AffectedDirs: []string{workspaceScope.Value},
				Reason:       "classifier could not prove read-only",
				Effect:       shellEffectUnknown,
			}
		},
	}
	reviewer := &toolReviewer{
		reviewMode: ReviewAuto,
		scope:      workspaceScope,
		reviewChan: make(chan toolReviewItem, 1),
	}
	data := []byte(`{"command":"opaque-command"}`)
	intent := buildAccessIntent("shell_run", data, registry, mods.analyzeShellCommand)

	errCh := make(chan error, 1)
	go func() {
		errCh <- reviewer.requestApproval(reviewerDeps{
			ctx:              context.Background(),
			isShellExecution: registry.ShellExecution,
			analyzeShell:     mods.analyzeShellCommand,
			accessIntent:     intent,
		}, "shell_run", data)
	}()

	item := receiveReviewItem(t, reviewer.reviewChan)
	require.Contains(t, item.summary, workspaceScope.Value)
	require.Contains(t, item.summary, "Risk: unknown")
	require.Contains(t, item.summary, "classifier could not prove read-only")
	require.Equal(t, interactionToneWarning, item.presentation.tone)
	require.Equal(t, "Warning", item.presentation.toneText)
	require.Equal(t, "Run a command with unknown effects", item.presentation.headline)
	require.Contains(t, item.presentation.rows, interactionRow{Label: "Scope", Value: workspaceScope.Value})
	require.Contains(t, item.presentation.rows, interactionRow{Label: "Reason", Value: "classifier could not prove read-only"})
	require.Len(t, item.candidateRules, 1)
	require.Equal(t, AccessWrite, item.candidateRules[0].Mode)
	item.resp <- reviewResponse{approved: true}
	require.NoError(t, <-errCh)
}

func TestReviewKeys(t *testing.T) {
	t.Run("yes does not remember", func(t *testing.T) {
		reviewer := &toolReviewer{
			reviewPending: true,
			scope:         testApprovalScope,
			reviewItem: &toolReviewItem{
				resp: make(chan reviewResponse, 1),
				candidateRules: []ApprovalRule{scopedRule(ApprovalRule{
					Type:  approvalDirAllow,
					Paths: []string{"a"},
				})},
			},
		}
		handled, _ := reviewer.handleKey(tea.KeyPressMsg{Code: 'y', Text: "y"})
		require.True(t, handled)
		require.Empty(t, reviewer.rules.Snapshot())
	})

	t.Run("always remembers displayed rules", func(t *testing.T) {
		rule := ApprovalRule{
			Type:  approvalDirAllow,
			Paths: []string{"a"},
		}
		rule = scopedRule(rule)
		reviewer := &toolReviewer{
			reviewPending: true,
			scope:         testApprovalScope,
			reviewItem: &toolReviewItem{
				resp:           make(chan reviewResponse, 1),
				candidateRules: []ApprovalRule{rule},
			},
		}
		handled, _ := reviewer.handleKey(tea.KeyPressMsg{Code: 'a', Text: "a"})
		require.True(t, handled)
		require.Equal(t, []ApprovalRule{rule}, reviewer.rules.Snapshot())
	})
}

func TestReviewBannerShowsSavedRule(t *testing.T) {
	reviewer := &toolReviewer{
		reviewPending: true,
		scope:         testApprovalScope,
		reviewItem: &toolReviewItem{
			name:    "shell_run",
			args:    []byte(`{"command":"rm -f /Users/panjie/temp/demo.gif"}`),
			summary: "Risk: external mutation - affects /Users/panjie/temp",
			candidateRules: []ApprovalRule{scopedRule(ApprovalRule{
				Type:  approvalDirAllow,
				Paths: []string{"/Users/panjie/temp"},
				Mode:  AccessWrite,
			})},
		},
	}
	rendered := reviewer.renderBanner(120, makeStyles(true).Interaction)
	require.Contains(t, rendered, "Always allow")
}

func TestReviewBannerAlwaysAllowReadsForExternalRead(t *testing.T) {
	reviewer := &toolReviewer{
		reviewPending: true,
		scope:         testApprovalScope,
		reviewItem: &toolReviewItem{
			name:    "shell_run",
			args:    []byte(`{"command":"du -sh /Users/panjie/temp/*"}`),
			summary: "Risk: external read - affects /Users/panjie/temp",
			candidateRules: []ApprovalRule{scopedRule(ApprovalRule{
				Type:  approvalDirAllow,
				Paths: []string{"/Users/panjie/temp"},
				Mode:  AccessRead,
			})},
		},
	}
	rendered := reviewer.renderBanner(120, makeStyles(true).Interaction)
	require.Contains(t, rendered, "Always allow")
}

// TestReviewBannerAlwaysAllowLegacyFallback covers rules persisted before
// mode-splitting (Mode == ""): the verb is inferred from the tool name and
// review summary rather than the rule's mode.
func TestReviewBannerAlwaysAllowLegacyFallback(t *testing.T) {
	reviewer := &toolReviewer{
		reviewPending: true,
		scope:         testApprovalScope,
		reviewItem: &toolReviewItem{
			name:    "fs_read_file",
			args:    []byte(`{"path":"/Users/panjie/temp/big.bin"}`),
			summary: "Target: /Users/panjie/temp/big.bin - external read",
			candidateRules: []ApprovalRule{scopedRule(ApprovalRule{
				Type:  approvalDirAllow,
				Paths: []string{"/Users/panjie/temp"},
			})},
		},
	}
	rendered := reviewer.renderBanner(120, makeStyles(true).Interaction)
	require.Contains(t, rendered, "Always allow")
}

func TestReviewBannerShowsNoReusableRuleSummary(t *testing.T) {
	reviewer := &toolReviewer{
		reviewPending: true,
		scope:         testApprovalScope,
		reviewItem: &toolReviewItem{
			name:           "shell_run",
			args:           []byte(`{"command":"git commit -m message"}`),
			candidateRules: nil,
		},
	}
	rendered := reviewer.renderBanner(120, makeStyles(true).Interaction)
	require.NotContains(t, rendered, "Always allow")
}

func TestReviewKeysIgnoreAlwaysAllowWithoutCandidateRules(t *testing.T) {
	reviewer := &toolReviewer{
		reviewPending: true,
		scope:         testApprovalScope,
		reviewItem: &toolReviewItem{
			resp:           make(chan reviewResponse, 1),
			candidateRules: nil,
		},
	}
	handled, _ := reviewer.handleKey(tea.KeyPressMsg{Code: 'a', Text: "a"})
	require.True(t, handled)
	require.Empty(t, reviewer.rules.Snapshot())
	select {
	case <-reviewer.reviewItem.resp:
		t.Fatal("always allow without candidate rules should not approve")
	default:
	}

	handled, _ = reviewer.handleKey(tea.KeyPressMsg{Code: tea.KeyRight})
	require.True(t, handled)
	require.Equal(t, 1, reviewer.selected)
	handled, _ = reviewer.handleKey(tea.KeyPressMsg{Code: tea.KeyRight})
	require.True(t, handled)
	require.Equal(t, 2, reviewer.selected)
	handled, _ = reviewer.handleKey(tea.KeyPressMsg{Code: tea.KeyRight})
	require.True(t, handled)
	require.Equal(t, 0, reviewer.selected)
}

func TestReviewBannerStylesSelectedActionAndKeys(t *testing.T) {
	reviewer := &toolReviewer{
		reviewPending: true,
		scope:         testApprovalScope,
		reviewItem: &toolReviewItem{
			name: "shell_run",
			args: []byte(`{"command":"git commit -m message"}`),
			candidateRules: []ApprovalRule{scopedRule(ApprovalRule{
				Type:  approvalDirAllow,
				Paths: []string{"git", "commit"},
			})},
		},
	}
	styles := makeStyles(true).Interaction
	rendered := reviewer.renderBanner(120, styles)
	require.Contains(t, rendered, styles.Selected.Render("Y Allow once"))
	require.Contains(t, rendered, styles.Key.Render("N"))
}

func TestReviewBannerTruncatesSavedRule(t *testing.T) {
	reviewer := &toolReviewer{
		reviewPending: true,
		scope:         testApprovalScope,
		reviewItem: &toolReviewItem{
			name:    "shell_run",
			args:    []byte(`{"command":"cat >> ~/.config/ghostty/config.ghostty << 'EOF'\nfont-family = JetBrainsMono Nerd Font\nfont-family-bold = JetBrainsMono Nerd Font\nfont-family-italic = JetBrainsMono Nerd Font\nfont-family-bold-italic = JetBrainsMono Nerd Font\nEOF"}`),
			summary: "Risk: external mutation - affects ~/.config/ghostty",
			candidateRules: []ApprovalRule{scopedRule(ApprovalRule{
				Type:  approvalDirAllow,
				Paths: []string{"~/.config/ghostty"},
			})},
		},
	}
	rendered := reviewer.renderBanner(80, makeStyles(true).Interaction)
	lines := strings.Split(rendered, "\n")
	for _, line := range lines {
		require.LessOrEqual(t, lipgloss.Width(line), 80, line)
	}
}

func TestReviewBannerWrapsLongCommandWithoutHidingTail(t *testing.T) {
	long := "find . -name '*.go' -not -path './.git/*' -exec awk '{print $1 / $2}' {} +"
	reviewer := &toolReviewer{
		reviewPending: true,
		scope:         testApprovalScope,
		reviewItem: &toolReviewItem{
			name:    "shell_run",
			args:    []byte(`{"command":"` + long + `"}`),
			summary: "Risk: read-only",
			presentation: reviewPresentation{
				tone: interactionToneInfo, toneText: "Info", headline: "Run a read-only command",
				rows: []interactionRow{{Label: "Command", Value: long}},
			},
		},
	}
	const width = 60
	rendered := reviewer.renderBanner(width, makeStyles(true).Interaction)
	lines := strings.Split(rendered, "\n")

	for _, line := range lines {
		require.LessOrEqual(t, lipgloss.Width(line), width, line)
	}
	require.Contains(t, rendered, "{} +", "security-sensitive command tail must remain visible")
}

func TestRequestApprovalUsesInteractiveReviewAvailability(t *testing.T) {
	oldIsInputTTY := IsInputTTY
	IsInputTTY = func() bool { return false }
	t.Cleanup(func() { IsInputTTY = oldIsInputTTY })

	registry := testReviewRegistry(t)
	mods := &Mods{
		ctx:                 context.Background(),
		Config:              testConfigForWorkspace(testApprovalScope.Value),
		currentToolRegistry: registry,
	}
	mods.Config.InteractiveTTYAvailable = true
	reviewer := newToolReviewer(mods.Config)
	reviewer.reviewChan = make(chan toolReviewItem, 1)

	errCh := make(chan error, 1)
	go func() {
		errCh <- reviewer.requestApproval(testReviewerDeps(mods), "fs_write_file", []byte(`{"path":"out.txt","content":"x"}`))
	}()

	item := receiveReviewItem(t, reviewer.reviewChan)
	require.Equal(t, "fs_write_file", item.name)
	item.resp <- reviewResponse{approved: true}
	require.NoError(t, <-errCh)
}

func TestRequestApprovalRawModeIgnoresInteractiveReviewAvailability(t *testing.T) {
	oldIsInputTTY := IsInputTTY
	IsInputTTY = func() bool { return false }
	t.Cleanup(func() { IsInputTTY = oldIsInputTTY })

	cfg := testConfigForWorkspace(testApprovalScope.Value)
	cfg.Raw = true
	cfg.InteractiveTTYAvailable = true
	reviewer := newToolReviewer(cfg)

	err := reviewer.requestApproval(
		reviewerDeps{ctx: context.Background(), accessIntent: AccessIntent{Class: AccessWrite}},
		"fs_write_file",
		[]byte(`{"path":"out.txt","content":"x"}`),
	)
	require.ErrorIs(t, err, errReviewUnavailable)
}

func TestRequestApprovalRawTTYModeDoesNotWaitForReview(t *testing.T) {
	oldIsInputTTY := IsInputTTY
	IsInputTTY = func() bool { return true }
	t.Cleanup(func() { IsInputTTY = oldIsInputTTY })

	cfg := testConfigForWorkspace(testApprovalScope.Value)
	cfg.Raw = true
	reviewer := newToolReviewer(cfg)
	reviewer.reviewChan = make(chan toolReviewItem, 1)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()

	err := reviewer.requestApproval(
		reviewerDeps{ctx: ctx, accessIntent: AccessIntent{Class: AccessWrite}},
		"fs_write_file",
		[]byte(`{"path":"out.txt","content":"x"}`),
	)
	require.ErrorIs(t, err, errReviewUnavailable)
	require.Empty(t, reviewer.reviewChan)
}

func TestRequestApprovalTTYInputWithoutReviewUIIsUnavailable(t *testing.T) {
	oldIsInputTTY := IsInputTTY
	IsInputTTY = func() bool { return true }
	t.Cleanup(func() { IsInputTTY = oldIsInputTTY })

	cfg := testConfigForWorkspace(testApprovalScope.Value)
	reviewer := newToolReviewer(cfg)
	reviewer.reviewChan = make(chan toolReviewItem, 1)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()

	err := reviewer.requestApproval(
		reviewerDeps{ctx: ctx, accessIntent: AccessIntent{Class: AccessWrite}},
		"fs_write_file",
		[]byte(`{"path":"out.txt","content":"x"}`),
	)
	require.ErrorIs(t, err, errReviewUnavailable)
	require.Empty(t, reviewer.reviewChan)
}

func TestReviewPolicyNonTTY(t *testing.T) {
	oldIsInputTTY := isInputTTY
	oldInputTTY := IsInputTTY
	isInputTTY = func() bool { return false }
	IsInputTTY = func() bool { return false }
	t.Cleanup(func() { isInputTTY = oldIsInputTTY })
	t.Cleanup(func() { IsInputTTY = oldInputTTY })

	scope := testShellWorkspaceScope(t)
	mods := &Mods{
		ctx:    context.Background(),
		Config: testConfigForWorkspace(scope.Value),
	}
	registry := testReviewRegistry(t)
	mods.currentToolRegistry = registry

	t.Run("review never allows mutable tool", func(t *testing.T) {
		reviewer := &toolReviewer{reviewMode: ReviewNever, scope: scope}
		intent := buildAccessIntent("fs_write_file", []byte(`{"path":"out.txt","content":"x"}`), registry, nil)
		require.NoError(t, reviewer.requestApproval(
			reviewerDeps{ctx: context.Background(), accessIntent: intent},
			"fs_write_file", []byte(`{"path":"out.txt","content":"x"}`),
		))
	})

	t.Run("auto denies write without interactive approval", func(t *testing.T) {
		reviewer := &toolReviewer{reviewMode: ReviewAuto, scope: scope}
		err := reviewer.requestApproval(testReviewerDeps(mods), "fs_write_file", []byte(`{"path":"out.txt","content":"x"}`))
		require.ErrorIs(t, err, errReviewUnavailable)
	})

	t.Run("auto allows read-only filesystem tool", func(t *testing.T) {
		reviewer := &toolReviewer{reviewMode: ReviewAuto, scope: scope}
		intent := buildAccessIntent("fs_read_file", []byte(`{"path":"README.md"}`), registry, nil)
		require.NoError(t, reviewer.requestApproval(
			reviewerDeps{ctx: context.Background(), accessIntent: intent},
			"fs_read_file", []byte(`{"path":"README.md"}`),
		))
	})

	t.Run("auto allows read-only tool without directory intent", func(t *testing.T) {
		reviewer := &toolReviewer{reviewMode: ReviewAuto, scope: scope}
		intent := buildAccessIntent("web_search", []byte(`{"query":"mods v2.5.0"}`), registry, nil)
		require.Equal(t, AccessRead, intent.Class)
		err := reviewer.requestApproval(reviewerDeps{ctx: context.Background(), accessIntent: intent}, "web_search", []byte(`{"query":"mods v2.5.0"}`))
		require.NoError(t, err)
	})

	t.Run("auto requires review for shell command when classifier unavailable", func(t *testing.T) {
		reviewer := &toolReviewer{reviewMode: ReviewAuto, scope: scope}
		command := "echo ok"
		if runtime.GOOS == "windows" {
			command = "Write-Output ok"
		}
		err := reviewer.requestApproval(testReviewerDeps(mods), "shell_run", []byte(fmt.Sprintf(`{"command":%q}`, command)))
		require.NoError(t, err)
	})

	t.Run("auto requires approval for read-only shell parent traversal in non-TTY", func(t *testing.T) {
		reviewer := &toolReviewer{reviewMode: ReviewAuto, scope: scope}
		err := reviewer.requestApproval(testReviewerDeps(mods), "shell_run", []byte(`{"command":"cat ../sibling/file"}`))
		require.ErrorIs(t, err, errReviewUnavailable)
	})

	t.Run("auto requires approval for read-only shell tilde path in non-TTY", func(t *testing.T) {
		reviewer := &toolReviewer{reviewMode: ReviewAuto, scope: scope}
		err := reviewer.requestApproval(testReviewerDeps(mods), "shell_run", []byte(`{"command":"cat ~/Downloads/file"}`))
		require.ErrorIs(t, err, errReviewUnavailable)
	})

	t.Run("auto denies compound shell after read-only prefix", func(t *testing.T) {
		reviewer := &toolReviewer{reviewMode: ReviewAuto, scope: scope}
		err := reviewer.requestApproval(testReviewerDeps(mods), "shell_run", []byte(`{"command":"echo ok; rm -rf ."}`))
		require.ErrorIs(t, err, errReviewUnavailable)
	})

	t.Run("auto requires approval for read-only powershell touching external path in non-TTY", func(t *testing.T) {
		reviewer := &toolReviewer{reviewMode: ReviewAuto, scope: scope}
		err := reviewer.requestApproval(testReviewerDeps(mods), "powershell_run", []byte(`{"command":"Get-ChildItem C:\\Users"}`))
		require.ErrorIs(t, err, errReviewUnavailable)
	})

	t.Run("auto denies nested powershell", func(t *testing.T) {
		reviewer := &toolReviewer{reviewMode: ReviewAuto, scope: scope}
		err := reviewer.requestApproval(testReviewerDeps(mods), "powershell_run", []byte(`{"command":"powershell -EncodedCommand AAAA"}`))
		require.ErrorIs(t, err, errReviewUnavailable)
	})

	t.Run("auto requires approval for read-only powershell pipeline touching external path in non-TTY", func(t *testing.T) {
		reviewer := &toolReviewer{reviewMode: ReviewAuto, scope: scope}
		err := reviewer.requestApproval(testReviewerDeps(mods), "powershell_run", []byte(`{"command":"Get-ChildItem C:\\Users | Where-Object { $_.Name -like 'p*' }"}`))
		require.ErrorIs(t, err, errReviewUnavailable)
	})

	t.Run("auto denies mutating powershell command without interactive approval", func(t *testing.T) {
		reviewer := &toolReviewer{reviewMode: ReviewAuto, scope: scope}
		err := reviewer.requestApproval(testReviewerDeps(mods), "powershell_run", []byte(`{"command":"Remove-Item C:\\tmp\\old.txt"}`))
		require.ErrorIs(t, err, errReviewUnavailable)
	})

	t.Run("saved rule allows matching powershell command", func(t *testing.T) {
		mods.shellAnalyzer = func(string, string) shellCommandAnalysis {
			return shellCommandAnalysis{NeedsReview: true, AffectedDirs: []string{"C:\\Users"}}
		}
		t.Cleanup(func() { mods.shellAnalyzer = nil })
		reviewer := &toolReviewer{reviewMode: ReviewAlways, scope: testApprovalScope}
		reviewer.rules.Add(scopedRule(ApprovalRule{Type: approvalDirAllow, Paths: []string{"C:\\Users"}}))
		err := reviewer.requestApproval(testReviewerDeps(mods), "powershell_run", []byte(`{"command":"Remove-Item C:\\Users\\old.txt"}`))
		require.NoError(t, err)
	})

	t.Run("saved powershell prefix does not allow pipeline mutation", func(t *testing.T) {
		mods.shellAnalyzer = func(string, string) shellCommandAnalysis {
			return shellCommandAnalysis{NeedsReview: true, AffectedDirs: []string{"C:\\Windows"}}
		}
		t.Cleanup(func() { mods.shellAnalyzer = nil })
		reviewer := &toolReviewer{reviewMode: ReviewAlways, scope: testApprovalScope}
		reviewer.rules.Add(scopedRule(ApprovalRule{Type: approvalDirAllow, Paths: []string{"C:\\Users"}}))
		err := reviewer.requestApproval(testReviewerDeps(mods), "powershell_run", []byte(`{"command":"Get-ChildItem C:\\Windows | Remove-Item -Recurse"}`))
		require.ErrorIs(t, err, errReviewUnavailable)
	})

	t.Run("always denies read-only tool without interactive approval", func(t *testing.T) {
		reviewer := &toolReviewer{reviewMode: ReviewAlways, scope: testApprovalScope}
		err := reviewer.requestApproval(testReviewerDeps(mods), "fs_read_file", []byte(`{"path":"README.md"}`))
		require.ErrorIs(t, err, errReviewUnavailable)
	})

	t.Run("always reviews read-only tool without directory intent", func(t *testing.T) {
		reviewer := &toolReviewer{reviewMode: ReviewAlways, scope: testApprovalScope}
		intent := buildAccessIntent("web_search", []byte(`{"query":"mods v2.5.0"}`), registry, nil)
		require.Equal(t, AccessRead, intent.Class)
		err := reviewer.requestApproval(reviewerDeps{ctx: context.Background(), accessIntent: intent}, "web_search", []byte(`{"query":"mods v2.5.0"}`))
		require.ErrorIs(t, err, errReviewUnavailable)
	})

	t.Run("saved rule allows matching tool", func(t *testing.T) {
		reviewer := &toolReviewer{reviewMode: ReviewAlways, scope: testApprovalScope}
		reviewer.rules.Add(scopedRule(ApprovalRule{Type: approvalDirAllow, Paths: []string{testApprovalScope.Value}, Mode: AccessWrite}))
		intent := AccessIntent{Class: AccessWrite, Dirs: []string{testApprovalScope.Value}}
		err := reviewer.requestApproval(reviewerDeps{ctx: context.Background(), accessIntent: intent}, "fs_write_file", []byte(`{"path":"out.txt","content":"x"}`))
		require.NoError(t, err)
	})
}

func TestShellReviewFlowUsesLLMAnalysis(t *testing.T) {
	oldInputTTY := IsInputTTY
	IsInputTTY = func() bool { return true }
	t.Cleanup(func() { IsInputTTY = oldInputTTY })

	registry := testReviewRegistry(t)
	workspaceScope := testShellWorkspaceScope(t)

	t.Run("auto skips review when LLM says no review", func(t *testing.T) {
		mods := &Mods{
			ctx:                 context.Background(),
			Config:              testConfigForWorkspace(workspaceScope.Value),
			currentToolRegistry: registry,
			shellAnalyzer: func(string, string) shellCommandAnalysis {
				return shellCommandAnalysis{NeedsReview: false, Reason: "read-only"}
			},
		}
		reviewer := &toolReviewer{reviewMode: ReviewAuto, scope: workspaceScope}
		err := reviewer.requestApproval(testReviewerDeps(mods), "shell_run", []byte(`{"command":"ls"}`))
		require.NoError(t, err)
	})

	t.Run("candidate rules come directly from LLM dirs", func(t *testing.T) {
		externalDir := `C:\mods-external\cache`
		mods := &Mods{
			ctx:                 context.Background(),
			Config:              &Config{},
			currentToolRegistry: registry,
			shellAnalyzer: func(string, string) shellCommandAnalysis {
				return shellCommandAnalysis{
					NeedsReview:  true,
					AffectedDirs: []string{externalDir},
					Reason:       "writes output",
				}
			},
		}
		reviewer := &toolReviewer{
			reviewMode: ReviewAuto,
			scope:      testApprovalScope,
			reviewChan: make(chan toolReviewItem, 1),
		}
		errCh := make(chan error, 1)
		go func() {
			errCh <- reviewer.requestApproval(testReviewerDeps(mods), "shell_run", []byte(`{"command":"some unsupported writer"}`))
		}()

		item := receiveReviewItem(t, reviewer.reviewChan)
		require.Len(t, item.candidateRules, 1)
		require.Equal(t, approvalDirAllow, item.candidateRules[0].Type)
		require.Equal(t, []string{externalDir}, item.candidateRules[0].Paths)
		item.resp <- reviewResponse{approved: true}
		require.NoError(t, <-errCh)
	})

	t.Run("review summary surfaces LLM reason via accessIntent", func(t *testing.T) {
		// Regression for P7: when requestApproval receives an accessIntent
		// with a non-empty Class (the common path via buildAccessIntent),
		// the Reason field must propagate into the review summary so the
		// user sees *why* the command needs review instead of a bare
		// "Risk: unknown".
		mods := &Mods{
			ctx:                 context.Background(),
			Config:              &Config{},
			currentToolRegistry: registry,
		}
		intent := AccessIntent{
			Class:  AccessWrite,
			Reason: "installs nodejs via scoop",
		}
		reviewer := &toolReviewer{
			reviewMode: ReviewAuto,
			scope:      testApprovalScope,
			reviewChan: make(chan toolReviewItem, 1),
		}
		errCh := make(chan error, 1)
		go func() {
			errCh <- reviewer.requestApproval(reviewerDeps{
				ctx:              mods.ctx,
				isShellExecution: registry.ShellExecution,
				analyzeShell:     mods.analyzeShellCommand,
				accessIntent:     intent,
			}, "powershell_run", []byte(`{"command":"scoop install nodejs"}`))
		}()

		item := receiveReviewItem(t, reviewer.reviewChan)
		require.Contains(t, item.summary, "installs nodejs via scoop",
			"review summary must surface the LLM reason carried by accessIntent")
		item.resp <- reviewResponse{approved: true}
		require.NoError(t, <-errCh)
	})

	t.Run("candidate dirs expand tilde before saving", func(t *testing.T) {
		home, err := os.UserHomeDir()
		require.NoError(t, err)
		require.NotEmpty(t, home)

		mods := &Mods{
			ctx:                 context.Background(),
			Config:              &Config{},
			currentToolRegistry: registry,
			shellAnalyzer: func(string, string) shellCommandAnalysis {
				return shellCommandAnalysis{
					NeedsReview:  true,
					AffectedDirs: []string{"~/.ssh"},
					Reason:       "writes ssh config",
				}
			},
		}
		reviewer := &toolReviewer{
			reviewMode: ReviewAuto,
			scope:      testApprovalScope,
			reviewChan: make(chan toolReviewItem, 1),
		}
		errCh := make(chan error, 1)
		go func() {
			errCh <- reviewer.requestApproval(testReviewerDeps(mods), "shell_run", []byte(`{"command":"rm -rf ~/.ssh"}`))
		}()

		item := receiveReviewItem(t, reviewer.reviewChan)
		require.Len(t, item.candidateRules, 1)
		require.Equal(t, []string{filepath.Join(home, ".ssh")}, item.candidateRules[0].Paths)
		item.resp <- reviewResponse{approved: true}
		require.NoError(t, <-errCh)
	})

	t.Run("shell glob affected dirs collapse before review and saving", func(t *testing.T) {
		home, err := os.UserHomeDir()
		require.NoError(t, err)
		require.NotEmpty(t, home)
		downloads := filepath.Join(home, "Downloads")

		mods := &Mods{
			ctx:                 context.Background(),
			Config:              &Config{},
			currentToolRegistry: registry,
			shellAnalyzer: func(string, string) shellCommandAnalysis {
				return shellCommandAnalysis{
					NeedsReview:  false,
					AffectedDirs: []string{"~/Downloads/*"},
					Reason:       "read-only",
				}
			},
		}
		reviewer := &toolReviewer{
			reviewMode: ReviewAuto,
			scope:      testApprovalScope,
			reviewChan: make(chan toolReviewItem, 1),
		}
		errCh := make(chan error, 1)
		go func() {
			errCh <- reviewer.requestApproval(testReviewerDeps(mods), "shell_run", []byte(`{"command":"du -sk ~/Downloads/* 2>/dev/null | sort -rn | head -20"}`))
		}()

		item := receiveReviewItem(t, reviewer.reviewChan)
		require.Contains(t, item.summary, downloads)
		require.NotContains(t, item.summary, downloads+string(filepath.Separator)+"*")
		require.Len(t, item.candidateRules, 1)
		require.Equal(t, approvalDirAllow, item.candidateRules[0].Type)
		require.Equal(t, AccessRead, item.candidateRules[0].Mode)
		require.Equal(t, []string{downloads}, item.candidateRules[0].Paths)
		item.resp <- reviewResponse{approved: true}
		require.NoError(t, <-errCh)
	})

	t.Run("redirection target dirs fill unknown shell risk", func(t *testing.T) {
		if runtime.GOOS == "windows" {
			t.Skip("POSIX heredoc redirection is not the Windows shell_run syntax")
		}
		targetDir := "/home/panjie/dev/myconfigs/vim"

		mods := &Mods{
			ctx:                 context.Background(),
			Config:              &Config{},
			currentToolRegistry: registry,
			shellAnalyzer: func(string, string) shellCommandAnalysis {
				return shellCommandAnalysis{NeedsReview: true, Reason: "writes heredoc"}
			},
		}
		reviewer := &toolReviewer{
			reviewMode: ReviewAuto,
			scope:      testApprovalScope,
			reviewChan: make(chan toolReviewItem, 1),
		}
		errCh := make(chan error, 1)
		go func() {
			errCh <- reviewer.requestApproval(testReviewerDeps(mods), "shell_run", []byte(`{"command":"cat > /home/panjie/dev/myconfigs/vim/vimrc <<'EOF'\nset path=/\n/this/looks/like/a/path\nEOF"}`))
		}()

		item := receiveReviewItem(t, reviewer.reviewChan)
		require.Contains(t, item.summary, "external mutation")
		require.Contains(t, item.summary, targetDir)
		require.NotEqual(t, "Risk: external mutation - affects /", item.summary)
		require.NotContains(t, item.summary, "unknown")
		require.Len(t, item.candidateRules, 1)
		require.Equal(t, []string{targetDir}, item.candidateRules[0].Paths)
		item.resp <- reviewResponse{approved: true}
		require.NoError(t, <-errCh)
	})

	t.Run("always still prompts when LLM says no review", func(t *testing.T) {
		mods := &Mods{
			ctx:                 context.Background(),
			Config:              testConfigForWorkspace(workspaceScope.Value),
			currentToolRegistry: registry,
			shellAnalyzer: func(string, string) shellCommandAnalysis {
				return shellCommandAnalysis{NeedsReview: false, Reason: "read-only"}
			},
		}
		reviewer := &toolReviewer{
			reviewMode: ReviewAlways,
			scope:      workspaceScope,
			reviewChan: make(chan toolReviewItem, 1),
		}
		errCh := make(chan error, 1)
		go func() {
			errCh <- reviewer.requestApproval(testReviewerDeps(mods), "shell_run", []byte(`{"command":"ls"}`))
		}()

		item := receiveReviewItem(t, reviewer.reviewChan)
		require.Empty(t, item.candidateRules)
		require.Contains(t, item.summary, "Risk: read-only - affects "+workspaceScope.Value)
		item.resp <- reviewResponse{approved: true}
		require.NoError(t, <-errCh)
	})

	t.Run("saved rule allows matching LLM dirs", func(t *testing.T) {
		mods := &Mods{
			ctx:                 context.Background(),
			Config:              &Config{},
			currentToolRegistry: registry,
			shellAnalyzer: func(string, string) shellCommandAnalysis {
				return shellCommandAnalysis{
					NeedsReview:  true,
					AffectedDirs: []string{"/tmp/cache/subdir"},
				}
			},
		}
		reviewer := &toolReviewer{reviewMode: ReviewAlways, scope: testApprovalScope}
		reviewer.rules.Add(scopedRule(ApprovalRule{Type: approvalDirAllow, Paths: []string{"/tmp/cache"}}))
		err := reviewer.requestApproval(testReviewerDeps(mods), "shell_run", []byte(`{"command":"some unsupported writer"}`))
		require.NoError(t, err)
	})

	t.Run("legacy tilde dir rule matches expanded affected dirs", func(t *testing.T) {
		home, err := os.UserHomeDir()
		require.NoError(t, err)
		require.NotEmpty(t, home)

		mods := &Mods{
			ctx:                 context.Background(),
			Config:              &Config{},
			currentToolRegistry: registry,
			shellAnalyzer: func(string, string) shellCommandAnalysis {
				return shellCommandAnalysis{
					NeedsReview:  true,
					AffectedDirs: []string{filepath.Join(home, ".config", "mods")},
				}
			},
		}
		reviewer := &toolReviewer{reviewMode: ReviewAlways, scope: testApprovalScope}
		reviewer.rules.Add(scopedRule(ApprovalRule{Type: approvalDirAllow, Paths: []string{"~/.config"}}))
		err = reviewer.requestApproval(testReviewerDeps(mods), "shell_run", []byte(`{"command":"some unsupported writer"}`))
		require.NoError(t, err)
	})

	t.Run("external read needs review even when LLM says no review", func(t *testing.T) {
		mods := &Mods{
			ctx:                 context.Background(),
			Config:              &Config{},
			currentToolRegistry: registry,
			shellAnalyzer: func(string, string) shellCommandAnalysis {
				return shellCommandAnalysis{NeedsReview: false, AffectedDirs: []string{"/etc"}, Reason: "read-only"}
			},
		}
		reviewer := &toolReviewer{
			reviewMode: ReviewAuto,
			scope:      testApprovalScope,
			reviewChan: make(chan toolReviewItem, 1),
		}
		errCh := make(chan error, 1)
		go func() {
			errCh <- reviewer.requestApproval(testReviewerDeps(mods), "shell_run", []byte(`{"command":"unknown-reader /etc/passwd"}`))
		}()

		item := receiveReviewItem(t, reviewer.reviewChan)
		require.Equal(t, "shell_run", item.name)
		// External read triggers review (H1 fix); the label says "external
		// read" because the LLM classified it as not mutable but it touches
		// /etc — outside the test scope /workspace.
		require.Contains(t, item.summary, "external read")
		require.Len(t, item.candidateRules, 1)
		require.Equal(t, approvalDirAllow, item.candidateRules[0].Type)
		require.Equal(t, AccessRead, item.candidateRules[0].Mode)
		require.Equal(t, []string{"/etc"}, item.candidateRules[0].Paths)
		item.resp <- reviewResponse{approved: true}
		require.NoError(t, <-errCh)
	})

	t.Run("fs external read offers directory read rule", func(t *testing.T) {
		home, err := os.UserHomeDir()
		require.NoError(t, err)
		require.NotEmpty(t, home)

		downloads := filepath.Join(home, "Downloads")
		target := filepath.Join(downloads, "Codex.dmg")
		reviewer := &toolReviewer{
			reviewMode: ReviewAuto,
			scope:      testApprovalScope,
			reviewChan: make(chan toolReviewItem, 1),
		}
		deps := reviewerDeps{
			ctx:          context.Background(),
			accessIntent: AccessIntent{Class: AccessRead, Dirs: []string{downloads}},
		}

		errCh := make(chan error, 1)
		go func() {
			errCh <- reviewer.requestApproval(deps, "fs_stat", []byte(fmt.Sprintf(`{"path":%q}`, target)))
		}()

		item := receiveReviewItem(t, reviewer.reviewChan)
		require.Equal(t, "fs_stat", item.name)
		require.Len(t, item.candidateRules, 1)
		require.Equal(t, approvalDirAllow, item.candidateRules[0].Type)
		require.Equal(t, AccessRead, item.candidateRules[0].Mode)
		require.Equal(t, []string{downloads}, item.candidateRules[0].Paths)
		require.Contains(t, item.summary, downloads)
		require.NotContains(t, item.summary, "Codex.dmg")

		item.resp <- reviewResponse{approved: true}
		require.NoError(t, <-errCh)
	})

	t.Run("saved fs directory read rule does not allow writes", func(t *testing.T) {
		home, err := os.UserHomeDir()
		require.NoError(t, err)
		require.NotEmpty(t, home)

		downloads := filepath.Join(home, "Downloads")
		reviewer := &toolReviewer{reviewMode: ReviewAuto, scope: testApprovalScope}
		reviewer.rules.Add(scopedRule(ApprovalRule{
			Type:  approvalDirAllow,
			Paths: []string{downloads},
			Mode:  AccessRead,
		}))

		readDeps := reviewerDeps{
			ctx:          context.Background(),
			accessIntent: AccessIntent{Class: AccessRead, Dirs: []string{downloads}},
		}
		require.NoError(t, reviewer.requestApproval(readDeps, "fs_stat", []byte(`{"path":"/unused/file"}`)))

		writeDeps := reviewerDeps{
			ctx:          context.Background(),
			accessIntent: AccessIntent{Class: AccessWrite, Dirs: []string{downloads}},
		}
		err = reviewer.requestApproval(writeDeps, "fs_write_file", []byte(`{"path":"/unused/file","content":"x"}`))
		require.ErrorIs(t, err, errReviewUnavailable)
	})
}

func TestReviewUnavailableIsFatal(t *testing.T) {
	mods := &Mods{
		Config:   &Config{},
		reviewer: &toolReviewer{},
		ctx:      context.Background(),
	}
	_, cmd := mods.Update(streamEventMsg{
		kind:   streamEventToolCalls,
		runner: newStreamRunner(staticStream{}, nil, nil, func(err error) tea.Msg { return modsError{Err: err} }),
		results: []proto.ToolCallStatus{{
			Name: "fs_write_file",
			Err:  fmt.Errorf("%w: fs_write_file requires approval", errReviewUnavailable),
		}},
	})

	msg := cmd()
	errMsg, ok := msg.(modsError)
	require.True(t, ok)
	require.Equal(t, "Tool execution requires review.", errMsg.ReasonText)
	require.True(t, errors.Is(errMsg.Err, errReviewUnavailable))
}

func testReviewRegistry(t *testing.T) *toolregistry.Registry {
	t.Helper()
	registry := toolregistry.NewRegistry()
	for _, tool := range []toolregistry.Tool{
		{Spec: proto.ToolSpec{Name: "fs_read_file"}, Capabilities: toolregistry.ToolCapabilities{ReadOnly: true}, Call: noopToolCall},
		{Spec: proto.ToolSpec{Name: "fs_write_file"}, Capabilities: toolregistry.ToolCapabilities{Mutable: true}, Call: noopToolCall},
		{Spec: proto.ToolSpec{Name: "shell_run"}, Capabilities: toolregistry.ToolCapabilities{Mutable: true, ShellExecution: true}, Call: noopToolCall},
		{Spec: proto.ToolSpec{Name: "powershell_run"}, Capabilities: toolregistry.ToolCapabilities{Mutable: true, ShellExecution: true}, Call: noopToolCall},
		{Spec: proto.ToolSpec{Name: "web_search"}, Capabilities: toolregistry.ToolCapabilities{ReadOnly: true}, Call: noopToolCall},
	} {
		require.NoError(t, registry.Register(tool))
	}
	return registry
}

func noopToolCall(context.Context, json.RawMessage) (string, error) { return "", nil }

func receiveReviewItem(t *testing.T, ch <-chan toolReviewItem) toolReviewItem {
	t.Helper()

	select {
	case item := <-ch:
		return item
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for review item")
		return toolReviewItem{}
	}
}

// testReviewerDeps builds reviewerDeps from a *Mods in the shape these
// tests historically construct. Prefer constructing reviewerDeps
// directly in new tests; this helper keeps the diff small while the
// existing tests migrate off the *Mods signature.
//
// Note: we capture mods.analyzeShellCommand (the method value) rather
// than mods.shellAnalyzer (the field) so the local regex fallback
// inside the method still runs when shellAnalyzer is unset.
func testReviewerDeps(mods *Mods) reviewerDeps {
	deps := reviewerDeps{ctx: mods.ctx}
	if mods.currentToolRegistry != nil {
		deps.isShellExecution = mods.currentToolRegistry.ShellExecution
	}
	deps.analyzeShell = mods.analyzeShellCommand
	return deps
}

func TestShellAnalysisParsing(t *testing.T) {
	t.Run("valid json with review and dirs", func(t *testing.T) {
		analysis, ok := parseShellAnalysisResponse(`{"needs_review":true,"affected_dirs":["/tmp/cache"],"reason":"writes file","effect":"write"}`)
		require.True(t, ok)
		require.True(t, analysis.NeedsReview)
		require.Equal(t, []string{"/tmp/cache"}, analysis.AffectedDirs)
		require.Equal(t, "writes file", analysis.Reason)
		require.Equal(t, shellEffectWrite, analysis.Effect)
	})

	t.Run("valid json without review", func(t *testing.T) {
		analysis, ok := parseShellAnalysisResponse(`{"needs_review":false,"affected_dirs":[],"reason":"read-only","effect":"read"}`)
		require.True(t, ok)
		require.False(t, analysis.NeedsReview)
		require.Empty(t, analysis.AffectedDirs)
		require.Equal(t, shellEffectRead, analysis.Effect)
	})

	t.Run("old json without effect remains compatible", func(t *testing.T) {
		analysis, ok := parseShellAnalysisResponse(`{"needs_review":false,"affected_dirs":[],"reason":"legacy read-only"}`)
		require.True(t, ok)
		require.False(t, analysis.NeedsReview)
		require.Equal(t, shellEffect(""), analysis.Effect)
	})

	t.Run("rejects write effect without review", func(t *testing.T) {
		_, ok := parseShellAnalysisResponse(`{"needs_review":false,"affected_dirs":[],"reason":"contradictory","effect":"write"}`)
		require.False(t, ok)
	})

	t.Run("rejects read effect with review", func(t *testing.T) {
		_, ok := parseShellAnalysisResponse(`{"needs_review":true,"affected_dirs":[],"reason":"contradictory","effect":"read"}`)
		require.False(t, ok)
	})

	t.Run("rejects unknown effect without review", func(t *testing.T) {
		_, ok := parseShellAnalysisResponse(`{"needs_review":false,"affected_dirs":[],"reason":"contradictory","effect":"unknown"}`)
		require.False(t, ok)
	})

	t.Run("thinking before json", func(t *testing.T) {
		raw := `<think>I should classify this as read-only.</think>
{"needs_review":false,"affected_dirs":[],"reason":"lists directory contents only"}`
		analysis, ok := parseShellAnalysisResponse(raw)
		require.True(t, ok)
		require.False(t, analysis.NeedsReview)
		require.Equal(t, "lists directory contents only", analysis.Reason)
	})

	t.Run("fenced json", func(t *testing.T) {
		raw := "```json\n{\"needs_review\":true,\"affected_dirs\":[\"/tmp/out\"],\"reason\":\"writes output\"}\n```"
		analysis, ok := parseShellAnalysisResponse(raw)
		require.True(t, ok)
		require.True(t, analysis.NeedsReview)
		require.Equal(t, []string{"/tmp/out"}, analysis.AffectedDirs)
	})

	t.Run("mixed text with balanced json", func(t *testing.T) {
		raw := `analysis text {"ignored":true}
final answer: {"needs_review":false,"affected_dirs":[],"reason":"read-only with {braces} in text"} thanks`
		analysis, ok := parseShellAnalysisResponse(raw)
		require.True(t, ok)
		require.False(t, analysis.NeedsReview)
		require.Equal(t, "read-only with {braces} in text", analysis.Reason)
	})

	t.Run("malformed json falls back safe", func(t *testing.T) {
		_, ok := parseShellAnalysisResponse(`YES`)
		require.False(t, ok)
		require.True(t, defaultShellCommandAnalysis().NeedsReview)
	})

	t.Run("legacy yes no parser still works", func(t *testing.T) {
		require.True(t, classifyResponse("YES"))
		require.False(t, classifyResponse("NO"))
	})
}

func TestShellCandidateRulesUseLLMAffectedDirs(t *testing.T) {
	require.Empty(t, approval.RulesFor("shell_run", []byte(`{"command":"rm -rf /tmp/cache/foo"}`), testApprovalScope))

	t.Run("write command stamps write mode", func(t *testing.T) {
		rules := RulesForDirs([]string{"/tmp/cache", "/var/tmp"}, testApprovalScope, AccessWrite)
		require.Len(t, rules, 1)
		require.Equal(t, approvalDirAllow, rules[0].Type)
		require.Equal(t, AccessWrite, rules[0].Mode)
		require.ElementsMatch(t, []string{"/tmp/cache", "/var/tmp"}, rules[0].Paths)
	})

	t.Run("read command stamps read mode", func(t *testing.T) {
		rules := RulesForDirs([]string{"/etc"}, testApprovalScope, AccessRead)
		require.Len(t, rules, 1)
		require.Equal(t, AccessRead, rules[0].Mode)
	})
}

func TestDirAllowMatching(t *testing.T) {
	t.Run("allows when target is within allowed dir", func(t *testing.T) {
		rule := scopedRule(ApprovalRule{Type: approvalDirAllow, Paths: []string{"/tmp/cache/"}})
		var rs approvalRuleSet
		rs.Add(rule)
		require.True(t, rs.Allows("shell_run", []byte(`{"command":"rm -rf /tmp/cache/foo"}`), testApprovalScope))
	})

	t.Run("allows when target equals allowed dir", func(t *testing.T) {
		rule := scopedRule(ApprovalRule{Type: approvalDirAllow, Paths: []string{"/tmp/"}})
		var rs approvalRuleSet
		rs.Add(rule)
		require.True(t, rs.Allows("shell_run", []byte(`{"command":"rm /tmp/foo"}`), testApprovalScope))
	})

	t.Run("denies when target is outside allowed dir", func(t *testing.T) {
		rule := scopedRule(ApprovalRule{Type: approvalDirAllow, Paths: []string{"/tmp/cache/"}})
		var rs approvalRuleSet
		rs.Add(rule)
		require.False(t, rs.Allows("shell_run", []byte(`{"command":"rm -rf /etc/passwd"}`), testApprovalScope))
	})

	t.Run("denies partial match", func(t *testing.T) {
		rule := scopedRule(ApprovalRule{Type: approvalDirAllow, Paths: []string{"/tmp/cache/"}})
		var rs approvalRuleSet
		rs.Add(rule)
		require.False(t, rs.Allows("shell_run", []byte(`{"command":"rm /tmp/other"}`), testApprovalScope))
	})

	t.Run("allows when all targets are within any allowed dir", func(t *testing.T) {
		rule := scopedRule(ApprovalRule{Type: approvalDirAllow, Paths: []string{"/tmp/", "/var/cache/"}})
		var rs approvalRuleSet
		rs.Add(rule)
		require.True(t, rs.Allows("shell_run", []byte(`{"command":"cp /tmp/a /var/cache/b"}`), testApprovalScope))
	})

	t.Run("denies when any target is outside allowed dirs", func(t *testing.T) {
		rule := scopedRule(ApprovalRule{Type: approvalDirAllow, Paths: []string{"/tmp/"}})
		var rs approvalRuleSet
		rs.Add(rule)
		require.False(t, rs.Allows("shell_run", []byte(`{"command":"cp /tmp/a /etc/b"}`), testApprovalScope))
	})

	t.Run("denies empty command", func(t *testing.T) {
		rule := scopedRule(ApprovalRule{Type: approvalDirAllow, Paths: []string{"/tmp/"}})
		var rs approvalRuleSet
		rs.Add(rule)
		require.False(t, rs.Allows("shell_run", []byte(`{"command":""}`), testApprovalScope))
	})

	t.Run("denies when no paths extracted", func(t *testing.T) {
		rule := scopedRule(ApprovalRule{Type: approvalDirAllow, Paths: []string{"/tmp/"}})
		var rs approvalRuleSet
		rs.Add(rule)
		require.False(t, rs.Allows("shell_run", []byte(`{"command":"echo"}`), testApprovalScope))
	})

	t.Run("cross-scope denial", func(t *testing.T) {
		rule := scopedRule(ApprovalRule{Type: approvalDirAllow, Paths: []string{"/tmp/"}})
		var rs approvalRuleSet
		rs.Add(rule)
		require.False(t, rs.Allows("shell_run", []byte(`{"command":"rm /tmp/foo"}`), WorkspaceScope("/other")))
	})

	t.Run("simple mode path extraction", func(t *testing.T) {
		rule := scopedRule(ApprovalRule{Type: approvalDirAllow, Paths: []string{"C:\\Users"}})
		var rs approvalRuleSet
		rs.Add(rule)
		require.True(t, rs.Allows("powershell_run", []byte(`{"command":"Remove-Item C:\\Users\\old.txt"}`), testApprovalScope))
		require.False(t, rs.Allows("powershell_run", []byte(`{"command":"Remove-Item C:\\Windows\\old.txt"}`), testApprovalScope))
	})

	t.Run("windows matching is case insensitive", func(t *testing.T) {
		rule := scopedRule(ApprovalRule{Type: approvalDirAllow, Paths: []string{"C:\\Users"}})
		var rs approvalRuleSet
		rs.Add(rule)
		require.True(t, rs.Allows("powershell_run", []byte(`{"command":"remove-item c:\\users\\old.txt"}`), testApprovalScope))
	})

	t.Run("powershell legacy glob rule matches containing directory", func(t *testing.T) {
		rule := scopedRule(ApprovalRule{Type: approvalDirAllow, Paths: []string{`C:\Users\Test\Downloads\*`}})
		var rs approvalRuleSet
		rs.Add(rule)
		require.True(t, rs.Allows("powershell_run", []byte(`{"command":"Remove-Item C:\\Users\\Test\\Downloads\\old.txt"}`), testApprovalScope))
		require.False(t, rs.Allows("powershell_run", []byte(`{"command":"Remove-Item C:\\Users\\Test\\Downloads2\\old.txt"}`), testApprovalScope))
	})

	t.Run("windows drive root allows children", func(t *testing.T) {
		rule := scopedRule(ApprovalRule{Type: approvalDirAllow, Paths: []string{`C:\`}})
		var rs approvalRuleSet
		rs.Add(rule)
		require.True(t, rs.Allows("powershell_run", []byte(`{"command":"Remove-Item C:\\old.txt"}`), testApprovalScope))
	})

	t.Run("denies sibling prefix", func(t *testing.T) {
		rule := scopedRule(ApprovalRule{Type: approvalDirAllow, Paths: []string{"/tmp/cache"}})
		var rs approvalRuleSet
		rs.Add(rule)
		require.False(t, rs.Allows("shell_run", []byte(`{"command":"rm /tmp/cache2/file"}`), testApprovalScope))
	})

	t.Run("legacy glob rule matches containing directory", func(t *testing.T) {
		rule := scopedRule(ApprovalRule{Type: approvalDirAllow, Paths: []string{"/tmp/cache/*"}})
		var rs approvalRuleSet
		rs.Add(rule)
		require.True(t, rs.Allows("shell_run", []byte(`{"command":"rm /tmp/cache/file"}`), testApprovalScope))
		require.False(t, rs.Allows("shell_run", []byte(`{"command":"rm /tmp/cache2/file"}`), testApprovalScope))
	})

	t.Run("denies windows sibling prefix", func(t *testing.T) {
		rule := scopedRule(ApprovalRule{Type: approvalDirAllow, Paths: []string{"C:\\Users"}})
		var rs approvalRuleSet
		rs.Add(rule)
		require.False(t, rs.Allows("powershell_run", []byte(`{"command":"Remove-Item C:\\Users2\\old.txt"}`), testApprovalScope))
	})

	t.Run("legacy tilde rule matches expanded shell write target", func(t *testing.T) {
		home, err := os.UserHomeDir()
		require.NoError(t, err)
		require.NotEmpty(t, home)

		rule := scopedRule(ApprovalRule{Type: approvalDirAllow, Paths: []string{"~/.config"}})
		var rs approvalRuleSet
		rs.Add(rule)
		require.True(t, rs.Allows("shell_run", []byte(`{"command":"rm ~/.config/mods/config.yml"}`), testApprovalScope))
		require.False(t, rs.Allows("shell_run", []byte(`{"command":"rm ~/.ssh/config"}`), testApprovalScope))
	})
}

func TestDirAllowModeSplit(t *testing.T) {
	t.Run("read rule does not satisfy a write command", func(t *testing.T) {
		rule := scopedRule(ApprovalRule{Type: approvalDirAllow, Paths: []string{"/tmp/"}, Mode: AccessRead})
		var rs approvalRuleSet
		rs.Add(rule)
		// dirAllowForCommand extracts write targets and only honours
		// write/legacy rules, so a read-only approval must not grant writes.
		require.False(t, rs.Allows("shell_run", []byte(`{"command":"rm /tmp/foo"}`), testApprovalScope))
	})

	t.Run("write rule satisfies a write command", func(t *testing.T) {
		rule := scopedRule(ApprovalRule{Type: approvalDirAllow, Paths: []string{"/tmp/"}, Mode: AccessWrite})
		var rs approvalRuleSet
		rs.Add(rule)
		require.True(t, rs.Allows("shell_run", []byte(`{"command":"rm /tmp/foo"}`), testApprovalScope))
	})

	t.Run("legacy rule satisfies a write command", func(t *testing.T) {
		rule := scopedRule(ApprovalRule{Type: approvalDirAllow, Paths: []string{"/tmp/"}})
		var rs approvalRuleSet
		rs.Add(rule)
		require.True(t, rs.Allows("shell_run", []byte(`{"command":"rm /tmp/foo"}`), testApprovalScope))
	})

	t.Run("RulesAllowDirs honours mode for read ops", func(t *testing.T) {
		readRule := scopedRule(ApprovalRule{Type: approvalDirAllow, Paths: []string{"/etc"}, Mode: AccessRead})
		writeRule := scopedRule(ApprovalRule{Type: approvalDirAllow, Paths: []string{"/etc"}, Mode: AccessWrite})
		require.True(t, RulesAllowDirs([]ApprovalRule{readRule}, []string{"/etc"}, testApprovalScope, AccessRead))
		require.False(t, RulesAllowDirs([]ApprovalRule{readRule}, []string{"/etc"}, testApprovalScope, AccessWrite))
		require.True(t, RulesAllowDirs([]ApprovalRule{writeRule}, []string{"/etc"}, testApprovalScope, AccessWrite))
		require.False(t, RulesAllowDirs([]ApprovalRule{writeRule}, []string{"/etc"}, testApprovalScope, AccessRead))
	})

	t.Run("legacy rule satisfies both read and write via RulesAllowDirs", func(t *testing.T) {
		rule := scopedRule(ApprovalRule{Type: approvalDirAllow, Paths: []string{"/etc"}})
		require.True(t, RulesAllowDirs([]ApprovalRule{rule}, []string{"/etc"}, testApprovalScope, AccessRead))
		require.True(t, RulesAllowDirs([]ApprovalRule{rule}, []string{"/etc"}, testApprovalScope, AccessWrite))
	})

	t.Run("separate rules collectively cover all directories", func(t *testing.T) {
		ruleA := scopedRule(ApprovalRule{Type: approvalDirAllow, Paths: []string{"/external/a"}, Mode: AccessRead})
		ruleB := scopedRule(ApprovalRule{Type: approvalDirAllow, Paths: []string{"/external/b"}, Mode: AccessRead})
		require.True(t, RulesAllowDirs(
			[]ApprovalRule{ruleA, ruleB},
			[]string{"/external/a/subdir", "/external/b/subdir"},
			testApprovalScope,
			AccessRead,
		))
	})

	t.Run("rules with different modes do not collectively cover", func(t *testing.T) {
		readRule := scopedRule(ApprovalRule{Type: approvalDirAllow, Paths: []string{"/external/a"}, Mode: AccessRead})
		writeRule := scopedRule(ApprovalRule{Type: approvalDirAllow, Paths: []string{"/external/b"}, Mode: AccessWrite})
		require.False(t, RulesAllowDirs(
			[]ApprovalRule{readRule, writeRule},
			[]string{"/external/a", "/external/b"},
			testApprovalScope,
			AccessRead,
		))
	})
}

func TestMixedAccessIntentRules(t *testing.T) {
	intent := AccessIntent{
		ReadDirs:  []string{"/external/source"},
		WriteDirs: []string{filepath.Join(testApprovalScope.Value, "dest")},
	}
	readRule := scopedRule(ApprovalRule{Type: approvalDirAllow, Paths: []string{"/external/source"}, Mode: AccessRead})
	writeRule := scopedRule(ApprovalRule{Type: approvalDirAllow, Paths: []string{testApprovalScope.Value}, Mode: AccessWrite})

	require.True(t, RulesAllowIntent(
		[]ApprovalRule{readRule, writeRule}, intent, testApprovalScope, safeDirs(), ApprovalReviewMode(ReviewAuto),
	))
	require.False(t, RulesAllowIntent(
		[]ApprovalRule{readRule}, intent, testApprovalScope, safeDirs(), ApprovalReviewMode(ReviewAuto),
	))

	candidates := candidateRulesForIntent(intent, testApprovalScope, safeDirs(), ApprovalReviewMode(ReviewAuto), false)
	require.Len(t, candidates, 2)
	require.Equal(t, AccessRead, candidates[0].Mode)
	require.Equal(t, []string{"/external/source"}, candidates[0].Paths)
	require.Equal(t, AccessWrite, candidates[1].Mode)
	require.Equal(t, []string{filepath.Join(testApprovalScope.Value, "dest")}, candidates[1].Paths)
}

// TestToolReviewerSnapshotChanRaceFree exercises the mu-guarded reviewChan
// replacement so go test -race does not flag the field load/store. The test
// reads the channel from a sender goroutine while the main goroutine swaps
// it via startSession / reset, which is the pattern that the production
// code uses across Update vs. tool-caller goroutines.
func TestToolReviewerSnapshotChanRaceFree(t *testing.T) {
	r := &toolReviewer{}

	var wg sync.WaitGroup
	done := make(chan struct{})
	// Reader: continually snapshot the channel (read by tea.Cmd /
	// tool-caller goroutines in production).
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			select {
			case <-done:
				return
			default:
				_ = r.snapshotChan()
			}
		}
	}()

	// Writer: alternate between startSession and reset on the main
	// goroutine (simulating Update replacing the channel between tool
	// rounds and at session teardown).
	for i := 0; i < 200; i++ {
		_ = r.startSession()
		r.reset()
	}
	close(done)
	wg.Wait()
}

// TestRequestApprovalAfterResetIsUnavailable confirms requestApproval bails
// out with errReviewUnavailable when no review session is active, instead of
// panicking on a nil channel send.
func TestRequestApprovalAfterResetIsUnavailable(t *testing.T) {
	r := &toolReviewer{reviewMode: ReviewAuto}
	_ = r.startSession()
	r.reset()

	mods := &Mods{
		ctx:                 context.Background(),
		Config:              &Config{},
		Styles:              makeStyles(true),
		currentToolRegistry: nil,
	}

	err := r.requestApproval(testReviewerDeps(mods), "fs_write_file", []byte(`{"path":"out.txt","content":"x"}`))
	require.Error(t, err)
	require.ErrorIs(t, err, errReviewUnavailable)
}

// TestPollReviewCmdSnapshotsChannel makes sure pollReviewCmd captures the
// review channel at construction time rather than reading r.reviewChan from
// inside the goroutine. A subsequent reset() must therefore unblock the
// existing poll goroutine via the snapshotted channel close, regardless of
// what r.reviewChan is replaced with later.
func TestPollReviewCmdSnapshotsChannel(t *testing.T) {
	r := &toolReviewer{}
	cmd := r.startSession()

	done := make(chan tea.Msg, 1)
	go func() { done <- cmd() }()

	// Replace the channel: reset closes the original, then startSession
	// creates a fresh one. The pre-existing cmd goroutine must observe the
	// close of the snapshotted channel and exit, not block on the new one.
	r.reset()

	select {
	case msg := <-done:
		require.Nil(t, msg, "closed channel must yield a nil tea.Msg from pollReviewCmd")
	case <-time.After(2 * time.Second):
		t.Fatal("pollReviewCmd did not return after reset() closed its snapshotted channel")
	}
}

// TestShellClassifyLRUEviction asserts the bounded cache evicts the
// least-recently-used entry once the capacity is exceeded, so a long
// session that issues many distinct mutable commands cannot grow the
// classifier cache without limit.
func TestShellClassifyLRUEviction(t *testing.T) {
	c := newShellClassifyLRU(3)
	c.Store("a", shellCommandAnalysis{Reason: "A"})
	c.Store("b", shellCommandAnalysis{Reason: "B"})
	c.Store("c", shellCommandAnalysis{Reason: "C"})
	require.Equal(t, 3, c.Len())

	// Touch "a" so it becomes most recently used; adding "d" must evict "b".
	if _, ok := c.Load("a"); !ok {
		t.Fatal("a must be present")
	}
	c.Store("d", shellCommandAnalysis{Reason: "D"})
	require.Equal(t, 3, c.Len())

	_, ok := c.Load("b")
	require.False(t, ok, "least recently used key b must have been evicted")
	for _, key := range []string{"a", "c", "d"} {
		_, ok := c.Load(key)
		require.True(t, ok, "%s must still be cached", key)
	}
}

// TestShellClassifyLRUUpdateInPlace makes sure storing the same key twice
// refreshes the value without growing the cache or breaking eviction.
func TestShellClassifyLRUUpdateInPlace(t *testing.T) {
	c := newShellClassifyLRU(2)
	c.Store("k", shellCommandAnalysis{Reason: "v1"})
	c.Store("k", shellCommandAnalysis{Reason: "v2"})
	require.Equal(t, 1, c.Len())

	got, ok := c.Load("k")
	require.True(t, ok)
	require.Equal(t, "v2", got.Reason)
}

func TestNormalizeAffectedDirs(t *testing.T) {
	t.Run("reduces file path to parent and drops covered entry", func(t *testing.T) {
		dir := t.TempDir()
		file := filepath.Join(dir, "mods")
		require.NoError(t, os.WriteFile(file, []byte("x"), 0o644))
		// Simulates the LLM returning both the directory and the file path.
		got := normalizeAffectedDirs([]string{dir, file})
		require.Equal(t, []string{filepath.Clean(dir)}, got)
	})

	t.Run("reduces lone file path to its parent directory", func(t *testing.T) {
		dir := t.TempDir()
		file := filepath.Join(dir, "data.txt")
		require.NoError(t, os.WriteFile(file, []byte("x"), 0o644))
		got := normalizeAffectedDirs([]string{file})
		require.Equal(t, []string{filepath.Clean(dir)}, got)
	})

	t.Run("keeps non-existent paths as-is", func(t *testing.T) {
		got := normalizeAffectedDirs([]string{"/tmp/does-not-exist-12345"})
		require.Equal(t, []string{"/tmp/does-not-exist-12345"}, got)
	})

	t.Run("drops nested directories covered by an ancestor", func(t *testing.T) {
		got := normalizeAffectedDirs([]string{"/a/b", "/a/b/c", "/a/b/c/d"})
		require.Equal(t, []string{"/a/b"}, got)
	})

	t.Run("keeps sibling directories separate", func(t *testing.T) {
		got := normalizeAffectedDirs([]string{"/a/b", "/a/c"})
		require.ElementsMatch(t, []string{"/a/b", "/a/c"}, got)
	})

	t.Run("empty input returns nil", func(t *testing.T) {
		require.Nil(t, normalizeAffectedDirs(nil))
	})
}
