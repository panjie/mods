package anthropic

import (
	"context"
	"errors"
	"testing"

	SDK "github.com/anthropics/anthropic-sdk-go"
	"github.com/panjie/mods/internal/proto"
	"github.com/stretchr/testify/require"
)

func TestMessageBudgeterRunsInitiallyAndStopsFailedFollowup(t *testing.T) {
	wantErr := errors.New("budget exceeded")
	calls := 0
	client := New(DefaultConfig("test"))
	st := client.Request(context.Background(), proto.Request{
		Messages: []proto.Message{{Role: proto.RoleUser, Content: "hello"}},
		MessageBudgeter: func(messages []proto.Message) ([]proto.Message, error) {
			calls++
			return nil, wantErr
		},
	})
	require.False(t, st.Next())
	require.ErrorIs(t, st.Err(), wantErr)
	require.Equal(t, 1, calls)

	followup := &Stream{
		done:     true,
		messages: []proto.Message{{Role: proto.RoleUser, Content: "hello"}},
		budgeter: func(messages []proto.Message) ([]proto.Message, error) {
			calls++
			return nil, wantErr
		},
	}
	require.False(t, followup.Next())
	require.ErrorIs(t, followup.Err(), wantErr)
	require.Equal(t, 2, calls)
}

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

func TestTokenUsageFromMessageIncludesCacheTokens(t *testing.T) {
	message := SDK.Message{Usage: SDK.Usage{
		InputTokens: 7, CacheCreationInputTokens: 3,
		CacheReadInputTokens: 5, OutputTokens: 4,
	}}
	want := proto.TokenUsage{InputTokens: 15, OutputTokens: 4, TotalTokens: 19}
	if got := tokenUsageFromMessage(message); got != want {
		t.Fatalf("tokenUsageFromMessage() = %#v, want %#v", got, want)
	}
}
