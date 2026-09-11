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
	"runtime"
	"sync/atomic"
	"testing"
	"time"

	"github.com/panjie/mods/internal/approval"
	"github.com/panjie/mods/internal/proto"
	"github.com/panjie/mods/internal/session"
	"github.com/panjie/mods/internal/stream"
	toolregistry "github.com/panjie/mods/internal/tools"
	"github.com/stretchr/testify/require"
)

type writeTargetClient struct {
	requests []proto.Request
	respond  func(context.Context, proto.Request, int) stream.Stream
}

func (c *writeTargetClient) Capabilities() stream.Capabilities {
	return stream.Capabilities{Tools: true, FunctionTools: true}
}

func (c *writeTargetClient) Request(ctx context.Context, request proto.Request) stream.Stream {
	c.requests = append(c.requests, request)
	return c.respond(ctx, request, len(c.requests))
}

type writeTargetStream struct {
	scriptedStream
	caller proto.ToolCaller
}

func (s *writeTargetStream) CallTools() []proto.ToolCallStatus {
	s.toolRuns++
	var results []proto.ToolCallStatus
	calls := s.msgs[len(s.msgs)-1].ToolCalls
	for i, call := range calls {
		message, result := stream.CallTool(proto.ToolCallRequest{
			ID: call.ID, Name: call.Function.Name, Arguments: call.Function.Arguments, Index: i + 1, Total: len(calls),
		}, s.caller)
		s.msgs = append(s.msgs, message)
		results = append(results, result)
	}
	return results
}

func writeTargetResponse(request proto.Request, content string, calls ...proto.ToolCall) *writeTargetStream {
	return &writeTargetStream{scriptedStream: scriptedStream{
		chunks: []proto.Chunk{{Content: content}},
		msgs: append(append([]proto.Message(nil), request.Messages...), proto.Message{
			Role: proto.RoleAssistant, Content: content, ToolCalls: calls,
		}),
		usage: proto.TokenUsage{InputTokens: 10, OutputTokens: 5, TotalTokens: 15},
	}, caller: request.ToolCaller}
}

func TestWriteTargetsOneReadBatch(t *testing.T) {
	for _, repeat := range []bool{false, true} {
		t.Run(map[bool]string{false: "final decision", true: "second batch refused"}[repeat], func(t *testing.T) {
			var reads int
			var streams []*writeTargetStream
			client := &writeTargetClient{respond: func(_ context.Context, request proto.Request, round int) stream.Stream {
				var result *writeTargetStream
				if round == 1 {
					require.Len(t, request.Tools, 1)
					result = writeTargetResponse(request, "", proto.ToolCall{ID: "one", Function: proto.Function{Name: "read"}}, proto.ToolCall{ID: "two", Function: proto.Function{Name: "read"}})
				} else {
					require.Equal(t, 2, round)
					require.Empty(t, request.Tools)
					require.Nil(t, request.ToolCaller)
					require.Equal(t, "git@github.com:example/repo.git", request.Messages[len(request.Messages)-1].Content)
					result = writeTargetResponse(request, `{"write_dirs":["."],"write_urls":["git@github.com:example/repo.git"]}`)
					if repeat {
						result.msgs[len(result.msgs)-1].ToolCalls = []proto.ToolCall{{ID: "three", Function: proto.Function{Name: "read"}}}
					}
				}
				streams = append(streams, result)
				return result
			}}
			request := proto.Request{Tools: []proto.ToolSpec{{Name: "read"}}, ToolCaller: func(proto.ToolCallRequest) (string, error) {
				reads++
				return "git@github.com:example/repo.git", nil
			}}
			rules, usage, err := inferWriteTargets(context.Background(), client, request, testApprovalScope)
			if repeat {
				require.Error(t, err)
				require.Empty(t, rules)
			} else {
				require.NoError(t, err)
				require.True(t, RulesAllowDirs(rules, []string{"/cwd/subdir"}, testApprovalScope, AccessWrite))
				require.True(t, RulesAllowRemoteOrigins(rules, []string{"ssh://github.com"}))
			}
			require.Equal(t, 2, reads)
			require.Equal(t, int64(30), usage.TotalTokens)
			require.Equal(t, 1, streams[0].toolRuns)
			require.Zero(t, streams[1].toolRuns)
			for _, st := range streams {
				require.True(t, st.closed)
			}
		})
	}
}

