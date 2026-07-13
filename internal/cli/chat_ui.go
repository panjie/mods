package cli

import (
	"fmt"
	"os"
	"strings"

	"charm.land/bubbles/v2/key"
	"charm.land/bubbles/v2/textarea"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/panjie/mods/internal/ui"
	"golang.org/x/term"
)

const (
	chatDefaultWidth = 80
	chatMinHeight    = 3
	chatMaxHeight    = 8
	chatMaxWidth     = 96
)

var chatTerminalWidth = func() int {
	width, _, err := term.GetSize(int(os.Stderr.Fd()))
	if err != nil || width <= 0 {
		return chatDefaultWidth
	}
	return width
}

type chatStyles struct {
	interaction ui.InteractionStyles
	userRail    lipgloss.Style
	text        lipgloss.Style
	mutedText   lipgloss.Style
}

func makeChatStyles() chatStyles {
	return makeChatStylesForTheme(ui.StderrIsDark())
}

func makeChatStylesForTheme(isDark bool) chatStyles {
	return makeChatStylesForInteraction(ui.MakeStylesWithTheme(config.Theme, isDark).Interaction)
}

func makeChatStylesForInteraction(interaction ui.InteractionStyles) chatStyles {
	return chatStyles{
		interaction: interaction,
		userRail: lipgloss.NewStyle().
			BorderStyle(lipgloss.NormalBorder()).
			BorderLeft(true).
			BorderForeground(interaction.Palette.Accent).
			PaddingLeft(1),
		text:      interaction.Body,
		mutedText: interaction.Muted,
	}
}

func renderChatBanner(width int) string {
	styles := makeChatStyles()
	width = normalizedChatWidth(width)
	meta := strings.Trim(strings.TrimSpace(config.API)+" / "+strings.TrimSpace(config.Model), " / ")
	if role := strings.TrimSpace(config.Role); role != "" && role != "default" {
		if meta != "" {
			meta += "  ·  "
		}
		meta += role
	}
	lines := []string{styles.interaction.Info.Render("╭─") + " " + styles.interaction.Title.Render("MODS CHAT")}
	if meta != "" {
		wrapped := strings.Split(ansi.Hardwrap(styles.interaction.Meta.Render(meta), max(1, width-3), false), "\n")
		for i, line := range wrapped {
			prefix := "   "
			if i == 0 {
				prefix = styles.interaction.Info.Render("╰─") + " "
			}
			lines = append(lines, prefix+line)
		}
	}
	return strings.Join(lines, "\n")
}

func normalizedChatWidth(width int) int {
	if width <= 0 {
		width = chatDefaultWidth
	}
	return min(width, chatMaxWidth)
}

func renderChatUser(prompt string) {
	if !IsErrorTTY() {
		return
	}
	styles := makeChatStyles()
	width := normalizedChatWidth(chatTerminalWidth())
	bodyWidth := max(1, width-styles.userRail.GetHorizontalFrameSize())
	body := styles.userRail.Render(ansi.Hardwrap(prompt, bodyWidth, false))
	_, _ = lipgloss.Fprintln(chatOutput, "\n"+styles.interaction.Info.Render("YOU")+"\n"+body)
}

func renderChatAssistant() {
	if !IsErrorTTY() {
		return
	}
	styles := makeChatStyles()
	_, _ = lipgloss.Fprintln(chatOutput, "\n"+styles.interaction.Success.Render("MODS"))
}

func renderChatSaved(id string) {
	if !IsErrorTTY() || len(id) < ShortIDLength {
		return
	}
	styles := makeChatStyles()
	_, _ = lipgloss.Fprintln(chatOutput, styles.interaction.Success.Render("✓ SAVED")+"  "+styles.interaction.Meta.Render(id[:ShortIDLength]))
}

type chatPromptModel struct {
	textarea textarea.Model
	width    int
	prompt   string
	killText string
	exit     bool
	done     bool
	isDark   bool
}

func newChatPromptModel() chatPromptModel {
	styles := makeChatStylesForTheme(true)
	input := textarea.New()
	input.Placeholder = "Type a message…"
	input.Prompt = ""
	input.ShowLineNumbers = false
	input.EndOfBufferCharacter = ' '
	input.DynamicHeight = true
	input.MinHeight = chatMinHeight
	input.MaxHeight = chatMaxHeight
	input.KeyMap.InsertNewline = key.NewBinding(
		key.WithKeys("enter"),
		key.WithHelp("enter", "new line"),
	)
	// Chat uses the panel rail for structure. A background on textarea.Base
	// colors its padded blank cells, which Apple Terminal renders as uneven
	// blocks. Keep the editor transparent and style only its text/cursor.
	inputStyles := input.Styles()
	inputStyles.Focused.Base = styles.text
	inputStyles.Focused.CursorLine = styles.text
	inputStyles.Focused.Text = styles.text
	inputStyles.Focused.Placeholder = styles.mutedText
	inputStyles.Focused.Prompt = styles.interaction.Info
	inputStyles.Focused.EndOfBuffer = styles.text
	inputStyles.Blurred = inputStyles.Focused
	input.SetStyles(inputStyles)
	input.SetVirtualCursor(false)
	input.SetWidth(chatDefaultWidth)
	_ = input.Focus()
	return chatPromptModel{textarea: input, width: chatDefaultWidth, isDark: true}
}

