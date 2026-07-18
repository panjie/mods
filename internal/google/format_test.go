package google

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/panjie/mods/internal/proto"
	"github.com/panjie/mods/internal/stream"
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

func newCapturingSequenceServer(t *testing.T, responses []string) (client *Client, captured *[][]byte, closeServer func()) {
	t.Helper()
	var bodies [][]byte
	var idx int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		bodies = append(bodies, body)
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		if idx < len(responses) {
			_, _ = io.WriteString(w, responses[idx])
			idx++
			return
		}
		_, _ = io.WriteString(w, "data: {\"candidates\":[]}\n\n")
	}))
	cfg := Config{
		BaseURL:    server.URL,
		HTTPClient: server.Client(),
		AuthToken:  "k",
	}
	return New(cfg), &bodies, server.Close
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

// TestFromProtoMessagesIncludesToolMessages ensures tool messages from a
// continued tool-capable conversation are replayed as function responses.
func TestFromProtoMessagesIncludesToolMessages(t *testing.T) {
	sys, contents := fromProtoMessages([]proto.Message{
		{Role: proto.RoleUser, Content: "list files"},
		{
			Role:    proto.RoleAssistant,
			Content: "calling tool",
			ToolCalls: []proto.ToolCall{{
				ID: "call_1",
				Function: proto.Function{
					Name:      "list_files",
					Arguments: []byte(`{"path":"."}`),
				},
			}},
		},
		{
			Role:    proto.RoleTool,
			Content: "a.txt\nb.txt",
			ToolCalls: []proto.ToolCall{{
				ID: "call_1",
				Function: proto.Function{
					Name: "list_files",
				},
			}},
		},
		{Role: proto.RoleAssistant, Content: "two files: a.txt, b.txt"},
	})

	require.Nil(t, sys)
	require.Len(t, contents, 4, "tool message must appear as functionResponse content")
	require.Equal(t, geminiRoleUser, contents[0].Role)
	require.Equal(t, geminiRoleModel, contents[1].Role)
	require.Equal(t, "calling tool", contents[1].Parts[0].Text)
	require.Equal(t, "list_files", contents[1].Parts[1].FunctionCall.Name)
	require.Equal(t, map[string]any{"path": "."}, contents[1].Parts[1].FunctionCall.Args)
	require.Equal(t, geminiRoleUser, contents[2].Role)
	require.Equal(t, "list_files", contents[2].Parts[0].FunctionResponse.Name)
	require.Equal(t, map[string]any{"result": "a.txt\nb.txt"}, contents[2].Parts[0].FunctionResponse.Response)
	require.Equal(t, geminiRoleModel, contents[3].Role)
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

func TestRequestBodyIncludesFunctionDeclarations(t *testing.T) {
	client, captured, closeServer := newCapturingServer(t)
	defer closeServer()

	_ = client.Request(context.Background(), proto.Request{
		Messages: []proto.Message{{Role: proto.RoleUser, Content: "read"}},
		Tools: []proto.ToolSpec{{
			Name:        "read_file",
			Description: "Read a file",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"path": map[string]any{
						"type":        "string",
						"description": "Path to read",
					},
				},
				"required": []any{"path"},
			},
		}},
	})

	var body map[string]any
	require.NoError(t, json.Unmarshal(*captured, &body))
	tools := body["tools"].([]any)
	require.Len(t, tools, 1)
	decls := tools[0].(map[string]any)["functionDeclarations"].([]any)
	require.Len(t, decls, 1)
	decl := decls[0].(map[string]any)
	require.Equal(t, "read_file", decl["name"])
	require.Equal(t, "Read a file", decl["description"])
	params := decl["parameters"].(map[string]any)
	require.Equal(t, "object", params["type"])
	require.Contains(t, params["properties"].(map[string]any), "path")
}

