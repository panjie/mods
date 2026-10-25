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
	"github.com/panjie/mods/internal/pathutil"
	"github.com/panjie/mods/internal/proto"
	"github.com/panjie/mods/internal/skills"
	toolregistry "github.com/panjie/mods/internal/tools"
	"github.com/stretchr/testify/require"
)

var testApprovalScope = WorkingDirScope("/cwd")

func testConfigForWorkingDir(cwd string) *Config {
	cfg := &Config{}
	cfg.WorkingDir = cwd
	return cfg
}

func testShellWorkingDirScope(t *testing.T) Scope {
	t.Helper()
	if runtime.GOOS == "windows" {
		home, err := os.UserHomeDir()
		require.NoError(t, err)
		require.NotEmpty(t, home)
		return WorkingDirScope(filepath.Join(home, "mods-test-cwd"))
	}
	return testApprovalScope
}

func scopedRule(rule ApprovalRule) ApprovalRule {
	rule.ScopeKind = testApprovalScope.Kind
	rule.ScopeValue = testApprovalScope.Value
	return rule
}

func TestShellUnknownEffectPresentationSurvivesPrebuiltAccessIntent(t *testing.T) {
	oldInputTTY := IsInputTTY
	IsInputTTY = func() bool { return true }
	t.Cleanup(func() { IsInputTTY = oldInputTTY })

	cwdScope := testShellWorkingDirScope(t)
	registry := testReviewRegistry(t)
	mods := &Mods{
		ctx:                 context.Background(),
		Config:              testConfigForWorkingDir(cwdScope.Value),
		currentToolRegistry: registry,
	}
	reviewer := &toolReviewer{
		reviewMode: ReviewAuto,
		scope:      cwdScope,
		reviewChan: make(chan toolReviewItem, 1),
	}
	data := []byte(`{"command":"opaque-command"}`)
	assessment := mods.assessCommand("shell_run", "opaque-command")
	intent := buildAccessIntent("shell_run", data, registry, &assessment)

	errCh := make(chan error, 1)
	go func() {
		errCh <- reviewer.requestApproval(reviewerDeps{
			ctx:            context.Background(),
			shellExecution: true,
			assessment:     &assessment,
			accessIntent:   intent,
		}, "shell_run", data)
	}()

	item := receiveReviewItem(t, reviewer.reviewChan)
	require.NotContains(t, item.summary, cwdScope.Value)
	require.Contains(t, item.summary, "Risk: unknown effect and location")
	require.NotContains(t, item.summary, "effects could not be proven")
	require.Equal(t, interactionToneWarning, item.presentation.tone)
	require.Equal(t, "Warning", item.presentation.toneText)
	require.Equal(t, "Run with unknown effects", item.presentation.headline)
	require.Contains(t, item.presentation.rows, interactionRow{Label: "Target", Value: "Unknown"})
	require.Len(t, item.presentation.rows, 3)
	require.Empty(t, item.candidateRules)
	item.resp <- reviewResponse{approved: true}
	require.NoError(t, <-errCh)
}

func TestDynamicProbeWithoutConcreteDirectorySkipsAutoReview(t *testing.T) {
	assessment := approval.CommandAssessment{
		Evidence:       approval.EvidenceStatic,
		Effect:         approval.EffectRead,
		DynamicTargets: []string{`$PROFILE.CurrentUserCurrentHost`},
		DynamicProbe:   true,
		Reason:         "read-only PowerShell command (AST analysis)",
	}
	intent := assessment.AccessIntent()
	require.Equal(t, AccessRead, intent.Class)
	require.Equal(t, []string{`$PROFILE.CurrentUserCurrentHost`}, intent.UnresolvedPaths)
	require.True(t, intent.DynamicProbe)

	reviewer := &toolReviewer{reviewMode: ReviewAuto, scope: testApprovalScope}
	err := reviewer.requestApproval(reviewerDeps{
		ctx:            context.Background(),
		shellExecution: true,
		assessment:     &assessment,
		accessIntent:   intent,
	}, "powershell_run", []byte(`{"command":"[PSCustomObject]@{ Path = $PROFILE.CurrentUserCurrentHost; Exists = Test-Path -LiteralPath $PROFILE.CurrentUserCurrentHost } | ConvertTo-Json -Compress"}`))
	require.NoError(t, err)
}

