package cli

import (
	"fmt"
	"os"
	"strings"

	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/textarea"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
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

var kittyChatCtrlKeys = map[string]tea.KeyType{
	kittyKeyMessage("13;5u"):  tea.KeyCtrlJ, // Ctrl+Enter
	kittyKeyMessage("97;5u"):  tea.KeyCtrlA,
	kittyKeyMessage("98;5u"):  tea.KeyCtrlB,
	kittyKeyMessage("99;5u"):  tea.KeyCtrlC,
	kittyKeyMessage("100;5u"): tea.KeyCtrlD,
	kittyKeyMessage("101;5u"): tea.KeyCtrlE,
	kittyKeyMessage("102;5u"): tea.KeyCtrlF,
	kittyKeyMessage("104;5u"): tea.KeyCtrlH,
	kittyKeyMessage("106;5u"): tea.KeyCtrlJ,
	kittyKeyMessage("107;5u"): tea.KeyCtrlK,
	kittyKeyMessage("110;5u"): tea.KeyCtrlN,
	kittyKeyMessage("112;5u"): tea.KeyCtrlP,
	kittyKeyMessage("115;5u"): tea.KeyCtrlS,
	kittyKeyMessage("116;5u"): tea.KeyCtrlT,
	kittyKeyMessage("117;5u"): tea.KeyCtrlU,
	kittyKeyMessage("118;5u"): tea.KeyCtrlV,
	kittyKeyMessage("119;5u"): tea.KeyCtrlW,
	kittyKeyMessage("121;5u"): tea.KeyCtrlY,
}

func kittyKeyMessage(sequence string) string {
	return fmt.Sprintf("?CSI%+v?", []byte(sequence))
}

type chatStyles struct {
	interaction ui.InteractionStyles
	userRail    lipgloss.Style
	text        lipgloss.Style
	mutedText   lipgloss.Style
}

func makeChatStyles() chatStyles {
	r := StderrRenderer()
	interaction := ui.MakeStylesWithTheme(r, config.Theme).Interaction
	return chatStyles{
		interaction: interaction,
		userRail: r.NewStyle().
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
	fmt.Fprintln(chatOutput, "\n"+styles.interaction.Info.Render("YOU")+"\n"+body)
}

func renderChatAssistant() {
	if !IsErrorTTY() {
		return
	}
	styles := makeChatStyles()
	fmt.Fprintln(chatOutput, "\n"+styles.interaction.Success.Render("MODS"))
}

func renderChatSaved(id string) {
	if !IsErrorTTY() || len(id) < ShortIDLength {
		return
	}
	styles := makeChatStyles()
	fmt.Fprintln(chatOutput, styles.interaction.Success.Render("✓ SAVED")+"  "+styles.interaction.Meta.Render(id[:ShortIDLength]))
}

type chatPromptModel struct {
	textarea textarea.Model
	width    int
	prompt   string
	killText string
	exit     bool
	done     bool
}

func newChatPromptModel() chatPromptModel {
	styles := makeChatStyles()
	input := textarea.New()
	input.Placeholder = ""
	input.Prompt = ""
	input.ShowLineNumbers = false
	input.EndOfBufferCharacter = ' '
	input.MaxHeight = chatMaxHeight
	input.KeyMap.InsertNewline = key.NewBinding(
		key.WithKeys("enter"),
		key.WithHelp("enter", "new line"),
	)
	// Chat uses the panel rail for structure. A background on textarea.Base
	// colors its padded blank cells, which Apple Terminal renders as uneven
	// blocks. Keep the editor transparent and style only its text/cursor.
	input.FocusedStyle.Base = styles.text
	input.FocusedStyle.CursorLine = styles.text
	input.FocusedStyle.Text = styles.text
	input.FocusedStyle.Placeholder = styles.mutedText
	input.FocusedStyle.Prompt = styles.interaction.Info
	input.FocusedStyle.EndOfBuffer = styles.text
	input.BlurredStyle = input.FocusedStyle
	input.SetHeight(chatMinHeight)
	input.SetWidth(chatDefaultWidth)
	_ = input.Focus()
	return chatPromptModel{textarea: input, width: chatDefaultWidth}
}

func (m chatPromptModel) Init() tea.Cmd {
	return m.textarea.Focus()
}

func (m chatPromptModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	msg = normalizeEnhancedChatKey(msg)
	editKey := ""
	switch msg := msg.(type) {
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
				m.resizeHeight()
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
	m.resizeHeight()
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

func normalizeEnhancedChatKey(msg tea.Msg) tea.Msg {
	stringer, ok := msg.(fmt.Stringer)
	if !ok {
		return msg
	}
	keyType, ok := kittyChatCtrlKeys[stringer.String()]
	if !ok {
		return msg
	}
	return tea.KeyMsg{Type: keyType}
}

func (m *chatPromptModel) resize(width int) {
	if width <= 0 {
		width = chatDefaultWidth
	}
	// The composer follows the terminal width. Conversation content keeps its
	// readability cap, but the active input surface should use all available
	// space and react immediately to WindowSizeMsg changes.
	m.width = width
	styles := makeChatStyles()
	innerWidth := ui.InteractionPanelInnerWidth(styles.interaction, m.width)
	m.textarea.SetWidth(innerWidth)
	m.resizeHeight()
}

func (m *chatPromptModel) resizeHeight() {
	lines := 0
	innerWidth := max(1, m.textarea.Width())
	for _, line := range strings.Split(m.textarea.Value(), "\n") {
		lines += max(1, (lipgloss.Width(line)+innerWidth-1)/innerWidth)
	}
	m.textarea.SetHeight(min(chatMaxHeight, max(chatMinHeight, lines)))
}

func (m chatPromptModel) View() string {
	if m.done {
		return ""
	}
	styles := makeChatStyles()
	body := m.textarea.View()
	if m.textarea.Value() == "" {
		body = renderEmptyChatEditor(styles, ui.InteractionPanelInnerWidth(styles.interaction, m.width), m.textarea.Cursor.Blink)
	}
	panel := ui.RenderInteractionPanel(styles.interaction, m.width, ui.InteractionPanel{
		Title: "Message",
		Tone:  ui.InteractionToneInfo,
		Body:  []string{body},
		Actions: []ui.InteractionAction{
			{Key: "Enter", Label: "New line"},
			{Key: "Ctrl+Enter/Ctrl+S", Label: "Send"},
			{Key: "Ctrl+C", Label: "Exit"},
		},
	})
	return "\n" + panel
}

func renderEmptyChatEditor(styles chatStyles, width int, cursorHidden bool) string {
	width = max(1, width)
	cursorCell := styles.interaction.Info.Render("▏")
	if cursorHidden {
		cursorCell = " "
	}
	first := cursorCell + styles.mutedText.Render("Type a message…")
	first += strings.Repeat(" ", max(0, width-lipgloss.Width(first)))
	blank := strings.Repeat(" ", width)
	return strings.Join([]string{first, blank, blank}, "\n")
}

func runInteractiveChatPrompt() (string, bool, error) {
	restoreKeyboard := enableChatKeyboardEnhancements()
	defer restoreKeyboard()
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

func enableChatKeyboardEnhancements() func() {
	input, ok := chatInput.(*os.File)
	if !ok || !term.IsTerminal(int(input.Fd())) || !IsErrorTTY() {
		return func() {}
	}
	_, _ = fmt.Fprint(chatOutput, ansi.PushKittyKeyboard(ansi.KittyDisambiguateEscapeCodes))
	return func() {
		_, _ = fmt.Fprint(chatOutput, ansi.PopKittyKeyboard(1))
	}
}
