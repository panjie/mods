package google

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/panjie/mods/internal/proto"
	"github.com/panjie/mods/internal/stream"
	"github.com/stretchr/testify/require"
)

func TestStreamMessagesIncludesAssistantResponse(t *testing.T) {
	stream := &Stream{
		messages: []proto.Message{
			{Role: proto.RoleUser, Content: "hello"},
		},
		message: proto.Message{Role: proto.RoleAssistant, Content: "hi there"},
	}

	require.Equal(t, []proto.Message{
		{Role: proto.RoleUser, Content: "hello"},
		{Role: proto.RoleAssistant, Content: "hi there"},
	}, stream.Messages())
}

// TestDefaultConfigDoesNotLeakKeyInURL ensures the API key is carried via the
// x-goog-api-key header rather than the request URL, where it would leak via
// proxy logs, error messages, and Referer headers.
func TestDefaultConfigDoesNotLeakKeyInURL(t *testing.T) {
	cfg := DefaultConfig("gemini-2.5-pro", "secret-key-with-&-and-#")

	require.NotContains(t, cfg.BaseURL, "key=")
	require.NotContains(t, cfg.BaseURL, "secret")
	require.Equal(t, "secret-key-with-&-and-#", cfg.AuthToken)
	require.Contains(t, cfg.BaseURL, "streamGenerateContent")
}

func TestDefaultConfigEscapesModelName(t *testing.T) {
	// A model name with characters that require escaping should be encoded.
	cfg := DefaultConfig("models/gemini-2.5-pro", "k")
	require.Contains(t, cfg.BaseURL, "models%2Fgemini-2.5-pro")
}

func TestRequestSendsAPIKeyHeader(t *testing.T) {
	var gotURL string
	var gotAuthHeader string
	var gotAuthQuery string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotURL = r.URL.String()
		gotAuthHeader = r.Header.Get("x-goog-api-key")
		gotAuthQuery = r.URL.Query().Get("key")
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, "data: {\"candidates\":[]}\n\n")
	}))
	defer server.Close()

	cfg := Config{
		BaseURL:    server.URL + "/v1beta/models/gemini-2.5-pro:streamGenerateContent?alt=sse",
		HTTPClient: server.Client(),
		AuthToken:  "test-secret-key",
	}
	client := New(cfg)
	_ = client.Request(context.Background(), proto.Request{
		Messages: []proto.Message{{Role: proto.RoleUser, Content: "hi"}},
	})

	require.Equal(t, "test-secret-key", gotAuthHeader, "API key must be sent in x-goog-api-key header")
	require.Empty(t, gotAuthQuery, "API key must NOT be sent in URL query")
	require.NotContains(t, gotURL, "key=", "URL must not contain the key parameter")
}

func TestRequestOmitsAPIKeyHeaderWhenEmpty(t *testing.T) {
	var headerPresent bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		headerPresent = r.Header.Get("x-goog-api-key") != ""
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, "data: {\"candidates\":[]}\n\n")
	}))
	defer server.Close()

	cfg := Config{
		BaseURL:    server.URL,
		HTTPClient: server.Client(),
		AuthToken:  "",
	}
	client := New(cfg)
	_ = client.Request(context.Background(), proto.Request{
		Messages: []proto.Message{{Role: proto.RoleUser, Content: "hi"}},
	})

	require.False(t, headerPresent, "no api key header should be sent when AuthToken is empty")
}

