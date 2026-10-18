package evals

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// Exercise the trusted checker even without a model/key. This establishes
// fixture quality, not evidence about model behavior or Qt rendering.
func TestEditFixtureDetectsHeadingOrderRegression(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("fixture checker requires Node.js")
	}
	source := filepath.Join("..", "..", "tests", "blackbox", "testdata", "editing")
	dir := t.TempDir()
	for _, name := range []string{"ResultContent.qml", "Markdown.js", "check.mjs"} {
		data, err := os.ReadFile(filepath.Join(source, name))
		require.NoError(t, err)
		require.NoError(t, os.WriteFile(filepath.Join(dir, name), data, 0600))
	}
	run := func() (string, error) {
		cmd := exec.Command(node, "check.mjs")
		cmd.Dir = dir
		out, err := cmd.CombinedOutput()
		return string(out), err
	}
	_, err = run()
	require.Error(t, err, "unmodified fixture must fail")
	qml, err := os.ReadFile(filepath.Join(dir, "ResultContent.qml"))
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "ResultContent.qml"), []byte(strings.ReplaceAll(string(qml), "Style.font.title", "Style.font.body")), 0600))
	bad := "function headingSize(base, level) { return level === 1 ? Math.round(base * 1.25) : level === 2 ? Math.round(base * 1.1) : level === 3 ? base : base + 1; }\n"
	require.NoError(t, os.WriteFile(filepath.Join(dir, "Markdown.js"), []byte(bad), 0600))
	out, err := run()
	require.Error(t, err)
	require.Contains(t, out, "H4 exceeds H3")
	good := strings.ReplaceAll(bad, ": base + 1", ": base")
	require.NoError(t, os.WriteFile(filepath.Join(dir, "Markdown.js"), []byte(good), 0600))
	out, err = run()
	require.NoError(t, err, out)
	require.Contains(t, out, "PASS:")
}
