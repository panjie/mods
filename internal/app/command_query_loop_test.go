package app

import (
	"context"
	"encoding/json"
	"runtime"
	"testing"

	"github.com/panjie/mods/internal/commandanalysis"
	"github.com/panjie/mods/internal/proto"
	toolregistry "github.com/panjie/mods/internal/tools"
	"github.com/stretchr/testify/require"
)

func TestPackageQueryLoopApprovalDispatch(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX shell analysis")
	}
	for _, tc := range []struct {
		command string
		allow   bool
	}{
		{`for p in dust duf; do pacman -Si $p 2>/dev/null | grep -E '^(Name|Version|Description)'; done; echo '--- installed?'; pacman -Q ncdu gdu dust duf btop 2>&1`, true},
		{`pacman -Si dust duf`, true},
		{`for p in gdu dust dua-cli btop bottom nvtop pacseek paru yay paccache bleachbit stacer tui-journal; do printf '%s: ' "$p"; pacman -Ss "^$p\$" | head -1; echo; done`, true},
		{`pacman -Ss '^gdu$'`, true},
		{`pacman -Ss "$(printf -- --refresh)"`, false},
		{`for p in dust duf; do pacman -Siy "$p"; done`, false},
		{`for p in dust --refresh; do pacman -Si "$p"; done`, false},
		{`for p in $(printf dust); do pacman -Si "$p"; done`, false},
		{`for p in dust; do touch "$p"; done`, false},
	} {
		t.Run(tc.command, func(t *testing.T) {
			cfg := defaultConfig()
			cfg.WorkingDir = t.TempDir()
			cfg.ReviewMode = ReviewAuto
			cfg.InteractiveTTYAvailable = false
			m := &Mods{Config: &cfg, ctx: context.Background(), reviewer: newToolReviewer(&cfg)}
			m.reviewer.rules.Add(RulesForDirs([]string{cfg.WorkingDir}, WorkingDirScope(cfg.WorkingDir), AccessWrite)...)
			registry := toolregistry.NewRegistry()
			calls := 0
			require.NoError(t, registry.Register(toolregistry.Tool{Spec: proto.ToolSpec{Name: "shell_run"}, Capabilities: toolregistry.ToolCapabilities{ShellExecution: true}, Call: func(context.Context, json.RawMessage) (string, error) { calls++; return "query fixture", nil }}))
			args, _ := json.Marshal(map[string]string{"command": tc.command})
			_, err := m.toolCaller(registry, &cfg)(proto.ToolCallRequest{Name: "shell_run", Arguments: args})
			if tc.allow {
				require.NoError(t, err)
				require.Equal(t, 1, calls)
			} else {
				require.ErrorIs(t, err, errReviewUnavailable)
				require.Zero(t, calls)
			}
		})
	}
}

func TestLoopReviewExplainsUnmodeledSemantics(t *testing.T) {
	source := `for p in $packages; do mystery "$p"; done`
	a := commandanalysis.AssessShellStaticWithPolicy(source, true, commandanalysis.ReadOnlyCommandPolicy{})
	rows := commandReviewRows(source, a, "unknown effect and location")
	require.Contains(t, rows, interactionRow{Label: "Analysis", Value: "Loop p: unsupported execution context or loop form"})
	require.Contains(t, rows, interactionRow{Label: "Analysis", Value: "Unmodeled command or arguments: mystery"})
	require.Contains(t, rows, interactionRow{Label: "Unresolved expression", Value: "$p"})
	require.NotContains(t, rows, interactionRow{Label: "Target", Value: "$p"})
}
