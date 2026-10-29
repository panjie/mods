package app

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
	"time"

	"github.com/panjie/mods/internal/proto"
	"github.com/panjie/mods/internal/stream"
	"github.com/stretchr/testify/require"
)

var providerToolContractRoutes = []struct{ api, endpoint, wire string }{
	{"openai", "chat-completions", "chat"},
	{"openai", "responses", "responses"},
	{"custom-gateway", "chat-completions", "chat"},
	{"deepseek", "chat-completions", "chat"},
	{"deepseek", "responses", "responses"},
	{"qwen", "chat-completions", "chat"},
	{"qwen", "responses", "responses"},
	{"glm", "chat-completions", "chat"},
	{"kimi", "chat-completions", "chat"},
	{"minimax", "chat-completions", "chat"},
	{"azure", "", "chat"},
	{"anthropic", "", "anthropic"},
	{"google", "", "google"},
	{"ollama", "", "ollama"},
}

// Exercise production config resolution, request assembly, tool registration,
// review and dispatch against local wire fixtures. Adapter-only tests with a
// hand-written ToolCaller cannot catch a broken application ResultCaller binding.
func TestProviderToolContract(t *testing.T) {
	for _, route := range providerToolContractRoutes {
		t.Run(route.api+"/"+route.wire, func(t *testing.T) {
			for _, scenario := range []string{"read", "approve", "deny", "unavailable", "tool-error", "unknown-tool"} {
				t.Run(scenario, func(t *testing.T) {
					root := t.TempDir()
					path := filepath.Join(root, "fixture.txt")
					require.NoError(t, os.WriteFile(path, []byte("contract-original"), 0o600))
					name, args := "fs_read_file", `{"path":"fixture.txt"}`
					write := scenario == "approve" || scenario == "deny" || scenario == "unavailable"
					if write {
						name, args = "fs_write_file", `{"path":"fixture.txt","content":"contract-updated"}`
					}
					if scenario == "tool-error" {
						args = `{"path":"missing.txt"}`
					}
					if scenario == "unknown-tool" {
						name = "not_registered"
					}
					requests := make(chan map[string]any, 4)
					round := 0
					server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
						var body map[string]any
						if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
							t.Error(err)
							w.WriteHeader(http.StatusBadRequest)
							return
						}
						requests <- body
						round++
						if round > 2 {
							t.Error("unexpected extra provider request")
							w.WriteHeader(http.StatusBadRequest)
							return
						}
						writeContractResponse(w, route.wire, round == 1, name, args)
					}))
					defer server.Close()
					cfg := defaultConfig()
					cfg.API, cfg.Model = route.api, "contract-model"
					cfg.APIs = []API{{Name: route.api, APIKey: "test-key", BaseURL: server.URL + "/", Models: map[string]Model{cfg.Model: {Endpoint: route.endpoint}}}}
					cfg.WorkingDir, cfg.SkillsDirs = root, []string{filepath.Join(root, "skills")}
					cfg.NoSave, cfg.NoInstructions = true, true
					cfg.WebSearch, cfg.MCPServers = false, nil
					cfg.BuiltinTools.Filesystem, cfg.BuiltinTools.Shell = FilesystemAlways, false
					// Always forces approval even for temporary-directory writes.
					cfg.ReviewMode = ReviewAlways
					cfg.InteractiveTTYAvailable = scenario != "unavailable"
					ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
					defer cancel()
					m, err := New(ctx, &cfg, nil)
					require.NoError(t, err)
					defer m.quit()
					session, err := m.buildRequestSession("Read or update fixture.txt as requested.")
					require.NoError(t, err)
					defer session.runner.close()
					m.reviewer.startSession()
					reviews, _ := m.reviewer.snapshotSession()
					drainContractStream(t, session.stream)
					initial := <-requests
					encoded, err := json.Marshal(initial["tools"])
					require.NoError(t, err)
					require.Contains(t, string(encoded), "fs_read_file")
					require.Contains(t, string(encoded), "fs_write_file")

					var statuses []proto.ToolCallStatus
					if scenario == "approve" || scenario == "deny" {
						finished := make(chan []proto.ToolCallStatus, 1)
						go func() { finished <- session.stream.CallTools() }()
						select {
						case review := <-reviews:
							require.Equal(t, name, review.name)
							before, err := os.ReadFile(path)
							require.NoError(t, err)
							require.Equal(t, "contract-original", string(before), "execution must wait for approval")
							response := reviewResponse{}
							if scenario == "approve" {
								response.action = reviewActionApprove
							}
							review.resp <- response
						case statuses = <-finished:
							t.Fatalf("write bypassed approval: %+v", statuses)
						case <-ctx.Done():
							t.Fatal("approval did not arrive")
						}
						select {
						case statuses = <-finished:
						case <-ctx.Done():
							t.Fatal("tool execution did not finish")
						}
					} else {
						require.NotPanics(t, func() { statuses = session.stream.CallTools() })
					}
					require.Len(t, statuses, 1)
					status := statuses[0]
					require.NotEmpty(t, status.ID)
					require.Equal(t, name, status.Name)
					require.JSONEq(t, args, string(status.Arguments))
					require.Equal(t, 1, status.Index)
					require.Equal(t, 1, status.Total)
					wantError := scenario != "read" && scenario != "approve"
					if wantError {
						require.Error(t, status.Err)
						require.NotEmpty(t, status.Output)
					} else {
						require.NoError(t, status.Err)
					}
					if scenario == "unavailable" {
						require.ErrorIs(t, status.Err, errReviewUnavailable)
					}
					switch scenario {
					case "read":
						require.Equal(t, "contract-original", status.Output)
					case "deny":
						require.ErrorContains(t, status.Err, "execution denied by user")
					case "tool-error":
						require.ErrorContains(t, status.Err, "missing.txt")
					case "unknown-tool":
						require.ErrorContains(t, status.Err, `unknown tool "not_registered"`)
					}
					select {
					case review := <-reviews:
						t.Fatalf("unexpected approval request for %s", review.name)
					default:
					}
					after, err := os.ReadFile(path)
					require.NoError(t, err)
					wantFile := "contract-original"
					if scenario == "approve" {
						wantFile = "contract-updated"
					}
					require.Equal(t, wantFile, string(after))
					require.Equal(t, "contract-done", drainContractStream(t, session.stream))
					followup := <-requests
					assertContractResult(t, route.wire, followup, status, wantError)
					require.Empty(t, session.stream.CallTools(), "final answer must not execute another tool")
					var results []proto.Message
					for _, message := range session.stream.Messages() {
						if message.Role == proto.RoleTool {
							results = append(results, message)
						}
					}
					require.Len(t, results, 1)
					require.Equal(t, status.Output, results[0].Content)
					require.Equal(t, wantError, results[0].ToolCalls[0].IsError)
					require.Equal(t, status.ID, results[0].ToolCalls[0].ID)
				})
			}
		})
	}
}

