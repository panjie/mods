//go:build windows

package approval

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestAssessPowerShellStaticElispArgIsNotDynamic(t *testing.T) {
	t.Cleanup(func() { CloseBridge() })

	assessment := AssessShellStaticWithPolicy(
		`& emacs --batch --eval "(json-insert (emacs-startup-usage))"`,
		false,
		ReadOnlyCommandPolicy{},
	)
	require.Empty(t, assessment.DynamicTargets,
		"a balanced, double-quoted elisp argument is program data, not a runtime target")

	assessment = AssessShellStaticWithPolicy(
		`& emacs --eval '(message "x" (f))'`,
		false,
		ReadOnlyCommandPolicy{},
	)
	require.Empty(t, assessment.DynamicTargets,
		"a single-quoted argument never interpolates, so its content is not a runtime target")
}

func TestAssessPowerShellStaticScriptBlockArgIsNotDynamic(t *testing.T) {
	t.Cleanup(func() { CloseBridge() })

	assessment := AssessShellStaticWithPolicy(
		`Measure-Command { & "C:\tools\emacs\bin\emacs.exe" --batch --eval "(progn (load \"C:/Users/panjie/AppData/Roaming/.emacs.d/init.el\"))" 2>$null | Out-Null } | Select-Object -ExpandProperty TotalSeconds`,
		false,
		ReadOnlyCommandPolicy{},
	)
	require.Empty(t, assessment.DynamicTargets,
		"a script block argument is code, not a runtime path target")

	assessment = AssessShellStaticWithPolicy(
		`Measure-Command { Set-Content $f x }`,
		false,
		ReadOnlyCommandPolicy{},
	)
	require.Contains(t, assessment.DynamicTargets, `$f`,
		"runtime targets inside the script block must still be surfaced")
}
