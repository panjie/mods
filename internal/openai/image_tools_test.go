package openai

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"image/png"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/panjie/mods/internal/proto"
	"github.com/panjie/mods/internal/stream"
	"github.com/panjie/mods/internal/tools"
	"github.com/stretchr/testify/require"
)

func TestNativeDirectoryImageToolLoop(t *testing.T) {
	for _, responsesAPI := range []bool{false, true} {
		t.Run(fmt.Sprint("responses=", responsesAPI), func(t *testing.T) {
			root := t.TempDir()
			var pixels bytes.Buffer
			require.NoError(t, png.Encode(&pixels, image.NewRGBA(image.Rect(0, 0, 2, 2))))
			for _, name := range []string{"a.png", "b.png"} {
				require.NoError(t, os.WriteFile(filepath.Join(root, name), pixels.Bytes(), 0600))
			}
			registry := tools.NewRegistry()
			require.NoError(t, tools.RegisterImages(registry, tools.FilesystemConfig{Root: root}))
			var bodies []map[string]any
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var body map[string]any
				require.NoError(t, json.NewDecoder(r.Body).Decode(&body))
				bodies = append(bodies, body)
				round := len(bodies)
				var calls []map[string]any
				switch round {
				case 1:
					calls = []map[string]any{{"id": "find", "name": "fs_find_images", "arguments": `{"path":".","recursive":true}`}}
				case 2:
					calls = []map[string]any{{"id": "read1", "name": "fs_read_images", "arguments": `{"paths":["a.png"]}`}, {"id": "read2", "name": "fs_read_images", "arguments": `{"paths":["b.png"]}`}}
				}
				if responsesAPI {
					var output []map[string]any
					for _, call := range calls {
						output = append(output, map[string]any{"type": "function_call", "id": "fc_" + call["id"].(string), "call_id": call["id"], "name": call["name"], "arguments": call["arguments"], "status": "completed"})
					}
					if round == 3 {
						output = []map[string]any{{"type": "message", "id": "m", "role": "assistant", "status": "completed", "content": []map[string]any{{"type": "output_text", "text": "done", "annotations": []any{}}}}}
					}
					encoded, _ := json.Marshal(output)
					writeResponsesSSE(t, w, completedResponseEvent(string(encoded), 10, 2))
					return
				}
				w.Header().Set("Content-Type", "text/event-stream")
				delta := map[string]any{"role": "assistant", "reasoning_content": "inspect pixels"}
				finish := "tool_calls"
				var wireCalls []map[string]any
				for i, call := range calls {
					wireCalls = append(wireCalls, map[string]any{"index": i, "id": call["id"], "type": "function", "function": map[string]any{"name": call["name"], "arguments": call["arguments"]}})
				}
				if round < 3 {
					delta["tool_calls"] = wireCalls
				} else {
					delta["content"] = "done"
					finish = "stop"
				}
				chunk, _ := json.Marshal(map[string]any{"id": "c", "object": "chat.completion.chunk", "created": 0, "model": "deepseek-flash", "choices": []map[string]any{{"index": 0, "delta": delta, "finish_reason": finish}}})
				_, _ = fmt.Fprintf(w, "data: %s\n\ndata: [DONE]\n\n", chunk)
			}))
			defer server.Close()
			client := New(Config{BaseURL: server.URL, HTTPClient: server.Client(), AuthToken: "k", UseResponses: responsesAPI, ProviderProfile: ProviderProfileDeepSeek})
			st := client.Request(context.Background(), proto.Request{Model: "custom-vision-model", Messages: []proto.Message{{Role: proto.RoleUser, Content: "Find images containing target text in this directory and its subdirectories."}}, Tools: registry.Specs(), ResultCaller: func(call proto.ToolCallRequest) (proto.ToolResult, error) {
				return registry.CallResult(context.Background(), call.Name, call.Arguments)
			}})
			for round := 0; round < 3; round++ {
				for st.Next() {
					_, err := st.Current()
					if !errors.Is(err, stream.ErrNoContent) {
						require.NoError(t, err)
					}
				}
				require.NoError(t, st.Err())
				statuses := st.CallTools()
				if round < 2 {
					require.NotEmpty(t, statuses)
				} else {
					require.Empty(t, statuses)
				}
				for _, status := range statuses {
					require.NoError(t, status.Err)
				}
			}
			require.NoError(t, st.Close())
			require.Len(t, bodies, 3)
			require.Empty(t, registry.ImageSearchWarning())
			imageCount := 0
			for _, msg := range st.Messages() {
				for _, img := range msg.ImageAttachments() {
					imageCount++
					require.Equal(t, pixels.Bytes(), img.Data)
					require.NotEmpty(t, img.Source)
				}
			}
			require.Equal(t, 2, imageCount)
			if responsesAPI {
				input := bodies[2]["input"].([]any)
				outputs := 0
				for _, item := range input {
					m := item.(map[string]any)
					if m["type"] != "function_call_output" || m["call_id"] == "find" {
						continue
					}
					outputs++
					parts := m["output"].([]any)
					require.Len(t, parts, 3)
					require.Equal(t, "input_image", parts[2].(map[string]any)["type"])
					require.Contains(t, parts[1].(map[string]any)["text"], ".png")
				}
				require.Equal(t, 2, outputs)
			} else {
				messages := bodies[2]["messages"].([]any)
				n := len(messages)
				require.Equal(t, "tool", messages[n-3].(map[string]any)["role"])
				require.Equal(t, "tool", messages[n-2].(map[string]any)["role"])
				last := messages[n-1].(map[string]any)
				require.Equal(t, "user", last["role"])
				parts := last["content"].([]any)
				require.Len(t, parts, 5)
				require.Equal(t, "image_url", parts[1].(map[string]any)["type"])
				require.Equal(t, "image_url", parts[3].(map[string]any)["type"])
			}
		})
	}
}

func TestMediaCapabilitiesFollowProfile(t *testing.T) {
	for _, responsesAPI := range []bool{false, true} {
		for _, profile := range []ProviderProfile{ProviderProfileOpenAI, ProviderProfileDeepSeek, ProviderProfileQwen} {
			c := New(Config{UseResponses: responsesAPI, ProviderProfile: profile})
			caps := c.Capabilities()
			require.True(t, caps.Images)
			require.True(t, caps.ToolImages)
			for _, enabled := range []bool{caps.Audio, caps.ToolAudio, caps.Video, caps.ToolVideo, caps.VideoAudio} {
				require.Equal(t, profile == ProviderProfileQwen, enabled)
			}
		}
	}
}

func TestDeepSeekResponsesAcceptsUserImage(t *testing.T) {
	var body []byte
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ = io.ReadAll(r.Body)
		writeResponsesSSE(t, w, completedResponseEvent(`[]`, 1, 1))
	}))
	defer server.Close()
	c := New(Config{BaseURL: server.URL, HTTPClient: server.Client(), AuthToken: "k", ProviderProfile: ProviderProfileDeepSeek, UseResponses: true})
	st := c.Request(context.Background(), proto.Request{Model: "custom-vision-model", Messages: []proto.Message{{Role: proto.RoleUser, Content: "read", Images: []proto.Image{{Data: []byte("pixels"), MimeType: "image/png"}}}}})
	for st.Next() {
		_, err := st.Current()
		if !errors.Is(err, stream.ErrNoContent) {
			require.NoError(t, err)
		}
	}
	require.NoError(t, st.Err())
	require.Contains(t, string(body), "input_image")
	require.NoError(t, st.Close())
}
