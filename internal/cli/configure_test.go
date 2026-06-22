package cli

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestBuildProviderOptionsIncludesAddProvider(t *testing.T) {
	saveConfig := config
	defer func() { config = saveConfig }()
	config = Config{
		PersistentConfig: PersistentConfig{
			APIs: []API{{Name: "openai"}},
		},
	}

	opts := buildProviderOptions()
	require.NotEmpty(t, opts)
	require.Equal(t, addProviderOption, opts[len(opts)-1].Value)
}

func TestBuildModelOptionsIncludesAddModel(t *testing.T) {
	saveConfig := config
	defer func() { config = saveConfig }()
	config = Config{
		PersistentConfig: PersistentConfig{
			APIs: []API{{
				Name: "openai",
				Models: map[string]Model{
					"gpt-5.5": {},
				},
			}},
		},
	}

	opts := buildModelOptions("openai")
	require.NotEmpty(t, opts)
	require.Equal(t, addModelOption, opts[len(opts)-1].Value)
}

func TestResolveWizardProviderModel(t *testing.T) {
	saveConfig := config
	defer func() { config = saveConfig }()
	config = Config{
		PersistentConfig: PersistentConfig{
			APIs: []API{{Name: "openrouter", BaseURL: "https://openrouter.ai/api/v1"}},
		},
	}

	apiName, modelName, baseURL, addedModel := resolveWizardProviderModel(
		addProviderOption,
		"",
		"groq",
		"llama-3.3-70b-versatile",
		"https://api.groq.com/openai/v1",
	)
	require.Equal(t, "groq", apiName)
	require.Equal(t, "llama-3.3-70b-versatile", modelName)
	require.Equal(t, "https://api.groq.com/openai/v1", baseURL)
	require.True(t, addedModel)

	apiName, modelName, baseURL, addedModel = resolveWizardProviderModel(
		"openrouter",
		addModelOption,
		"",
		"vendor/gpt-5.5:latest",
		"",
	)
	require.Equal(t, "openrouter", apiName)
	require.Equal(t, "vendor/gpt-5.5:latest", modelName)
	require.Equal(t, "https://openrouter.ai/api/v1", baseURL)
	require.True(t, addedModel)

	apiName, modelName, baseURL, addedModel = resolveWizardProviderModel(
		"openrouter",
		"anthropic/claude-sonnet-4-6",
		"",
		"",
		"",
	)
	require.Equal(t, "openrouter", apiName)
	require.Equal(t, "anthropic/claude-sonnet-4-6", modelName)
	require.Equal(t, "https://openrouter.ai/api/v1", baseURL)
	require.False(t, addedModel)
}

func TestValidateNewProviderName(t *testing.T) {
	saveConfig := config
	defer func() { config = saveConfig }()
	config = Config{
		PersistentConfig: PersistentConfig{
			APIs: []API{{Name: "openai"}},
		},
	}

	require.NoError(t, validateNewProviderName("groq_1"))
	require.Error(t, validateNewProviderName(""))
	require.Error(t, validateNewProviderName("Groq"))
	require.Error(t, validateNewProviderName("openai"))
}

func TestValidateNewModelName(t *testing.T) {
	saveConfig := config
	defer func() { config = saveConfig }()
	config = Config{
		PersistentConfig: PersistentConfig{
			APIs: []API{{
				Name: "openrouter",
				Models: map[string]Model{
					"anthropic/claude-sonnet-4-6": {},
				},
			}},
		},
	}

	require.NoError(t, validateNewModelName("openrouter", "vendor/gpt-5.5:latest"))
	require.Error(t, validateNewModelName("openrouter", ""))
	require.Error(t, validateNewModelName("openrouter", "anthropic/claude-sonnet-4-6"))
}
