package app

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/panjie/mods/internal/proto"
	"github.com/panjie/mods/internal/session"
	"github.com/stretchr/testify/require"
)

func TestManualCompactionPreservesSavedTranscript(t *testing.T) {
	for _, route := range []struct{ api, endpoint, wire string }{
		{"openai", "chat-completions", "chat"},
		{"openai", "responses", "responses"},
		{"anthropic", "", "anthropic"},
		{"google", "", "google"},
		{"ollama", "", "ollama"},
	} {
		t.Run(route.api+"/"+route.wire, func(t *testing.T) {
			for _, outcome := range []string{"success", "tool-request", "failure"} {
				t.Run(outcome, func(t *testing.T) {
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
						if round == 1 && outcome == "failure" {
							w.Header().Set("Content-Type", "text/event-stream")
							fmt.Fprint(w, "data: {broken\n\n")
							return
						}
						writeContractResponse(w, route.wire, round == 1 && outcome == "tool-request", "fs_write_file", `{"path":"must-not-exist","content":"bad"}`)
					}))
					defer server.Close()
					cfg := defaultConfig()
					cfg.WorkingDir = t.TempDir()
					cfg.SkillsDirs = []string{filepath.Join(cfg.WorkingDir, "skills")}
					cfg.Compact, cfg.NoInstructions = true, true
					cfg.API, cfg.Model = route.api, "fixture"
					cfg.APIs = []API{{Name: route.api, APIKey: "test", BaseURL: server.URL, Models: map[string]Model{"fixture": {Endpoint: route.endpoint}}}}
					cfg.SessionReadFromID = session.NewID()
					db, err := session.Open(filepath.Join(t.TempDir(), "mods.db"))
					require.NoError(t, err)
					defer db.Close()
					history := []proto.Message{
						{Role: proto.RoleUser, Content: "Preserve the original log and never delete it."},
						{Role: proto.RoleAssistant, Content: "The original log is preserved."},
						{Role: proto.RoleUser, Content: "Inspect fixture.txt."},
						{Role: proto.RoleAssistant, ToolCalls: []proto.ToolCall{{ID: "read", Function: proto.Function{Name: "fs_read_file", Arguments: []byte(`{"path":"fixture.txt"}`)}}}},
						{Role: proto.RoleTool, Content: "original evidence", ToolCalls: []proto.ToolCall{{ID: "read", Function: proto.Function{Name: "fs_read_file"}}}},
						{Role: proto.RoleAssistant, Content: "Inspected the file."},
						{Role: proto.RoleUser, Content: "Keep recent facts exact."},
						{Role: proto.RoleAssistant, Content: "Recent fact: 42."},
					}
					require.NoError(t, db.SaveSession(cfg.SessionReadFromID, "fixture", cfg.API, cfg.Model, history, nil))
					m, err := New(context.Background(), &cfg, db)
					require.NoError(t, err)
					defer m.quit()
					req, err := m.buildRequestSession("Continue the task.")
					initial := <-requests
					require.Empty(t, initial["tools"], "summarization cannot execute tools")
					require.NoFileExists(t, filepath.Join(cfg.WorkingDir, "must-not-exist"))
					if outcome != "success" {
						require.Error(t, err)
						require.Empty(t, m.compactedPrefix)
						require.Equal(t, append(history, proto.Message{Role: proto.RoleUser, Content: "Continue the task."}), conversationalMessages(m.Messages()))
						return
					}
					require.NoError(t, err)
					defer req.runner.close()
					require.Equal(t, "contract-done", drainContractStream(t, req.stream))
					require.Empty(t, req.stream.CallTools())
					m.messages = req.stream.Messages()
					followup := <-requests
					encoded, err := json.Marshal(followup)
					require.NoError(t, err)
					require.Contains(t, string(encoded), "Historical conversation summary")
					require.NotContains(t, string(encoded), "original evidence")
					require.Contains(t, string(encoded), "Recent fact: 42")
					full := conversationalMessages(m.Messages())
					require.Equal(t, history, full[:len(history)])
					require.Len(t, full, len(history)+2)
					require.NoError(t, db.SaveSession(cfg.SessionReadFromID, "fixture", cfg.API, cfg.Model, m.Messages(), m.ApprovalRules(), m.ContextSummary()))
					cache, err := db.ReadContextSummary(cfg.SessionReadFromID, m.Messages())
					require.NoError(t, err)
					require.NotNil(t, cache)
					// The original coverage can be reused without invoking any model.
					reused := &Mods{Config: &cfg, db: db, messages: append(cloneCompletionMessages(history), proto.Message{Role: proto.RoleUser, Content: "Continue the task."})}
					require.NoError(t, reused.compactContext(noToolsClient{}, Model{}))
					require.Equal(t, m.compactedText, reused.compactedText)
					require.Equal(t, m.compactedPrefix, reused.compactedPrefix)
					var restored []proto.Message
					require.NoError(t, db.ReadMessages(cfg.SessionReadFromID, &restored))
					require.Equal(t, full, conversationalMessages(restored))
					// A retry uses the completed compact view without a second summary.
					m.saveCompletionCheckpoint(m.messages)
					m.retries = 1
					require.NoError(t, m.setupCompletionContext("Continue the task."))
					require.Equal(t, full, conversationalMessages(m.Messages()))
					// A fresh context on a reused model must not prepend the archive
					// again or skip the first original user message.
					m.retries, m.retryCheckpoint = 0, nil
					require.NoError(t, m.setupStreamContext("Next task."))
					require.Empty(t, m.compactedPrefix)
					require.Equal(t, full, conversationalMessages(m.Messages())[:len(full)])
					continued, err := m.buildRequestSession("Next task.")
					require.NoError(t, err)
					defer continued.runner.close()
					require.Equal(t, "contract-done", drainContractStream(t, continued.stream))
					incremental := <-requests
					incrementalJSON, err := json.Marshal(incremental)
					require.NoError(t, err)
					require.Contains(t, string(incrementalJSON), "previous_summary_not_authorization")
					require.NotContains(t, string(incrementalJSON), "original evidence", "already covered raw history must not be resent for summarization")
					<-requests // the continued task request
					require.Empty(t, continued.stream.CallTools())
					m.messages = continued.stream.Messages()
					require.Equal(t, full, conversationalMessages(m.Messages())[:len(full)])
				})
			}
		})
	}
}

