package main

import (
	"fmt"
	"os"
	"runtime"
	"strings"
	"unicode/utf8"

	"github.com/charmbracelet/mods/internal/clipboard"
	imageutil "github.com/charmbracelet/mods/internal/image"
	"github.com/charmbracelet/mods/internal/proto"
)

func (m *Mods) setupStreamContext(content string, mod Model) error {
	cfg := m.Config
	m.messages = []proto.Message{}

	cwd, _ := os.Getwd()
	hostname, _ := os.Hostname()
	user := os.Getenv("USER")
	if user == "" {
		user = os.Getenv("USERNAME")
	}
	shell := "sh"
	if runtime.GOOS == "windows" {
		shell = "cmd.exe"
	}
	sysInfo := fmt.Sprintf("System info: cwd=%s, user=%s, host=%s, os=%s/%s, shell=%s",
		cwd, user, hostname, runtime.GOOS, runtime.GOARCH, shell)
	m.messages = append(m.messages, proto.Message{
		Role:    proto.RoleSystem,
		Content: sysInfo,
	})
	if txt := cfg.FormatText[cfg.FormatAs]; cfg.Format && !cfg.Minimal && txt != "" {
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
			content, err := loadMsg(m.ctx, msg)
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

	if cfg.Minimal {
		m.messages = append(m.messages, proto.Message{
			Role:    proto.RoleSystem,
			Content: minimalSystemPrompt,
		})
	}

	if prefix := cfg.Prefix; prefix != "" {
		content = strings.TrimSpace(prefix + "\n\n" + content)
	}

	origLen := int64(len(content))
	if !cfg.NoLimit && origLen > mod.MaxChars {
		end := int(mod.MaxChars)
		for end > 0 && !utf8.RuneStart(content[end]) {
			end--
		}
		content = content[:end]
	}

	debugPrintf("Context: %d system message(s), %d existing message(s)", len(m.messages), 0)
	for i, msg := range m.messages {
		debugPrintf("  System message #%d (%d chars): %s", i+1, len(msg.Content), truncateStr(msg.Content, 200))
	}
	if origLen > 0 {
		truncNote := ""
		if !cfg.NoLimit && origLen > mod.MaxChars {
			truncNote = fmt.Sprintf(" (truncated from %d to %d chars, max-input-chars=%d)", origLen, len(content), mod.MaxChars)
		}
		debugPrintf("  User message (%d chars): %s%s", len(content), truncateStr(strings.ReplaceAll(content, "\n", "\\n"), 300), truncNote)
	}

	if !cfg.NoCache && cfg.cacheReadFromID != "" {
		if err := m.db.ReadMessages(cfg.cacheReadFromID, &m.messages); err != nil {
			return modsError{
				err: err,
				reason: fmt.Sprintf(
					"There was a problem reading the stored conversation. Use %s / %s to disable persistence.",
					m.Styles.InlineCode.Render("--no-cache"),
					m.Styles.InlineCode.Render("NO_CACHE"),
				),
			}
		}
		debugPrintf("Conversation: read %d messages from %s", len(m.messages), cfg.cacheReadFromID[:min(sha1short, len(cfg.cacheReadFromID))])
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
		images, err = m.appendImageWithMime(images, &totalBytes, data, mime)
		if err != nil {
			return err
		}
	}
	// Attach stdin image data if present
	if len(m.stdinImageData) > 0 {
		mime, err := imageutil.DetectMimeType(m.stdinImageData)
		if err != nil {
			return modsError{err: err, reason: "Could not detect stdin image format"}
		}
		images, err = m.appendImageWithMime(images, &totalBytes, m.stdinImageData, mime)
		if err != nil {
			return err
		}
	}
	// Attach clipboard image if requested
	if cfg.ClipboardImage {
		data, _, err := clipboard.ReadImage()
		if err != nil {
			return modsError{err: err, reason: "Could not read image from clipboard"}
		}
		supportedMime, err := imageutil.DetectMimeType(data)
		if err != nil {
			return modsError{err: err, reason: "Unsupported clipboard image format"}
		}
		images, err = m.appendImageWithMime(images, &totalBytes, data, supportedMime)
		if err != nil {
			return err
		}
	}
	if len(images) > 0 {
		lastIdx := len(m.messages) - 1
		m.messages[lastIdx].Images = images
		debugPrintf("Images: %d image(s), %d total bytes", len(images), totalBytes)
	}

	return nil
}

func (m *Mods) appendImageWithMime(images []proto.Image, totalBytes *int, data []byte, mime string) ([]proto.Image, error) {
	*totalBytes += len(data)
	if *totalBytes > imageutil.MaxTotalImageBytes {
		return images, modsError{
			err:    fmt.Errorf("total image size exceeds limit of %d bytes", imageutil.MaxTotalImageBytes),
			reason: "Images too large",
		}
	}
	return append(images, proto.Image{Data: data, MimeType: mime}), nil
}
