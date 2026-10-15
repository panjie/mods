package stream

import (
	"strings"
	"testing"

	imageutil "github.com/panjie/mods/internal/image"
	"github.com/panjie/mods/internal/proto"
	"github.com/stretchr/testify/require"
)

func TestNativeToolResultAndBudget(t *testing.T) {
	msg, status := CallTool(proto.ToolCallRequest{ID: "a", Name: "images"}, nil, func(proto.ToolCallRequest) (proto.ToolResult, error) {
		return proto.ToolResult{Text: "attached", Images: []proto.Image{{Data: []byte("pixels"), MimeType: "image/png", Source: "a.png"}}}, nil
	})
	require.Len(t, msg.ImageAttachments(), 1)
	require.Equal(t, "a.png", msg.ImageAttachments()[0].Source)
	require.Equal(t, "attached", status.Output)
	require.NoError(t, ValidateImages([]proto.Message{msg}))
	require.Error(t, ValidateImages([]proto.Message{{Images: make([]proto.Image, imageutil.MaxImageCount+1)}}))
	require.Error(t, ValidateImageWireRequest([]proto.Message{msg}, map[string]string{"text": strings.Repeat("x", 41*1024*1024)}))
}
