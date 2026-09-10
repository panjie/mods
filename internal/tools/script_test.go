package tools

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestScriptExecutesSourceSnapshot(t *testing.T) {
	root := t.TempDir()
	interpreter, source := "sh", `printf '%s' 'reviewed-source'`
	if runtime.GOOS == "windows" {
		interpreter, source = "powershell", `Write-Output 'reviewed-source'`
	}
	path := filepath.Join(root, "source.txt")
	require.NoError(t, os.WriteFile(path, []byte(source), 0600))
	snapshot, err := os.ReadFile(path)
	require.NoError(t, err)
	data, _ := json.Marshal(scriptArgs{Interpreter: interpreter, Source: string(snapshot), Cwd: root})
	processData, err := ScriptProcessArguments(data)
	require.NoError(t, err)
	binding, err := PrepareProcessProgram(processData)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(path, []byte("unreviewed replacement"), 0600))
	registry := NewRegistry()
	require.NoError(t, RegisterScript(registry, ProcessConfig{Root: root}))
	out, err := registry.Call(WithProcessProgramBinding(context.Background(), binding), "script_run", data)
	require.NoError(t, err)
	require.Contains(t, out, "reviewed-source")
	require.NotContains(t, out, "unreviewed replacement")
}

func TestScriptValidation(t *testing.T) {
	for _, script := range []scriptArgs{
		{Interpreter: "sh", Source: ""}, {Interpreter: "sh", Source: strings.Repeat("x", 8193)},
		{Interpreter: "cmd", Source: "echo x"}, {Interpreter: "node", Source: "x\x00y"},
		{Interpreter: "powershell", Source: "Write-Output x", Args: []string{"extra"}},
	} {
		data, _ := json.Marshal(script)
		_, err := ScriptProcessArguments(data)
		require.Error(t, err)
	}
}

func TestShellLiteralCwd(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "中文 path $literal")
	require.NoError(t, os.Mkdir(target, 0700))
	registry := NewRegistry()
	require.NoError(t, RegisterShell(registry, ShellConfig{Root: root}))
	command := "pwd"
	if runtime.GOOS == "windows" {
		command = "(Get-Location).Path"
	}
	data, _ := json.Marshal(map[string]string{"command": command, "cwd": target})
	out, err := registry.Call(context.Background(), "shell_run", data)
	require.NoError(t, err)
	require.Contains(t, out, filepath.Base(target))
	data, _ = json.Marshal(map[string]string{"command": command, "cwd": filepath.Join(root, "missing")})
	_, err = registry.Call(context.Background(), "shell_run", data)
	require.Error(t, err)
}
