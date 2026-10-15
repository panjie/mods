package media

import (
	"context"
	"testing"

	"github.com/panjie/mods/internal/proto"
	"github.com/panjie/mods/internal/testutil/mediafixture"
	videoutil "github.com/panjie/mods/internal/video"
	"github.com/stretchr/testify/require"
)

func TestVideoBudgetSharesBytesAndAudioTime(t *testing.T) {
	a, err := videoutil.Parse(context.Background(), mediafixture.Video())
	require.NoError(t, err)
	u := Usage{AudioMillis: 15*60*1000 - a.Video.DurationMillis}
	u.Add(a)
	require.Equal(t, len(a.Data), u.Bytes)
	require.NoError(t, u.Validate())
	u.AudioMillis++
	require.ErrorContains(t, u.Validate(), "audio context")
	u = Usage{Bytes: 20 * 1024 * 1024}
	u.Add(a)
	require.ErrorContains(t, u.Validate(), "budget exhausted")
	u = Usage{VideoMillis: videoutil.MaxTotalDurationMillis}
	u.Add(a)
	require.ErrorContains(t, u.Validate(), "video context")
	require.NoError(t, Validate([]proto.Message{{Media: []proto.MediaAttachment{a}}}))
	a.Video.HasAudio = false
	require.ErrorContains(t, Validate([]proto.Message{{Media: []proto.MediaAttachment{a}}}), "metadata")
}