func TestWriteTargetsFallbackAndPartialResult(t *testing.T) {
	for _, tc := range []struct {
		name, output string
		streamErr    error
		wantError    bool
		wantRules    int
	}{
		{name: "no writes", output: `{"write_dirs":[],"write_urls":[]}`},
		{name: "bad JSON", output: "I cannot tell", wantError: true},
		{name: "bad field type", output: `{"write_dirs":"/cwd"}`, wantError: true},
		{name: "stream error", streamErr: errors.New("offline"), wantError: true},
		{name: "partial targets", output: `{"write_dirs":[".","$unknown/out"],"write_urls":["not a URL"]}`, wantRules: 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			client := &writeTargetClient{respond: func(_ context.Context, request proto.Request, _ int) stream.Stream {
				st := writeTargetResponse(request, tc.output)
				st.err = tc.streamErr
				return st
			}}
			rules, _, err := inferWriteTargets(context.Background(), client, proto.Request{}, testApprovalScope)
			require.Equal(t, tc.wantError, err != nil)
			require.Len(t, rules, tc.wantRules)
			require.Len(t, client.requests, 1)
		})
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	client := &writeTargetClient{}
	_, _, err := inferWriteTargets(ctx, client, proto.Request{}, testApprovalScope)
	require.ErrorIs(t, err, context.Canceled)
	require.Empty(t, client.requests)
}

func TestWriteTargetsReviewModesAndRetry(t *testing.T) {
	for _, mode := range []ReviewMode{ReviewAuto, ReviewAlways, ReviewNever} {
		t.Run(string(mode), func(t *testing.T) {
			cfg := defaultConfig()
			cfg.ReviewMode = mode
			cfg.Minimal = true
			cfg.NoSave = true
			m := &Mods{ctx: context.Background(), Config: &cfg, reviewer: newToolReviewer(&cfg)}
			registry := toolregistry.NewRegistry()
			require.NoError(t, registry.Register(toolregistry.Tool{Spec: proto.ToolSpec{Name: "read"}, Capabilities: toolregistry.ToolCapabilities{ReadOnly: true}, Call: func(context.Context, json.RawMessage) (string, error) { return "", nil }}))
			client := &writeTargetClient{respond: func(_ context.Context, request proto.Request, _ int) stream.Stream {
				return writeTargetResponse(request, `{"write_dirs":["."],"write_urls":["ssh://github.com"]}`)
			}}
			require.NoError(t, m.prepareWriteTargets(client, registry, proto.Request{}))
			require.NoError(t, m.prepareWriteTargets(client, registry, proto.Request{}))
			if mode == ReviewAuto {
				require.Len(t, client.requests, 1, "minimal and no-save still infer, retries do not")
				require.Len(t, m.ApprovalRules(), 2)
			} else {
				require.Empty(t, client.requests)
				require.Empty(t, m.ApprovalRules())
			}
		})
	}
}

func TestWriteTargetsMessagesKeepUserIntentSeparate(t *testing.T) {
	messages := []proto.Message{
		{Role: proto.RoleSystem, Content: "unrelated output format"},
		{Role: proto.RoleUser, Content: "update /work/repo"},
		{Role: proto.RoleAssistant, Content: "Changes are ready"},
		{Role: proto.RoleUser, Content: "请提交并推送它"},
	}
	result := writeTargetMessages(messages, "/work/repo")
	var envelope map[string]any
	require.NoError(t, json.Unmarshal([]byte(result[1].Content), &envelope))
	require.Equal(t, "请提交并推送它", envelope["current_request"])
	require.Equal(t, "/work/repo", envelope["cwd"])
	require.Len(t, envelope["history"], 2)
	require.NotContains(t, result[1].Content, "unrelated output format")
	require.Len(t, messages, 4)
}

