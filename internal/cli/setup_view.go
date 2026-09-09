package cli

import (
	"fmt"
	"slices"
	"strings"

	"charm.land/bubbles/v2/textarea"
	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/panjie/mods/internal/ui"
)

func (m *setupModel) styles() ui.InteractionStyles {
	return ui.MakeInteractionStyles(config.Theme, m.isDark, m.backgroundKnown)
}
func (m *setupModel) panelWidth() int { return max(1, min(92, m.width-1)) }
func (m *setupModel) contentWidth() int {
	return ui.InteractionPanelInnerWidth(m.styles(), m.panelWidth())
}
func (m *setupModel) styleInputs() {
	s := m.styles()
	ti := textinput.DefaultStyles(m.isDark)
	ti.Focused = textinput.StyleState{Text: s.Body, Placeholder: s.Muted, Suggestion: s.Muted, Prompt: s.Title}
	ti.Blurred = ti.Focused
	ti.Cursor.Color = s.Palette.Accent
	m.input.SetStyles(ti)
	m.filter.SetStyles(ti)
	ta := textarea.DefaultStyles(m.isDark)
	ta.Focused = textarea.StyleState{Text: s.Body, Placeholder: s.Muted, Prompt: s.Title, EndOfBuffer: s.Muted, CursorLine: s.Body}
	ta.Blurred = ta.Focused
	ta.Cursor.Color = s.Palette.Accent
	m.area.SetStyles(ta)
}
func (m *setupModel) heading() (string, string) {
	switch m.page {
	case setupProvider:
		return "Provider", "Choose the API backend mods should use by default."
	case setupName:
		return "New provider name", "Use lowercase letters, digits, '-' or '_'."
	case setupProtocol:
		return "API type", "Choose the protocol your endpoint supports."
	case setupEndpoint:
		return "Provider endpoint", "Base URL for " + m.api() + ". Shared by this provider's models."
	case setupCredentials:
		return "API key", "Environment variables keep secrets out of the YAML file."
	case setupKey:
		return "Saved API key", "The key is stored in plaintext in your config file."
	case setupAuth:
		return "GitHub Copilot sign in", "Authorize mods using GitHub's device flow."
	case setupDiscovery:
		return "Discover models", "Choose models for " + m.api() + "."
	case setupManual:
		return "New models", configWizardManualModelsDescription(m.draft().discoveryErr)
	case setupDefault:
		return "Default model", "Choose the model mods uses when no model is specified."
	case setupFilesystem:
		return "Filesystem", "Controls whether mods can read and write local files."
	case setupShell:
		return "Shell execution", "Enable shell execution? Tool review settings control approval."
	case setupSearch:
		return "Web search", "Enable web search when the provider supports tools?"
	case setupSearchProvider:
		return "Search provider", "Choose where web_search sends queries."
	case setupSearchURL:
		return "Custom search URL", "Search endpoint responding to /search?q=...&limit=... ."
	case setupSearchCredentials:
		return "Search credentials", "Choose where mods reads the search API key."
	case setupSearchKey:
		return "Saved search key", "The key is stored in plaintext in your config file."
	case setupReview:
		return "Tool review", "Choose how often mods asks before running tools."
	case setupStorage:
		return "Config file location", "Portable keeps configuration and sessions next to the executable."
	case setupSummary:
		return "Configuration summary", "Enter saves this configuration. Esc returns to editing."
	case setupConnectionFailed:
		return "Connection test failed", "Save configuration anyway?"
	}
	return "mods setup", ""
}
func (m *setupModel) summary() string {
	d := m.draft()
	names, _ := configWizardModelNames(m.api(), d.selected, d.manual)
	rows := []string{"Provider: " + m.api(), "API type: " + m.protocol(), "Base URL: " + m.redact(m.endpoint()), "Default model: " + d.defaultModel, fmt.Sprintf("Models: %d", len(names)), "API key: " + setupStorageLabel(d.storage), "Filesystem: " + m.data.fsMode, "Shell: " + boolLabel(m.data.shellOn), "Web search: " + boolLabel(m.data.webSearchOn)}
	if d.storage == "env" && m.api() != "ollama" {
		rows = append(rows, "Key environment: "+resolveEnvVar(m.api()))
	}
	if m.data.webSearchOn {
		rows = append(rows, "Search provider: "+m.data.webSearchProvider, "Search key: "+m.data.webSearchKeyStorage)
		if m.data.webSearchProvider == "custom" {
			rows = append(rows, "Search URL: "+m.redact(m.data.webSearchProviderValue))
		}
		if m.data.webSearchKeyStorage == "env" {
			rows = append(rows, "Search environment: "+m.data.webSearchAPIKeyEnv)
		}
	}
	return strings.Join(append(rows, "Review: "+m.data.reviewMode, "Config file: "+m.savePath()), "\n")
}
func (m *setupModel) redact(text string) string {
	for _, secret := range []string{m.draft().key, m.data.webSearchAPIKey, resolveKeyForDiscovery(m.api(), m.draft().key)} {
		if secret != "" {
			text = strings.ReplaceAll(text, secret, "[redacted]")
		}
	}
	return text
}
func (m *setupModel) View() tea.View {
	if m.done || m.canceled {
		return tea.NewView("")
	}
	w := m.contentWidth()
	s := m.styles()
	if m.width < 16 || m.height < 8 {
		return tea.NewView(ansi.Truncate("Enlarge terminal. Ctrl+C cancels.", max(1, m.width-1), ""))
	}
	title, description := m.heading()
	description = m.redact(description)
	panel := ui.InteractionPanel{Title: ansi.Truncate(strings.ToUpper(title), w, "…"), Actions: m.actions()}
	if m.filtering {
		panel.Body = []string{m.filter.View()}
	}
	fixedHeight := lipgloss.Height(ui.RenderInteractionPanel(s, m.panelWidth(), panel))
	// A short terminal still needs room for content. Keep the essential keys;
	// the full keyboard map remains active regardless of available space.
	if fixedHeight > m.height-3 {
		panel.Actions = []ui.InteractionAction{{Key: "enter", Label: "next"}, {Key: "esc", Label: "back"}}
		fixedHeight = lipgloss.Height(ui.RenderInteractionPanel(s, m.panelWidth(), panel))
	}
	visible := max(1, m.height-1-fixedHeight)
	// Keep headings and help fixed. Long descriptions and bodies scroll together.
	lines := strings.Split(ansi.Wrap(s.Muted.Render(description), w, ""), "\n")
	lines = append(lines, "")
	focusLine := -1
	focusEnd := -1
	switch {
	case m.isInput():
		focusLine = len(lines)
		lines = append(lines, m.input.View())
		focusEnd = len(lines) - 1
	case m.page == setupManual:
		focusLine = len(lines)
		lines = append(lines, strings.Split(m.area.View(), "\n")...)
		focusEnd = len(lines) - 1
	case m.page == setupSummary:
		lines = append(lines, strings.Split(ansi.Wrap(s.Body.Render(m.summary()), w, ""), "\n")...)
	case m.page == setupAuth:
		if m.device.UserCode != "" {
			lines = append(lines, "Open this URL in your browser:")
			lines = append(lines, strings.Split(ansi.Wrap(m.device.VerificationURI, w, ""), "\n")...)
			lines = append(lines, "Device code: "+s.Title.Render(m.device.UserCode))
		} else if !configWizardWaitingForCopilotAuth(m.api(), m.draft().key) {
			lines = append(lines, s.Success.Render("[OK] Already signed in. Enter continues."))
		} else {
			lines = append(lines, "Enter to sign in or retry.")
		}
	default:
		opts := m.filtered()
		if len(opts) == 0 && m.busy == "" {
			lines = append(lines, "No matching options.")
		}
		for i, o := range opts {
			focused := i == m.cursor
			if focused {
				focusLine = len(lines)
			}
			lines = append(lines, setupOptionLines(s, w, o.Key, focused, m.page == setupDiscovery, slices.Contains(m.draft().selected, o.Value))...)
			if focused {
				focusEnd = len(lines) - 1
			}
		}
	}
	if m.busy != "" {
		lines = append(lines, s.Muted.Render(m.busy+"…"))
	}
	if m.err != nil {
		if len(m.options()) == 0 {
			focusLine = len(lines)
		}
		lines = append(lines, strings.Split(ansi.Wrap(s.Danger.Render("Error: "+m.redact(m.err.Error())), w, ""), "\n")...)
		if len(m.options()) == 0 {
			focusEnd = len(lines) - 1
		}
	}
	offset := min(m.scroll, max(0, len(lines)-visible))
	if focusLine >= 0 && !m.manualScroll {
		if focusLine < offset {
			offset = focusLine
		}
		if focusEnd >= offset+visible {
			if focusEnd-focusLine+1 > visible {
				offset = focusLine
			} else {
				offset = max(focusLine, focusEnd-visible+1)
			}
		}
	}
	body := append([]string{}, lines[offset:min(len(lines), offset+visible)]...)
	// The body can contain Bubbles-generated ANSI, so truncate by display cells.
	for i, line := range body {
		body[i] = ansi.Truncate(line, w, "")
	}
	panel.Body = append(panel.Body, body...)
	return tea.NewView(ui.RenderInteractionPanel(s, m.panelWidth(), panel))
}

