//go:build integration

package blackbox_test

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// This evaluates model behavior, unlike prompt assembly assertions. The fixture
// only writes its invocation counter; it never queries login items or credentials.
func TestRealAIBlackBoxDiagnosticReuse(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fixture uses POSIX sh")
	}
	h := newRealAIHarnessWithTools(t, true, true, "never")
	if !h.profile.filesystemTools {
		t.Skip("provider does not support tools")
	}
	script := `#!/bin/sh
cd "$(dirname "$0")" || exit 2
printf 'call\n' >> calls.log
i=0
while [ "$i" -lt 2500 ]; do
 printf 'Background record %04d: unrelated application, disabled\n' "$i"
 i=$((i + 1))
done
printf 'Juniper loginEnabled=false marker=CEDAR-7391\n'
`
	if err := os.WriteFile(filepath.Join(h.cwd, "diagnostic.sh"), []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	stdout, _ := h.run(t, "", "Ensure the fictional Juniper app is disabled at login. This is an isolated simulation: ./diagnostic.sh is the authoritative diagnostic and produces about 140 KB of records. Do not inspect real system settings. Report its current loginEnabled value and marker. If already disabled, no change is needed.")
	calls, err := os.ReadFile(filepath.Join(h.cwd, "calls.log"))
	if err != nil {
		t.Fatal(err)
	}
	count := strings.Count(string(calls), "call\n")
	if count < 1 || count > 2 {
		t.Fatalf("expected one capture (at most one rerun after truncation), got %d", count)
	}
	if !strings.Contains(stdout, "CEDAR-7391") || !strings.Contains(stdout, "false") {
		t.Fatalf("missing diagnostic evidence: %s", stdout)
	}
	t.Logf("provider: %s; diagnostic invocations: %d; response: %s", h.profile.name, count, stdout)
}
