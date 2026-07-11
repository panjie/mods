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
	title     lipgloss.Style
	meta      lipgloss.Style
	user      lipgloss.Style
	assistant lipgloss.Style
	userBody  lipgloss.Style
	help      lipgloss.Style
	saved     lipgloss.Style
	inputBase lipgloss.Style
	text      lipgloss.Style
	mutedText lipgloss.Style
}

func makeChatStyles() chatStyles {
	r := StderrRenderer()
	purple := lipgloss.AdaptiveColor{Light: "#5B3FD1", Dark: "#9D86FF"}
	cyan := lipgloss.AdaptiveColor{Light: "#008F83", Dark: "#3EEFCF"}
	muted := lipgloss.AdaptiveColor{Light: "#6F6F78", Dark: "#777781"}
	border := lipgloss.AdaptiveColor{Light: "#D8D3EA", Dark: "#393446"}
	return chatStyles{
		title: r.NewStyle().
			Foreground(lipgloss.Color("#FFFFFF")).
			Background(lipgloss.Color("#6C50FF")).
			Bold(true).
			Padding(0, 1),
		meta:      r.NewStyle().Foreground(muted),
		user:      r.NewStyle().Foreground(purple).Bold(true),
		assistant: r.NewStyle().Foreground(cyan).Bold(true),
		userBody: r.NewStyle().
			BorderLeft(true).
			BorderStyle(lipgloss.ThickBorder()).
			BorderForeground(purple).
			PaddingLeft(1),
		help:  r.NewStyle().Foreground(muted),
		saved: r.NewStyle().Foreground(muted),
		inputBase: r.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(border).
			Padding(0, 1),
		text:      r.NewStyle(),
		mutedText: r.NewStyle().Foreground(muted),
	}
}

func renderChatBanner(width int) string {
	styles := makeChatStyles()
	title := styles.title.Render("MODS CHAT")
	meta := strings.TrimSpace(config.API + " / " + config.Model)
	if config.Role != "" && config.Role != "default" {
		meta += "  ·  role: " + config.Role
	}

	line := title
	if width >= 48 && (config.API != "" || config.Model != "") {
		candidate := title + "  " + styles.meta.Render(meta)
		if lipgloss.Width(candidate) <= width {
			line = candidate
		}
	}
	help := "Enter send  ·  Ctrl+J newline  ·  /quit exit"
	if width < 54 {
		help = "Enter send  ·  Ctrl+J newline"
	}
	if width < 32 {
		help = "Enter send"
	}
	return line + "\n" + styles.help.Render(help)
}

func renderChatUser(prompt string) {
	if !IsErrorTTY() {
		return
	}
	styles := makeChatStyles()
	width := min(chatTerminalWidth(), chatMaxWidth)
	bodyWidth := max(1, width-styles.userBody.GetHorizontalFrameSize())
	body := styles.userBody.Render(ansi.Hardwrap(prompt, bodyWidth, false))
	fmt.Fprintln(chatOutput, "\n"+styles.user.Render("YOU")+"\n"+body)
}

func renderChatAssistant() {
	if !IsErrorTTY() {
		return
	}
	fmt.Fprintln(chatOutput, "\n"+makeChatStyles().assistant.Render("MODS"))
}

func renderChatSaved(id string) {
	if !IsErrorTTY() || len(id) < ShortIDLength {
		return
	}
	fmt.Fprintln(chatOutput, makeChatStyles().saved.Render("saved · "+id[:ShortIDLength]))
}

type chatPromptModel struct {
	textarea textarea.Model
	width    int
	prompt   string
	exit     bool
	done     bool
}

func newChatPromptModel() chatPromptModel {
	styles := makeChatStyles()
	input := textarea.New()
	input.Placeholder = "Type a message…"
	input.Prompt = ""
	input.ShowLineNumbers = false
	input.EndOfBufferCharacter = ' '
	input.MaxHeight = chatMaxHeight
	input.KeyMap.InsertNewline = key.NewBinding(
		key.WithKeys("ctrl+j"),
		key.WithHelp("ctrl+j", "new line"),
	)
	input.FocusedStyle.Base = styles.inputBase
	input.FocusedStyle.CursorLine = styles.text
	input.FocusedStyle.Text = styles.text
	input.FocusedStyle.Placeholder = styles.mutedText
	input.FocusedStyle.Prompt = styles.user
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
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.resize(msg.Width)
		return m, nil
	case tea.KeyMsg:
		switch msg.String() {
		case "ctrl+c":
			m.exit = true
			m.done = true
			return m, tea.Quit
		case "enter":
			prompt := strings.TrimSpace(m.textarea.Value())
			if prompt == "" {
				return m, nil
			}
			m.prompt = prompt
			m.done = true
			return m, tea.Quit
		}
	}

	var cmd tea.Cmd
	m.textarea, cmd = m.textarea.Update(msg)
	m.resizeHeight()
	return m, cmd
}

func (m *chatPromptModel) resize(width int) {
	if width <= 0 {
		width = chatDefaultWidth
	}
	m.width = min(width, chatMaxWidth)
	m.textarea.SetWidth(max(1, m.width))
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
	help := "Enter send  ·  Ctrl+J newline  ·  Ctrl+C exit"
	if m.width < 48 {
		help = "Enter send  ·  Ctrl+J newline"
	}
	if m.width < 32 {
		help = "Enter send"
	}
	return "\n" + styles.user.Render("YOU") + "\n" + m.textarea.View() + "\n" + styles.help.Render(help)
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
