package app

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"

	"github.com/panjie/mods/internal/proto"
	toolregistry "github.com/panjie/mods/internal/tools"
	"github.com/stretchr/testify/require"
)

// Positive calls execute only inside an isolated fixture. Denied calls use the
// same dispatch callback and must never reach it, even with destination rules.
func TestStage5TransferApprovalDispatch(t *testing.T) {
	if runtime.GOOS != "linux" && runtime.GOOS != "darwin" {
		t.Skip("POSIX transfer proof")
	}
	for _, tc := range []struct {
		name, command      string
		sourceGrant, allow bool
	}{
		{"copy", "cp <SRC> <DST>", false, true},
		{"move missing source grant", "mv <SRC> <DST>", false, false},
		{"move both grants", "mv <SRC> <DST>", true, true},
		{"copy unknown option", "cp --backup <SRC> <DST>", false, false},
		{"copy recursive", "cp -r <SRC> <DST>", false, false},
		{"copy unknown sibling", "cp <SRC> <DST>; mystery", false, false},
		{"text numeric option", "uniq --skip-fields=1 <SRC> <DST>", false, true},
		{"text missing value", "uniq --skip-fields <SRC> <DST>", false, false},
		{"text unknown option", "uniq --mystery <SRC> <DST>", false, false},
		{"reference and bundled flags", "touch -am -r <SRC> <DST>", false, true},
		{"env exact writes", "env LANG=C touch --reference=<SRC> <DST>", false, true},
		{"env unknown interpreter", "env LANG=C mystery <DST>", false, false},
		{"env path override", "env PATH=/unknown touch <DST>", false, false},
		{"unknown bundled flag", "touch -az <DST>", false, false},
		{"unknown timestamp option", "touch --mystery <SRC> <DST>", false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root, err := filepath.EvalSymlinks(t.TempDir())
			require.NoError(t, err)
			source, dest := filepath.Join(root, "input"), filepath.Join(root, "output")
			require.NoError(t, os.Mkdir(source, 0700))
			require.NoError(t, os.Mkdir(dest, 0700))
			src, dst := filepath.Join(source, "in"), filepath.Join(dest, "out")
			require.NoError(t, os.WriteFile(src, []byte("same line\nsame line\n"), 0600))
			command := strings.NewReplacer("<SRC>", strconv.Quote(src), "<DST>", strconv.Quote(dst)).Replace(tc.command)
			cfg := defaultConfig()
			cfg.WorkingDir = root
			cfg.ReviewMode = ReviewAuto
			cfg.InteractiveTTYAvailable = false
			m := &Mods{Config: &cfg, ctx: context.Background(), reviewer: newToolReviewer(&cfg)}
			m.reviewer.rules.Add(RulesForDirs([]string{dest}, WorkingDirScope(root), AccessWrite)...)
			if tc.sourceGrant {
				m.reviewer.rules.Add(RulesForDirs([]string{source}, WorkingDirScope(root), AccessWrite)...)
			}
			calls := 0
			registry := toolregistry.NewRegistry()
			require.NoError(t, registry.Register(toolregistry.Tool{Spec: proto.ToolSpec{Name: "shell_run"}, Capabilities: toolregistry.ToolCapabilities{ShellExecution: true}, Call: func(ctx context.Context, _ json.RawMessage) (string, error) {
				calls++
				if !tc.allow {
					return "unexpected dispatch", nil
				}
				output, err := exec.CommandContext(ctx, "sh", "-c", command).CombinedOutput()
				return string(output), err
			}}))
			args, _ := json.Marshal(map[string]string{"command": command})
			_, err = m.toolCaller(registry, &cfg)(proto.ToolCallRequest{Name: "shell_run", Arguments: args})
			if tc.allow {
				require.NoError(t, err)
				require.Equal(t, 1, calls)
				data, err := os.ReadFile(dst)
				require.NoError(t, err)
				if strings.Contains(tc.command, "touch ") {
					srcInfo, err := os.Stat(src)
					require.NoError(t, err)
					dstInfo, err := os.Stat(dst)
					require.NoError(t, err)
					require.Equal(t, srcInfo.ModTime(), dstInfo.ModTime())
				} else {
					require.NotEmpty(t, data)
				}
				if tc.sourceGrant {
					_, err = os.Stat(src)
					require.True(t, os.IsNotExist(err))
				}
			} else {
				require.ErrorIs(t, err, errReviewUnavailable)
				require.Zero(t, calls)
				_, err = os.Stat(dst)
				require.True(t, os.IsNotExist(err))
			}
		})
	}
}
