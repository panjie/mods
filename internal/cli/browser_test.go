package cli

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestBrowserCommandPreservesAuthorizationURL(t *testing.T) {
	target := "https://auth.openai.com/api/accounts/authorize?state=one&redirect_uri=http%3A%2F%2F127.0.0.1%3A1234%2Fauth%2Fcallback"
	for _, tc := range []struct {
		goos, command string
		args          []string
	}{
		{"linux", "xdg-open", []string{target}},
		{"darwin", "open", []string{target}},
		{"windows", "rundll32.exe", []string{"url.dll,FileProtocolHandler", target}},
	} {
		name, args := browserCommand(tc.goos, target)
		require.Equal(t, tc.command, name)
		require.Equal(t, tc.args, args)
	}
}

func TestChatGPTBrowserLaunchPresentation(t *testing.T) {
	for _, failed := range []bool{false, true} {
		setupFixture(t, func(m *setupModel) {
			m.provider = "chatgpt"
			m.page = setupAuth
			result := setupResult{request: m.request, provider: "chatgpt", kind: "Starting ChatGPT authorization", authURL: "https://auth.openai.com/authorization-fallback", authDone: make(chan error, 1)}
			if failed {
				result.browserErr = errors.New("opener failed")
			}
			require.NotNil(t, m.receive(result))
			view := m.View().Content
			if failed {
				require.Contains(t, view, "Could not open your browser automatically")
				require.Contains(t, view, "authorization-fallback")
			} else {
				require.Contains(t, view, "Browser opened")
				require.NotContains(t, view, "authorization-fallback")
			}
			require.Equal(t, "Waiting for ChatGPT authorization", m.busy)
		})
	}
}
