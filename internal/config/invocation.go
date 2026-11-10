package config

import (
	"maps"

	"github.com/panjie/mods/internal/proto"
)

// TurnInput is consumed once by a user turn. Model retries reuse its prepared
// messages, including original media bytes, instead of rereading these sources.
type TurnInput struct {
	Prompt                              string
	Media                               []proto.MediaAttachment
	Images, Audio, Video                []string
	StdinImage, ClipboardImage, Compact bool
}

// InvocationOptions freezes parsed options at the CLI boundary. Config remains
// the settings/flag compatibility model; each turn receives an isolated copy.
type InvocationOptions struct{ config Config }

func NewInvocationOptions(cfg Config) InvocationOptions {
	cfg = cfg.Clone()
	cfg.Prefix = ""
	cfg.Media = nil
	cfg.Images, cfg.Audio, cfg.Video = nil, nil, nil
	cfg.StdinImage, cfg.ClipboardImage, cfg.Compact = false, false, false
	cfg.SessionReadFromID, cfg.SessionWriteToID, cfg.SessionWriteToTitle = "", "", ""
	return InvocationOptions{config: cfg}
}
func (c Config) InitialInput() TurnInput {
	return TurnInput{Media: cloneInputMedia(c.Media), Prompt: c.Prefix, Images: append([]string(nil), c.Images...), Audio: append([]string(nil), c.Audio...), Video: append([]string(nil), c.Video...), StdinImage: c.StdinImage, ClipboardImage: c.ClipboardImage, Compact: c.Compact}
}
func (o InvocationOptions) ForTurn(input TurnInput, sessionID string) Config {
	c := o.config.Clone()
	c.Prefix = input.Prompt
	c.Media = cloneInputMedia(input.Media)
	c.Images, c.Audio, c.Video = append([]string(nil), input.Images...), append([]string(nil), input.Audio...), append([]string(nil), input.Video...)
	c.StdinImage, c.ClipboardImage, c.Compact = input.StdinImage, input.ClipboardImage, input.Compact
	if sessionID != "" {
		c.Continue = sessionID
		c.ContinueLast = false
	}
	return c
}

// Clone isolates maps and slices that model resolution and request assembly may
// otherwise share across turns, including nested provider extra parameters.
func (c Config) Clone() Config {
	c.Media = cloneInputMedia(c.Media)
	c.Images = append([]string(nil), c.Images...)
	c.Audio = append([]string(nil), c.Audio...)
	c.Video = append([]string(nil), c.Video...)
	c.SkillsDirs = append([]string(nil), c.SkillsDirs...)
	c.FormatText = maps.Clone(c.FormatText)
	c.Roles = maps.Clone(c.Roles)
	for k, v := range c.Roles {
		c.Roles[k] = append([]string(nil), v...)
	}
	c.MCPServers = maps.Clone(c.MCPServers)
	for k, v := range c.MCPServers {
		v.Args = append([]string(nil), v.Args...)
		v.Env = append([]string(nil), v.Env...)
		c.MCPServers[k] = v
	}
	c.APIs = append(APIs(nil), c.APIs...)
	for i := range c.APIs {
		c.APIs[i].Models = maps.Clone(c.APIs[i].Models)
		for k, v := range c.APIs[i].Models {
			v.Aliases = append([]string(nil), v.Aliases...)
			v.ThinkFields = append([]string(nil), v.ThinkFields...)
			if v.ExtraParams != nil {
				v.ExtraParams = cloneConfigValue(v.ExtraParams).(map[string]any)
			}
			c.APIs[i].Models[k] = v
		}
	}
	return c
}
func cloneConfigValue(value any) any {
	switch v := value.(type) {
	case map[string]any:
		out := make(map[string]any, len(v))
		for k, item := range v {
			out[k] = cloneConfigValue(item)
		}
		return out
	case []any:
		out := make([]any, len(v))
		for i, item := range v {
			out[i] = cloneConfigValue(item)
		}
		return out
	case []string:
		return append([]string(nil), v...)
	default:
		return value
	}
}

func cloneInputMedia(media []proto.MediaAttachment) []proto.MediaAttachment {
	if media == nil {
		return nil
	}
	return proto.CloneMessages([]proto.Message{{Media: media}})[0].Media
}
