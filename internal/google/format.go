package google

import (
	"encoding/base64"
	"encoding/json"

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
//   - proto.RoleTool becomes a user content with functionResponse parts, which
//     is the generateContent wire shape for returning client-side tool results.
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
		default:
			if c, ok := fromProtoMessageOK(in); ok {
				contents = append(contents, c)
			}
		}
	}

	var sysInstr *Content
	if len(sysParts) > 0 {
		sysInstr = &Content{Parts: sysParts}
	}
	return sysInstr, contents
}

func fromProtoMessage(input proto.Message) Content {
	c, _ := fromProtoMessageOK(input)
	return c
}

func fromProtoMessageOK(input proto.Message) (Content, bool) {
	switch input.Role {
	case proto.RoleUser:
		c := Content{Role: geminiRoleUser}
		for _, img := range input.Images {
			b64 := base64.StdEncoding.EncodeToString(img.Data)
			c.Parts = append(c.Parts, Part{
				InlineData: &Blob{MimeType: img.MimeType, Data: b64},
			})
		}
		if input.Content != "" || len(c.Parts) == 0 {
			c.Parts = append(c.Parts, Part{Text: input.Content})
		}
		return c, true
	case proto.RoleAssistant:
		c := Content{Role: geminiRoleModel}
		if input.Content != "" {
			c.Parts = append(c.Parts, Part{Text: input.Content})
		}
		for _, call := range input.ToolCalls {
			var args map[string]any
			if len(call.Function.Arguments) > 0 {
				_ = json.Unmarshal(call.Function.Arguments, &args)
			}
			if args == nil {
				args = map[string]any{}
			}
			// Google requires thought parts preceding a function call to
			// carry their thoughtSignature back in subsequent requests.
			// mods discards the thought text but preserves the signature
			// as an empty thought part so the API stays satisfied.
			if call.Function.ThoughtSignature != "" {
				thoughtTrue := true
				c.Parts = append(c.Parts, Part{
					Thought:          &thoughtTrue,
					ThoughtSignature: call.Function.ThoughtSignature,
				})
			}
			c.Parts = append(c.Parts, Part{FunctionCall: &FunctionCall{
				ID:               call.ID,
				Name:             call.Function.Name,
				Args:             args,
				ThoughtSignature: call.Function.ThoughtSignature,
			}})
		}
		return c, len(c.Parts) > 0
	case proto.RoleTool:
		c := Content{Role: geminiRoleUser}
		for _, call := range input.ToolCalls {
			key := "result"
			if call.IsError {
				key = "error"
			}
			c.Parts = append(c.Parts, Part{FunctionResponse: &FunctionResponse{
				ID:   call.ID,
				Name: call.Function.Name,
				Response: map[string]any{
					key: input.Content,
				},
			}})
		}
		return c, len(c.Parts) > 0
	default:
		return Content{}, false
	}
}

func fromToolSpecs(specs []proto.ToolSpec) []Tool {
	if len(specs) == 0 {
		return nil
	}
	declarations := make([]FunctionDeclaration, 0, len(specs))
	for _, spec := range specs {
		params := spec.InputSchema
		if params == nil {
			params = map[string]any{"type": "object"}
		}
		declarations = append(declarations, FunctionDeclaration{
			Name:        spec.Name,
			Description: spec.Description,
			Parameters:  params,
		})
	}
	return []Tool{{FunctionDeclarations: declarations}}
}