func drainContractStream(t *testing.T, st stream.Stream) string {
	t.Helper()
	var content strings.Builder
	for st.Next() {
		chunk, err := st.Current()
		if errors.Is(err, stream.ErrNoContent) {
			continue
		}
		require.NoError(t, err)
		content.WriteString(chunk.Content)
	}
	require.NoError(t, st.Err())
	require.NoError(t, st.Close())
	return content.String()
}

func assertContractResult(t *testing.T, wire string, body map[string]any, status proto.ToolCallStatus, failed bool) {
	t.Helper()
	var result map[string]any
	switch wire {
	case "chat", "ollama":
		for _, raw := range body["messages"].([]any) {
			msg := raw.(map[string]any)
			if msg["role"] == "tool" {
				result = msg
			}
		}
		require.NotNil(t, result)
		require.Equal(t, status.Output, result["content"])
		if wire == "chat" {
			require.Equal(t, status.ID, result["tool_call_id"])
		}
	case "responses":
		for _, raw := range body["input"].([]any) {
			item := raw.(map[string]any)
			if item["type"] == "function_call_output" {
				result = item
			}
		}
		require.NotNil(t, result)
		require.Equal(t, status.ID, result["call_id"])
		require.Equal(t, status.Output, result["output"])
	case "anthropic":
		for _, raw := range body["messages"].([]any) {
			parts, _ := raw.(map[string]any)["content"].([]any)
			for _, part := range parts {
				item := part.(map[string]any)
				if item["type"] == "tool_result" {
					result = item
				}
			}
		}
		require.NotNil(t, result)
		require.Equal(t, status.ID, result["tool_use_id"])
		require.Equal(t, failed, result["is_error"] == true)
		content, err := json.Marshal(result["content"])
		require.NoError(t, err)
		text, err := json.Marshal(status.Output)
		require.NoError(t, err)
		require.Contains(t, string(content), string(text))
	case "google":
		for _, raw := range body["contents"].([]any) {
			for _, part := range raw.(map[string]any)["parts"].([]any) {
				if response, ok := part.(map[string]any)["functionResponse"]; ok {
					result = response.(map[string]any)
				}
			}
		}
		require.NotNil(t, result)
		require.Equal(t, status.Name, result["name"])
		key := "result"
		if failed {
			key = "error"
		}
		require.Equal(t, map[string]any{key: status.Output}, result["response"])
	}
}

