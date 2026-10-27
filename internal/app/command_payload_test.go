package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	toolregistry "github.com/panjie/mods/internal/tools"
	"github.com/stretchr/testify/require"
)

func preparePayloadFixture(t *testing.T, program string, args []string, cwd string) *commandPayload {
	t.Helper()
	data, err := json.Marshal(map[string]any{"program": program, "args": args, "cwd": cwd})
	require.NoError(t, err)
	payload, err := prepareCommandPayload(context.Background(), "process_run", data, cwd, toolregistry.ProcessProgramBinding{Requested: program, Resolved: program})
	require.NoError(t, err)
	return payload
}

func TestCommandPayloadBindsLiteralProcessScript(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "script.py")
	content := "print('one')\nprint('two')\n"
	require.NoError(t, os.WriteFile(path, []byte(content), 0o600))
	program := filepath.Join(root, "python")
	payload := preparePayloadFixture(t, program, []string{path, "argument with spaces"}, root)
	require.NotNil(t, payload)
	require.True(t, payload.Complete)
	require.Equal(t, program, payload.Interpreter)
	require.Equal(t, path, payload.Path)
	require.Equal(t, []string{path, "argument with spaces"}, payload.Argv)
	digest := sha256.Sum256([]byte(content))
	require.Equal(t, hex.EncodeToString(digest[:]), payload.Digest)
	require.Equal(t, "UTF-8", payload.Encoding)
	rows := payload.reviewRows(nil)
	require.Contains(t, rows, interactionRow{Label: "Script source", Value: "1 | print('one')\n2 | print('two')\n3 | "})
	require.Contains(t, rows, interactionRow{Label: "Script path", Value: path})
}

func TestCommandPayloadSkipsUnboundInvocations(t *testing.T) {
	root := t.TempDir()
	require.Nil(t, preparePayloadFixture(t, "custom", []string{"script.py"}, root))
	require.Nil(t, preparePayloadFixture(t, "python", []string{"-c", "print(1)"}, root))
}

func TestCommandPayloadLimitsAndEncodingAreExplicit(t *testing.T) {
	root := t.TempDir()
	program := filepath.Join(root, "python")
	for _, tc := range []struct {
		name    string
		content []byte
		want    string
	}{
		{name: "oversized", content: []byte(strings.Repeat("x", maxCommandPayloadBytes+1)), want: "256 KiB limit"},
		{name: "binary", content: []byte{'a', 0, 'b'}, want: "binary data"},
		{name: "utf16", content: []byte{0xff, 0xfe, 'a', 0}, want: "UTF-16"},
		{name: "invalid utf8", content: []byte{0xff, 'a'}, want: "valid UTF-8"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(root, tc.name+".py")
			require.NoError(t, os.WriteFile(path, tc.content, 0o600))
			payload := preparePayloadFixture(t, program, []string{path}, root)
			require.NotNil(t, payload)
			require.False(t, payload.Complete)
			require.Empty(t, payload.Source)
			require.Contains(t, payload.Unavailable, tc.want)
			require.Contains(t, payload.reviewRows(nil), interactionRow{Label: "Script preview", Value: payload.Unavailable + " Full script content was not reviewed; execution keeps the original path and argv."})
		})
	}
}

func TestCommandPayloadMissingAndContentChange(t *testing.T) {
	root := t.TempDir()
	program := filepath.Join(root, "python")
	path := filepath.Join(root, "missing.py")
	payload := preparePayloadFixture(t, program, []string{path}, root)
	require.NotNil(t, payload)
	require.False(t, payload.Complete)
	require.Contains(t, payload.Unavailable, "cannot be read")
	changed, err := payload.revalidate(context.Background())
	require.NoError(t, err)
	require.False(t, changed)
	require.NoError(t, os.WriteFile(path, []byte("print('first')\n"), 0o600))
	changed, err = payload.revalidate(context.Background())
	require.NoError(t, err)
	require.True(t, changed)

	fresh := preparePayloadFixture(t, program, []string{path}, root)
	changed, err = fresh.revalidate(context.Background())
	require.NoError(t, err)
	require.False(t, changed)
	require.NoError(t, os.WriteFile(path, []byte("print('second')\n"), 0o600))
	changed, err = fresh.revalidate(context.Background())
	require.NoError(t, err)
	require.True(t, changed)
}
