package tools

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/panjie/mods/internal/proto"
	"github.com/stretchr/testify/require"
)

func TestHistoryReadPaginationAndIsolation(t *testing.T) {
	r := NewRegistry()
	require.NoError(t, RegisterHistoryRead(r, []proto.Message{{Role: proto.RoleUser, Content: "你好，original evidence", ProviderData: map[string]json.RawMessage{"secret": json.RawMessage(`"opaque-secret"`)}, Media: []proto.MediaAttachment{{Kind: "image", Source: "original.png", Data: []byte("private-image-bytes")}}}}))
	var text string
	offset := 0
	for page := 0; page < 100; page++ {
		args, err := json.Marshal(map[string]int{"message": 1, "offset": offset, "limit": 11})
		require.NoError(t, err)
		output, err := r.Call(context.Background(), "history_read", args)
		require.NoError(t, err)
		var result struct {
			Text string `json:"text_fragment"`
			Next int    `json:"next_offset"`
			Done bool   `json:"done"`
		}
		require.NoError(t, json.Unmarshal([]byte(output), &result))
		text += result.Text
		if result.Done {
			break
		}
		require.Greater(t, result.Next, offset)
		offset = result.Next
	}
	require.Contains(t, text, "你好，original evidence")
	require.Contains(t, text, "original.png")
	require.NotContains(t, text, "opaque-secret")
	require.NotContains(t, text, "private-image-bytes")
	for _, args := range []string{`{"message":0}`, `{"message":2}`, `{"message":1,"offset":-1}`, `{"message":1,"limit":8193}`} {
		_, err := r.Call(context.Background(), "history_read", []byte(args))
		require.Error(t, err)
	}
}
