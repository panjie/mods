package stream

import (
	"testing"

	audioutil "github.com/panjie/mods/internal/audio"
	"github.com/panjie/mods/internal/proto"
	"github.com/panjie/mods/internal/testutil/mediafixture"
	"github.com/stretchr/testify/require"
)

func TestSharedMediaBudgetAndReplay(t *testing.T) {
	a, err := audioutil.Parse(mediafixture.WAV(1))
	require.NoError(t, err)
	require.NoError(t, ValidateImages([]proto.Message{{Media: []proto.MediaAttachment{a}}}))
	require.ErrorContains(t, ValidateImages([]proto.Message{{Images: []proto.Image{{Data: make([]byte, 20*1024*1024)}}, Media: []proto.MediaAttachment{a}}}), "budget")
	a.Audio.DurationMillis = 0
	require.ErrorContains(t, ValidateImages([]proto.Message{{Media: []proto.MediaAttachment{a}}}), "metadata")
	require.Error(t, ValidateImages([]proto.Message{{Media: []proto.MediaAttachment{{Kind: "audio", Data: []byte("junk")}}}}))
}

func TestAudioCumulativeDurationIncludesHistory(t *testing.T) {
	a, err := audioutil.Parse(mediafixture.WAV(180))
	require.NoError(t, err)
	var messages []proto.Message
	for i := 0; i < 6; i++ {
		messages = append(messages, proto.Message{Role: proto.RoleTool, Media: []proto.MediaAttachment{a}})
	}
	require.ErrorContains(t, ValidateMedia(messages), "15 minutes")
}

func TestVideoAndUnknownMediaCapabilities(t *testing.T) {
	input := []proto.Message{{Media: []proto.MediaAttachment{{Kind: "video", Video: proto.VideoMetadata{HasAudio: true}}}}}
	require.Error(t, ValidateMediaCapabilities(input, Capabilities{Images: true, Audio: true}))
	require.Error(t, ValidateMediaCapabilities(input, Capabilities{Video: true}))
	require.NoError(t, ValidateMediaCapabilities(input, Capabilities{Video: true, VideoAudio: true}))
	input[0].Media[0].Video.HasAudio = false
	require.NoError(t, ValidateMediaCapabilities(input, Capabilities{Video: true}))
	input[0].Media[0].Kind = "unknown"
	require.Error(t, ValidateMediaCapabilities(input, Capabilities{Images: true, Audio: true, Video: true, VideoAudio: true}))
}
