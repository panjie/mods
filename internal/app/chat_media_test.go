package app

import (
	"context"
	"testing"

	"github.com/panjie/mods/internal/proto"
	"github.com/stretchr/testify/require"
)

func TestComposerMediaReachesUserMessageAsSnapshot(t *testing.T) {
	cfg := defaultConfig()
	cfg.NoSave = true
	cfg.NoInstructions = true
	cfg.WorkingDir = t.TempDir()
	cfg.Media = []proto.MediaAttachment{{Kind: "image", MimeType: "image/png", Source: "clipboard image 1", Data: []byte{1, 2, 3}}}
	m := &Mods{Config: &cfg, Styles: makeStyles(true), ctx: context.Background()}
	require.NoError(t, m.setupStreamContext(""))
	last := m.messages[len(m.messages)-1]
	require.Equal(t, proto.RoleUser, last.Role)
	require.Equal(t, cfg.Media, last.Media)
	require.Empty(t, last.Images)
	cfg.Media[0].Data[0] = 9
	require.Equal(t, byte(1), last.Media[0].Data[0])
}

func TestComposerImageOnlyStartsCompletion(t *testing.T) {
	cfg := defaultConfig()
	cfg.Media = []proto.MediaAttachment{{Kind: "image", Data: []byte{1}}}
	m := &Mods{Config: &cfg, Styles: makeStyles(true), ctx: context.Background()}
	_, cmd := m.Update(completionInput{})
	require.Equal(t, requestState, m.state)
	require.NotNil(t, cmd)
}
