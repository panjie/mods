package openai

import (
	"testing"

	"github.com/panjie/mods/internal/proto"
	"github.com/stretchr/testify/require"
)

func TestFromProtoMessagesMergesStructuredSystemPrompt(t *testing.T) {
	identity := proto.Message{Role: proto.RoleSystem, Content: "identity"}
	identity.SetSystemSection(proto.SystemSectionRuntimeIdentity)
	format := proto.Message{Role: proto.RoleSystem, Content: "format"}
	format.SetSystemSection(proto.SystemSectionOutputFormat)

	got := fromProtoMessages([]proto.Message{
		format,
		{Role: proto.RoleUser, Content: "hello"},
		identity,
	})
	require.Len(t, got, 2)
	require.Equal(t, proto.RoleSystem, msgRole(got[0]))
	require.Contains(t, toProtoMessage(got[0]).Content, "identity")
	require.Contains(t, toProtoMessage(got[0]).Content, "format")
	require.Equal(t, proto.RoleUser, msgRole(got[1]))
}

func TestStripSchema(t *testing.T) {
	t.Run("nil returns nil", func(t *testing.T) {
		if proto.StripSchema(nil) != nil {
			t.Error("expected nil for nil input")
		}
	})

	t.Run("removes description title examples default", func(t *testing.T) {
		props := map[string]any{
			"path": map[string]any{
				"type":        "string",
				"description": "the file path",
				"title":       "File Path",
				"examples":    []string{"/tmp/test"},
				"default":     "/tmp/default",
			},
		}
		result := proto.StripSchema(props)
		path := result["path"].(map[string]any)
		if _, ok := path["description"]; ok {
			t.Error("expected description to be stripped")
		}
		if _, ok := path["title"]; ok {
			t.Error("expected title to be stripped")
		}
		if _, ok := path["examples"]; ok {
			t.Error("expected examples to be stripped")
		}
		if _, ok := path["default"]; ok {
			t.Error("expected default to be stripped")
		}
		if path["type"] != "string" {
			t.Errorf("expected type=string, got %v", path["type"])
		}
	})

	t.Run("recursively strips nested properties", func(t *testing.T) {
		props := map[string]any{
			"config": map[string]any{
				"type":        "object",
				"description": "config block",
				"properties": map[string]any{
					"name": map[string]any{
						"type":        "string",
						"description": "the name",
					},
				},
			},
		}
		result := proto.StripSchema(props)
		cfg := result["config"].(map[string]any)
		if _, ok := cfg["description"]; ok {
			t.Error("expected top-level description to be stripped")
		}
		innerProps := cfg["properties"].(map[string]any)
		name := innerProps["name"].(map[string]any)
		if _, ok := name["description"]; ok {
			t.Error("expected nested description to be stripped")
		}
		if name["type"] != "string" {
			t.Errorf("expected nested type=string, got %v", name["type"])
		}
	})

	t.Run("plain values pass through", func(t *testing.T) {
		props := map[string]any{
			"enum": []string{"a", "b"},
			"type": "string",
		}
		result := proto.StripSchema(props)
		if result["enum"].([]string)[0] != "a" {
			t.Error("expected enum to pass through (top-level key)")
		}
	})
}

func TestFromToolSpecs_stripsToolSchemas(t *testing.T) {
	schemaProps := map[string]any{
		"query": map[string]any{
			"type":        "string",
			"description": "the search query",
			"title":       "Search Query",
			"default":     "",
		},
	}
	tools := []proto.ToolSpec{
		{
			Name:        "web_search",
			Description: "search the web",
			InputSchema: map[string]any{
				"type":       "object",
				"properties": schemaProps,
				"required":   []string{"query"},
			},
		},
	}
	result := fromToolSpecs(tools)
	if len(result) != 1 {
		t.Fatalf("expected 1 tool, got %d", len(result))
	}
	params := result[0].Function.Parameters
	props, ok := params["properties"].(map[string]any)
	if !ok {
		t.Fatalf("expected properties map, got %T", params["properties"])
	}
	query, ok := props["query"].(map[string]any)
	if !ok {
		t.Fatalf("expected query map, got %T", props["query"])
	}
	if _, ok := query["description"]; ok {
		t.Error("expected description to be stripped from tool schema")
	}
	if _, ok := query["title"]; ok {
		t.Error("expected title to be stripped from tool schema")
	}
	if query["type"] != "string" {
		t.Errorf("expected type=string, got %v", query["type"])
	}
}
