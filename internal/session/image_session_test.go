package session

import (
	"testing"

	"github.com/panjie/mods/internal/proto"
	"github.com/stretchr/testify/require"
)

func TestToolImagesPersistWithSourceAndCallID(t *testing.T) {
	db := testDB(t)
	messages := []proto.Message{{Role: proto.RoleTool, Content: "attached", Images: []proto.Image{{Source: "子目录/a.png", MimeType: "image/png", Data: []byte("pixels")}}, ToolCalls: []proto.ToolCall{{ID: "read1", Function: proto.Function{Name: "fs_read_images", Arguments: []byte(`{"paths":["子目录/a.png"]}`)}}}}}
	id := newSessionID()
	require.NoError(t, db.SaveSession(id, "images", "deepseek", "deepseek-flash", messages, nil))
	var loaded []proto.Message
	require.NoError(t, db.ReadMessages(id, &loaded))
	require.Equal(t, messages, loaded)
	require.NotContains(t, proto.Session(loaded).String(), "pixels")
}
