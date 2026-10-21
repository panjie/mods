package openai

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/panjie/mods/internal/proto"
	"github.com/stretchr/testify/require"
)

func TestUnsupportedAudioFailsBeforeHTTP(t *testing.T) {
	client := New(Config{BaseURL: "http://127.0.0.1:1"})
	// Even the previously special-cased name cannot override the adapter profile.
	st := client.Request(context.Background(), proto.Request{Model: "qwen3.8-omni-flash", Messages: []proto.Message{{Media: []proto.MediaAttachment{{Kind: "audio", Data: []byte("x")}}}}})
	require.False(t, st.Next())
	require.ErrorContains(t, st.Err(), "does not support native audio")
}

func TestQwenMixedEncodingPreservesSourcesAndOrder(t *testing.T) {
	audio := proto.MediaAttachment{Kind: "audio", Source: "first.wav", MimeType: "audio/wav", Data: []byte("audio"), Audio: proto.AudioMetadata{Container: "wav"}}
	image := proto.MediaAttachment{Kind: "image", Source: "second.png", MimeType: "image/png", Data: []byte("pixels")}
	input := []proto.Message{{Role: proto.RoleUser, Content: "compare", Media: []proto.MediaAttachment{audio, image}}}
	chat, err := json.Marshal(fromProtoMessagesForProfile(input, ProviderProfileQwen))
	require.NoError(t, err)
	responses, err := fromProtoResponseInput(input, ProviderProfileQwen)
	require.NoError(t, err)
	wire, err := json.Marshal(responses)
	require.NoError(t, err)
	for _, b := range [][]byte{chat, wire} {
		s := string(b)
		require.Less(t, strings.Index(s, "first.wav"), strings.Index(s, "second.png"))
		require.Contains(t, s, "data:audio/wav;base64,YXVkaW8=")
		require.Contains(t, s, "data:image/png;base64,cGl4ZWxz")
	}
}
