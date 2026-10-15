package proto

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestMediaNormalizationAndProjection(t *testing.T) {
	image := Image{Data: []byte("pixels"), Source: "a.png", MimeType: "image/png"}
	media := MediaAttachment{Kind: "image", Data: image.Data, Source: image.Source, MimeType: image.MimeType}
	audio := MediaAttachment{Kind: "audio", Data: []byte("sound"), Source: "b.wav"}
	input := []Message{{Role: RoleTool, Images: []Image{image}, Media: []MediaAttachment{audio, media}}, {Role: RoleTool, Content: "second result"}, {Role: RoleAssistant, Content: "done"}}
	normalized := NormalizeMedia(input)
	require.Len(t, normalized[0].Media, 2)
	require.Empty(t, normalized[0].Images)
	require.Equal(t, normalized, NormalizeMedia(normalized))
	require.Len(t, input[0].Images, 1)
	projected := ProjectToolMedia(normalized)
	require.Equal(t, RoleTool, projected[1].Role)
	require.Equal(t, RoleUser, projected[2].Role)
	require.Equal(t, []MediaAttachment{audio, media}, projected[2].Media)
	require.Equal(t, projected, ProjectToolMedia(projected))
	// Repeated legacy inputs still count individually; only migrated copies dedup.
	require.Len(t, (Message{Images: []Image{image, image}}).Attachments(), 2)
}
