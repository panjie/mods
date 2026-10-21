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

func TestQwenNativeMediaToolLoop(t *testing.T) {
	for _, fixture := range []struct {
		kind, name, mime string
		data             []byte
	}{
		{"audio", "a.wav", "audio/wav", mediafixture.WAV(1)},
		{"video", "a.mp4", "video/mp4", mediafixture.Video()},
	} {
		t.Run(fixture.kind, func(t *testing.T) {
			for _, responsesAPI := range []bool{false, true} {
				api := "chat"
				if responsesAPI {
					api = "responses"
				}
				t.Run(api, func(t *testing.T) {
					root := t.TempDir()
					require.NoError(t, os.WriteFile(filepath.Join(root, fixture.name), fixture.data, 0600))
					registry := tools.NewRegistry()
					cfg := tools.FilesystemConfig{Root: root}
					require.NoError(t, tools.RegisterImages(registry, cfg))
					require.NoError(t, tools.RegisterMedia(registry, cfg))
					registry.ConfigureMedia(true, true, nil, fixture.kind == "video")
					readArgs := fmt.Sprintf(`{"paths":[%q]}`, fixture.name)
					findArgs := fmt.Sprintf(`{"path":".","kinds":[%q]}`, fixture.kind)
					var bodies []map[string]any
					server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
						var body map[string]any
						require.NoError(t, json.NewDecoder(r.Body).Decode(&body))
						bodies = append(bodies, body)
						round := len(bodies)
						if responsesAPI {
							outputJSON, err := json.Marshal([]map[string]any{
								{"type": "function_call", "id": "fc_read", "call_id": "read", "name": "fs_read_media", "arguments": readArgs, "status": "completed"},
								{"type": "function_call", "id": "fc_find", "call_id": "find", "name": "fs_find_media", "arguments": findArgs, "status": "completed"},
							})
							require.NoError(t, err)
							output := string(outputJSON)
							if round == 2 {
								output = `[{"type":"message","id":"m","role":"assistant","status":"completed","content":[{"type":"output_text","text":"done","annotations":[]}]}]`
							}
							writeResponsesSSE(t, w, completedResponseEvent(output, 10, 2))
							return
						}
						delta := map[string]any{"role": "assistant", "reasoning_content": "listen to the file"}
						finish := "stop"
						if round == 1 {
							delta["tool_calls"] = []map[string]any{{"index": 0, "id": "read", "type": "function", "function": map[string]any{"name": "fs_read_media", "arguments": readArgs}}, {"index": 1, "id": "find", "type": "function", "function": map[string]any{"name": "fs_find_media", "arguments": findArgs}}}
							finish = "tool_calls"
						} else {
							delta["content"] = "done"
						}
						chunk, _ := json.Marshal(map[string]any{"id": "c", "object": "chat.completion.chunk", "created": 0, "model": "custom-media-model", "choices": []map[string]any{{"index": 0, "delta": delta, "finish_reason": finish}}})
						w.Header().Set("Content-Type", "text/event-stream")
						fmt.Fprintf(w, "data: %s\n\ndata: [DONE]\n\n", chunk)
					}))
					defer server.Close()
					client := New(Config{BaseURL: server.URL, HTTPClient: server.Client(), ProviderProfile: ProviderProfileQwen, UseResponses: responsesAPI})
					st := client.Request(context.Background(), proto.Request{Model: "custom-media-model", Messages: []proto.Message{{Role: proto.RoleUser, Content: "Find recordings mentioning refunds."}}, Tools: registry.Specs(), ResultCaller: func(call proto.ToolCallRequest) (proto.ToolResult, error) {
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
							require.Equal(t, fixture.data, m.Media[0].Data)
						}
					}
					require.Equal(t, 1, attachments)
					partType := "input_audio"
					if fixture.kind == "video" {
						partType = "video_url"
						if responsesAPI {
							partType = "input_video"
						}
					}
					key := "messages"
					if responsesAPI {
						key = "input"
					}
					items := bodies[1][key].([]any)
					raw, _ := json.Marshal(items)
					require.Equal(t, 1, strings.Count(string(raw), `"type":"`+partType+`"`))
					require.Contains(t, string(raw), "data:"+fixture.mime+";base64,")
					mediaIndex := -1
					lastTool := -1
					for i, item := range items {
						m := item.(map[string]any)
						if m["role"] == "tool" || m["type"] == "function_call_output" {
							lastTool = i
						}
						encoded, _ := json.Marshal(m)
						if strings.Contains(string(encoded), `"type":"`+partType+`"`) {
							mediaIndex = i
							require.Equal(t, "user", m["role"])
							if fixture.kind == "video" {
								require.Contains(t, string(encoded), `"video_url":`)
								require.Contains(t, string(encoded), `"fps":2`)
								require.Contains(t, string(raw), "audio track present: true")
							} else if responsesAPI {
								require.Contains(t, string(encoded), `"audio_url"`)
							} else {
								require.Contains(t, string(encoded), `"input_audio":`)
							}
						}
					}
					require.Greater(t, mediaIndex, lastTool)
					if !responsesAPI {
						require.Contains(t, string(raw), `"reasoning_content":"listen to the file"`)
					}
				})
			}
		})
	}
}
