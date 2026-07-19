package cli

import (
	"testing"

	"github.com/panjie/mods/internal/selfhelp"
	"github.com/spf13/pflag"
	"github.com/stretchr/testify/require"
)

func TestSelfHelpFlagCatalogOmitsRuntimeDefaults(t *testing.T) {
	flags := pflag.NewFlagSet("test", pflag.ContinueOnError)
	flags.String("model", "private-model-name", "Choose the model")

	groups := selfHelpFlagGroups(flags)
	require.Len(t, groups, 1)
	require.Equal(t, flagCategoryModelProvider, groups[0].Name)
	require.Equal(t, "model", groups[0].Flags[0].Name)

	reference := selfhelp.NewReference(selfhelp.Catalog{Flags: groups})
	content, err := reference.Lookup(selfhelp.TopicCLI)
	require.NoError(t, err)
	require.Contains(t, content, "--model")
	require.Contains(t, content, "Choose the model")
	require.NotContains(t, content, "private-model-name")
}
