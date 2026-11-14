package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/panjie/mods/internal/proto"
	"github.com/panjie/mods/internal/session"
	"github.com/panjie/mods/internal/stream"
	toolregistry "github.com/panjie/mods/internal/tools"
	"github.com/stretchr/testify/require"
)

type summaryFixtureClient struct {
	request func(context.Context, proto.Request) stream.Stream
}

func (c summaryFixtureClient) Request(ctx context.Context, r proto.Request) stream.Stream {
	return c.request(ctx, r)
}
func (summaryFixtureClient) Capabilities() stream.Capabilities { return stream.Capabilities{} }

func TestChunkedCompactionPreservesAllEvidenceAndIsAtomic(t *testing.T) {
	for _, outcome := range []string{"success", "failure", "cancel", "tool"} {
		t.Run(outcome, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			cfg := defaultConfig()
			m := &Mods{ctx: ctx, Config: &cfg, messages: []proto.Message{
				{Role: proto.RoleUser, Content: strings.Repeat("中文\n\"<> evidence ", 30000)},
				{Role: proto.RoleAssistant, Content: "old result"},
				{Role: proto.RoleUser, Content: "previous"}, {Role: proto.RoleAssistant, Content: "recent result"},
				{Role: proto.RoleUser, Content: "current"},
			}}
			original := m.Messages()
			var fragments strings.Builder
			calls := 0
			client := summaryFixtureClient{request: func(_ context.Context, r proto.Request) stream.Stream {
				calls++
				require.Empty(t, r.Tools)
				require.Nil(t, r.ToolCaller)
				require.Nil(t, r.ResultCaller)
				source := r.Messages[1].Content
				require.LessOrEqual(t, len(source), maxSummarySource)
				require.True(t, utf8.ValidString(source))
				var part struct {
					Fragment string `json:"serialized_history_fragment"`
					Offset   int    `json:"offset_bytes"`
					Previous string `json:"previous_summary_not_authorization"`
				}
				require.NoError(t, json.Unmarshal([]byte(source), &part))
				require.Equal(t, fragments.Len(), part.Offset)
				if calls > 1 {
					require.Equal(t, "Keep constraints; work incomplete.", part.Previous)
				}
				fragments.WriteString(part.Fragment)
				st := &scriptedStream{chunks: []proto.Chunk{{Content: "Keep constraints; work incomplete."}}, usage: proto.TokenUsage{TotalTokens: 3}}
				if calls == 2 {
					switch outcome {
					case "failure":
						st.err = errors.New("summary failed")
					case "cancel":
						cancel()
					case "tool":
						st.msgs = []proto.Message{{Role: proto.RoleAssistant, ToolCalls: []proto.ToolCall{{ID: "forbidden"}}}}
					}
				}
				return st
			}}
			err := m.compactContext(client, Model{})
			require.GreaterOrEqual(t, calls, 2)
			require.Equal(t, int64(calls*3), m.TokenUsage().TotalTokens)
			require.Equal(t, original, m.Messages())
			if outcome == "success" {
				require.NoError(t, err)
				var records []map[string]any
				require.NoError(t, json.Unmarshal([]byte(fragments.String()), &records))
				require.Equal(t, original[0].Content, records[0]["content"])
				require.Equal(t, original[1].Content, records[1]["content"])
				require.Len(t, m.compactedPrefix, 2)
			} else {
				require.Error(t, err)
				require.Empty(t, m.compactedPrefix)
			}
		})
	}
}

