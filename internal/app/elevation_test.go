package app

import (
	"context"
	"encoding/json"
	"os"
	"testing"

	"github.com/panjie/mods/internal/proto"
	toolregistry "github.com/panjie/mods/internal/tools"
	"github.com/stretchr/testify/require"
)

func TestElevatedToolDispatchStillRequiresApproval(t *testing.T) {
	cwd, err := os.Getwd()
	require.NoError(t, err)
	exe, err := os.Executable()
	require.NoError(t, err)
	for _, name := range []string{"process_run", "powershell_run", "shell_run"} {
		for _, mode := range []ReviewMode{ReviewAuto, ReviewAlways, ReviewNever} {
			t.Run(name+"/"+string(mode), func(t *testing.T) {
				cfg := defaultConfig()
				cfg.WorkingDir, cfg.ReviewMode, cfg.InteractiveTTYAvailable = cwd, mode, false
				m := &Mods{Config: &cfg, ctx: context.Background(), reviewer: newToolReviewer(&cfg)}

				registry := toolregistry.NewRegistry()
				called := false
				require.NoError(t, registry.Register(toolregistry.Tool{
					Spec:         proto.ToolSpec{Name: name},
					Capabilities: toolregistry.ToolCapabilities{Mutable: true, ShellExecution: true},
					Call: func(_ context.Context, data json.RawMessage) (string, error) {
						called = true
						var args map[string]any
						require.NoError(t, json.Unmarshal(data, &args))
						require.Equal(t, true, args["elevated"])
						return "ok", nil
					},
				}))
				data, err := json.Marshal(map[string]any{"program": exe, "args": []string{"--unknown-effects"}, "command": "mods-test-uac-unknown-effects", "elevated": true, "effect": "may_write"})
				require.NoError(t, err)
				_, err = m.toolCaller(registry, &cfg)(proto.ToolCallRequest{Name: name, Arguments: data})
				if mode == ReviewNever {
					require.NoError(t, err)
				} else {
					require.ErrorIs(t, err, errReviewUnavailable)
				}
				require.Equal(t, mode == ReviewNever, called, "denied review must never reach the UAC execution layer")
			})
		}
	}
}

func TestElevationVisibleInReview(t *testing.T) {
	for _, name := range []string{"process_run", "powershell_run", "shell_run"} {
		p := formatReviewPresentationWithIntent(name, []byte(`{"command":"powercfg /h off","program":"powercfg","args":["/h","off"],"elevated":true}`), Scope{}, AccessIntent{WriteCoverage: testCompleteCoverage, Class: AccessWrite})
		require.Contains(t, p.rows, interactionRow{Label: "Elevation", Value: "Windows UAC administrator privileges (this command only)"})
	}
}
