package mcpclient

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestIsSensitiveEnvName(t *testing.T) {
	t.Run("rejects provider api keys", func(t *testing.T) {
		for _, name := range []string{
			"OPENAI_API_KEY",
			"ANTHROPIC_API_KEY",
			"GOOGLE_API_KEY",
			"COHERE_API_KEY",
			"DEEPSEEK_API_KEY",
			"OPENROUTER_API_KEY",
			"HF_TOKEN",
			"GITHUB_TOKEN",
			"AWS_SECRET_ACCESS_KEY",
			"AWS_SESSION_TOKEN",
			"AZURE_OPENAI_API_KEY",
			"DOCKER_PASSWORD",
			"NPM_TOKEN",
			"PYPI_API_TOKEN",
			"SSH_AUTH_SOCK",
			"GPG_TTY",
			"KUBECONFIG_DATA",
			"MODS_CACHE_PATH",
			"MY_DATABASE_PASSWORD",
			"WEBHOOK_SECRET",
			"PRIVATE_KEY",
		} {
			require.True(t, isSensitiveEnvName(name), "%s should be filtered as sensitive", name)
		}
	})

	t.Run("allows neutral runtime vars", func(t *testing.T) {
		for _, name := range []string{
			"PATH",
			"HOME",
			"USER",
			"USERPROFILE",
			"SystemRoot",
			"LANG",
			"LC_ALL",
			"TERM",
			"COLORTERM",
			"TMPDIR",
			"TEMP",
			"PYTHONPATH",
			"NODE_OPTIONS",
		} {
			require.False(t, isSensitiveEnvName(name), "%s should be allowed", name)
		}
	})
}

func TestFilterEnvForMCPSubprocess(t *testing.T) {
	input := []string{
		"PATH=/usr/bin",
		"OPENAI_API_KEY=sk-secret",
		"AWS_SECRET_ACCESS_KEY=abc",
		"HOME=/home/me",
		"GITHUB_TOKEN=ghp_xxx",
		"MY_DB_PASSWORD=hunter2",
		"malformed-no-equals",
		"=empty-name",
		"LANG=en_US.UTF-8",
	}
	got := filterEnvForMCPSubprocess(input)

	want := map[string]bool{
		"PATH=/usr/bin":     true,
		"HOME=/home/me":     true,
		"LANG=en_US.UTF-8":  true,
	}
	require.Len(t, got, len(want), "filtered env: %v", got)
	for _, kv := range got {
		require.True(t, want[kv], "unexpected entry preserved: %q", kv)
	}
}

func TestMCPSubprocessEnvRespectsPassEnvAll(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "sk-secret")
	t.Setenv("PATH", "/usr/bin")

	t.Run("default filters secrets", func(t *testing.T) {
		env := mcpSubprocessEnv(MCPServerConfig{})
		require.NotContains(t, env, "OPENAI_API_KEY=sk-secret",
			"default behaviour must drop sensitive parent vars")
		require.Contains(t, env, "PATH=/usr/bin")
	})

	t.Run("pass-env-all opts back into legacy behaviour", func(t *testing.T) {
		env := mcpSubprocessEnv(MCPServerConfig{PassEnvAll: true})
		require.Contains(t, env, "OPENAI_API_KEY=sk-secret",
			"pass-env-all must forward every parent var, including secrets")
	})

	t.Run("server env block is always forwarded verbatim", func(t *testing.T) {
		env := mcpSubprocessEnv(MCPServerConfig{
			Env: []string{"SERVER_SPECIFIC_TOKEN=server-only"},
		})
		require.Contains(t, env, "SERVER_SPECIFIC_TOKEN=server-only")
		require.NotContains(t, env, "OPENAI_API_KEY=sk-secret")
	})
}

func TestValidateMCPRemoteURL(t *testing.T) {
	t.Setenv("MODS_MCP_ALLOW_PRIVATE", "")

	rejectCases := map[string]string{
		"loopback hostname":     "https://localhost:8080",
		"loopback v4":           "http://127.0.0.1/api",
		"loopback v6":           "http://[::1]:8080",
		"link-local v4":         "http://169.254.169.254/latest/meta-data",
		"private v4 (10/8)":     "http://10.0.0.1/",
		"private v4 (192.168)":  "http://192.168.1.5/",
		"private v4 (172.16/12)": "http://172.16.5.5/",
		"missing host":          "http:///path",
		"empty url":             "",
		"bad scheme":            "ftp://example.com",
		"file scheme":           "file:///etc/passwd",
	}
	for name, raw := range rejectCases {
		t.Run("reject/"+name, func(t *testing.T) {
			err := validateMCPRemoteURL(raw)
			require.Error(t, err, "must reject %q", raw)
		})
	}

	acceptCases := map[string]string{
		"public https": "https://example.com/mcp",
		"public http":  "http://example.com/mcp",
		"with port":    "https://api.example.com:8443/sse",
	}
	for name, raw := range acceptCases {
		t.Run("accept/"+name, func(t *testing.T) {
			require.NoError(t, validateMCPRemoteURL(raw))
		})
	}
}

func TestValidateMCPRemoteURLOptIn(t *testing.T) {
	t.Setenv("MODS_MCP_ALLOW_PRIVATE", "1")
	for _, raw := range []string{
		"http://localhost:8080",
		"http://127.0.0.1/",
		"http://169.254.169.254/",
	} {
		require.NoError(t, validateMCPRemoteURL(raw),
			"MODS_MCP_ALLOW_PRIVATE=1 must permit %q for local development", raw)
	}
}

// TestValidateMCPRemoteURLMessageContainsOptInHint helps the user discover
// the escape hatch without having to read documentation.
func TestValidateMCPRemoteURLMessageContainsOptInHint(t *testing.T) {
	t.Setenv("MODS_MCP_ALLOW_PRIVATE", "")
	err := validateMCPRemoteURL("http://127.0.0.1/")
	require.Error(t, err)
	require.True(t, strings.Contains(err.Error(), "MODS_MCP_ALLOW_PRIVATE"),
		"error must mention the env-var opt-in: %v", err)
}
