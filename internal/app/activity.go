package app

import (
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/charmbracelet/x/ansi"
	toolregistry "github.com/panjie/mods/internal/tools"
)

// Activity is presentation-only. The complete tool result remains in session
// messages; compact status lines never contribute to raw stdout.
type activityRecord struct {
	status  string
	read    bool
	failure string
	// pinned records always render on their own line: they never fold into
	// a counted run and never get absorbed by one. Skill loads are pinned so
	// the skills a run used remain visible while collapsed.
	pinned bool
}

const activityStatusLimit = 16384

func cloneActivityBlocks(src map[string][]activityRecord) map[string][]activityRecord {
	dst := maps.Clone(src)
	for key, records := range dst {
		dst[key] = slices.Clone(records)
	}
	return dst
}

func (m *Mods) recordActivity(name, status, output string, err error) {
	marker, ok := m.trailingToolResultBlockMarker()
	if !ok {
		return
	}
	if m.activityBlocks == nil {
		m.activityBlocks = map[string][]activityRecord{}
	}
	status = m.redactActivityText(status)
	status = reviewLiteral(ansi.Strip(status))
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
			if first, _, _ := strings.Cut(strings.TrimSpace(output), "\n"); first != "" {
				failure += " · " + OneLinePreview(m.redactActivityText(first))
			}
		}
	}
	m.activityBlocks[marker] = append(m.activityBlocks[marker], activityRecord{
		status:  status,
		read:    read,
		failure: failure,
		pinned:  name == toolregistry.LoadSkillToolName,
	})
	m.renderDirty = true
	m.flushRender()
}

// Only runs that actually fold can be expanded. Lone operations and pinned
// skill loads already show their status and have no extra fold to reveal.
func (m *Mods) hasFoldableActivity() bool {
	for _, records := range m.activityBlocks {
		previous := false
		for _, item := range records {
			foldable := !item.pinned
			if previous && foldable {
				return true
			}
			previous = foldable
		}
	}
	return false
}

// collapsedActivitySummary describes a folded run of tool calls. It always
// carries the operation, success, and failure counts so hidden failures cannot
// go unnoticed, and keeps the newest call's status as trailing context when
// room allows. All-read runs use a friendlier label.
func collapsedActivitySummary(records []activityRecord) string {
	succeeded := 0
	failed := 0
	allRead := true
	for _, record := range records {
		if record.failure != "" {
			failed++
		} else {
			succeeded++
		}
		if !record.read {
			allRead = false
		}
	}
	marker := "✓"
	if failed > 0 {
		marker = "✗"
	}
	if allRead {
		return fmt.Sprintf("%s Read files · %d succeeded · %d failed", marker, succeeded, failed)
	}
	line := fmt.Sprintf("%s ×%d  %d succeeded · %d failed", marker, len(records), succeeded, failed)
	last := records[len(records)-1].status
	if rest, ok := strings.CutPrefix(last, "✓ "); ok {
		last = rest
	} else if rest, ok := strings.CutPrefix(last, "✗ "); ok {
		last = rest
	}
	if last != "" {
		line += "  ·  " + last
	}
	return line
}

func (m *Mods) renderActivity(records []activityRecord) string {
	width := max(1, m.toolResultStatusWidth())
	var lines []string
	for i := 0; i < len(records); {
		item := records[i]
		end := i + 1
		if !m.activityExpanded && !item.pinned {
			// Collapse consecutive operations into a single summary line
			// so a long tool run cannot crowd out the response text.
			// Failed calls fold too: the summary marker and counts keep
			// them visible, and the short diagnostic renders underneath
			// its own status line once expanded. Reads fold together with
			// commands — an interleaved read must not split a command run.
			// Pinned records (skill loads) split runs: which skills a run
			// used stays readable while collapsed.
			for end < len(records) && !records[end].pinned {
				end++
			}
		}
		if end-i > 1 {
			line := collapsedActivitySummary(records[i:end])
			lines = append(lines, m.toolResultDisplayLine(ansi.Truncate(line, width, "…")))
		} else {
			lines = append(lines, m.toolResultDisplayLine(ansi.Truncate(item.status, width, "…")))
			if item.failure != "" {
				lines = append(lines, m.toolResultDisplayLine(ansi.Truncate(item.failure, width, "…")))
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
	if m.hasFoldableActivity() {
		label := "expand"
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
