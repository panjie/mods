package approval

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// Pre-written script payloads must be exempt from the advisory nudges: asking
// the model to restructure them is what breaks a skill's own scripts. Ad-hoc
// generated source keeps the nudges, because its text is all there is to review.
func TestScriptFilePayloadExemptsPreWrittenScripts(t *testing.T) {
	for _, tc := range []struct {
		name    string
		command string
	}{
		{name: "shell host with a script", command: "sh scripts/build.sh"},
		{name: "bash with script arguments", command: "bash scripts/deploy.sh production --dry-run"},
		{name: "interpreter with a script", command: "python tools/check.py"},
		{name: "interpreter with flags after the script", command: "python tools/check.py --verbose"},
		{name: "interpreter buffering flag", command: "python -u tools/check.py"},
		{name: "node script", command: "node server.js"},
		{name: "absolute script path", command: "/usr/local/bin/backup.sh"},
		{name: "relative script path", command: "./tools/release.sh"},
		{name: "powershell file switch", command: "pwsh -File build.ps1"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := AssessShellStaticWithPolicy(tc.command, true, ReadOnlyCommandPolicy{})
			require.True(t, got.Reviewability.ScriptFilePayload, "expected a pre-written script payload for %q", tc.command)
		})
	}
}

func TestScriptFilePayloadKeepsAdHocSourceNudged(t *testing.T) {
	for _, tc := range []struct {
		name    string
		command string
	}{
		{name: "inline python", command: "python -c 'print(1)'"},
		{name: "python module", command: "python -m pytest"},
		{name: "node eval", command: "node -e 'console.log(1)'"},
		{name: "php inline", command: "php -r 'echo 1;'"},
		{name: "shell host source flag", command: "sh -c 'rm out.txt'"},
		{name: "cmd source flag", command: "cmd /c deploy.bat"},
		{name: "encoded payload", command: "powershell -EncodedCommand ZQB4AGkAdAA="},
		{name: "compound with a script", command: "cd tools && ./build.sh"},
		{name: "pipeline around a script", command: "python tools/check.py | tee log"},
		{name: "redirected script", command: "sh build.sh > out.txt"},
		{name: "dynamic script path", command: "sh $SCRIPT"},
		{name: "glob script path", command: "sh *.sh"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := AssessShellStaticWithPolicy(tc.command, true, ReadOnlyCommandPolicy{})
			require.False(t, got.Reviewability.ScriptFilePayload, "expected ad-hoc source for %q", tc.command)
		})
	}
}

func TestScriptFilePayloadArgvShape(t *testing.T) {
	require.True(t, AnalyzeProcessReviewability("python", []string{"tools/check.py"}, true).ScriptFilePayload)
	require.True(t, AnalyzeProcessReviewability("bash", []string{"build.sh", "release"}, true).ScriptFilePayload)
	require.True(t, AnalyzeProcessReviewability("./tools/release.sh", nil, true).ScriptFilePayload)
	require.False(t, AnalyzeProcessReviewability("python", []string{"-c", "print(1)"}, true).ScriptFilePayload)
	require.False(t, AnalyzeProcessReviewability("sh", []string{"-c", "rm out.txt"}, true).ScriptFilePayload)
	require.False(t, AnalyzeProcessReviewability("node", []string{"-e", "console.log(1)"}, true).ScriptFilePayload)
	require.False(t, AnalyzeProcessReviewability("git", []string{"status"}, true).ScriptFilePayload)
}

// The exemption is syntax only: it never becomes a path decision, so a script
// path outside the workspace is still recognised.
func TestScriptFilePayloadIgnoresLocation(t *testing.T) {
	require.True(t, AnalyzeProcessReviewability("python", []string{`/opt/skills/run.py`}, true).ScriptFilePayload)
	require.True(t, AnalyzeProcessReviewability("bash", []string{`C:\skills\run.sh`}, false).ScriptFilePayload)
}
