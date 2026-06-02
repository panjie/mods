package main

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/mods/internal/proto"

	imageutil "github.com/charmbracelet/mods/internal/image"
)

func (m *Mods) setupStreamContext(content string, mod Model) error {
	cfg := m.Config
	m.messages = []proto.Message{}
	if txt := cfg.FormatText[cfg.FormatAs]; cfg.Format && txt != "" {
		m.messages = append(m.messages, proto.Message{
			Role:    proto.RoleSystem,
			Content: txt,
		})
	}

	if cfg.Role != "" {
		roleSetup, ok := cfg.Roles[cfg.Role]
		if !ok {
			return modsError{
				err:    fmt.Errorf("role %q does not exist", cfg.Role),
				reason: "Could not use role",
			}
		}
		for _, msg := range roleSetup {
			content, err := loadMsg(msg)
			if err != nil {
				return modsError{
					err:    err,
					reason: "Could not use role",
				}
			}
			m.messages = append(m.messages, proto.Message{
				Role:    proto.RoleSystem,
				Content: content,
			})
		}
	}

	if prefix := cfg.Prefix; prefix != "" {
		content = strings.TrimSpace(prefix + "\n\n" + content)
	}

	if !cfg.NoLimit && int64(len(content)) > mod.MaxChars {
		content = content[:mod.MaxChars]
	}

	if !cfg.NoCache && cfg.cacheReadFromID != "" {
		if err := m.cache.Read(cfg.cacheReadFromID, &m.messages); err != nil {
			return modsError{
				err: err,
				reason: fmt.Sprintf(
					"There was a problem reading the cache. Use %s / %s to disable it.",
					m.Styles.InlineCode.Render("--no-cache"),
					m.Styles.InlineCode.Render("NO_CACHE"),
				),
			}
		}
	}

	m.messages = append(m.messages, proto.Message{
		Role:    proto.RoleUser,
		Content: content,
	})
	// Attach images from CLI flags
	var images []proto.Image
	var totalBytes int
	for _, path := range cfg.Images {
		data, mime, err := imageutil.ReadImage(path)
		if err != nil {
			return modsError{err: err, reason: "Could not read image file"}
		}
		totalBytes += len(data)
		if totalBytes > imageutil.MaxTotalImageBytes {
			return modsError{
				err:    fmt.Errorf("total image size exceeds limit of %d bytes", imageutil.MaxTotalImageBytes),
				reason: "Images too large",
			}
		}
		images = append(images, proto.Image{Data: data, MimeType: mime})
	}
	// Attach stdin image data if present
	if len(m.stdinImageData) > 0 {
		mime, err := imageutil.DetectMimeType(m.stdinImageData)
		if err != nil {
			return modsError{err: err, reason: "Could not detect stdin image format"}
		}
		images = append(images, proto.Image{Data: m.stdinImageData, MimeType: mime})
	}
	if len(images) > 0 {
		lastIdx := len(m.messages) - 1
		m.messages[lastIdx].Images = images
	}

	return nil
}
