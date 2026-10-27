package app

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/panjie/mods/internal/commandanalysis"
	"github.com/panjie/mods/internal/proto"
	toolregistry "github.com/panjie/mods/internal/tools"
	"github.com/stretchr/testify/require"
)

func TestMixedReadWriteApprovalDispatch(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX shell fixture")
	}
	for _, suffix := range []string{`echo "$(wc -l < output/out)"`, `echo "$(touch forbidden)"`, `echo "$(mystery)"`, `echo "$(( $(cat input) ))"`} {
		t.Run(suffix, func(t *testing.T) {
			root, err := filepath.EvalSymlinks(t.TempDir())
			require.NoError(t, err)
			dest := filepath.Join(root, "output")
			require.NoError(t, os.Mkdir(dest, 0700))
			require.NoError(t, os.WriteFile(filepath.Join(root, "input"), []byte("hello\nworld\n"), 0600))
			cfg := defaultConfig()
			cfg.WorkingDir = root
			cfg.ReviewMode = ReviewAuto
			cfg.InteractiveTTYAvailable = false
			m := &Mods{Config: &cfg, ctx: context.Background(), reviewer: newToolReviewer(&cfg)}
			m.reviewer.rules.Add(RulesForDirs([]string{dest}, WorkingDirScope(root), AccessWrite)...)
			command := "cat input > output/out; " + suffix
			registry := toolregistry.NewRegistry()
			calls := 0
			require.NoError(t, registry.Register(toolregistry.Tool{Spec: proto.ToolSpec{Name: "shell_run"}, Capabilities: toolregistry.ToolCapabilities{ShellExecution: true}, Call: func(ctx context.Context, _ json.RawMessage) (string, error) {
				calls++
				cmd := exec.CommandContext(ctx, "sh", "-c", command)
				cmd.Dir = root
				out, err := cmd.CombinedOutput()
				return string(out), err
			}}))
			args, _ := json.Marshal(map[string]string{"command": command, "cwd": root})
			_, err = m.toolCaller(registry, &cfg)(proto.ToolCallRequest{Name: "shell_run", Arguments: args})
			if suffix == `echo "$(wc -l < output/out)"` {
				require.NoError(t, err)
				require.Equal(t, 1, calls)
				data, err := os.ReadFile(filepath.Join(dest, "out"))
				require.NoError(t, err)
				require.Equal(t, "hello\nworld\n", string(data))
			} else {
				require.ErrorIs(t, err, errReviewUnavailable)
				require.Zero(t, calls)
			}
		})
	}
}

func TestCommandReviewDeduplicatesDisplayOnly(t *testing.T) {
	a := commandanalysis.CommandAssessment{Effect: commandanalysis.EffectWrite, WriteCoverage: commandanalysis.CoverageComplete, Targets: []commandanalysis.Target{
		{NodeID: 1, Kind: commandanalysis.TargetFilesystem, Role: commandanalysis.TargetWrite, Path: "/tmp/out", State: commandanalysis.TargetResolved},
		{NodeID: 2, Kind: commandanalysis.TargetFilesystem, Role: commandanalysis.TargetRead, Path: "/tmp/out", State: commandanalysis.TargetResolved},
		{NodeID: 3, Kind: commandanalysis.TargetFilesystem, Role: commandanalysis.TargetRead, Path: "/tmp/out", State: commandanalysis.TargetResolved},
		{Kind: commandanalysis.TargetFilesystem, Role: commandanalysis.TargetCandidate, Path: "/tmp/out", State: commandanalysis.TargetResolved},
		{Kind: commandanalysis.TargetFilesystem, Role: commandanalysis.TargetCandidate, Raw: "$unknown"},
	}}
	before := a.Clone()
	rows := commandReviewRows("fixture", a, "")
	require.Equal(t, before, a)
	require.Equal(t, []interactionRow{{Label: "Source", Value: "1 | fixture"}, {Label: "Recognized", Value: "write filesystem; read filesystem"}, {Label: "Write target", Value: "/tmp/out"}, {Label: "Read source", Value: "/tmp/out"}, {Label: "Candidate (unresolved)", Value: "$unknown"}}, rows)
}