func writeContractResponse(w http.ResponseWriter, wire string, tool bool, name, args string) {
	w.Header().Set("Content-Type", "text/event-stream")
	quotedArgs, _ := json.Marshal(args)
	sse := func(event string) { fmt.Fprintf(w, "data: %s\n\n", event) }
	switch wire {
	case "chat":
		if tool {
			sse(fmt.Sprintf(`{"choices":[{"index":0,"delta":{"role":"assistant","tool_calls":[{"index":0,"id":"call_1","type":"function","function":{"name":%q,"arguments":%s}}]},"finish_reason":"tool_calls"}]}`, name, quotedArgs))
		} else {
			sse(`{"choices":[{"index":0,"delta":{"role":"assistant","content":"contract-done"},"finish_reason":"stop"}]}`)
		}
		sse("[DONE]")
	case "responses":
		output := `[{"type":"message","id":"msg_1","role":"assistant","status":"completed","content":[{"type":"output_text","text":"contract-done","annotations":[]}]}]`
		if tool {
			output = fmt.Sprintf(`[{"type":"function_call","id":"fc_1","call_id":"call_1","name":%q,"arguments":%s,"status":"completed"}]`, name, quotedArgs)
		} else {
			sse(`{"type":"response.output_text.delta","sequence_number":0,"item_id":"msg_1","output_index":0,"content_index":0,"delta":"contract-done"}`)
		}
		sse(`{"type":"response.completed","sequence_number":1,"response":{"id":"resp_1","object":"response","status":"completed","model":"contract-model","output":` + output + `}}`)
		sse("[DONE]")
	case "google":
		part := `{"text":"contract-done"}`
		if tool {
			part = fmt.Sprintf(`{"functionCall":{"id":"call_1","name":%q,"args":%s}}`, name, args)
		}
		sse(`{"candidates":[{"content":{"role":"model","parts":[` + part + `]}}]}`)
	case "ollama":
		w.Header().Set("Content-Type", "application/x-ndjson")
		msg := `{"role":"assistant","content":"contract-done"}`
		if tool {
			msg = fmt.Sprintf(`{"role":"assistant","tool_calls":[{"function":{"name":%q,"arguments":%s}}]}`, name, args)
		}
		fmt.Fprintf(w, "{\"model\":\"contract-model\",\"message\":%s,\"done\":true}\n", msg)
	case "anthropic":
		event := func(kind, data string) { fmt.Fprintf(w, "event: %s\ndata: %s\n\n", kind, data) }
		event("message_start", `{"type":"message_start","message":{"id":"msg_1","type":"message","role":"assistant","model":"contract-model","content":[],"usage":{"input_tokens":1,"output_tokens":0}}}`)
		reason := "end_turn"
		if tool {
			reason = "tool_use"
			event("content_block_start", fmt.Sprintf(`{"type":"content_block_start","index":0,"content_block":{"type":"tool_use","id":"call_1","name":%q,"input":{}}}`, name))
			event("content_block_delta", fmt.Sprintf(`{"type":"content_block_delta","index":0,"delta":{"type":"input_json_delta","partial_json":%s}}`, quotedArgs))
		} else {
			event("content_block_start", `{"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`)
			event("content_block_delta", `{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"contract-done"}}`)
		}
		event("content_block_stop", `{"type":"content_block_stop","index":0}`)
		event("message_delta", fmt.Sprintf(`{"type":"message_delta","delta":{"stop_reason":%q},"usage":{"output_tokens":1}}`, reason))
		event("message_stop", `{"type":"message_stop"}`)
	}
}