func TestWriteTargetsPreparationFallsBackUnlessCancelled(t *testing.T) {
	for _, tc := range []struct {
		name      string
		err       error
		cancelled bool
	}{
		{name: "invalid response"},
		{name: "provider failure", err: errors.New("provider unavailable")},
		{name: "inference timeout", err: context.DeadlineExceeded},
		{name: "user cancellation", cancelled: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			cfg := defaultConfig()
			cfg.ReviewMode = ReviewAuto
			m := &Mods{ctx: ctx, Config: &cfg, reviewer: newToolReviewer(&cfg)}
			existing := RulesForRemoteOrigins([]string{"https://existing.example.com"})
			m.reviewer.rules.Add(existing...)
			registry := toolregistry.NewRegistry()
			require.NoError(t, registry.Register(toolregistry.Tool{Spec: proto.ToolSpec{Name: "read"}, Capabilities: toolregistry.ToolCapabilities{ReadOnly: true}, Call: func(context.Context, json.RawMessage) (string, error) { return "", nil }}))
			client := &writeTargetClient{respond: func(_ context.Context, request proto.Request, _ int) stream.Stream {
				if tc.cancelled {
					cancel()
				}
				st := writeTargetResponse(request, "not JSON")
				st.err = tc.err
				return st
			}}
			err := m.prepareWriteTargets(client, registry, proto.Request{})
			if tc.cancelled {
				require.ErrorIs(t, err, context.Canceled)
			} else {
				require.NoError(t, err, "preflight failure must not fail the task")
				require.NoError(t, m.prepareWriteTargets(client, registry, proto.Request{}))
			}
			require.Equal(t, existing, m.ApprovalRules())
			require.Len(t, client.requests, 1)
		})
	}
}

func TestWriteTargetsDiscoveryOnlyReads(t *testing.T) {
	cfg := defaultConfig()
	cfg.WorkingDir = t.TempDir()
	cfg.MCPTimeout = time.Second
	m := &Mods{ctx: context.Background(), Config: &cfg, reviewer: newToolReviewer(&cfg)}
	registry := toolregistry.NewRegistry()
	var ran []string
	for _, tc := range []struct {
		name string
		caps toolregistry.ToolCapabilities
	}{
		{"read", toolregistry.ToolCapabilities{ReadOnly: true}},
		{"write", toolregistry.ToolCapabilities{Mutable: true}},
		{"ask", toolregistry.ToolCapabilities{ReadOnly: true, Interactive: true}},
		{"process_run", toolregistry.ToolCapabilities{ShellExecution: true}},
	} {
		name := tc.name
		require.NoError(t, registry.Register(toolregistry.Tool{Spec: proto.ToolSpec{Name: name}, Capabilities: tc.caps, Call: func(context.Context, json.RawMessage) (string, error) {
			ran = append(ran, name)
			return "ok", nil
		}}))
	}
	caller := m.writeTargetToolCaller(m.ctx, registry)
	_, err := caller(proto.ToolCallRequest{Name: "read", Arguments: []byte(`{}`)})
	require.NoError(t, err)
	for _, name := range []string{"write", "ask", "unknown"} {
		_, err = caller(proto.ToolCallRequest{Name: name, Arguments: []byte(`{}`)})
		require.Error(t, err)
	}
	for _, args := range []string{
		`{"program":"git","args":["commit","-m","oops"]}`,
		`{"program":"git","args":["push","https://example.com/repo"]}`,
		`{"program":"git","args":["unknown-subcommand"]}`,
	} {
		_, err = caller(proto.ToolCallRequest{Name: "process_run", Arguments: []byte(args)})
		require.Error(t, err)
	}
	_, err = caller(proto.ToolCallRequest{Name: "process_run", Arguments: []byte(`{"program":"git","args":["remote","get-url","--push","origin"]}`)})
	require.NoError(t, err)
	require.Equal(t, []string{"read", "process_run"}, ran)
	require.Empty(t, m.ApprovalRules())
}

