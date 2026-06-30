package prompts

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestBuiltinPrompts(t *testing.T) {
	defs := Builtin()
	byName := make(map[string]Definition, len(defs))
	for _, def := range defs {
		require.NotEmpty(t, def.Name)
		require.NotEmpty(t, def.Default)
		byName[def.Name] = def
	}

	for _, name := range []string{
		KeyIdentity,
		KeyToolSelection,
		KeyPlan,
		KeyShellClassifier,
		KeyMinimal,
		KeyFormatMarkdown,
		KeyFormatJSON,
		KeySafeWorkspaceTemplate,
		KeyApprovedPlanTemplate,
	} {
		require.Contains(t, byName, name)
	}

	require.True(t, byName[KeyIdentity].Configurable)
	require.True(t, byName[KeyShellClassifier].Configurable)
	require.False(t, byName[KeyMinimal].Configurable)
	require.Equal(t, Plan, byName[KeyPlan].Default)
	require.Equal(t, ShellClassifier, byName[KeyShellClassifier].Default)

	require.Contains(t, Identity, "perform it directly with")
	require.Contains(t, Identity, "built-in review step that prompts")
	require.Contains(t, Identity, "still carry out the requested action")
	require.Contains(t, ToolSelection, "attempt it directly rather than asking for permission first")
}
