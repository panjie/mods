package openai

import (
	"context"
	"fmt"
	"github.com/panjie/mods/internal/proto"
	"github.com/stretchr/testify/require"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestTruncatedStreamNeverExecutesTools(t *testing.T) {
	for _, reason := range []string{"", "length", "content_filter"} {
		t.Run(reason, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "text/event-stream")
				fmt.Fprintf(w, "data: {\"id\":\"partial\",\"choices\":[{\"index\":0,\"delta\":{\"role\":\"assistant\",\"tool_calls\":[{\"index\":0,\"id\":\"call_1\",\"type\":\"function\",\"function\":{\"name\":\"counter\",\"arguments\":\"{}\"}}]},\"finish_reason\":%q}]}\n\n", reason)
			}))
			defer server.Close()
			calls := 0
			client := New(Config{AuthToken: "fake", BaseURL: server.URL + "/"})
			s := client.Request(context.Background(), proto.Request{Model: "test", Messages: []proto.Message{{Role: proto.RoleUser, Content: "test"}}, ResultCaller: func(proto.ToolCallRequest) (proto.ToolResult, error) { calls++; return proto.ToolResult{}, nil }})
			defer s.Close()
			for s.Next() {
				s.Current()
			}
			require.Error(t, s.Err())
			require.Empty(t, s.CallTools())
			require.Zero(t, calls)
		})
	}
}