func TestWriteTargetsExternalRead(t *testing.T) {
	cfg := defaultConfig()
	cfg.WorkingDir = t.TempDir()
	cfg.MCPTimeout = time.Second
	m := &Mods{ctx: context.Background(), Config: &cfg, reviewer: newToolReviewer(&cfg)}
	registry := toolregistry.NewRegistry()
	home, err := os.UserHomeDir()
	require.NoError(t, err)
	externalDir := filepath.Join(home, "mods-discovery-fixture")
	require.NoError(t, registry.Register(toolregistry.Tool{
		Spec: proto.ToolSpec{Name: "read"}, Capabilities: toolregistry.ToolCapabilities{ReadOnly: true},
		IntentExtractor: func(json.RawMessage) approval.AccessIntent {
			return AccessIntent{Class: AccessRead, Dirs: []string{externalDir}}
		},
		Call: func(ctx context.Context, _ json.RawMessage) (string, error) {
			require.Contains(t, slashSet(toolregistry.AuthorizedDirs(ctx)), filepath.ToSlash(externalDir))
			return "remote facts", nil
		},
	}))
	result, err := m.writeTargetToolCaller(m.ctx, registry)(proto.ToolCallRequest{Name: "read", Arguments: []byte(`{}`)})
	require.NoError(t, err)
	require.Equal(t, "remote facts", result)
	require.Empty(t, m.ApprovalRules())
}

// Exercise the actual request entry point and OpenAI adapter, including the
// first discovery batch, tool-free final decision, and normal task request.
func TestWriteTargetsRequestSession(t *testing.T) {
	var count atomic.Int32
	requests := make(chan map[string]any, 4)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request map[string]any
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		requests <- request
		round := count.Add(1)
		delta := map[string]any{"role": "assistant", "content": "done"}
		finish := "stop"
		switch round {
		case 1:
			delta = map[string]any{"role": "assistant", "tool_calls": []any{map[string]any{
				"index": 0, "id": "discovery-read", "type": "function",
				"function": map[string]any{"name": "fs_read_file", "arguments": `{"path":"facts.txt"}`},
			}}}
			finish = "tool_calls"
		case 2:
			delta["content"] = `{"write_dirs":["."],"write_urls":["ssh://github.com"]}`
		}
		chunk, _ := json.Marshal(map[string]any{
			"id": "test", "object": "chat.completion.chunk", "model": "test-model", "created": 0,
			"choices": []any{map[string]any{"index": 0, "delta": delta, "finish_reason": finish}},
		})
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = fmt.Fprintf(w, "data: %s\n\ndata: {\"id\":\"test\",\"object\":\"chat.completion.chunk\",\"model\":\"test-model\",\"created\":0,\"choices\":[],\"usage\":{\"prompt_tokens\":10,\"completion_tokens\":2,\"total_tokens\":12}}\n\ndata: [DONE]\n\n", chunk)
	}))
	defer server.Close()
	cfg := defaultConfig()
	cfg.NoSave = true
	cfg.ReviewMode = ReviewAuto
	cfg.ShowTokenUsage = true
	cfg.WorkingDir = t.TempDir()
	cfg.BuiltinTools.Filesystem = FilesystemAlways
	cfg.API, cfg.Model = "openai", "test-model"
	cfg.APIs = []API{{Name: "openai", BaseURL: server.URL, APIKey: "test-key", Models: map[string]Model{
		"test-model": {Name: "test-model", API: "openai", Endpoint: "chat-completions"},
	}}}
	require.NoError(t, os.WriteFile(filepath.Join(cfg.WorkingDir, "facts.txt"), []byte("discovery fact"), 0o600))
	m := &Mods{ctx: context.Background(), Config: &cfg, reviewer: newToolReviewer(&cfg)}
	requestSession, err := m.buildRequestSession("please edit the current repository")
	require.NoError(t, err)
	defer requestSession.runner.close()
	require.Len(t, m.ApprovalRules(), 2, "grants must exist before the normal task executes")
	for requestSession.stream.Next() {
		_, _ = requestSession.stream.Current()
	}
	require.NoError(t, requestSession.stream.Err())
	require.Equal(t, int32(3), count.Load())
	first, second, third := <-requests, <-requests, <-requests
	require.NotEmpty(t, first["tools"])
	require.Empty(t, second["tools"])
	require.NotEmpty(t, third["tools"])
	secondJSON, _ := json.Marshal(second["messages"])
	require.Contains(t, string(secondJSON), "discovery fact")
	thirdJSON, _ := json.Marshal(third["messages"])
	require.NotContains(t, string(thirdJSON), "discovery fact")
	require.NotContains(t, string(thirdJSON), "write_dirs")
	require.Contains(t, string(thirdJSON), "please edit the current repository")
	require.Equal(t, int64(24), m.writeTargetsUsage.TotalTokens)
	m.Update(streamEventMsg{kind: streamEventDone, runner: requestSession.runner})
	require.Equal(t, int64(36), m.TokenUsage().TotalTokens)
	require.Zero(t, requestSession.runner.takeUsage().TotalTokens, "usage must only be counted once")
	// Rebuild exactly as the retry path does: no additional inference request.
	requestSession.runner.close()
	retry, err := m.buildRequestSession("please edit the current repository")
	require.NoError(t, err)
	defer retry.runner.close()
	for retry.stream.Next() {
		_, _ = retry.stream.Current()
	}
	require.NoError(t, retry.stream.Err())
	require.Equal(t, int32(4), count.Load())
}

