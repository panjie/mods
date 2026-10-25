package app

import (
	"context"
	"encoding/json"
	"os"
	"testing"

	"github.com/panjie/mods/internal/approval"
	"github.com/panjie/mods/internal/commandanalysis"
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
				data, err := json.Marshal(map[string]any{"program": exe, "args": []string{"--unknown-effects"}, "command": "mods-test-uac-unknown-effects", "elevated": true})
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

func TestElevationPreservesTargetRulesAndReadPolicy(t *testing.T) {
	cwd, err := os.Getwd()
	require.NoError(t, err)
	for _, name := range []string{"process_run", "powershell_run", "shell_run"} {
		registry := toolregistry.NewRegistry()
		require.NoError(t, registry.Register(toolregistry.Tool{Spec: proto.ToolSpec{Name: name}, Capabilities: toolregistry.ToolCapabilities{ShellExecution: true}, Call: func(context.Context, json.RawMessage) (string, error) { return "", nil }}))
		for _, tc := range []struct {
			name        string
			mode        ReviewMode
			effect      commandanalysis.CommandEffect
			rule, allow bool
		}{
			{"read", ReviewAlways, commandanalysis.EffectRead, false, true},
			{"write denied", ReviewAuto, commandanalysis.EffectWrite, false, false},
			{"saved write rule", ReviewAuto, commandanalysis.EffectWrite, true, true},
			{"always ignores rule", ReviewAlways, commandanalysis.EffectWrite, true, false},
		} {
			t.Run(name+"/"+tc.name, func(t *testing.T) {
				assessment := commandanalysis.CommandAssessment{Evidence: commandanalysis.EvidenceStatic, WriteCoverage: approval.CoverageComplete, Effect: tc.effect, KnownDirs: []string{cwd}}
				role := commandanalysis.TargetWrite
				op := commandanalysis.OperationDirectory
				if tc.effect == commandanalysis.EffectRead {
					role = commandanalysis.TargetRead
				}
				assessment.Targets = []commandanalysis.Target{{Kind: commandanalysis.TargetFilesystem, Role: role, Operation: op, Raw: cwd, Path: cwd, Dir: cwd, State: commandanalysis.TargetResolved}}
				plain := buildAccessIntent(name, []byte(`{}`), registry, &assessment)
				elevated := buildAccessIntent(name, []byte(`{"elevated":true}`), registry, &assessment)
				require.Equal(t, plain, elevated)
				r := &toolReviewer{reviewMode: tc.mode, scope: WorkingDirScope(cwd), reviewAvailabilityKnown: true}
				if tc.rule {
					r.rules.Add(RulesForDirs([]string{cwd}, r.scope, AccessWrite)...)
				}
				err := r.requestApproval(reviewerDeps{ctx: context.Background(), accessIntent: elevated, safeDirs: []string{t.TempDir()}}, name, []byte(`{"elevated":true}`))
				if tc.allow {
					require.NoError(t, err)
				} else {
					require.ErrorIs(t, err, errReviewUnavailable)
				}
			})
		}
	}
}

func TestElevationVisibleInReview(t *testing.T) {
	for _, name := range []string{"process_run", "powershell_run", "shell_run"} {
		p := formatReviewPresentationWithIntent(name, []byte(`{"command":"powercfg /h off","program":"powercfg","args":["/h","off"],"elevated":true}`), commandanalysis.UnknownCommandAssessment(), Scope{}, AccessIntent{WriteCoverage: testCompleteCoverage, Class: AccessWrite})
		require.Contains(t, p.rows, interactionRow{Label: "Elevation", Value: "Windows UAC administrator privileges (this command only)"})
	}
}