func TestStreamParsesFunctionCallsAndPreservesText(t *testing.T) {
	st := &Stream{
		reader: bufio.NewReader(bytes.NewBufferString(
			"data: {\"candidates\":[{\"content\":{\"parts\":[" +
				"{\"text\":\"checking\"}," +
				"{\"functionCall\":{\"name\":\"read_file\",\"args\":{\"path\":\"README.md\"}}}," +
				"{\"text\":\"thinking\",\"thought\":true}" +
				"]}}]}\n\n",
		)),
		unmarshaler: &JSONUnmarshaler{},
	}

	chunk, err := st.Current()
	require.NoError(t, err)
	require.Equal(t, "checking", chunk.Content)
	require.Equal(t, "thinking", chunk.Thought)

	messages := st.Messages()
	require.Len(t, messages, 1)
	require.Equal(t, proto.RoleAssistant, messages[0].Role)
	require.Equal(t, "checking", messages[0].Content)
	require.Len(t, messages[0].ToolCalls, 1)
	require.Equal(t, "google_call_0", messages[0].ToolCalls[0].ID)
	require.Equal(t, "read_file", messages[0].ToolCalls[0].Function.Name)
	require.JSONEq(t, `{"path":"README.md"}`, string(messages[0].ToolCalls[0].Function.Arguments))
}

func TestStreamParsesThoughtSignatureFromFunctionCall(t *testing.T) {
	st := &Stream{
		reader: bufio.NewReader(bytes.NewBufferString(
			"data: {\"candidates\":[{\"content\":{\"parts\":[" +
				"{\"text\":\"thinking\",\"thought\":true,\"thoughtSignature\":\"sig_abc\"}," +
				"{\"functionCall\":{\"name\":\"example_tool\",\"args\":{\"input\":\"step\"},\"thoughtSignature\":\"sig_def\"}}" +
				"]}}]}\n\n",
		)),
		unmarshaler: &JSONUnmarshaler{},
	}

	_, err := st.Current()
	require.NoError(t, err)

	messages := st.Messages()
	require.Len(t, messages, 1)
	require.Len(t, messages[0].ToolCalls, 1)
	require.Equal(t, "example_tool", messages[0].ToolCalls[0].Function.Name)
	require.Equal(t, "sig_def", messages[0].ToolCalls[0].Function.ThoughtSignature,
		"thoughtSignature from functionCall must be preserved in proto.ToolCall")
}

func TestFromProtoMessagePreservesThoughtSignature(t *testing.T) {
	content, ok := fromProtoMessageOK(proto.Message{
		Role: proto.RoleAssistant,
		ToolCalls: []proto.ToolCall{{
			ID: "call_1",
			Function: proto.Function{
				Name:             "example_tool",
				Arguments:        []byte(`{"input":"step"}`),
				ThoughtSignature: "sig_xyz",
			},
		}},
	})
	require.True(t, ok)

	// The thought part must carry the signature back as an empty thought.
	require.Len(t, content.Parts, 2, "expected thought part + functionCall part")
	require.NotNil(t, content.Parts[0].Thought)
	require.True(t, *content.Parts[0].Thought)
	require.Empty(t, content.Parts[0].Text, "thought text is discarded; only signature preserved")
	require.Equal(t, "sig_xyz", content.Parts[0].ThoughtSignature)

	// The functionCall part must also carry the signature.
	require.NotNil(t, content.Parts[1].FunctionCall)
	require.Equal(t, "sig_xyz", content.Parts[1].FunctionCall.ThoughtSignature)
}

func TestFromProtoMessageOmitsThoughtPartWhenNoSignature(t *testing.T) {
	content, ok := fromProtoMessageOK(proto.Message{
		Role: proto.RoleAssistant,
		ToolCalls: []proto.ToolCall{{
			ID: "call_1",
			Function: proto.Function{
				Name:      "read_file",
				Arguments: []byte(`{"path":"."}`),
			},
		}},
	})
	require.True(t, ok)
	require.Len(t, content.Parts, 1, "no thought part when ThoughtSignature is empty")
	require.NotNil(t, content.Parts[0].FunctionCall)
	require.Empty(t, content.Parts[0].FunctionCall.ThoughtSignature)
}

