package app

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/charmbracelet/mods/internal/clipboard"
	imageutil "github.com/charmbracelet/mods/internal/image"
	"github.com/charmbracelet/mods/internal/proto"
)

func (m *Mods) setupStreamContext(content string, mod Model) error {
	cfg := m.Config
	m.messages = []proto.Message{}

	root := m.Config.ResolveWorkspaceRoot()
	hostname, _ := os.Hostname()
	user := os.Getenv("USER")
	if user == "" {
		user = os.Getenv("USERNAME")
	}
	shell := "sh"
	if runtime.GOOS == "windows" {
		if s := os.Getenv("SHELL"); s != "" {
			shell = filepath.Base(s)
		} else {
			shell = "cmd.exe"
		}
	}
	sysParts := []string{
		fmt.Sprintf("workspace_root=%s", root),
		fmt.Sprintf("user=%s", user),
		fmt.Sprintf("host=%s", hostname),
		fmt.Sprintf("os=%s/%s", runtime.GOOS, runtime.GOARCH),
		fmt.Sprintf("shell=%s", shell),
	}
	if runtime.GOOS == "windows" {
		sysParts = append(sysParts, windowsPowerShellCapabilities())
	}
	sysParts = append(sysParts, fmt.Sprintf("date=%s", time.Now().Format("2006-01-02")))
	sysInfo := "System info: " + strings.Join(sysParts, ", ")
	m.messages = append(m.messages, proto.Message{
		Role:    proto.RoleSystem,
		Content: sysInfo,
	})
	if !cfg.Minimal {
		m.messages = append(m.messages, proto.Message{
			Role:    proto.RoleSystem,
			Content: ToolSelectionRules,
		})
	}
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
				Err:        fmt.Errorf("role %q does not exist", cfg.Role),
				ReasonText: "Could not use role",
			}
		}
		for _, msg := range roleSetup {
			content, err := loadMsg(m.ctx, msg)
			if err != nil {
				return modsError{
					Err:        err,
					ReasonText: "Could not use role",
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
			Content: MinimalSystemPrompt,
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

	debug.Printf("Context: %d system message(s), %d existing message(s)", len(m.messages), 0)
	for i, msg := range m.messages {
		debug.Printf("  System message #%d (%d chars): %s", i+1, len(msg.Content), debug.Truncate(msg.Content, 200))
	}
	if origLen > 0 {
		truncNote := ""
		if !cfg.NoLimit && origLen > mod.MaxChars {
			truncNote = fmt.Sprintf(" (truncated from %d to %d chars, max-input-chars=%d)", origLen, len(content), mod.MaxChars)
		}
		debug.Printf("  User message (%d chars): %s%s", len(content), debug.Truncate(strings.ReplaceAll(content, "\n", "\\n"), 300), truncNote)
	}

	if !cfg.NoCache && cfg.CacheReadFromID != "" {
		if err := m.db.ReadMessages(cfg.CacheReadFromID, &m.messages); err != nil {
			return modsError{
				Err: err,
				ReasonText: fmt.Sprintf(
					"There was a problem reading the stored conversation. Use %s / %s to disable persistence.",
					m.Styles.InlineCode.Render("--no-cache"),
					m.Styles.InlineCode.Render("NO_CACHE"),
				),
			}
		}
		debug.Printf("Conversation: read %d messages from %s", len(m.messages), cfg.CacheReadFromID[:min(ShortIDLength, len(cfg.CacheReadFromID))])
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
			return modsError{Err: err, ReasonText: "Could not read image file"}
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
			return modsError{Err: err, ReasonText: "Could not detect stdin image format"}
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
			return modsError{Err: err, ReasonText: "Could not read image from clipboard"}
		}
		supportedMime, err := imageutil.DetectMimeType(data)
		if err != nil {
			return modsError{Err: err, ReasonText: "Unsupported clipboard image format"}
		}
		images, err = m.appendImageWithMime(images, &totalBytes, data, supportedMime)
		if err != nil {
			return err
		}
	}
	if len(images) > 0 {
		lastIdx := len(m.messages) - 1
		m.messages[lastIdx].Images = images
		debug.Printf("Images: %d image(s), %d total bytes", len(images), totalBytes)
	}

	return nil
}

type shellVersionProbe func(context.Context, string) (string, error)

var (
	windowsPowerShellCapabilitiesOnce  sync.Once
	windowsPowerShellCapabilitiesValue string
)

func windowsPowerShellCapabilities() string {
	windowsPowerShellCapabilitiesOnce.Do(func() {
		windowsPowerShellCapabilitiesValue = probeWindowsPowerShellCapabilities(defaultShellVersionProbe)
	})
	return windowsPowerShellCapabilitiesValue
}

func probeWindowsPowerShellCapabilities(probe shellVersionProbe) string {
	return fmt.Sprintf("powershell=%s, pwsh=%s",
		probeShellVersionStatus(probe, "powershell"),
		probeShellVersionStatus(probe, "pwsh"),
	)
}

func probeShellVersionStatus(probe shellVersionProbe, name string) string {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	version, err := probe(ctx, name)
	if err == nil {
		return version
	}
	if errors.Is(err, exec.ErrNotFound) {
		return "not-found"
	}
	return "unknown"
}

func defaultShellVersionProbe(ctx context.Context, name string) (string, error) {
	path, err := exec.LookPath(name)
	if err != nil {
		return "", err
	}
	cmd := exec.CommandContext(ctx, path, "-NoProfile", "-Command", "$PSVersionTable.PSVersion.ToString()")
	out, err := cmd.Output()
	if err != nil {
		return "", err
	}
	version := strings.TrimSpace(string(out))
	if version == "" {
		return "", fmt.Errorf("%s returned empty version output", name)
	}
	return version, nil
}

func (m *Mods) appendImageWithMime(images []proto.Image, totalBytes *int, data []byte, mime string) ([]proto.Image, error) {
	*totalBytes += len(data)
	if *totalBytes > imageutil.MaxTotalImageBytes {
		return images, modsError{
			Err:        fmt.Errorf("total image size exceeds limit of %d bytes", imageutil.MaxTotalImageBytes),
			ReasonText: "Images too large",
		}
	}
	return append(images, proto.Image{Data: data, MimeType: mime}), nil
}