func TestOldestDownloadsPipelineReadNeedsNoApproval(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX pipeline coverage applies to non-Windows shell_run")
	}
	oldInputTTY := IsInputTTY
	IsInputTTY = func() bool { return true }
	t.Cleanup(func() { IsInputTTY = oldInputTTY })

	home, err := os.UserHomeDir()
	require.NoError(t, err)
	downloads := filepath.Join(home, "Downloads")
	cwd := canonicalTestPath(t, t.TempDir())
	registry := testReviewRegistry(t)
	mods := &Mods{
		ctx:                 context.Background(),
		Config:              testConfigForWorkingDir(cwd),
		currentToolRegistry: registry,
	}
	reviewer := &toolReviewer{
		reviewMode: ReviewAuto,
		scope:      WorkingDirScope(cwd),
		reviewChan: make(chan toolReviewItem, 1),
	}
	cmd := `find "$HOME/Downloads" -type f -print0 | xargs -0 stat -f '%m %N' | sort -n | head -1`
	data := []byte(fmt.Sprintf(`{"command":%q}`, cmd))
	assessment := mods.assessCommand("shell_run", cmd)
	intent := buildAccessIntent("shell_run", data, registry, &assessment)

	require.Contains(t, intent.ReadDirs, downloads)
	require.Empty(t, intent.WriteDirs)
	require.NoError(t, reviewer.requestApproval(reviewerDeps{
		ctx:            context.Background(),
		shellExecution: true,
		assessment:     &assessment,
		accessIntent:   intent,
	}, "shell_run", data))

	reviewer.reviewChan = nil
	writeIntent := AccessIntent{WriteCoverage: testCompleteCoverage, Class: AccessWrite, Dirs: []string{downloads}}
	err = reviewer.requestApproval(
		reviewerDeps{ctx: context.Background(), accessIntent: writeIntent},
		"fs_delete_file",
		[]byte(fmt.Sprintf(`{"path":%q}`, filepath.Join(downloads, "old.txt"))),
	)
	require.ErrorIs(t, err, errReviewUnavailable)
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
	require.Contains(t, rendered, "Allow for session")
}

func TestReviewBannerAlwaysAllowRemoteWrite(t *testing.T) {
	reviewer := &toolReviewer{
		reviewPending: true,
		scope:         testApprovalScope,
		reviewItem: &toolReviewItem{
			name:    "github_create_issue",
			summary: "Remote write: https://api.github.com",
			candidateRules: []ApprovalRule{{
				Type:    approval.RemoteAllow,
				Origins: []string{"https://api.github.com"},
				Mode:    AccessWrite,
			}},
		},
	}
	rendered := reviewer.renderBanner(120, makeStyles(true).Interaction)
	require.Contains(t, rendered, "Allow for session")
	require.Contains(t, rendered, "https://api.github.com")
}

func TestReviewBannerDoesNotOfferLegacyRule(t *testing.T) {
	reviewer := &toolReviewer{
		reviewPending: true,
		scope:         testApprovalScope,
		reviewItem: &toolReviewItem{
			name:           "fs_write_file",
			args:           []byte(`{"path":"/Users/panjie/temp/out.txt","content":"x"}`),
			summary:        "Target: /Users/panjie/temp/out.txt - creates new file",
			candidateRules: nil,
		},
	}
	rendered := reviewer.renderBanner(120, makeStyles(true).Interaction)
	require.NotContains(t, rendered, "Allow for session")
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
	require.NotContains(t, rendered, "Allow for session")
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
	require.NotContains(t, rendered, "shell_run", "the technical tool name should not clutter the review header")
	require.Contains(t, rendered, "{} +", "security-sensitive command tail must remain visible")
}

