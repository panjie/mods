//go:build integration

package app

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/panjie/mods/internal/openai"
	"github.com/panjie/mods/internal/proto"
	"github.com/panjie/mods/internal/stream"
	"github.com/stretchr/testify/require"
)

// Opt-in provider evidence, separate from the offline routing/wire tests.
// Only synthetic build metadata is sent. Never log credentials or raw errors.
func TestOpenCodeGoResponsesIntegration(t *testing.T) {
	key := os.Getenv("OPENCODE_GO_API_KEY")
	if key == "" {
		t.Skip("OPENCODE_GO_API_KEY is required")
	}
	for _, name := range []string{"gpt-6-luna", "grok-4.7", "muse-spark-1.3-contributor"} {
		t.Run(name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
			defer cancel()
			mods := &Mods{Styles: makeStyles(true), Config: &Config{}}
			cfg, err := mods.buildProviderConfigs(Model{API: "opencode-go", Name: name}, API{Name: "opencode-go", APIKey: key})
			require.NoError(t, err)
			require.True(t, cfg.OpenAI.UseResponses)
			client := openai.New(cfg.OpenAI)
			limit := int64(1024)
			calls := 0
			s := client.Request(ctx, proto.Request{
				Model: name, MaxTokens: &limit, TrackUsage: true,
				Messages: []proto.Message{
					{Role: proto.RoleSystem, Content: "You are a coding assistant checking a synthetic build. Call read_build_nonce before answering, then report the nonce. Do not invent the tool result."},
					{Role: proto.RoleUser, Content: "Read the build nonce using read_build_nonce and report it."},
				},
				Tools: []proto.ToolSpec{{Name: "read_build_nonce", Description: "Read the nonce for the synthetic build connectivity test.", InputSchema: map[string]any{"type": "object", "properties": map[string]any{}, "required": []string{}, "additionalProperties": false}}},
				ToolCaller: func(call proto.ToolCallRequest) (string, error) {
					if call.Name != "read_build_nonce" {
						return "", errors.New("unexpected tool")
					}
					calls++
					return "Build nonce: ORCHID-5826", nil
				},
			})
			defer func() { _ = s.Close() }()
			drain := func(s stream.Stream) {
				for s.Next() {
					_, err := s.Current()
					if err != nil && !errors.Is(err, stream.ErrNoContent) {
						t.Fatalf("stream decode failed: %s", strings.ReplaceAll(err.Error(), key, "[redacted]"))
					}
				}
				if err := s.Err(); err != nil {
					t.Fatalf("provider failed: %s", strings.ReplaceAll(err.Error(), key, "[redacted]"))
				}
			}
			for round := 0; round < 3; round++ {
				drain(s)
				if len(s.CallTools()) == 0 {
					break
				}
			}
			require.Positive(t, calls, "provider must actually call the tool")
			messages := s.Messages()
			require.Contains(t, messages[len(messages)-1].Content, "ORCHID-5826")
			// Round-trip persisted provider state, then use a fresh client.
			data, err := json.Marshal(messages)
			require.NoError(t, err)
			var restored []proto.Message
			require.NoError(t, json.Unmarshal(data, &restored))
			restored = append(restored, proto.Message{Role: proto.RoleUser, Content: "What was the build nonce? Repeat only that nonce; do not call tools again."})
			next := openai.New(cfg.OpenAI).Request(ctx, proto.Request{Model: name, MaxTokens: &limit, Messages: restored})
			defer func() { _ = next.Close() }()
			drain(next)
			continued := next.Messages()
			require.Greater(t, len(continued), len(restored))
			require.Contains(t, continued[len(continued)-1].Content, "ORCHID-5826")
			t.Logf("native Responses tool round and restored-history continuation passed; tokens=%d", s.Usage().TotalTokens)
		})
	}
}
