package contextplan

import (
	"testing"

	"github.com/panjie/mods/internal/proto"
	"github.com/stretchr/testify/require"
)

func TestPrefix(t *testing.T) {
	user := proto.Message{Role: proto.RoleUser, Content: "goal"}
	answer := proto.Message{Role: proto.RoleAssistant, Content: "done"}
	call := proto.Message{Role: proto.RoleAssistant, ToolCalls: []proto.ToolCall{{ID: "a"}, {ID: "b"}}}
	result := func(id string) proto.Message {
		return proto.Message{Role: proto.RoleTool, ToolCalls: []proto.ToolCall{{ID: id}}}
	}
	for _, tc := range []struct {
		name       string
		messages   []proto.Message
		keep, want int
	}{
		{"empty", nil, 1, 0},
		{"one turn", []proto.Message{user, answer}, 1, 0},
		{"retain latest", []proto.Message{user, answer, user}, 1, 2},
		{"retain two", []proto.Message{user, answer, user, answer, user}, 2, 2},
		{"invalid retention", []proto.Message{user, answer, user}, 0, 0},
		{"whole tool batch", []proto.Message{user, call, result("a"), result("b"), answer, user}, 1, 5},
		{"pending tool", []proto.Message{user, call, result("a"), user}, 1, 0},
		{"orphan result", []proto.Message{user, result("a"), user}, 1, 0},
		{"duplicate result", []proto.Message{user, call, result("a"), result("a"), result("b"), user}, 1, 0},
		{"system excluded", []proto.Message{{Role: proto.RoleSystem}, user, answer, user}, 1, 0},
		{"attachment inside batch", []proto.Message{user, call, result("a"), user, result("b"), answer, user}, 1, 6},
	} {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.want, Prefix(tc.messages, tc.keep))
		})
	}
}

func TestToolPrefix(t *testing.T) {
	user := proto.Message{Role: proto.RoleUser, Content: "task"}
	call := proto.Message{Role: proto.RoleAssistant, ToolCalls: []proto.ToolCall{{ID: "a"}, {ID: "b"}}}
	a := proto.Message{Role: proto.RoleTool, ToolCalls: []proto.ToolCall{{ID: "a"}}}
	b := proto.Message{Role: proto.RoleTool, ToolCalls: []proto.ToolCall{{ID: "b"}}}
	for _, tc := range []struct {
		name     string
		messages []proto.Message
		want     int
	}{
		{"no group", []proto.Message{user}, 0},
		{"one group", []proto.Message{user, call, a, b}, 0},
		{"two complete groups", []proto.Message{user, call, a, b, call, a, b}, 4},
		{"pending", []proto.Message{user, call, a, b, call, a}, 0},
		{"orphan", []proto.Message{user, a, call, a, b, call, a, b}, 0},
		{"overlapping", []proto.Message{user, call, a, call, a, b}, 0},
		{"duplicate result", []proto.Message{user, call, a, b, a, call, a, b}, 0},
		{"projected media", []proto.Message{user, call, a, user, b, call, a, b}, 5},
	} {
		t.Run(tc.name, func(t *testing.T) { require.Equal(t, tc.want, ToolPrefix(tc.messages)) })
	}
}