// TestRequestBuildErrorFinishesStream pins the fix for the missing
// isFinished assignment on the first error path in Request(). If a
// malformed BaseURL prevents newRequest from constructing the
// *http.Request, the returned Stream used to report Next() == true
// because isFinished stayed false. The next Current() call then
// dereferenced the nil reader and panicked. After the fix the stream
// must surface the error via Err() and report Next() == false so the
// caller does not enter Current() at all.
func TestRequestBuildErrorFinishesStream(t *testing.T) {
	cfg := Config{
		// Missing scheme makes http.NewRequestWithContext fail, exercising
		// the c.newRequest error branch without spinning up a server.
		BaseURL:    "://bad-url",
		HTTPClient: &http.Client{},
		AuthToken:  "k",
	}
	client := New(cfg)
	st := client.Request(context.Background(), proto.Request{
		Messages: []proto.Message{{Role: proto.RoleUser, Content: "hi"}},
	})

	require.Error(t, st.Err(), "request build failure must surface via Err()")
	require.False(t, st.Next(),
		"stream must report finished after a build-time failure so callers skip Current()")
	require.NotPanics(t, func() {
		_ = st.Close()
	}, "Close() must be safe even when the underlying response was never opened")
}

// streamFromSSE constructs a Stream pre-loaded with the given SSE payload so
// Current() can be exercised without a live HTTP server.
func streamFromSSE(payload string) *Stream {
	return &Stream{
		reader:      bufio.NewReader(strings.NewReader(payload)),
		unmarshaler: &JSONUnmarshaler{},
	}
}

// TestPartThoughtUnmarshalsBool guards against the regression where Part.Thought
// was declared as string while Gemini's API returns a boolean. With *bool the
// presence vs. absence of the field is distinguishable from an explicit false.
func TestPartThoughtUnmarshalsBool(t *testing.T) {
	t.Run("true", func(t *testing.T) {
		var p Part
		require.NoError(t, json.Unmarshal([]byte(`{"text":"x","thought":true}`), &p))
		require.NotNil(t, p.Thought)
		require.True(t, *p.Thought)
	})
	t.Run("false", func(t *testing.T) {
		var p Part
		require.NoError(t, json.Unmarshal([]byte(`{"text":"x","thought":false}`), &p))
		require.NotNil(t, p.Thought)
		require.False(t, *p.Thought)
	})
	t.Run("absent", func(t *testing.T) {
		var p Part
		require.NoError(t, json.Unmarshal([]byte(`{"text":"x"}`), &p))
		require.Nil(t, p.Thought)
	})
	t.Run("marshal omits nil", func(t *testing.T) {
		// fromProtoMessages constructs Part values without setting Thought, so
		// marshalling must omit the field rather than emitting null/false to
		// avoid confusing the Gemini API on subsequent turns.
		b, err := json.Marshal(Part{Text: "hi"})
		require.NoError(t, err)
		require.NotContains(t, string(b), "thought")
	})
}

// TestCurrentRoutesThoughtSeparately verifies that Current() dispatches each
// part's Text to exactly one of {Chunk.Content, Chunk.Thought} based on the
// Thought flag, and never double-counts.
func TestCurrentRoutesThoughtSeparately(t *testing.T) {
	t.Run("non-thought part goes to content", func(t *testing.T) {
		s := streamFromSSE(`data: {"candidates":[{"content":{"parts":[{"text":"hello"}]}}]}` + "\n\n")
		chunk, err := s.Current()
		require.NoError(t, err)
		require.Equal(t, "hello", chunk.Content)
		require.Empty(t, chunk.Thought)
	})

	t.Run("thought part goes to thought only", func(t *testing.T) {
		s := streamFromSSE(`data: {"candidates":[{"content":{"parts":[{"text":"reasoning","thought":true}]}}]}` + "\n\n")
		chunk, err := s.Current()
		require.NoError(t, err)
		require.Empty(t, chunk.Content, "thought part must not leak into Content")
		require.Equal(t, "reasoning", chunk.Thought)
	})

	t.Run("mixed parts in single chunk", func(t *testing.T) {
		s := streamFromSSE(`data: {"candidates":[{"content":{"parts":[{"text":"思考","thought":true},{"text":"answer"}]}}]}` + "\n\n")
		chunk, err := s.Current()
		require.NoError(t, err)
		require.Equal(t, "answer", chunk.Content)
		require.Equal(t, "思考", chunk.Thought)
	})

	t.Run("explicit thought=false counts as content", func(t *testing.T) {
		s := streamFromSSE(`data: {"candidates":[{"content":{"parts":[{"text":"hi","thought":false}]}}]}` + "\n\n")
		chunk, err := s.Current()
		require.NoError(t, err)
		require.Equal(t, "hi", chunk.Content)
		require.Empty(t, chunk.Thought)
	})

	t.Run("empty text part is skipped", func(t *testing.T) {
		s := streamFromSSE(`data: {"candidates":[{"content":{"parts":[{"text":"","thought":true},{"text":"answer"}]}}]}` + "\n\n")
		chunk, err := s.Current()
		require.NoError(t, err)
		require.Equal(t, "answer", chunk.Content)
		require.Empty(t, chunk.Thought, "empty thought text must not produce a thought chunk")
	})
}