func TestCallToolsSendsFunctionResponseAndContinues(t *testing.T) {
	client, captured, closeServer := newCapturingSequenceServer(t, []string{
		"data: {\"candidates\":[{\"content\":{\"parts\":[{\"functionCall\":{\"id\":\"call_1\",\"name\":\"read_file\",\"args\":{\"path\":\"README.md\"}}}]}}]}\n\n",
		"data: {\"candidates\":[{\"content\":{\"parts\":[{\"text\":\"done\"}]}}]}\n\n",
	})
	defer closeServer()

	st := client.Request(context.Background(), proto.Request{
		Messages: []proto.Message{{Role: proto.RoleUser, Content: "read README"}},
		ToolCaller: func(name string, data []byte) (string, error) {
			require.Equal(t, "read_file", name)
			require.JSONEq(t, `{"path":"README.md"}`, string(data))
			return "README contents", nil
		},
	})
	require.True(t, st.Next())
	_, err := st.Current()
	require.NoError(t, err)
	require.True(t, st.Next())
	_, err = st.Current()
	require.ErrorIs(t, err, stream.ErrNoContent)
	require.False(t, st.Next())

	statuses := st.CallTools()
	require.Len(t, statuses, 1)
	require.NoError(t, statuses[0].Err)
	require.Len(t, *captured, 2)

	var followup map[string]any
	require.NoError(t, json.Unmarshal((*captured)[1], &followup))
	contents := followup["contents"].([]any)
	require.Len(t, contents, 3)
	assistant := contents[1].(map[string]any)
	require.Equal(t, geminiRoleModel, assistant["role"])
	require.Contains(t, assistant["parts"].([]any)[0].(map[string]any), "functionCall")
	toolResult := contents[2].(map[string]any)
	require.Equal(t, geminiRoleUser, toolResult["role"])
	fnResp := toolResult["parts"].([]any)[0].(map[string]any)["functionResponse"].(map[string]any)
	require.Equal(t, "call_1", fnResp["id"])
	require.Equal(t, "read_file", fnResp["name"])
	require.Equal(t, map[string]any{"result": "README contents"}, fnResp["response"])

	require.True(t, st.Next())
	chunk, err := st.Current()
	require.NoError(t, err)
	require.Equal(t, "done", chunk.Content)
}

func TestCallToolsSerializesFailureAsError(t *testing.T) {
	client, captured, closeServer := newCapturingSequenceServer(t, []string{
		"data: {\"candidates\":[{\"content\":{\"parts\":[{\"functionCall\":{\"id\":\"call_1\",\"name\":\"read_file\",\"args\":{\"path\":\"missing\"}}}]}}]}\n\n",
		"data: {\"candidates\":[{\"content\":{\"parts\":[{\"text\":\"handled\"}]}}]}\n\n",
	})
	defer closeServer()

	st := client.Request(context.Background(), proto.Request{
		Messages: []proto.Message{{Role: proto.RoleUser, Content: "read missing"}},
		ToolCaller: func(name string, data []byte) (string, error) {
			return "", errors.New("not found")
		},
	})
	require.True(t, st.Next())
	_, err := st.Current()
	require.NoError(t, err)
	require.True(t, st.Next())
	_, err = st.Current()
	require.ErrorIs(t, err, stream.ErrNoContent)
	require.False(t, st.Next())

	statuses := st.CallTools()
	require.Len(t, statuses, 1)
	require.Error(t, statuses[0].Err)

	var followup map[string]any
	require.NoError(t, json.Unmarshal((*captured)[1], &followup))
	contents := followup["contents"].([]any)
	fnResp := contents[2].(map[string]any)["parts"].([]any)[0].(map[string]any)["functionResponse"].(map[string]any)
	require.Equal(t, map[string]any{"error": "not found"}, fnResp["response"])

	messages := st.Messages()
	require.Len(t, messages, 3)
	require.Len(t, messages[1].ToolCalls, 1)
	require.Equal(t, proto.RoleTool, messages[2].Role)
	require.True(t, messages[2].ToolCalls[0].IsError)
}
