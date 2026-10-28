package app

import (
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/charmbracelet/x/ansi"
)

// Activity is presentation-only. The complete tool result remains in session
// messages; the live preview is bounded and never contributes to raw stdout.
type activityRecord struct {
	status  string
	detail  string
	read    bool
	failure string
}

const activityPreviewLimit = 16384

func cloneActivityBlocks(src map[string][]activityRecord) map[string][]activityRecord {
	dst := maps.Clone(src)
	for key, records := range dst {
		dst[key] = slices.Clone(records)
	}
	return dst
}

func (m *Mods) recordActivity(name, status string, data []byte, output string, err error) {
	marker, ok := m.trailingToolResultBlockMarker()
	if !ok {
		return
	}
	if m.activityBlocks == nil {
		m.activityBlocks = map[string][]activityRecord{}
	}
	rawOutput := output
	output = "Tool: " + name + "\nArguments: " + string(m.activityDisplayArgs(data)) + "\n" + output
	if err != nil {
		output = err.Error() + "\n" + output
	}
	output = m.redactActivityText(output)
	status = m.redactActivityText(status)
	status = reviewLiteral(ansi.Strip(status))
	// Strip styling and escape remaining controls before rendering untrusted text.
	output = strings.TrimSpace(reviewLiteral(ansi.Strip(output)))
	if len(output) > activityPreviewLimit {
		output = strings.ToValidUTF8(output[:activityPreviewLimit], "") + "\n… Preview shortened."
	}
	read := false
	switch name {
	case "fs_read_file", "fs_list_dir", "fs_stat", "fs_search", "fs_largest", "fs_find_images", "fs_read_images", "fs_find_media", "fs_read_media":
		read = err == nil
	}
	failure := ""
	if err != nil {
		failure = OneLinePreview(m.redactActivityText(err.Error()))
		// Exit-only errors carry no diagnostic; preserve the first output line.
		var ec interface{ ExitCode() int }
		if errors.As(err, &ec) {
			if first, _, _ := strings.Cut(strings.TrimSpace(rawOutput), "\n"); first != "" {
				failure += " · " + OneLinePreview(m.redactActivityText(first))
			}
		}
	}
	m.activityBlocks[marker] = append(m.activityBlocks[marker], activityRecord{status: status, detail: output, read: read, failure: failure})
	m.renderDirty = true
	m.flushRender()
}

func (m *Mods) renderActivity(records []activityRecord) string {
	width := max(1, m.toolResultStatusWidth())
	var lines []string
	for i := 0; i < len(records); {
		item := records[i]
		end := i + 1
		if !m.activityExpanded && item.read {
			for end < len(records) && records[end].read {
				end++
			}
		}
		if end-i > 1 {
			lines = append(lines, m.toolResultDisplayLine(ansi.Truncate(fmt.Sprintf("✓ Read files · %d operations", end-i), width, "…")))
		} else {
			lines = append(lines, m.toolResultDisplayLine(ansi.Truncate(item.status, width, "…")))
			if !m.activityExpanded && item.failure != "" {
				lines = append(lines, m.toolResultDisplayLine(ansi.Truncate(item.failure, width, "…")))
			}
			if item.detail != "" && m.activityExpanded {
				detail := strings.Split(ansi.Hardwrap(item.detail, width, false), "\n")
				const limit = 200
				if len(detail) > limit {
					detail = append(detail[:limit], "… More output omitted from live preview")
				}
				for _, line := range detail {
					lines = append(lines, m.Styles.Comment.Render("  │ "+ansi.Truncate(line, width, "…")))
				}
			}
		}
		i = end
	}
	return strings.Join(lines, "\n")
}

func (m *Mods) activityHelp() string {
	if m.Config == nil || m.Config.Raw || m.Config.Minimal || m.Config.HideToolStatus || !IsOutputTTY() {
		return ""
	}
	var keys []string
	if len(m.activityBlocks) > 0 {
		label := "details"
		if m.activityExpanded {
			label = "collapse"
		}
		keys = append(keys, "Ctrl+O "+label)
	}
	if m.todoPanelVisible() {
		if m.planExpanded {
			keys = append(keys, "↑↓ plan · Ctrl+P collapse")
		} else {
			keys = append(keys, "Ctrl+P plan")
		}
	}
	if len(keys) == 0 || m.height < 12 {
		return ""
	}
	// Align with the phase label after the one-cell spinner and its space.
	const indent = "  "
	if m.width <= len(indent) {
		return ""
	}
	return indent + m.Styles.Comment.Faint(true).Render(ansi.Truncate(strings.Join(keys, " · "), m.width-len(indent), "…"))
}
