package google

import (
	"encoding/base64"

	"github.com/charmbracelet/mods/internal/proto"
)

func fromProtoMessages(input []proto.Message) []Content {
	result := make([]Content, 0, len(input))
	for _, in := range input {
		switch in.Role {
		case proto.RoleSystem, proto.RoleUser:
			c := Content{Role: proto.RoleUser}
			for _, img := range in.Images {
				b64 := base64.StdEncoding.EncodeToString(img.Data)
				c.Parts = append(c.Parts, Part{
					InlineData: &Blob{MimeType: img.MimeType, Data: b64},
				})
			}
			if in.Content != "" || len(c.Parts) == 0 {
				c.Parts = append(c.Parts, Part{Text: in.Content})
			}
			result = append(result, c)
		}
	}
	return result
}