func TestWriteTargetsPersistAndUseOrdinaryReview(t *testing.T) {
	scope := testShellWorkingDirScope(t)
	data, err := json.Marshal(map[string]any{"write_dirs": []string{scope.Value}, "write_urls": []string{"git@github.com:example/repo.git"}})
	require.NoError(t, err)
	rules, err := parseWriteTargets(string(data), scope)
	require.NoError(t, err)
	db, err := session.Open(filepath.Join(t.TempDir(), "mods.db"))
	require.NoError(t, err)
	defer func() { require.NoError(t, db.Close()) }()
	id := session.NewID()
	require.NoError(t, db.SaveSession(id, "push", "test", "test", []proto.Message{{Role: proto.RoleUser, Content: "commit and push"}}, rules))
	loaded, err := db.ApprovalRules(id)
	require.NoError(t, err)
	r := &toolReviewer{reviewMode: ReviewAuto, scope: scope, reviewAvailabilityKnown: true}
	r.rules.Replace(loaded)
	intent := AccessIntent{Class: AccessWrite, Dirs: []string{scope.Value}, RemoteOrigins: []string{"ssh://github.com"}}
	deps := reviewerDeps{ctx: context.Background(), accessIntent: intent, safeDirs: []string{filepath.Join(scope.Value, "safe")}}
	require.NoError(t, r.requestApproval(deps, "process_run", nil), "mixed local and remote writes should be covered")
	deps.accessIntent.RemoteOrigins = []string{"https://github.com"}
	require.ErrorIs(t, r.requestApproval(deps, "process_run", nil), errReviewUnavailable)
	deps.accessIntent = AccessIntent{Class: AccessWrite, UncertainEffect: true}
	require.ErrorIs(t, r.requestApproval(deps, "process_run", nil), errReviewUnavailable)
	deps.accessIntent = intent
	r.reviewMode = ReviewAlways
	require.ErrorIs(t, r.requestApproval(deps, "process_run", nil), errReviewUnavailable)
	r.reviewMode = ReviewAuto
	r.rules.Replace(nil)
	require.ErrorIs(t, r.requestApproval(deps, "process_run", nil), errReviewUnavailable, "another session must not inherit grants")
	if runtime.GOOS != "windows" {
		require.Equal(t, "/cwd", rules[0].Paths[0])
	}
}
