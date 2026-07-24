package websearch

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestSearchResultFormat(t *testing.T) {
	results := []Result{
		{Title: "T1", URL: "https://u1.com", Snippet: "S1"},
		{Title: "T2", URL: "https://u2.com", Snippet: "S2"},
	}
	output := formatResults("test query", results)
	require.Contains(t, output, "test query")
	require.Contains(t, output, "1. T1")
	require.Contains(t, output, "2. T2")
	require.Contains(t, output, "https://u1.com")
	require.Contains(t, output, "S1")
}

func TestNormalizeProvider(t *testing.T) {
	tests := map[string]provider{
		"":          providerTavily,
		"tavily":    providerTavily,
		"custom":    providerCustom,
		"https://x": providerCustom,
		"unknown":   providerTavily,
	}
	for input, expected := range tests {
		t.Run(input, func(t *testing.T) {
			require.Equal(t, expected, normalizeProvider(input))
		})
	}
}

func TestSearchProviderValidation(t *testing.T) {
	ctx := context.Background()

	t.Run("default tavily requires api key", func(t *testing.T) {
		_, err := search(ctx, Config{}, "query")
		require.EqualError(t, err, "web search: tavily provider requires an API key")
	})

	t.Run("tavily requires api key", func(t *testing.T) {
		_, err := search(ctx, Config{Provider: "tavily"}, "query")
		require.EqualError(t, err, "web search: tavily provider requires an API key")
	})

	t.Run("custom requires base url", func(t *testing.T) {
		_, err := search(ctx, Config{Provider: "custom"}, "query")
		require.EqualError(t, err, "web search: custom provider requires a base URL")
	})
}

func TestIsBlockedAddress(t *testing.T) {
	cases := map[string]bool{
		// blocked
		"127.0.0.1":       true,
		"127.1.2.3":       true,
		"::1":             true,
		"10.0.0.1":        true,
		"172.16.0.1":      true,
		"192.168.1.1":     true,
		"169.254.169.254": true, // cloud metadata
		"0.0.0.0":         true,
		"fe80::1":         true, // IPv6 link-local
		"fc00::1":         true, // IPv6 ULA
		// allowed
		"8.8.8.8":       false,
		"1.1.1.1":       false,
		"104.16.123.96": false,
	}
	for ipStr, wantBlocked := range cases {
		t.Run(ipStr, func(t *testing.T) {
			ip := net.ParseIP(ipStr)
			require.NotNil(t, ip, "invalid test IP %q", ipStr)
			require.Equal(t, wantBlocked, isBlockedAddress(ip))
		})
	}
}

func TestValidateProviderURL(t *testing.T) {
	t.Run("rejects loopback IP literal", func(t *testing.T) {
		err := validateProviderURL("http://127.0.0.1:8080/search")
		require.Error(t, err)
		require.Contains(t, err.Error(), "private or loopback")
	})

	t.Run("rejects cloud metadata endpoint", func(t *testing.T) {
		err := validateProviderURL("http://169.254.169.254/latest/meta-data/")
		require.Error(t, err)
		require.Contains(t, err.Error(), "private or loopback")
	})

	t.Run("rejects private RFC1918", func(t *testing.T) {
		for _, u := range []string{
			"http://10.0.0.1/x",
			"http://192.168.1.1/x",
			"http://172.16.5.4/x",
		} {
			require.Error(t, validateProviderURL(u), "expected %s to be rejected", u)
		}
	})

	t.Run("allows public IP literal", func(t *testing.T) {
		require.NoError(t, validateProviderURL("http://8.8.8.8/search"))
	})

	t.Run("allows hostname (checked at dial time)", func(t *testing.T) {
		// Hostnames are not IP-literal checked here; the DialContext enforces
		// the policy at connect time. So validation must pass for a hostname.
		require.NoError(t, validateProviderURL("https://example.com/search"))
	})

	t.Run("rejects URL with no host", func(t *testing.T) {
		require.Error(t, validateProviderURL("http:///search"))
	})

	t.Run("opt-in env bypasses check", func(t *testing.T) {
		t.Setenv("MODS_WEB_SEARCH_ALLOW_PRIVATE", "1")
		require.NoError(t, validateProviderURL("http://127.0.0.1:8080/search"))
	})
}

// TestSearchCustomRefusesLoopback asserts that a custom provider URL pointing
// at a loopback address (typical SSRF target) is refused end-to-end.
func TestSearchCustomRefusesLoopback(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		t.Errorf("should not have reached SSRF target")
	}))
	defer server.Close()

	_, err := searchCustom(context.Background(), server.URL, "", "query", 5)
	require.Error(t, err)
	require.Contains(t, err.Error(), "private or loopback")
}

// TestSearchCustomAllowedViaEnvOptIn verifies the opt-in escape hatch lets a
// local search API be reached.
func TestSearchCustomAllowedViaEnvOptIn(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"results":[{"title":"T","url":"https://x","snippet":"S"}]}`))
	}))
	defer server.Close()

	t.Setenv("MODS_WEB_SEARCH_ALLOW_PRIVATE", "1")
	results, err := searchCustom(context.Background(), server.URL, "", "query", 5)
	require.NoError(t, err)
	require.Len(t, results, 1)
	require.Equal(t, "T", results[0].Title)
}
