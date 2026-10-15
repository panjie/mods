package proto

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestProjectToolImagesKeepsParallelResultsTogether(t *testing.T) {
	input := []Message{{Role: RoleAssistant, Content: "thinking"}, {Role: RoleTool, Content: "a", Images: []Image{{Source: "a.png", Data: []byte{1}}}}, {Role: RoleTool, Content: "b", Images: []Image{{Source: "b.png", Data: []byte{2}}}}, {Role: RoleAssistant, Content: "done"}}
	output := ProjectToolImages(input)
	require.Len(t, output, 5)
	require.Equal(t, RoleTool, output[1].Role)
	require.Equal(t, RoleTool, output[2].Role)
	require.Equal(t, RoleUser, output[3].Role)
	require.Len(t, output[3].Images, 2)
	require.Equal(t, "b.png", output[3].Images[1].Source)
	require.Empty(t, output[1].Images)
	require.Len(t, input[1].Images, 1)
	require.Equal(t, output, ProjectToolImages(output))
}