func (m chatPromptModel) Init() tea.Cmd {
	return tea.Batch(m.textarea.Focus(), tea.RequestBackgroundColor)
}

func (m chatPromptModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	editKey := ""
	switch msg := msg.(type) {
	case tea.BackgroundColorMsg:
		m.isDark = msg.IsDark()
		m.applyStyles()
		return m, nil
	case tea.WindowSizeMsg:
		m.resize(msg.Width)
		return m, nil
	case tea.KeyMsg:
		editKey = msg.String()
		switch msg.String() {
		case "ctrl+c":
			m.exit = true
			m.done = true
			return m, tea.Quit
		case "ctrl+j", "ctrl+enter", "ctrl+s":
			return m.submit()
		case "ctrl+y":
			if m.killText != "" {
				m.textarea.InsertString(m.killText)
			}
			return m, nil
		}
	}

	before := m.textarea.Value()
	var cmd tea.Cmd
	m.textarea, cmd = m.textarea.Update(msg)
	if editKey == "ctrl+k" || editKey == "ctrl+u" || editKey == "ctrl+w" {
		if removed := removedText(before, m.textarea.Value()); removed != "" {
			m.killText = removed
		}
	}
	return m, cmd
}

func removedText(before, after string) string {
	prefix := 0
	for prefix < len(before) && prefix < len(after) && before[prefix] == after[prefix] {
		prefix++
	}
	suffix := 0
	for suffix < len(before)-prefix && suffix < len(after)-prefix && before[len(before)-1-suffix] == after[len(after)-1-suffix] {
		suffix++
	}
	return before[prefix : len(before)-suffix]
}

func (m chatPromptModel) submit() (tea.Model, tea.Cmd) {
	prompt := strings.TrimSpace(m.textarea.Value())
	if prompt == "" {
		return m, nil
	}
	m.prompt = prompt
	m.done = true
	return m, tea.Quit
}

func (m *chatPromptModel) resize(width int) {
	if width <= 0 {
		width = chatDefaultWidth
	}
	// The composer follows the terminal width. Conversation content keeps its
	// readability cap, but the active input surface should use all available
	// space and react immediately to WindowSizeMsg changes.
	m.width = width
	styles := makeChatStylesForTheme(m.isDark)
	innerWidth := ui.InteractionPanelInnerWidth(styles.interaction, m.width)
	m.textarea.SetWidth(innerWidth)
}

func (m *chatPromptModel) applyStyles() {
	styles := makeChatStylesForTheme(m.isDark)
	inputStyles := m.textarea.Styles()
	inputStyles.Focused.Base = styles.text
	inputStyles.Focused.CursorLine = styles.text
	inputStyles.Focused.Text = styles.text
	inputStyles.Focused.Placeholder = styles.mutedText
	inputStyles.Focused.Prompt = styles.interaction.Info
	inputStyles.Focused.EndOfBuffer = styles.text
	inputStyles.Blurred = inputStyles.Focused
	m.textarea.SetStyles(inputStyles)
}

func (m chatPromptModel) View() tea.View {
	if m.done {
		return tea.NewView("")
	}
	styles := makeChatStylesForTheme(m.isDark)
	body := m.textarea.View()
	panel := ui.RenderInteractionPanelView(styles.interaction, m.width, ui.InteractionPanel{
		Title:  "Message",
		Tone:   ui.InteractionToneInfo,
		Body:   []string{body},
		Cursor: m.textarea.Cursor(),
		Actions: []ui.InteractionAction{
			{Key: "Enter", Label: "New line"},
			{Key: "Ctrl+Enter/Ctrl+S", Label: "Send"},
			{Key: "Ctrl+C", Label: "Exit"},
		},
	})
	panel.Content = "\n" + panel.Content
	panel = panel.Translate(0, 1)
	return panel.TeaView()
}

func runInteractiveChatPrompt() (string, bool, error) {
	program := tea.NewProgram(
		newChatPromptModel(),
		tea.WithInput(chatInput),
		tea.WithOutput(chatOutput),
	)
	result, err := program.Run()
	if err != nil {
		return "", false, err
	}
	model, ok := result.(chatPromptModel)
	if !ok {
		return "", false, fmt.Errorf("unexpected chat input model %T", result)
	}
	return model.prompt, model.exit, nil
}
