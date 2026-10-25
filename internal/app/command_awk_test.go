package app

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/panjie/mods/internal/proto"
	toolregistry "github.com/panjie/mods/internal/tools"
	"github.com/stretchr/testify/require"
)

func TestAWKApprovalDispatch(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX shell")
	}
	for _, tc := range []struct {
		source string
		allow  bool
	}{
		{`git ls-files -z | xargs -0 wc -l | tail -n 1; echo '--- by ext ---'; git ls-files -z | xargs -0 wc -l | awk 'NF==2 {print $2}' | grep -E '\.' | sed 's/.*\.//' | sort | uniq -c | sort -rn | head -20`, true},
		{`awk 'BEGIN {system("touch out")}'`, false},
		{`awk '{print $2 > "out"}'`, false},
		{`gawk --profile=out '{print $2}'`, false},
		{`mawk -f script.awk`, false},
		{`awk "$program"`, false},
	} {
		t.Run(tc.source, func(t *testing.T) {
			cfg := defaultConfig()
			cfg.WorkingDir = t.TempDir()
			cfg.ReviewMode = ReviewAuto
			cfg.InteractiveTTYAvailable = false
			m := &Mods{Config: &cfg, ctx: context.Background(), reviewer: newToolReviewer(&cfg)}
			m.reviewer.rules.Add(RulesForDirs([]string{cfg.WorkingDir}, WorkingDirScope(cfg.WorkingDir), AccessWrite)...)
			calls := 0
			registry := toolregistry.NewRegistry()
			require.NoError(t, registry.Register(toolregistry.Tool{Spec: proto.ToolSpec{Name: "shell_run"}, Capabilities: toolregistry.ToolCapabilities{ShellExecution: true}, Call: func(context.Context, json.RawMessage) (string, error) { calls++; return "fixture", nil }}))
			args, _ := json.Marshal(map[string]string{"command": tc.source})
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

func TestAWKReadOnlyFixtureExecutes(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX host fixture")
	}
	program, err := exec.LookPath("awk")
	if err != nil {
		t.Skip("awk unavailable")
	}
	root := t.TempDir()
	input := filepath.Join(root, "input")
	require.NoError(t, os.WriteFile(input, []byte("one first\nsingle\ntwo second\n"), 0600))
	out, err := exec.Command(program, `NF==2 {print $2}`, input).CombinedOutput()
	require.NoError(t, err)
	require.Equal(t, "first\nsecond\n", string(out))
}
