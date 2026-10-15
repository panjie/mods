package session

import (
	"context"
	"testing"

	audioutil "github.com/panjie/mods/internal/audio"
	"github.com/panjie/mods/internal/proto"
	"github.com/panjie/mods/internal/testutil/mediafixture"
	videoutil "github.com/panjie/mods/internal/video"
	"github.com/stretchr/testify/require"
)

func TestMixedMediaSessionRoundTrip(t *testing.T) {
	db := testDB(t)
	a, err := audioutil.Parse(mediafixture.WAV(1))
	require.NoError(t, err)
	a.Source = "deleted-source.wav"
	v, err := videoutil.Parse(context.Background(), mediafixture.Video())
	require.NoError(t, err)
	v.Source = "deleted-source.mp4"
	messages := []proto.Message{{Role: proto.RoleTool, Media: []proto.MediaAttachment{a, v, {Kind: "image", MimeType: "image/png", Source: "a.png", Data: []byte("pixels")}}, ToolCalls: []proto.ToolCall{{ID: "read", Function: proto.Function{Name: "fs_read_media"}}}}}
	id := newSessionID()
	require.NoError(t, db.SaveSession(id, "media", "qwen", "qwen3.8-omni-flash", messages, nil))
	var loaded []proto.Message
	require.NoError(t, db.ReadMessages(id, &loaded))
	require.Equal(t, messages, loaded)
	transcript := proto.Session(loaded).String()
	require.Contains(t, transcript, "deleted-source.wav")
	require.Contains(t, transcript, "1.00s")
	require.NotContains(t, transcript, "RIFF")
	require.NotContains(t, transcript, "pixels")
	require.Contains(t, transcript, "deleted-source.mp4")
	require.NotContains(t, transcript, "ftyp")
}