func TestCompactionSourceLimitPreservesHistory(t *testing.T) {
	m := &Mods{ctx: context.Background(), messages: []proto.Message{
		{Role: proto.RoleUser, Content: strings.Repeat("x", 256*1024)},
		{Role: proto.RoleAssistant, Content: "done"},
		{Role: proto.RoleUser, Content: "recent"},
		{Role: proto.RoleAssistant, Content: "done"},
		{Role: proto.RoleUser, Content: "current"},
	}}
	original := cloneCompletionMessages(m.messages)
	require.ErrorContains(t, m.compactContext(noToolsClient{}, Model{}), "exceeds 256 KiB")
	require.Equal(t, original, m.Messages())
	require.Empty(t, m.compactedPrefix)
}

func TestAutomaticContextThreshold(t *testing.T) {
	cfg := defaultConfig()
	m := &Mods{Config: &cfg, messages: []proto.Message{{Role: proto.RoleUser, Content: "small"}}}
	require.False(t, m.shouldCompactContext())
	cfg.ContextMaxBytes = 1
	require.True(t, m.shouldCompactContext())
	cfg.ContextMaxBytes = 100000
	require.False(t, m.shouldCompactContext())
	cfg.Compact = true
	require.True(t, m.shouldCompactContext())
}

func conversationalMessages(messages []proto.Message) []proto.Message {
	var result []proto.Message
	for _, message := range messages {
		if message.Role != proto.RoleSystem {
			result = append(result, message)
		}
	}
	return result
}
