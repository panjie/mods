package google

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/panjie/mods/internal/proto"
	"github.com/stretchr/testify/require"
)

func newCapturingServer(t *testing.T) (client *Client, captured *[]byte, closeServer func()) {
	t.Helper()
	var buf []byte
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		buf, _ = io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, "data: {\"candidates\":[]}\n\n")
	}))
	cfg := Config{
		BaseURL:    server.URL,
		HTTPClient: server.Client(),
		AuthToken:  "k",
	}
	return New(cfg), &buf, server.Close
}

// TestFromProtoMessagesIncludesAssistantWithModelRole guards against the
// regression where assistant messages were silently dropped and where, if
// re-introduced naively, they would carry the wire role "assistant" which
// Gemini rejects.
func TestFromProtoMessagesIncludesAssistantWithModelRole(t *testing.T) {
	sys, contents := fromProtoMessages([]proto.Message{
		{Role: proto.RoleUser, Content: "hi"},
		{Role: proto.RoleAssistant, Content: "hello back"},
	})

	require.Nil(t, sys)
	require.Len(t, contents, 2)

	require.Equal(t, geminiRoleUser, contents[0].Role)
	require.Equal(t, []Part{{Text: "hi"}}, contents[0].Parts)

	require.Equal(t, geminiRoleModel, contents[1].Role)
	require.NotEqual(t, proto.RoleAssistant, contents[1].Role,
		"wire role must be Gemini's \"model\", never proto.RoleAssistant=\"assistant\"")
	require.Equal(t, []Part{{Text: "hello back"}}, contents[1].Parts)
}

// TestFromProtoMessagesPromotesSystemToInstruction ensures system messages
// surface as Gemini's top-level systemInstruction rather than leaking into
// contents.
func TestFromProtoMessagesPromotesSystemToInstruction(t *testing.T) {
	t.Run("single system", func(t *testing.T) {
		sys, contents := fromProtoMessages([]proto.Message{
			{Role: proto.RoleSystem, Content: "be concise"},
			{Role: proto.RoleUser, Content: "hi"},
		})

		require.NotNil(t, sys)
		require.Empty(t, sys.Role, "systemInstruction has no role field")
		require.Equal(t, []Part{{Text: "be concise"}}, sys.Parts)

		require.Len(t, contents, 1)
		require.Equal(t, geminiRoleUser, contents[0].Role)
	})

	t.Run("multiple systems preserve order as separate parts", func(t *testing.T) {
		sys, contents := fromProtoMessages([]proto.Message{
			{Role: proto.RoleSystem, Content: "first"},
			{Role: proto.RoleSystem, Content: "second"},
			{Role: proto.RoleUser, Content: "hi"},
			{Role: proto.RoleSystem, Content: "third (later)"},
		})

		require.NotNil(t, sys)
		require.Equal(t, []Part{
			{Text: "first"},
			{Text: "second"},
			{Text: "third (later)"},
		}, sys.Parts)

		require.Len(t, contents, 1)
	})

	t.Run("no system yields nil instruction", func(t *testing.T) {
		sys, contents := fromProtoMessages([]proto.Message{
			{Role: proto.RoleUser, Content: "hi"},
		})

		require.Nil(t, sys)
		require.Len(t, contents, 1)
	})

	t.Run("empty system is skipped", func(t *testing.T) {
		sys, contents := fromProtoMessages([]proto.Message{
			{Role: proto.RoleSystem, Content: ""},
			{Role: proto.RoleUser, Content: "hi"},
		})

		require.Nil(t, sys, "empty system text must not produce an empty part")
		require.Len(t, contents, 1)
	})
}

// TestFromProtoMessagesInterleavesUserAndAssistant verifies multi-turn
// conversations preserve order and role mapping.
func TestFromProtoMessagesInterleavesUserAndAssistant(t *testing.T) {
	sys, contents := fromProtoMessages([]proto.Message{
		{Role: proto.RoleSystem, Content: "S"},
		{Role: proto.RoleUser, Content: "u1"},
		{Role: proto.RoleAssistant, Content: "a1"},
		{Role: proto.RoleUser, Content: "u2"},
		{Role: proto.RoleAssistant, Content: "a2"},
		{Role: proto.RoleUser, Content: "u3"},
	})

	require.NotNil(t, sys)
	require.Equal(t, []Part{{Text: "S"}}, sys.Parts)

	require.Len(t, contents, 5)
	require.Equal(t, geminiRoleUser, contents[0].Role)
	require.Equal(t, "u1", contents[0].Parts[0].Text)
	require.Equal(t, geminiRoleModel, contents[1].Role)
	require.Equal(t, "a1", contents[1].Parts[0].Text)
	require.Equal(t, geminiRoleUser, contents[2].Role)
	require.Equal(t, "u2", contents[2].Parts[0].Text)
	require.Equal(t, geminiRoleModel, contents[3].Role)
	require.Equal(t, "a2", contents[3].Parts[0].Text)
	require.Equal(t, geminiRoleUser, contents[4].Role)
	require.Equal(t, "u3", contents[4].Parts[0].Text)
}

