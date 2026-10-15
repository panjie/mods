package openai

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/panjie/mods/internal/proto"
	"github.com/panjie/mods/internal/stream"
	"github.com/panjie/mods/internal/testutil/mediafixture"
	"github.com/panjie/mods/internal/tools"
	"github.com/stretchr/testify/require"
)

func TestQwenNativeAudioToolLoop(t *testing.T) {
	for _, responsesAPI := range []bool{false, true} {
		t.Run(fmt.Sprint(responsesAPI), func(t *testing.T) {
			root := t.TempDir()
			wav := mediafixture.WAV(1)
			require.NoError(t, os.WriteFile(filepath.Join(root, "a.wav"), wav, 0600))
			registry := tools.NewRegistry()
			cfg := tools.FilesystemConfig{Root: root}
			require.NoError(t, tools.RegisterImages(registry, cfg))
			require.NoError(t, tools.RegisterMedia(registry, cfg))
			registry.ConfigureMedia(true, true, nil)
			var bodies []map[string]any
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var body map[string]any
				require.NoError(t, json.NewDecoder(r.Body).Decode(&body))
				bodies = append(bodies, body)
				round := len(bodies)
				if responsesAPI {
					output := `[{"type":"function_call","id":"fc_read","call_id":"read","name":"fs_read_media","arguments":"{\"paths\":[\"a.wav\"]}","status":"completed"},{"type":"function_call","id":"fc_find","call_id":"find","name":"fs_find_media","arguments":"{\"path\":\".\",\"kinds\":[\"audio\"]}","status":"completed"}]`
					if round == 2 {
						output = `[{"type":"message","id":"m","role":"assistant","status":"completed","content":[{"type":"output_text","text":"done","annotations":[]}]}]`
					}
					writeResponsesSSE(t, w, completedResponseEvent(output, 10, 2))
					return
				}
				delta := map[string]any{"role": "assistant", "reasoning_content": "listen to the file"}
				finish := "stop"
				if round == 1 {
					delta["tool_calls"] = []map[string]any{{"index": 0, "id": "read", "type": "function", "function": map[string]any{"name": "fs_read_media", "arguments": `{"paths":["a.wav"]}`}}, {"index": 1, "id": "find", "type": "function", "function": map[string]any{"name": "fs_find_media", "arguments": `{"path":".","kinds":["audio"]}`}}}
					finish = "tool_calls"
				} else {
					delta["content"] = "done"
				}
				chunk, _ := json.Marshal(map[string]any{"id": "c", "object": "chat.completion.chunk", "created": 0, "model": "qwen3.8-omni-flash", "choices": []map[string]any{{"index": 0, "delta": delta, "finish_reason": finish}}})
				w.Header().Set("Content-Type", "text/event-stream")
				fmt.Fprintf(w, "data: %s\n\ndata: [DONE]\n\n", chunk)
			}))
			defer server.Close()
			client := New(Config{BaseURL: server.URL, HTTPClient: server.Client(), Model: "qwen3.8-omni-flash", UseResponses: responsesAPI})
			st := client.Request(context.Background(), proto.Request{Model: "qwen3.8-omni-flash", Messages: []proto.Message{{Role: proto.RoleUser, Content: "Find recordings mentioning refunds."}}, Tools: registry.Specs(), ResultCaller: func(call proto.ToolCallRequest) (proto.ToolResult, error) {
				return registry.CallResult(context.Background(), call.Name, call.Arguments)
			}})
			for round := 0; round < 2; round++ {
				for st.Next() {
					_, err := st.Current()
					if !errors.Is(err, stream.ErrNoContent) {
						require.NoError(t, err)
					}
				}
				require.NoError(t, st.Err())
				statuses := st.CallTools()
				if round == 0 {
					require.Len(t, statuses, 2)
				} else {
					require.Empty(t, statuses)
				}
				for _, s := range statuses {
					require.NoError(t, s.Err)
				}
			}
			require.NoError(t, st.Close())
			require.Len(t, bodies, 2)
			require.Empty(t, registry.ImageSearchWarning())
			attachments := 0
			for _, m := range st.Messages() {
				if len(m.Media) > 0 {
					require.Equal(t, proto.RoleTool, m.Role)
					attachments++
					require.Equal(t, wav, m.Media[0].Data)
				}
			}
			require.Equal(t, 1, attachments)
			key := "messages"
			if responsesAPI {
				key = "input"
			}
			items := bodies[1][key].([]any)
			raw, _ := json.Marshal(items)
			require.Equal(t, 1, strings.Count(string(raw), `"type":"input_audio"`))
			require.Contains(t, string(raw), "data:audio/wav;base64,")
			audioIndex := -1
			lastTool := -1
			for i, item := range items {
				m := item.(map[string]any)
				if m["role"] == "tool" || m["type"] == "function_call_output" {
					lastTool = i
				}
				encoded, _ := json.Marshal(m)
				if strings.Contains(string(encoded), `"type":"input_audio"`) {
					audioIndex = i
					require.Equal(t, "user", m["role"])
					if responsesAPI {
						require.Contains(t, string(encoded), `"audio_url"`)
					} else {
						require.Contains(t, string(encoded), `"input_audio":`)
					}
				}
			}
			require.Greater(t, audioIndex, lastTool)
			if !responsesAPI {
				require.Contains(t, string(raw), `"reasoning_content":"listen to the file"`)
			}
		})
	}
}
func TestUnsupportedAudioFailsBeforeHTTP(t *testing.T) {
	client := New(Config{Model: "gpt-other", BaseURL: "http://127.0.0.1:1"})
	st := client.Request(context.Background(), proto.Request{Model: "gpt-other", Messages: []proto.Message{{Media: []proto.MediaAttachment{{Kind: "audio", Data: []byte("x")}}}}})
	require.False(t, st.Next())
	require.ErrorContains(t, st.Err(), "does not support native audio")
}

func TestQwenMixedEncodingPreservesSourcesAndOrder(t *testing.T) {
	audio := proto.MediaAttachment{Kind: "audio", Source: "first.wav", MimeType: "audio/wav", Data: []byte("audio"), Audio: proto.AudioMetadata{Container: "wav"}}
	image := proto.MediaAttachment{Kind: "image", Source: "second.png", MimeType: "image/png", Data: []byte("pixels")}
	input := []proto.Message{{Role: proto.RoleUser, Content: "compare", Media: []proto.MediaAttachment{audio, image}}}
	chat, err := json.Marshal(fromProtoMessagesForProfile(input, providerProfileQwen))
	require.NoError(t, err)
	responses, err := fromProtoResponseInput(input, providerProfileQwen)
	require.NoError(t, err)
	wire, err := json.Marshal(responses)
	require.NoError(t, err)
	for _, b := range [][]byte{chat, wire} {
		s := string(b)
		require.Less(t, strings.Index(s, "first.wav"), strings.Index(s, "second.png"))
		require.Contains(t, s, "data:audio/wav;base64,YXVkaW8=")
		require.Contains(t, s, "data:image/png;base64,cGl4ZWxz")
	}
}