func TestToolLoopCompactionProviderContinuation(t *testing.T) {
	for _, route := range []struct{ api, endpoint, wire string }{
		{"openai", "chat-completions", "chat"}, {"openai", "responses", "responses"},
		{"anthropic", "", "anthropic"}, {"google", "", "google"}, {"ollama", "", "ollama"},
	} {
		t.Run(route.wire, func(t *testing.T) {
			requests := make(chan map[string]any, 16)
			taskRounds := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var body map[string]any
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
					t.Error(err)
					w.WriteHeader(400)
					return
				}
				requests <- body
				encoded, _ := json.Marshal(body)
				summary := strings.Contains(string(encoded), "Summarize the supplied conversation")
				if !summary {
					taskRounds++
				}
				writeContractResponse(w, route.wire, !summary && taskRounds <= 3, "counter", `{}`)
			}))
			defer server.Close()
			cfg := defaultConfig()
			cfg.WorkingDir = t.TempDir()
			cfg.SkillsDirs = []string{filepath.Join(cfg.WorkingDir, "skills")}
			cfg.NoInstructions = true
			cfg.ContextMaxBytes = 1024
			cfg.ReviewMode = ReviewNever
			cfg.API, cfg.Model = route.api, "fixture"
			cfg.APIs = []API{{Name: route.api, APIKey: "test", BaseURL: server.URL, Models: map[string]Model{"fixture": {Endpoint: route.endpoint}}}}
			db, err := session.Open(filepath.Join(t.TempDir(), "mods.db"))
			require.NoError(t, err)
			defer db.Close()
			m, err := New(context.Background(), &cfg, db)
			require.NoError(t, err)
			defer m.quit()
			req, err := m.buildRequestSession("Keep original.txt unchanged. Complete all steps.")
			require.NoError(t, err)
			defer req.runner.close()
			calls := 0
			require.NoError(t, req.cleanup.Register(toolregistry.Tool{
				Spec: proto.ToolSpec{Name: "counter"}, Capabilities: toolregistry.ToolCapabilities{Mutable: true},
				Call: func(context.Context, json.RawMessage) (string, error) {
					calls++
					return fmt.Sprintf("completed-%d ", calls) + strings.Repeat("evidence ", 500), nil
				},
			}))
			for batch := 1; batch <= 3; batch++ {
				drainContractStream(t, req.stream)
				statuses := req.stream.CallTools()
				require.Len(t, statuses, 1)
				require.NoError(t, statuses[0].Err)
				m.handleToolCallsDone(streamEventMsg{runner: req.runner, results: statuses})
				original := m.Messages()
				cmd := m.compactToolLoopCmd(req.runner)
				if batch == 1 {
					require.Nil(t, cmd)
					continue
				}
				require.NotNil(t, cmd)
				result := cmd().(streamEventMsg)
				require.NoError(t, result.err)
				require.Equal(t, original, m.Messages(), "worker must not mutate committed state")
				require.NotNil(t, m.handleContextCompacted(result))
				require.Equal(t, original, m.Messages())
				require.Equal(t, 1, m.compactedRetained)
				require.Contains(t, m.messages[len(m.messages)-3].Content, "Keep original.txt unchanged.")
				text, err := req.cleanup.Call(context.Background(), "history_read", []byte(`{"message":3,"limit":8192}`))
				require.NoError(t, err)
				require.Contains(t, text, "completed-1")
				// Retry assembly must keep the compact view and original archive distinct.
				m.retries = 1
				require.NoError(t, m.setupCompletionContext("ignored retry prompt"))
				require.Equal(t, original, m.Messages())
				m.retries = 0
			}
			require.Equal(t, "contract-done", drainContractStream(t, req.stream))
			require.Empty(t, req.stream.CallTools())
			full := m.Messages()
			require.Equal(t, 3, calls)
			require.NoError(t, db.SaveSession("loop", "fixture", cfg.API, cfg.Model, full, nil, m.ContextSummary()))
			var saved []proto.Message
			require.NoError(t, db.ReadMessages("loop", &saved))
			require.Equal(t, conversationalMessages(full), conversationalMessages(saved))
			cached, err := db.ReadContextSummary("loop", full)
			require.NoError(t, err)
			require.NotNil(t, cached)
			// Four task requests and two summaries; the final request retains only the latest batch.
			var bodies []map[string]any
			for i := 0; i < 6; i++ {
				bodies = append(bodies, <-requests)
			}
			final, _ := json.Marshal(bodies[5])
			require.Contains(t, string(final), "completed-3")
			require.NotContains(t, string(final), "completed-1")
			require.NotContains(t, string(final), "completed-2")
			require.Contains(t, string(final), "Keep original.txt unchanged.")
			for _, i := range []int{2, 4} {
				require.Empty(t, bodies[i]["tools"])
			}
		})
	}
}

func TestToolLoopCompactionFailureCancellationAndMedia(t *testing.T) {
	for _, outcome := range []string{"success", "failure", "cancel"} {
		t.Run(outcome, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			cfg := defaultConfig()
			cfg.ContextMaxBytes = 1
			input := proto.Message{Role: proto.RoleUser, Content: "Do not change original.txt.", Media: []proto.MediaAttachment{{Kind: "image", Source: "reference.png", Data: []byte("original-image-bytes")}}}
			call := func(id string) proto.Message {
				return proto.Message{Role: proto.RoleAssistant, ToolCalls: []proto.ToolCall{{ID: id, Function: proto.Function{Name: "write", Arguments: []byte(`{}`)}}}, ProviderData: map[string]json.RawMessage{"opaque": json.RawMessage(`"private-state"`)}}
			}
			result := func(id string) proto.Message {
				return proto.Message{Role: proto.RoleTool, Content: strings.Repeat("completed write evidence ", 500), ToolCalls: []proto.ToolCall{{ID: id}}}
			}
			original := []proto.Message{input, call("a"), result("a"), call("b"), result("b")}
			m := &Mods{ctx: ctx, Config: &cfg, messages: proto.CloneMessages(original), transcript: &stream.Transcript{}, historyArchive: &stream.Transcript{}}
			m.transcript.Replace(original)
			started := make(chan struct{})
			release := make(chan struct{})
			client := summaryFixtureClient{request: func(ctx context.Context, r proto.Request) stream.Stream {
				source := r.Messages[1].Content
				require.NotContains(t, source, "original-image-bytes")
				require.NotContains(t, source, "private-state")
				require.Contains(t, source, "reference.png")
				close(started)
				if outcome == "cancel" {
					<-ctx.Done()
				} else {
					<-release
				}
				st := &scriptedStream{chunks: []proto.Chunk{{Content: "Writes a and b completed; preserve original.txt."}}}
				if outcome == "failure" {
					st.err = errors.New("summary failed")
				}
				return st
			}}
			runner := &streamRunner{ctx: ctx, client: client, cancel: cancel, stream: &scriptedStream{msgs: original}}
			cmd := m.compactToolLoopCmd(runner)
			require.NotNil(t, cmd)
			done := make(chan streamEventMsg, 1)
			go func() { done <- cmd().(streamEventMsg) }()
			<-started
			require.Equal(t, original, m.Messages(), "saving while summarizing must preserve all results")
			if outcome == "cancel" {
				runner.close()
			} else {
				close(release)
			}
			event := <-done
			next := m.handleContextCompacted(event)
			require.Equal(t, original, m.Messages())
			if outcome == "success" {
				require.NoError(t, event.err)
				require.NotNil(t, next)
				require.Equal(t, input, m.messages[1])
				require.Equal(t, original[3:], m.messages[2:])
				// The archived original and retained media must own separate byte slices.
				m.messages[1].Media[0].Data[0] = 'x'
				require.Equal(t, byte('o'), m.compactedPrefix[0].Media[0].Data[0])
			} else {
				require.Error(t, event.err)
				require.Empty(t, m.compactedPrefix)
				if outcome == "failure" {
					require.IsType(t, modsError{}, next())
				} else {
					require.Nil(t, next)
				}
			}
		})
	}
}