func TestCurrentCollectsUsageOnlyChunk(t *testing.T) {
	s := streamFromSSE(`data: {"candidates":[],"usageMetadata":{"promptTokenCount":10,"candidatesTokenCount":3,"thoughtsTokenCount":2,"totalTokenCount":15}}` + "\n\n")
	s.trackUsage = true

	_, err := s.Current()
	require.ErrorIs(t, err, stream.ErrNoContent)
	_, err = s.Current() // consume EOF and commit the completed round
	require.ErrorIs(t, err, stream.ErrNoContent)
	require.Equal(t, proto.TokenUsage{InputTokens: 10, OutputTokens: 5, TotalTokens: 15}, s.Usage())
}

func TestCurrentUsageFallsBackToComponentsWithoutTotal(t *testing.T) {
	s := streamFromSSE(`data: {"candidates":[],"usageMetadata":{"promptTokenCount":8,"candidatesTokenCount":3,"thoughtsTokenCount":1}}` + "\n\n")
	s.trackUsage = true
	_, _ = s.Current()
	_, _ = s.Current()
	require.Equal(t, proto.TokenUsage{InputTokens: 8, OutputTokens: 4, TotalTokens: 12}, s.Usage())
}

// TestCurrentDoesNotPolluteMessageHistory verifies that reasoning content is
// never persisted into s.message, which is what Messages() returns to upstream
// for multi-turn replay. Polluting history with reasoning would (a) waste
// tokens, (b) leak hidden chain-of-thought to subsequent prompts.
func TestCurrentDoesNotPolluteMessageHistory(t *testing.T) {
	s := streamFromSSE(
		`data: {"candidates":[{"content":{"parts":[{"text":"step 1","thought":true}]}}]}` + "\n\n" +
			`data: {"candidates":[{"content":{"parts":[{"text":"step 2","thought":true},{"text":"final"}]}}]}` + "\n\n" +
			`data: {"candidates":[{"content":{"parts":[{"text":" answer"}]}}]}` + "\n\n",
	)
	s.messages = []proto.Message{{Role: proto.RoleUser, Content: "q"}}

	// First chunk: pure reasoning.
	c1, err := s.Current()
	require.NoError(t, err)
	require.Empty(t, c1.Content)
	require.Equal(t, "step 1", c1.Thought)

	// Second chunk: mixed reasoning and answer.
	c2, err := s.Current()
	require.NoError(t, err)
	require.Equal(t, "final", c2.Content)
	require.Equal(t, "step 2", c2.Thought)

	// Third chunk: pure answer.
	c3, err := s.Current()
	require.NoError(t, err)
	require.Equal(t, " answer", c3.Content)
	require.Empty(t, c3.Thought)

	// The persisted assistant message must contain only the user-facing
	// answer, never any reasoning text.
	require.Equal(t, "final answer", s.message.Content)
	require.Equal(t, []proto.Message{
		{Role: proto.RoleUser, Content: "q"},
		{Role: proto.RoleAssistant, Content: "final answer"},
	}, s.Messages())
}
