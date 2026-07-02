package anthropic

import "testing"

func TestNormalizeBaseURL(t *testing.T) {
	cases := []struct {
		name, in, want string
	}{
		{"host root", "https://api.anthropic.com", "https://api.anthropic.com"},
		{"trailing v1", "https://api.anthropic.com/v1", "https://api.anthropic.com"},
		{"full messages endpoint", "https://api.anthropic.com/v1/messages", "https://api.anthropic.com"},
		{"documented gateway endpoint", "https://opencode.ai/zen/go/v1/messages", "https://opencode.ai/zen/go"},
		{"bare messages suffix", "https://gateway.example.com/messages", "https://gateway.example.com"},
		{"custom path preserved", "https://gateway.example.com/custom", "https://gateway.example.com/custom"},
		{"trailing v1 with custom path", "https://gateway.example.com/proxy/v1", "https://gateway.example.com/proxy"},
		{"trailing slash on v1", "https://host/v1/", "https://host"},
		{"trailing slash on messages", "https://host/v1/messages/", "https://host"},
		{"surrounding whitespace trimmed", "  https://host/v1/messages  ", "https://host"},
		{"empty stays empty", "", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := NormalizeBaseURL(c.in); got != c.want {
				t.Errorf("NormalizeBaseURL(%q) = %q, want %q", c.in, got, c.want)
			}
		})
	}
}
