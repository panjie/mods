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
