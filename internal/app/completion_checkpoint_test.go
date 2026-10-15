package app

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/panjie/mods/internal/proto"
	"github.com/panjie/mods/internal/session"
	toolregistry "github.com/panjie/mods/internal/tools"
	"github.com/stretchr/testify/require"
)

func TestCompletionRetryPreservesExecutedTools(t *testing.T) {
	for _, saved := range []bool{false, true} {
		t.Run(fmt.Sprintf("saved=%v", saved), func(t *testing.T) {
			requests := make(chan []json.RawMessage, 8)
			requestNumber := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var body struct {
					Messages []json.RawMessage `json:"messages"`
				}
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
					t.Error(err)
					w.WriteHeader(http.StatusBadRequest)
					return
				}
				requests <- body.Messages
				requestNumber++
				w.Header().Set("Content-Type", "text/event-stream")
				if requestNumber == 1 {
					fmt.Fprint(w, "data: {\"id\":\"tool\",\"choices\":[{\"index\":0,\"delta\":{\"role\":\"assistant\",\"content\":\"Working.\",\"tool_calls\":[{\"index\":0,\"id\":\"call_1\",\"type\":\"function\",\"function\":{\"name\":\"counter\",\"arguments\":\"{}\"}}]},\"finish_reason\":\"tool_calls\"}]}\n\ndata: [DONE]\n\n")
					return
				}
				if requestNumber < 4 {
					fmt.Fprint(w, "data: {\"id\":\"partial\",\"choices\":[{\"index\":0,\"delta\":{\"role\":\"assistant\",\"content\":\"broken partial\"}}]}\n\ndata: {broken-json\n\n")
					return
				}
				fmt.Fprint(w, "data: {\"id\":\"answer\",\"choices\":[{\"index\":0,\"delta\":{\"role\":\"assistant\",\"content\":\"Done.\"},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n")
			}))
			defer server.Close()
			cfg := defaultConfig()
			cfg.WorkingDir = t.TempDir()
			cfg.API, cfg.Model = "openai", "test"
			cfg.APIs = []API{{Name: "openai", APIKey: "fake", BaseURL: server.URL + "/", Models: map[string]Model{"test": {Endpoint: "chat-completions"}}}}
			cfg.NoSave, cfg.NoInstructions = !saved, true
			cfg.ReviewMode, cfg.MaxRetries = ReviewNever, 4
			cfg.SkillsDirs = []string{t.TempDir()}
			cfg.Prefix = "Only once."
			var db *DB
			if saved {
				var err error
				db, err = session.Open(filepath.Join(t.TempDir(), "mods.db"))
				require.NoError(t, err)
				defer db.Close()
				cfg.SessionReadFromID = session.NewID()
				require.NoError(t, db.SaveSession(cfg.SessionReadFromID, "test", "openai", "test", []proto.Message{{Role: proto.RoleUser, Content: "Earlier."}, {Role: proto.RoleAssistant, Content: "Earlier answer."}}, nil))
			}
			m, err := New(context.Background(), &cfg, db)
			require.NoError(t, err)
			defer m.quit()
			m.Input = "increment once"
			calls := 0
			run := m.startCompletionCmd(m.Input)
			firstEvent := run().(streamEventMsg)
			runner := firstEvent.runner
			require.NoError(t, runner.cleanup.Register(toolregistry.Tool{
				Spec: proto.ToolSpec{Name: "counter"}, Capabilities: toolregistry.ToolCapabilities{Mutable: true},
				Call: func(context.Context, json.RawMessage) (string, error) {
					calls++
					return "incremented", nil
				},
			}))
			consume := func(event streamEventMsg) streamEventMsg {
				for i := 0; i < 20 && event.kind == streamEventChunk; i++ {
					_, cmd := m.Update(event)
					event = cmd().(streamEventMsg)
				}
				return event
			}
			require.Equal(t, streamEventToolCallsStart, consume(firstEvent).kind)
			results := runner.toolCallsCmd()().(streamEventMsg)
			require.Len(t, results.results, 1)
			require.NoError(t, results.results[0].Err)
			m.Update(results) // publish the completed batch before receiving the next round
			checkpointLen := len(m.retryCheckpoint.messages)
			event := consume(runner.receiveCmd()().(streamEventMsg))
			<-requests // initial request
			completedRequest, err := json.Marshal(<-requests)
			require.NoError(t, err)
			require.Contains(t, string(completedRequest), `"role":"tool"`)
			require.Contains(t, string(completedRequest), `"tool_call_id":"call_1"`)
			for retry := 0; retry < 2; retry++ {
				require.Equal(t, streamEventError, event.kind)
				_, errCmd := m.Update(event)
				rm, ok := errCmd().(retryMsg)
				require.True(t, ok)
				cmd := m.startCompletionCmd(rm.content)
				require.Equal(t, "Working.", m.Output, "discard only the failed round's output")
				event = consume(cmd().(streamEventMsg))
			}
			require.Equal(t, streamEventToolCallsStart, event.kind)
			m.Update(event.runner.toolCallsCmd()().(streamEventMsg))
			require.Equal(t, 1, calls)
			require.Contains(t, m.Output, "Working.")
			require.Contains(t, m.Output, "Done.")
			require.NotContains(t, m.Output, "broken partial")
			require.Equal(t, checkpointLen+1, len(m.messages))
			for retry := 0; retry < 2; retry++ {
				messages, err := json.Marshal(<-requests)
				require.NoError(t, err)
				require.JSONEq(t, string(completedRequest), string(messages), "every retry must contain exactly the completed tool history")
			}
		})
	}
}

func TestCompletionCheckpointIsolatesProviderState(t *testing.T) {
	m := testMods(t)
	system := structuredSystemMessage("tools", proto.SystemSectionExecutionTools)
	messages := []proto.Message{system, {
		Role:         proto.RoleAssistant,
		Images:       []proto.Image{{Data: []byte("image")}},
		Media:        []proto.MediaAttachment{{Kind: "audio", Data: []byte("audio")}},
		ToolCalls:    []proto.ToolCall{{Function: proto.Function{Arguments: []byte("{}")}}},
		ProviderData: map[string]json.RawMessage{"anthropic.messages.content": []byte(`[]`)},
	}}
	m.saveCompletionCheckpoint(messages)
	messages[1].Images[0].Data[0] = 'X'
	messages[1].Media[0].Data[0] = 'X'
	messages[1].ToolCalls[0].Function.Arguments[0] = 'X'
	messages[1].ProviderData["anthropic.messages.content"][0] = 'X'
	m.retries = 1
	require.NoError(t, m.setupCompletionContext("original user"))
	require.Len(t, m.messages, 1)
	require.Equal(t, "image", string(m.messages[0].Images[0].Data))
	require.Equal(t, "audio", string(m.messages[0].Media[0].Data))
	require.Equal(t, "{}", string(m.messages[0].ToolCalls[0].Function.Arguments))
	require.Equal(t, "[]", string(m.messages[0].ProviderData["anthropic.messages.content"]))
	// A subsequent attempt must not mutate the checkpoint either.
	m.messages[0].ProviderData["anthropic.messages.content"][0] = 'X'
	require.NoError(t, m.setupCompletionContext("original user"))
	require.Equal(t, "[]", string(m.messages[0].ProviderData["anthropic.messages.content"]))
}