func TestRequestApprovalUsesInteractiveReviewAvailability(t *testing.T) {
	oldIsInputTTY := IsInputTTY
	IsInputTTY = func() bool { return false }
	t.Cleanup(func() { IsInputTTY = oldIsInputTTY })

	registry := testReviewRegistry(t)
	mods := &Mods{
		ctx:                 context.Background(),
		Config:              testConfigForWorkingDir(testApprovalScope.Value),
		currentToolRegistry: registry,
	}
	mods.Config.InteractiveTTYAvailable = true
	reviewer := newToolReviewer(mods.Config)
	reviewer.reviewChan = make(chan toolReviewItem, 1)

	errCh := make(chan error, 1)
	go func() {
		errCh <- testRequestApproval(reviewer, mods, "fs_write_file", []byte(`{"path":"out.txt","content":"x"}`))
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

	cfg := testConfigForWorkingDir(testApprovalScope.Value)
	cfg.Raw = true
	cfg.InteractiveTTYAvailable = true
	reviewer := newToolReviewer(cfg)

	err := reviewer.requestApproval(
		reviewerDeps{ctx: context.Background(), accessIntent: AccessIntent{WriteCoverage: testCompleteCoverage, Class: AccessWrite}},
		"fs_write_file",
		[]byte(`{"path":"out.txt","content":"x"}`),
	)
	require.ErrorIs(t, err, errReviewUnavailable)
}

func TestRequestApprovalRawTTYModeDoesNotWaitForReview(t *testing.T) {
	oldIsInputTTY := IsInputTTY
	IsInputTTY = func() bool { return true }
	t.Cleanup(func() { IsInputTTY = oldIsInputTTY })

	cfg := testConfigForWorkingDir(testApprovalScope.Value)
	cfg.Raw = true
	reviewer := newToolReviewer(cfg)
	reviewer.reviewChan = make(chan toolReviewItem, 1)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()

	err := reviewer.requestApproval(
		reviewerDeps{ctx: ctx, accessIntent: AccessIntent{WriteCoverage: testCompleteCoverage, Class: AccessWrite}},
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

	cfg := testConfigForWorkingDir(testApprovalScope.Value)
	reviewer := newToolReviewer(cfg)
	reviewer.reviewChan = make(chan toolReviewItem, 1)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()

	err := reviewer.requestApproval(
		reviewerDeps{ctx: ctx, accessIntent: AccessIntent{WriteCoverage: testCompleteCoverage, Class: AccessWrite}},
		"fs_write_file",
		[]byte(`{"path":"out.txt","content":"x"}`),
	)
	require.ErrorIs(t, err, errReviewUnavailable)
	require.Empty(t, reviewer.reviewChan)
}

func TestReviewPolicyNonTTY(t *testing.T) {
	oldInputTTY := IsInputTTY
	IsInputTTY = func() bool { return false }
	t.Cleanup(func() { IsInputTTY = oldInputTTY })

	scope := testShellWorkingDirScope(t)
	mods := &Mods{
		ctx:    context.Background(),
		Config: testConfigForWorkingDir(scope.Value),
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

	t.Run("review never allows a missing access intent", func(t *testing.T) {
		reviewer := &toolReviewer{reviewMode: ReviewNever, scope: scope}
		require.NoError(t, reviewer.requestApproval(reviewerDeps{ctx: context.Background()}, "unknown_tool", nil))
	})

	t.Run("auto denies write without interactive approval", func(t *testing.T) {
		reviewer := &toolReviewer{reviewMode: ReviewAuto, scope: scope}
		err := testRequestApproval(reviewer, mods, "fs_write_file", []byte(`{"path":"out.txt","content":"x"}`))
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

	t.Run("auto requires review for write in loaded skill directory", func(t *testing.T) {
		skillDir := filepath.Join(filepath.Dir(scope.Value), "mods-test-skill")
		previousCatalog := mods.skillCatalog
		mods.skillCatalog = []skills.Skill{{Name: "demo", Dir: skillDir}}
		defer func() { mods.skillCatalog = previousCatalog }()
		intent := AccessIntent{Class: AccessWrite, Dirs: []string{skillDir}}
		deps := reviewerDeps{
			ctx:            context.Background(),
			shellExecution: true,
			accessIntent:   intent,
			safeDirs:       mods.safeDirs(),
		}
		reviewer := &toolReviewer{reviewMode: ReviewAuto, scope: scope}
		err := reviewer.requestApproval(deps, "shell_run", []byte(`{"command":"touch generated.txt"}`))
		require.ErrorIs(t, err, errReviewUnavailable)
	})

	t.Run("auto allows statically proven read", func(t *testing.T) {
		reviewer := &toolReviewer{reviewMode: ReviewAuto, scope: scope}
		command := "echo ok"
		if runtime.GOOS == "windows" {
			command = "Write-Output ok"
		}
		err := testRequestApproval(reviewer, mods, "shell_run", []byte(fmt.Sprintf(`{"command":%q}`, command)))
		require.NoError(t, err)
	})

	t.Run("auto allows read-only shell parent traversal in non-TTY", func(t *testing.T) {
		reviewer := &toolReviewer{reviewMode: ReviewAuto, scope: scope}
		err := testRequestApproval(reviewer, mods, "shell_run", []byte(`{"command":"cat ../sibling/file"}`))
		require.NoError(t, err)
	})

	t.Run("auto allows read-only shell tilde path in non-TTY", func(t *testing.T) {
		reviewer := &toolReviewer{reviewMode: ReviewAuto, scope: scope}
		err := testRequestApproval(reviewer, mods, "shell_run", []byte(`{"command":"cat ~/Downloads/file"}`))
		require.NoError(t, err)
	})

	t.Run("auto denies compound shell after read-only prefix", func(t *testing.T) {
		reviewer := &toolReviewer{reviewMode: ReviewAuto, scope: scope}
		err := testRequestApproval(reviewer, mods, "shell_run", []byte(`{"command":"echo ok; rm -rf ."}`))
		require.ErrorIs(t, err, errReviewUnavailable)
	})

	t.Run("auto allows read-only powershell touching external path in non-TTY", func(t *testing.T) {
		if runtime.GOOS != "windows" {
			t.Skip("real PowerShell bridge requires Windows")
		}

		reviewer := &toolReviewer{reviewMode: ReviewAuto, scope: scope}
		err := testRequestApproval(reviewer, mods, "powershell_run", []byte(`{"command":"Get-ChildItem C:\\Users"}`))
		require.NoError(t, err)
	})

	t.Run("auto denies nested powershell", func(t *testing.T) {
		reviewer := &toolReviewer{reviewMode: ReviewAuto, scope: scope}
		err := testRequestApproval(reviewer, mods, "powershell_run", []byte(`{"command":"powershell -EncodedCommand AAAA"}`))
		require.ErrorIs(t, err, errReviewUnavailable)
	})

	t.Run("auto allows read-only powershell pipeline touching external path", func(t *testing.T) {
		if runtime.GOOS != "windows" {
			t.Skip("real PowerShell bridge requires Windows")
		}

		reviewer := &toolReviewer{reviewMode: ReviewAuto, scope: scope}
		err := testRequestApproval(reviewer, mods, "powershell_run", []byte(`{"command":"Get-ChildItem C:\\Users | Where-Object { $_.Name -like 'p*' }"}`))
		require.NoError(t, err)
	})

	t.Run("auto denies mutating powershell command without interactive approval", func(t *testing.T) {
		reviewer := &toolReviewer{reviewMode: ReviewAuto, scope: scope}
		err := testRequestApproval(reviewer, mods, "powershell_run", []byte(`{"command":"Remove-Item C:\\tmp\\old.txt"}`))
		require.ErrorIs(t, err, errReviewUnavailable)
	})

	t.Run("saved rule allows matching powershell command", func(t *testing.T) {
		if runtime.GOOS != "windows" {
			t.Skip("real PowerShell bridge requires Windows")
		}

		reviewer := &toolReviewer{reviewMode: ReviewAuto, scope: testApprovalScope}
		reviewer.rules.Add(scopedRule(ApprovalRule{Type: approvalDirAllow, Paths: []string{"C:\\Users"}, Mode: AccessWrite}))
		err := testRequestApproval(reviewer, mods, "powershell_run", []byte(`{"command":"Remove-Item C:\\Users\\old.txt"}`))
		require.NoError(t, err)
	})

	t.Run("saved powershell prefix does not allow pipeline mutation", func(t *testing.T) {

		reviewer := &toolReviewer{reviewMode: ReviewAlways, scope: testApprovalScope}
		reviewer.rules.Add(scopedRule(ApprovalRule{Type: approvalDirAllow, Paths: []string{"C:\\Users"}}))
		err := testRequestApproval(reviewer, mods, "powershell_run", []byte(`{"command":"Get-ChildItem C:\\Windows | Remove-Item -Recurse"}`))
		require.ErrorIs(t, err, errReviewUnavailable)
	})

	t.Run("always allows read-only tool without interactive approval", func(t *testing.T) {
		reviewer := &toolReviewer{reviewMode: ReviewAlways, scope: testApprovalScope}
		err := testRequestApproval(reviewer, mods, "fs_read_file", []byte(`{"path":"README.md"}`))
		require.NoError(t, err)
	})

	t.Run("always allows read-only tool without directory intent", func(t *testing.T) {
		reviewer := &toolReviewer{reviewMode: ReviewAlways, scope: testApprovalScope}
		intent := buildAccessIntent("web_search", []byte(`{"query":"mods v2.5.0"}`), registry, nil)
		require.Equal(t, AccessRead, intent.Class)
		err := reviewer.requestApproval(reviewerDeps{ctx: context.Background(), accessIntent: intent}, "web_search", []byte(`{"query":"mods v2.5.0"}`))
		require.NoError(t, err)
	})

	t.Run("always ignores saved rule for matching write tool", func(t *testing.T) {
		reviewer := &toolReviewer{reviewMode: ReviewAlways, scope: testApprovalScope}
		reviewer.rules.Add(scopedRule(ApprovalRule{Type: approvalDirAllow, Paths: []string{testApprovalScope.Value}, Mode: AccessWrite}))
		intent := AccessIntent{Class: AccessWrite, Dirs: []string{testApprovalScope.Value}}
		err := reviewer.requestApproval(reviewerDeps{ctx: context.Background(), accessIntent: intent}, "fs_write_file", []byte(`{"path":"out.txt","content":"x"}`))
		require.ErrorIs(t, err, errReviewUnavailable)
	})
}

func TestToolCallerReadsOutsideWorkingDirWithoutReview(t *testing.T) {
	cwd := t.TempDir()
	packageDir, err := os.Getwd()
	require.NoError(t, err)
	target := filepath.Join(packageDir, "approval_rules_test.go")

	cfg := defaultConfig()
	cfg.WorkingDir = cwd
	cfg.ReviewMode = ReviewAlways
	cfg.MCPTimeout = time.Second
	mods := &Mods{Config: &cfg, ctx: context.Background(), reviewer: newToolReviewer(&cfg)}
	registry := toolregistry.NewRegistry()
	require.NoError(t, toolregistry.RegisterFilesystem(registry, toolregistry.FilesystemConfig{Root: cwd, SafeDirs: mods.safeDirs()}))

	out, err := mods.toolCaller(registry, &cfg)(proto.ToolCallRequest{
		ID: "external_read", Index: 1, Total: 1, Name: "fs_read_file",
		Arguments: []byte(fmt.Sprintf(`{"path":%q,"start_line":1,"end_line":3}`, target)),
	})
	require.NoError(t, err)
	require.Contains(t, out, "package app")
}

const testCompleteCoverage = approval.CoverageComplete

func TestShellReviewFlowUsesDeterministicAssessment(t *testing.T) {
	oldInputTTY := IsInputTTY
	IsInputTTY = func() bool { return true }
	t.Cleanup(func() { IsInputTTY = oldInputTTY })

	registry := testReviewRegistry(t)
	cwdScope := testShellWorkingDirScope(t)

	t.Run("auto skips review for proven reads", func(t *testing.T) {
		mods := &Mods{
			ctx:                 context.Background(),
			Config:              testConfigForWorkingDir(cwdScope.Value),
			currentToolRegistry: registry,
		}
		reviewer := &toolReviewer{reviewMode: ReviewAuto, scope: cwdScope}
		err := testRequestApproval(reviewer, mods, "shell_run", []byte(`{"command":"ls"}`))
		require.NoError(t, err)
	})

	t.Run("unknown command cannot create candidate rules", func(t *testing.T) {
		mods := &Mods{
			ctx:                 context.Background(),
			Config:              &Config{},
			currentToolRegistry: registry,
		}
		reviewer := &toolReviewer{
			reviewMode: ReviewAuto,
			scope:      testApprovalScope,
			reviewChan: make(chan toolReviewItem, 1),
		}
		errCh := make(chan error, 1)
		go func() {
			errCh <- testRequestApproval(reviewer, mods, "shell_run", []byte(`{"command":"some unsupported writer"}`))
		}()

		item := receiveReviewItem(t, reviewer.reviewChan)
		require.Empty(t, item.candidateRules)
		item.resp <- reviewResponse{approved: true}
		require.NoError(t, <-errCh)
	})

	t.Run("review summary surfaces assessment reason for known scope", func(t *testing.T) {
		// Reasons remain visible when the assessment carries a verified scope;
		// only unknown-location reasons are hidden as speculative.
		mods := &Mods{
			ctx:                 context.Background(),
			Config:              &Config{},
			currentToolRegistry: registry,
		}
		intent := AccessIntent{WriteCoverage: testCompleteCoverage,
			Class:  AccessWrite,
			Dirs:   []string{"/opt/scoop"},
			Reason: "installs nodejs via scoop",
		}
		assessment := approval.CommandAssessment{Effect: approval.EffectWrite, KnownDirs: intent.Dirs, Reason: intent.Reason}
		reviewer := &toolReviewer{
			reviewMode: ReviewAuto,
			scope:      testApprovalScope,
			reviewChan: make(chan toolReviewItem, 1),
		}
		errCh := make(chan error, 1)
		go func() {
			errCh <- reviewer.requestApproval(reviewerDeps{
				ctx:            mods.ctx,
				shellExecution: true,
				assessment:     &assessment,
				accessIntent:   intent,
			}, "powershell_run", []byte(`{"command":"scoop install nodejs"}`))
		}()

		item := receiveReviewItem(t, reviewer.reviewChan)
		require.Contains(t, item.summary, "installs nodejs via scoop")
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
		}
		reviewer := &toolReviewer{
			reviewMode: ReviewAuto,
			scope:      testApprovalScope,
			reviewChan: make(chan toolReviewItem, 1),
		}
		errCh := make(chan error, 1)
		go func() {
			errCh <- testRequestApproval(reviewer, mods, "shell_run", []byte(`{"command":"rm ~/.ssh"}`))
		}()

		item := receiveReviewItem(t, reviewer.reviewChan)
		require.Len(t, item.candidateRules, 1)
		require.Equal(t, []string{home}, item.candidateRules[0].Paths, "removing the .ssh entry requires its parent directory")
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
		}
		reviewer := &toolReviewer{
			reviewMode: ReviewAuto,
			scope:      testApprovalScope,
			reviewChan: make(chan toolReviewItem, 1),
		}
		assessment := mods.assessShellCommand("shell_run", pathutil.FlavorPOSIX, `du -sk ~/Downloads/* 2>/dev/null | sort -rn | head -20`, nil, testApprovalScope.Value)
		intent := normalizeAccessIntentDirs(assessment.AccessIntent(), testApprovalScope.Value, "shell_run", true)
		require.Contains(t, intent.Dirs, downloads)
		require.Empty(t, intent.WriteDirs)
		require.Empty(t, candidateRulesForIntent(intent, testApprovalScope, nil, ApprovalReviewMode(ReviewAuto)))
		require.NoError(t, reviewer.requestApproval(reviewerDeps{ctx: context.Background(), shellExecution: true, assessment: &assessment, accessIntent: intent}, "shell_run", []byte(`{"command":"du -sk ~/Downloads/* 2>/dev/null | sort -rn | head -20"}`)))
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
		}
		reviewer := &toolReviewer{
			reviewMode: ReviewAuto,
			scope:      testApprovalScope,
			reviewChan: make(chan toolReviewItem, 1),
		}
		errCh := make(chan error, 1)
		go func() {
			errCh <- testRequestApproval(reviewer, mods, "shell_run", []byte(`{"command":"cat > /home/panjie/dev/myconfigs/vim/vimrc <<'EOF'\nset path=/\n/this/looks/like/a/path\nEOF"}`))
		}()

		item := receiveReviewItem(t, reviewer.reviewChan)
		require.Contains(t, item.summary, "local mutation")
		require.Contains(t, item.summary, targetDir)
		require.NotEqual(t, "Risk: local mutation - affects /", item.summary)
		require.NotContains(t, item.summary, "unknown")
		require.Len(t, item.candidateRules, 1)
		require.Equal(t, []string{targetDir}, item.candidateRules[0].Paths)
		item.resp <- reviewResponse{approved: true}
		require.NoError(t, <-errCh)
	})

	t.Run("always allows statically proven reads", func(t *testing.T) {
		mods := &Mods{
			ctx:                 context.Background(),
			Config:              testConfigForWorkingDir(cwdScope.Value),
			currentToolRegistry: registry,
		}
		reviewer := &toolReviewer{
			reviewMode: ReviewAlways,
			scope:      cwdScope,
			reviewChan: make(chan toolReviewItem, 1),
		}
		errCh := make(chan error, 1)
		go func() {
			errCh <- testRequestApproval(reviewer, mods, "shell_run", []byte(`{"command":"ls"}`))
		}()

		require.NoError(t, <-errCh)
	})

	t.Run("saved rule cannot authorize unknown targets", func(t *testing.T) {
		cacheDir := "/tmp/cache"
		if runtime.GOOS == "windows" {
			// shell_run uses the PowerShell dialect on Windows, where a
			// leading-slash token is a native-program switch, not a path.
			cacheDir = `C:\tmp\cache`
		}
		mods := &Mods{
			ctx:                 context.Background(),
			Config:              &Config{},
			currentToolRegistry: registry,
		}
		reviewer := &toolReviewer{reviewMode: ReviewAuto, scope: testApprovalScope}
		reviewer.rules.Add(scopedRule(ApprovalRule{Type: approvalDirAllow, Paths: []string{cacheDir}, Mode: AccessWrite}))
		err := testRequestApproval(reviewer, mods, "shell_run", []byte(`{"command":"some unsupported writer"}`))
		require.ErrorIs(t, err, errReviewUnavailable)
	})

	t.Run("legacy rule cannot authorize unknown targets", func(t *testing.T) {
		home, err := os.UserHomeDir()
		require.NoError(t, err)
		require.NotEmpty(t, home)

		mods := &Mods{
			ctx:                 context.Background(),
			Config:              &Config{},
			currentToolRegistry: registry,
		}
		reviewer := &toolReviewer{reviewMode: ReviewAuto, scope: testApprovalScope}
		reviewer.rules.Add(scopedRule(ApprovalRule{Type: approvalDirAllow, Paths: []string{"~/.config"}, Mode: AccessWrite}))
		err = testRequestApproval(reviewer, mods, "shell_run", []byte(`{"command":"some unsupported writer"}`))
		require.ErrorIs(t, err, errReviewUnavailable)
	})

	t.Run("unknown script requires review", func(t *testing.T) {
		mods := &Mods{
			ctx:                 context.Background(),
			Config:              &Config{},
			currentToolRegistry: registry,
		}
		reviewer := &toolReviewer{
			reviewMode: ReviewAuto,
			scope:      testApprovalScope,
		}
		require.ErrorIs(t, testRequestApproval(reviewer, mods, "shell_run", []byte(`{"command":"unknown-reader /etc/passwd"}`)), errReviewUnavailable)
	})

	t.Run("fs external read needs no review or reusable rule", func(t *testing.T) {
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
			accessIntent: AccessIntent{WriteCoverage: testCompleteCoverage, Class: AccessRead, Dirs: []string{downloads}},
		}

		require.NoError(t, reviewer.requestApproval(deps, "fs_stat", []byte(fmt.Sprintf(`{"path":%q}`, target))))
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
			accessIntent: AccessIntent{WriteCoverage: testCompleteCoverage, Class: AccessRead, Dirs: []string{downloads}},
		}
		require.NoError(t, reviewer.requestApproval(readDeps, "fs_stat", []byte(`{"path":"/unused/file"}`)))

		writeDeps := reviewerDeps{
			ctx:          context.Background(),
			accessIntent: AccessIntent{WriteCoverage: testCompleteCoverage, Class: AccessWrite, Dirs: []string{downloads}},
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
// Always use the production deterministic assessment pipeline.
func testRequestApproval(reviewer *toolReviewer, mods *Mods, name string, data []byte) error {
	registry := mods.currentToolRegistry
	shellExecution := registry != nil && registry.ShellExecution(name)
	var assessment *approval.CommandAssessment
	if shellExecution {
		input := string(data)
		if name != "process_run" {
			var args struct {
				Command string `json:"command"`
			}
			_ = json.Unmarshal(data, &args)
			input = args.Command
		}
		assessed := mods.assessCommand(name, input)
		assessment = &assessed
	}
	intent := buildAccessIntent(name, data, registry, assessment)
	ctx, cancel := context.WithTimeout(mods.ctx, 3*time.Second)
	defer cancel()
	return reviewer.requestApproval(reviewerDeps{
		ctx:            ctx,
		shellExecution: shellExecution,
		assessment:     assessment,
		accessIntent:   intent,
		safeDirs:       mods.safeDirs(),
	}, name, data)
}

func TestShellCandidateRulesUseKnownAffectedDirs(t *testing.T) {
	t.Run("write command stamps write mode", func(t *testing.T) {
		rules := RulesForDirs([]string{"/tmp/cache", "/var/tmp"}, testApprovalScope, AccessWrite)
		require.Len(t, rules, 1)
		require.Equal(t, approvalDirAllow, rules[0].Type)
		require.Equal(t, AccessWrite, rules[0].Mode)
		require.Empty(t, rules[0].ScopeKind)
		require.Empty(t, rules[0].ScopeValue)
		require.ElementsMatch(t, []string{"/tmp/cache", "/var/tmp"}, rules[0].Paths)
	})

	t.Run("relative target is saved as an absolute task path", func(t *testing.T) {
		rules := RulesForDirs([]string{"build"}, testApprovalScope, AccessWrite)
		require.Len(t, rules, 1)
		// Stored rule paths use the host separator; the scope value is POSIX
		// style, so compare through FromSlash instead of the raw literal.
		require.Equal(t, []string{filepath.FromSlash("/cwd/build")}, rules[0].Paths)
		require.Empty(t, rules[0].ScopeKind)
		require.Empty(t, rules[0].ScopeValue)
	})

	t.Run("read command stamps read mode", func(t *testing.T) {
		rules := RulesForDirs([]string{"/etc"}, testApprovalScope, AccessRead)
		require.Empty(t, rules)
	})
}

func TestDirAllowModeSplit(t *testing.T) {
	t.Run("RulesAllowDirs honours mode for read ops", func(t *testing.T) {
		readRule := scopedRule(ApprovalRule{Type: approvalDirAllow, Paths: []string{"/etc"}, Mode: AccessRead})
		writeRule := scopedRule(ApprovalRule{Type: approvalDirAllow, Paths: []string{"/etc"}, Mode: AccessWrite})
		require.False(t, RulesAllowDirs([]ApprovalRule{readRule}, []string{"/etc"}, testApprovalScope, AccessRead))
		require.False(t, RulesAllowDirs([]ApprovalRule{readRule}, []string{"/etc"}, testApprovalScope, AccessWrite))
		require.True(t, RulesAllowDirs([]ApprovalRule{writeRule}, []string{"/etc"}, testApprovalScope, AccessWrite))
		require.False(t, RulesAllowDirs([]ApprovalRule{writeRule}, []string{"/etc"}, testApprovalScope, AccessRead))
	})

	t.Run("legacy empty-mode rule satisfies neither mode via RulesAllowDirs", func(t *testing.T) {
		rule := scopedRule(ApprovalRule{Type: approvalDirAllow, Paths: []string{"/etc"}})
		require.False(t, RulesAllowDirs([]ApprovalRule{rule}, []string{"/etc"}, testApprovalScope, AccessRead))
		require.False(t, RulesAllowDirs([]ApprovalRule{rule}, []string{"/etc"}, testApprovalScope, AccessWrite))
	})

	t.Run("separate rules collectively cover all directories", func(t *testing.T) {
		ruleA := scopedRule(ApprovalRule{Type: approvalDirAllow, Paths: []string{"/external/a"}, Mode: AccessWrite})
		ruleB := scopedRule(ApprovalRule{Type: approvalDirAllow, Paths: []string{"/external/b"}, Mode: AccessWrite})
		require.True(t, RulesAllowDirs(
			[]ApprovalRule{ruleA, ruleB},
			[]string{"/external/a/subdir", "/external/b/subdir"},
			testApprovalScope,
			AccessWrite,
		))
	})

	t.Run("directory coverage is independent of current cwd", func(t *testing.T) {
		rule := scopedRule(ApprovalRule{Type: approvalDirAllow, Paths: []string{"/external/a"}, Mode: AccessWrite})
		require.True(t, RulesAllowDirs(
			[]ApprovalRule{rule},
			[]string{"/external/a/subdir"},
			WorkingDirScope("/different-cwd"),
			AccessWrite,
		))
	})

	t.Run("legacy relative rule resolves against its original cwd", func(t *testing.T) {
		rule := scopedRule(ApprovalRule{Type: approvalDirAllow, Paths: []string{"generated"}, Mode: AccessWrite})
		require.True(t, RulesAllowDirs(
			[]ApprovalRule{rule},
			[]string{"/cwd/generated/output"},
			WorkingDirScope("/different-cwd"),
			AccessWrite,
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
	// Use a native cwd: a POSIX scope value combined with the host path
	// normalizer is not a Windows-absolute input, so it cannot exercise rule
	// matching on Windows.
	cwd := t.TempDir()
	scope := WorkingDirScope(cwd)
	writeDir := filepath.Join(cwd, "dest")
	intent := AccessIntent{WriteCoverage: testCompleteCoverage,
		ReadDirs:  []string{filepath.Join(cwd, "source")},
		WriteDirs: []string{writeDir},
	}
	writeRule := ApprovalRule{
		Type: approvalDirAllow, Paths: []string{cwd}, Mode: AccessWrite,
		ScopeKind: scope.Kind, ScopeValue: scope.Value,
	}

	require.True(t, RulesAllowIntent(
		[]ApprovalRule{writeRule}, intent, scope, safeDirs(), ApprovalReviewMode(ReviewAuto),
	))
	require.False(t, RulesAllowIntent(
		nil, intent, scope, safeDirs(), ApprovalReviewMode(ReviewAuto),
	))

	candidates := candidateRulesForIntent(intent, scope, safeDirs(), ApprovalReviewMode(ReviewAuto))
	require.Len(t, candidates, 1)
	require.Equal(t, AccessWrite, candidates[0].Mode)
	require.Equal(t, []string{writeDir}, candidates[0].Paths)
}

// TestToolReviewerSnapshotSessionRaceFree exercises the mu-guarded reviewChan
// replacement so go test -race does not flag the field load/store. The test
// reads the channel from a sender goroutine while the main goroutine swaps
// it via startSession / reset, which is the pattern that the production
// code uses across Update vs. tool-caller goroutines.
func TestToolReviewerSnapshotSessionRaceFree(t *testing.T) {
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
				_, _ = r.snapshotSession()
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

	err := testRequestApproval(r, mods, "fs_write_file", []byte(`{"path":"out.txt","content":"x"}`))
	require.Error(t, err)
	require.ErrorIs(t, err, errReviewUnavailable)
}

// TestPollReviewCmdSnapshotsChannel makes sure pollReviewCmd captures one
// review session at construction time. A subsequent reset() must unblock the
// existing poll goroutine through that session's done signal, regardless of
// what session is installed later.
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
		require.Nil(t, msg, "ended session must yield a nil tea.Msg from pollReviewCmd")
	case <-time.After(2 * time.Second):
		t.Fatal("pollReviewCmd did not return after reset() ended its snapshotted session")
	}
}

func TestToolReviewerResetUnblocksPendingApproval(t *testing.T) {
	r := &toolReviewer{
		reviewMode:                 ReviewAlways,
		reviewAvailabilityKnown:    true,
		interactiveReviewAvailable: true,
		scope:                      testApprovalScope,
	}
	_ = r.startSession()

	done := make(chan error, 1)
	go func() {
		done <- r.requestApproval(reviewerDeps{
			ctx:          context.Background(),
			accessIntent: AccessIntent{WriteCoverage: testCompleteCoverage, Class: AccessWrite},
		}, "mutable_tool", []byte(`{}`))
	}()

	ch, _ := r.snapshotSession()
	select {
	case <-ch:
	case <-time.After(time.Second):
		t.Fatal("approval request was not queued")
	}
	r.reset()

	select {
	case err := <-done:
		require.ErrorIs(t, err, errReviewUnavailable)
	case <-time.After(time.Second):
		t.Fatal("pending approval was not released by reset")
	}
}

func TestNormalizeAffectedDirs(t *testing.T) {
	t.Run("reduces file path to parent and drops covered entry", func(t *testing.T) {
		dir := t.TempDir()
		file := filepath.Join(dir, "mods")
		require.NoError(t, os.WriteFile(file, []byte("x"), 0o644))
		// Include both a directory and its leaf path to test normalization.
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

func TestUnknownEffectCannotReuseKnownTargets(t *testing.T) {
	for _, tool := range []string{"shell_run", "powershell_run", "process_run"} {
		t.Run(fmt.Sprintf("%s/known-target", tool), func(t *testing.T) {
			dir := filepath.Clean(t.TempDir())
			scope := WorkingDirScope(dir)
			assessment := approval.CommandAssessment{Effect: approval.EffectUnknown, KnownDirs: []string{dir}}
			intent := assessment.AccessIntent()
			r := &toolReviewer{reviewMode: ReviewAuto, scope: scope, reviewAvailabilityKnown: true, interactiveReviewAvailable: true}
			r.startSession()
			defer r.reset()
			deps := reviewerDeps{ctx: context.Background(), shellExecution: true, assessment: &assessment, accessIntent: intent, safeDirs: []string{filepath.Clean(t.TempDir())}}
			data := []byte(`{"command":"opaque-command"}`)
			done := make(chan error, 1)
			go func() { done <- r.requestApproval(deps, tool, data) }()
			item := receiveReviewItem(t, r.reviewChan)
			require.Empty(t, item.candidateRules)
			r.handleStartMsg(toolReviewStartMsg{item: item})
			banner := r.renderBanner(160, makeStyles(true).Interaction, 40)
			require.NotContains(t, banner, "Allow for session")
			require.NotContains(t, banner, "FULL REVIEW")
			require.NotContains(t, banner, "REVIEW REQUIRED")
			r.handleKey(tea.KeyPressMsg{Code: 'y', Text: "y"})
			require.NoError(t, <-done)
			r.raw = true // Reuse must succeed without another interactive prompt.
			require.ErrorIs(t, r.requestApproval(deps, tool, data), errReviewUnavailable)
			deps.accessIntent.Dirs = []string{filepath.Join(filepath.Dir(dir), "other-target")}
			require.ErrorIs(t, r.requestApproval(deps, tool, data), errReviewUnavailable)
			deps.accessIntent = intent
			deps.accessIntent.UnresolvedPaths = []string{"$target"}
			require.ErrorIs(t, r.requestApproval(deps, tool, data), errReviewUnavailable)
			require.Empty(t, candidateRulesForIntent(deps.accessIntent, scope, nil, ApprovalReviewMode(ReviewAuto)))
			deps.accessIntent = intent
			r.reviewMode = ReviewAlways
			require.ErrorIs(t, r.requestApproval(deps, tool, data), errReviewUnavailable)
		})

		t.Run(fmt.Sprintf("%s/safe-temp-target", tool), func(t *testing.T) {
			safe := filepath.Clean(t.TempDir())
			scope := WorkingDirScope(filepath.Clean(t.TempDir()))
			assessment := approval.CommandAssessment{Effect: approval.EffectUnknown, KnownDirs: []string{safe}}
			deps := reviewerDeps{
				ctx:            context.Background(),
				shellExecution: true,
				assessment:     &assessment,
				accessIntent:   assessment.AccessIntent(),
				safeDirs:       []string{safe},
			}
			r := &toolReviewer{reviewMode: ReviewAuto, scope: scope, reviewAvailabilityKnown: true, interactiveReviewAvailable: false}
			require.ErrorIs(t, r.requestApproval(deps, tool, []byte(`{"command":"opaque-command"}`)), errReviewUnavailable)
		})
	}
}
