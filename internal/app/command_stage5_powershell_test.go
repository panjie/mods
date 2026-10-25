package app

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	ca "github.com/panjie/mods/internal/commandanalysis"
	"github.com/panjie/mods/internal/pathutil"
	"github.com/panjie/mods/internal/proto"
	toolregistry "github.com/panjie/mods/internal/tools"
	"github.com/stretchr/testify/require"
)

type stage5IRParser []byte

func (p stage5IRParser) Parse(context.Context, string) ([]byte, error) { return p, nil }

func TestStage5PowerShellAnalysisApproval(t *testing.T) {
	root := t.TempDir()
	out := filepath.Join(root, "output")
	require.NoError(t, os.Mkdir(out, 0700))
	scope := WorkingDirScope(root)
	for _, tc := range []struct {
		name     string
		args     []string
		redirect bool
		allow    bool
	}{
		{"Set-Content", []string{"-LiteralPath", "'" + filepath.Join(out, "file") + "'", "-Value", "text", "-Encoding", "utf8"}, false, true},
		{"Set-Content", []string{"-Path", "'" + filepath.Join(out, "file") + "'", "-Unknown", "extra", "-Value", "text"}, false, false},
		{"Set-Content", []string{"@params"}, false, false},
		{"Write-Output", []string{"text"}, true, true},
		{"mystery", []string{"text"}, true, false},
	} {
		source := tc.name + " " + strings.Join(tc.args, " ")
		nodes := []map[string]any{{"id": 1, "parent": 0, "start": 0, "end": len(source), "kind": "CommandAst"}}
		ir := map[string]any{"version": "2", "commands": []string{tc.name}, "command_invocations": []map[string]any{{"node_id": 1, "name": tc.name, "args": tc.args}}, "nodes": nodes}
		if tc.redirect {
			target := "'" + filepath.Join(out, "file") + "'"
			source += " > " + target
			nodes[0]["end"] = len(source)
			ir["nodes"] = append(nodes, map[string]any{"id": 2, "parent": 1, "start": 0, "end": len(source), "kind": "FileRedirectionAst"})
			ir["redirects"] = []string{"FileRedirection"}
			ir["redirections"] = []map[string]any{{"node_id": 2, "owner": 1, "target": target, "literal": true, "stream": 1}}
		}
		raw, err := json.Marshal(ir)
		require.NoError(t, err)
		result, err := ca.NewEngine(ca.Settings{}, ca.Dependencies{PowerShell: stage5IRParser(raw)}).Analyze(context.Background(), ca.Input{Cwd: root, Dialect: pathutil.FlavorPowerShell, Shell: &ca.ShellInput{Source: source}})
		require.NoError(t, err)
		require.NotContains(t, result.Gaps, ca.GapParse)
		cfg := defaultConfig()
		cfg.WorkingDir = root
		cfg.ReviewMode = ReviewAuto
		cfg.InteractiveTTYAvailable = false
		reviewer := newToolReviewer(&cfg)
		reviewer.rules.Add(RulesForDirs([]string{out}, scope, AccessWrite)...)
		assessed := commandResult(result)
		err = reviewer.requestApproval(reviewerDeps{ctx: context.Background(), shellExecution: true, assessment: &assessed, accessIntent: commandResultToIntent(result), safeDirs: []string{t.TempDir()}}, "powershell_run", []byte(`{}`))
		if tc.allow {
			require.NoError(t, err, source)
		} else {
			require.ErrorIs(t, err, errReviewUnavailable, source)
		}
	}
}

func TestStage5PowerShellDispatch(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("real Windows bridge and dispatch")
	}
	host, err := exec.LookPath("pwsh.exe")
	if err != nil {
		host, err = exec.LookPath("powershell.exe")
	}
	require.NoError(t, err)
	for _, known := range []bool{true, false} {
		root := t.TempDir()
		dest := filepath.Join(root, "output.txt")
		source := "Write-Output hello"
		if !known {
			source = "mods_nonexistent_command"
		}
		source += " > '" + strings.ReplaceAll(dest, "'", "''") + "'"
		cfg := defaultConfig()
		cfg.WorkingDir = root
		cfg.ReviewMode = ReviewAuto
		cfg.InteractiveTTYAvailable = false
		m := &Mods{Config: &cfg, ctx: context.Background(), reviewer: newToolReviewer(&cfg)}
		m.reviewer.rules.Add(RulesForDirs([]string{root}, WorkingDirScope(root), AccessWrite)...)
		calls := 0
		registry := toolregistry.NewRegistry()
		require.NoError(t, registry.Register(toolregistry.Tool{Spec: proto.ToolSpec{Name: "powershell_run"}, Capabilities: toolregistry.ToolCapabilities{ShellExecution: true}, Call: func(ctx context.Context, _ json.RawMessage) (string, error) {
			calls++
			if !known {
				return "unexpected dispatch", nil
			}
			out, err := exec.CommandContext(ctx, host, "-NoProfile", "-NonInteractive", "-Command", source).CombinedOutput()
			return string(out), err
		}}))
		args, _ := json.Marshal(map[string]string{"command": source})
		_, err = m.toolCaller(registry, &cfg)(proto.ToolCallRequest{Name: "powershell_run", Arguments: args})
		if known {
			require.NoError(t, err)
			require.Equal(t, 1, calls)
			data, err := os.ReadFile(dest)
			require.NoError(t, err)
			require.NotEmpty(t, data)
		} else {
			require.ErrorIs(t, err, errReviewUnavailable)
			require.Zero(t, calls)
		}
	}
}