// TestFromProtoMessagesDropsToolMessages ensures tool messages from a
// continued tool-capable conversation do not corrupt contents.
func TestFromProtoMessagesDropsToolMessages(t *testing.T) {
	sys, contents := fromProtoMessages([]proto.Message{
		{Role: proto.RoleUser, Content: "list files"},
		{Role: proto.RoleAssistant, Content: "calling tool"},
		{Role: proto.RoleTool, Content: "a.txt\nb.txt", ToolCalls: []proto.ToolCall{{ID: "call_1"}}},
		{Role: proto.RoleAssistant, Content: "two files: a.txt, b.txt"},
	})

	require.Nil(t, sys)
	require.Len(t, contents, 3, "tool message must not appear in contents")
	require.Equal(t, geminiRoleUser, contents[0].Role)
	require.Equal(t, geminiRoleModel, contents[1].Role)
	require.Equal(t, geminiRoleModel, contents[2].Role)
}

// TestFromProtoMessagesSkipsEmptyAssistant prevents emitting parts:[] which
// would trigger a 400 from Gemini.
func TestFromProtoMessagesSkipsEmptyAssistant(t *testing.T) {
	_, contents := fromProtoMessages([]proto.Message{
		{Role: proto.RoleUser, Content: "hi"},
		{Role: proto.RoleAssistant, Content: ""},
		{Role: proto.RoleUser, Content: "still here?"},
	})

	require.Len(t, contents, 2)
	require.Equal(t, geminiRoleUser, contents[0].Role)
	require.Equal(t, geminiRoleUser, contents[1].Role)
}

// TestFromProtoMessagesUserWithImages is a regression test ensuring image
// handling remains intact after the role-handling refactor.
func TestFromProtoMessagesUserWithImages(t *testing.T) {
	_, contents := fromProtoMessages([]proto.Message{
		{
			Role:    proto.RoleUser,
			Content: "what is this?",
			Images: []proto.Image{
				{Data: []byte{0x89, 0x50, 0x4e, 0x47}, MimeType: "image/png"},
			},
		},
	})

	require.Len(t, contents, 1)
	require.Equal(t, geminiRoleUser, contents[0].Role)
	require.Len(t, contents[0].Parts, 2, "one image part and one text part")
	require.NotNil(t, contents[0].Parts[0].InlineData)
	require.Equal(t, "image/png", contents[0].Parts[0].InlineData.MimeType)
	require.NotEmpty(t, contents[0].Parts[0].InlineData.Data, "base64-encoded data must be present")
	require.Equal(t, "what is this?", contents[0].Parts[1].Text)
}

// TestRequestBodyEmitsSystemInstruction asserts the wire JSON matches Gemini's
// schema: systemInstruction at top level, contents[*].role only "user"/"model".
func TestRequestBodyEmitsSystemInstruction(t *testing.T) {
	client, captured, closeServer := newCapturingServer(t)
	defer closeServer()

	_ = client.Request(context.Background(), proto.Request{
		Messages: []proto.Message{
			{Role: proto.RoleSystem, Content: "be concise"},
			{Role: proto.RoleUser, Content: "hi"},
			{Role: proto.RoleAssistant, Content: "hello"},
			{Role: proto.RoleUser, Content: "again"},
		},
	})

	require.NotEmpty(t, *captured, "no request body was captured")
	require.True(t, bytes.Contains(*captured, []byte(`"systemInstruction"`)),
		"wire JSON must contain systemInstruction: %s", *captured)

	var body map[string]any
	require.NoError(t, json.Unmarshal(*captured, &body))

	si, ok := body["systemInstruction"].(map[string]any)
	require.True(t, ok, "systemInstruction must be an object")
	parts, ok := si["parts"].([]any)
	require.True(t, ok)
	require.Len(t, parts, 1)
	require.Equal(t, "be concise", parts[0].(map[string]any)["text"])

	contentsAny, ok := body["contents"].([]any)
	require.True(t, ok)
	require.Len(t, contentsAny, 3)
	for _, c := range contentsAny {
		role := c.(map[string]any)["role"]
		require.Contains(t, []any{"user", "model"}, role,
			"contents[*].role must be \"user\" or \"model\", got %v", role)
	}
}

// TestRequestBodyOmitsSystemInstructionWhenAbsent confirms *Content+omitempty
// produces no top-level systemInstruction field when there are no system
// messages.
func TestRequestBodyOmitsSystemInstructionWhenAbsent(t *testing.T) {
	client, captured, closeServer := newCapturingServer(t)
	defer closeServer()

	_ = client.Request(context.Background(), proto.Request{
		Messages: []proto.Message{{Role: proto.RoleUser, Content: "hi"}},
	})

	require.NotEmpty(t, *captured)
	require.False(t, bytes.Contains(*captured, []byte(`"systemInstruction"`)),
		"systemInstruction must be omitted when no system messages exist: %s", *captured)
}