func setupStorageLabel(storage string) string {
	if storage == "config" {
		return "saved in config"
	}
	return "environment variable"
}

// Option geometry is independent of focus and selection. A fixed gutter keeps
// the cursor, checkbox, label, and wrapped continuations in their own columns.
func setupOptionLines(s ui.InteractionStyles, width int, label string, focused, multi, checked bool) []string {
	prefix := "  "
	if focused {
		prefix = "> "
	}
	if multi {
		if checked {
			prefix += "[x] "
		} else {
			prefix += "[ ] "
		}
	}
	indent := strings.Repeat(" ", lipgloss.Width(prefix))
	style := s.Body
	if focused {
		style = s.Selected.Padding(0)
	}
	wrapped := strings.Split(ansi.Wrap(ansi.Strip(label), max(1, width-lipgloss.Width(prefix)), ""), "\n")
	lines := make([]string, 0, len(wrapped))
	for i, line := range wrapped {
		gutter := indent
		if i == 0 {
			gutter = prefix
		}
		lines = append(lines, style.Render(gutter+line))
	}
	return lines
}

func (m *setupModel) actions() []ui.InteractionAction {
	if m.exitArmed {
		return []ui.InteractionAction{{Key: "esc", Label: "again to exit without saving"}}
	}
	next := "next"
	if m.page == setupSummary {
		next = "save"
	}
	actions := []ui.InteractionAction{}
	if len(m.options()) > 0 {
		moveKey := "↑↓/j/k"
		if m.filtering {
			moveKey = "↑↓"
		}
		actions = append(actions, ui.InteractionAction{Key: moveKey, Label: "move"}, ui.InteractionAction{Key: "/", Label: "filter"})
	}
	if m.page == setupDiscovery {
		actions = append(actions, ui.InteractionAction{Key: "space", Label: "select"}, ui.InteractionAction{Key: "m", Label: "manual"}, ui.InteractionAction{Key: "r", Label: "retry"})
	}
	if m.page == setupManual {
		actions = append(actions, ui.InteractionAction{Key: "ctrl+j", Label: "new line"})
	}
	actions = append(actions, ui.InteractionAction{Key: "enter", Label: next}, ui.InteractionAction{Key: "esc", Label: "back"})
	if m.page == setupSummary {
		actions = append(actions, ui.InteractionAction{Key: "pgup/pgdn", Label: "scroll"})
	}
	quitKey := "q/ctrl+c"
	if m.isInput() || m.page == setupManual || m.filtering {
		quitKey = "ctrl+c"
	}
	return append(actions, ui.InteractionAction{Key: quitKey, Label: "cancel"})
}
