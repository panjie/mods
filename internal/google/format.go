package google

import (
	"encoding/base64"

	"github.com/panjie/mods/internal/proto"
)

// Gemini's content.role accepts only "user" or "model". System directives are
// carried by the top-level systemInstruction field, not by a content with
// role "system". These constants exist so callers do not accidentally pipe a
// proto role string (e.g. "assistant") into a wire role that Gemini rejects.
const (
	geminiRoleUser  = "user"
	geminiRoleModel = "model"
)

// fromProtoMessages converts a provider-neutral message slice into Gemini's
// (systemInstruction, contents) request shape.
//
//   - proto.RoleSystem messages are aggregated into systemInstruction.parts so
//     that Gemini treats them as high-priority directives rather than user
//     content. A nil result means there were no system messages and the
//     systemInstruction field will be omitted from the request.
//   - proto.RoleUser and proto.RoleAssistant become contents with role "user"
//     and "model" respectively.
//   - proto.RoleTool messages are dropped: the Google stream client does not
//     implement function calling, and preserving tool results as user/model
//     content would corrupt the conversation if a tool-capable session
//     (OpenAI, Anthropic, Ollama) was continued under --api google.
func fromProtoMessages(input []proto.Message) (*Content, []Content) {
	var sysParts []Part
	contents := make([]Content, 0, len(input))

	for _, in := range input {
		switch in.Role {
		case proto.RoleSystem:
			if in.Content == "" {
				continue
			}
			sysParts = append(sysParts, Part{Text: in.Content})
		case proto.RoleUser:
			c := Content{Role: geminiRoleUser}
			for _, img := range in.Images {
				b64 := base64.StdEncoding.EncodeToString(img.Data)
				c.Parts = append(c.Parts, Part{
					InlineData: &Blob{MimeType: img.MimeType, Data: b64},
				})
			}
			if in.Content != "" || len(c.Parts) == 0 {
				c.Parts = append(c.Parts, Part{Text: in.Content})
			}
			contents = append(contents, c)
		case proto.RoleAssistant:
			// Empty assistant content with no other parts would produce
			// parts: [], which Gemini rejects. Skip such messages.
			if in.Content == "" {
				continue
			}
			contents = append(contents, Content{
				Role:  geminiRoleModel,
				Parts: []Part{{Text: in.Content}},
			})
		case proto.RoleTool:
			continue
		}
	}

	var sysInstr *Content
	if len(sysParts) > 0 {
		sysInstr = &Content{Parts: sysParts}
	}
	return sysInstr, contents
}
