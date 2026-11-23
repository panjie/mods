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

// Only runs that actually fold can be expanded. Lone operations, failures,
// and pinned skill loads already show their status and have no detail view.
func (m *Mods) hasFoldableActivity() bool {
	for _, records := range m.activityBlocks {
		previous := false
		for _, item := range records {
			foldable := item.failure == "" && !item.pinned
			if previous && foldable {
				return true
			}
			previous = foldable
		}
	}
	return false
}

// collapsedCountPrefix heads a folded run's line with the operation count so
// the total stays visible even when the status text itself is truncated,
// e.g. "✓ ×4 shell  git status · exit 0".
func collapsedCountPrefix(n int) string { return fmt.Sprintf("✓ ×%d ", n) }

func (m *Mods) renderActivity(records []activityRecord) string {
	width := max(1, m.toolResultStatusWidth())
	var lines []string
	for i := 0; i < len(records); {
		item := records[i]
		end := i + 1
		if !m.activityExpanded && item.failure == "" && !item.pinned {
			// Collapse consecutive successful operations into a single
			// line so a long tool run cannot crowd out the response
			// text. Reads fold together with commands — an interleaved
			// read must not split a command run into separate lines.
			// Failed records never fold and split the run, so their
			// diagnostic stays visible. Pinned records
			// (skill loads) split runs the same way: which skills a run
			// used stays readable while collapsed.
			for end < len(records) && records[end].failure == "" && !records[end].pinned {
				end++
			}
		}
		allRead := true
		for j := i; j < end; j++ {
			if !records[j].read {
				allRead = false
				break
			}
		}
		switch {
		case end-i > 1 && allRead:
			lines = append(lines, m.toolResultDisplayLine(ansi.Truncate(fmt.Sprintf("✓ Read files · %d operations", end-i), width, "…")))
		case end-i > 1:
			line := records[end-1].status
			if rest, ok := strings.CutPrefix(line, "✓ "); ok {
				line = collapsedCountPrefix(end-i) + rest
			} else {
				line = collapsedCountPrefix(end-i) + "· " + line
			}
			lines = append(lines, m.toolResultDisplayLine(ansi.Truncate(line, width, "…")))
		default:
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
