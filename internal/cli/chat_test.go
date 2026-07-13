package cli

import (
	"bytes"
	"context"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/require"
)

func TestIsChatExit(t *testing.T) {
	require.True(t, isChatExit("/exit"))
	require.True(t, isChatExit("/quit"))
	require.True(t, isChatExit("  /exit  "))
	require.False(t, isChatExit("exit"))
	require.False(t, isChatExit("hello"))
}

func TestRunChatExitsWithoutTurn(t *testing.T) {
	withChatTest(t, "/exit\n", func(calls *[]string) {
		require.NoError(t, runChat(context.Background(), nil, nil))
		require.Empty(t, *calls)
	})
}

func TestRunChatIgnoresEmptyLines(t *testing.T) {
	withChatTest(t, "\nhello\n/quit\n", func(calls *[]string) {
		require.NoError(t, runChat(context.Background(), nil, nil))
		require.Equal(t, []string{"hello"}, *calls)
	})
}

func TestRunChatUsesArgsBeforePromptedInput(t *testing.T) {
	withChatTest(t, "second\n/exit\n", func(calls *[]string) {
		require.NoError(t, runChat(context.Background(), []string{"first"}, nil))
		require.Equal(t, []string{"first", "second"}, *calls)
	})
}

func TestRunChatPreservesSessionID(t *testing.T) {
	withChatTest(t, "second\n/exit\n", func(calls *[]string) {
		var continues []string
		chatTurn = func(_ context.Context, prompt string, _ []tea.ProgramOption) (*Mods, error) {
			*calls = append(*calls, prompt)
			continues = append(continues, config.Continue)
			config.SessionWriteToID = "df31ae23ab8b75b5643c2f846c570997edc71333"
			return &Mods{}, nil
		}

		require.NoError(t, runChat(context.Background(), []string{"first"}, nil))

		require.Equal(t, []string{"first", "second"}, *calls)
		require.Equal(t, []string{"", "df31ae23ab8b75b5643c2f846c570997edc71333"}, continues)
	})
}

func TestRunChatRejectsNoSave(t *testing.T) {
	withChatTest(t, "/exit\n", func(_ *[]string) {
		config.NoSave = true

		err := runChat(context.Background(), nil, nil)

		require.Error(t, err)
		merr, ok := err.(modsError)
		require.True(t, ok)
		require.Equal(t, "Chat mode requires session saving.", merr.ReasonText)
	})
}

func TestRunChatRejectsSessionActions(t *testing.T) {
	withChatTest(t, "/exit\n", func(_ *[]string) {
		config.ListPrompts = true

		err := runChat(context.Background(), nil, nil)

		require.Error(t, err)
		merr, ok := err.(modsError)
		require.True(t, ok)
		require.Equal(t, "Chat mode cannot be combined with one-shot session actions.", merr.ReasonText)
	})
}

func TestRunChatRejectsListSkills(t *testing.T) {
	withChatTest(t, "/exit\n", func(_ *[]string) {
		config.ListSkills = true

		err := runChat(context.Background(), nil, nil)

		require.Error(t, err)
		merr, ok := err.(modsError)
		require.True(t, ok)
		require.Equal(t, "Chat mode cannot be combined with one-shot session actions.", merr.ReasonText)
	})
}

func TestValidateChatModeRejectsConfigSetup(t *testing.T) {
	withChatTest(t, "/exit\n", func(_ *[]string) {
		config.Chat = true
		config.ConfigSetup = true

		err := validateChatMode()

		require.Error(t, err)
		merr, ok := err.(modsError)
		require.True(t, ok)
		require.Equal(t, "Chat mode cannot be combined with one-shot session actions.", merr.ReasonText)
	})
}

func TestRunChatTurnPrintsTTYOutput(t *testing.T) {
	saveConfig := config
	saveOneTurn := oneTurn
	saveIsOutputTTY := IsOutputTTY
	defer func() {
		config = saveConfig
		oneTurn = saveOneTurn
		IsOutputTTY = saveIsOutputTTY
	}()

	config = Config{Chat: true}
	IsOutputTTY = func() bool { return true }
	oneTurn = func(_ context.Context, _ []tea.ProgramOption) (*Mods, error) {
		mods := &Mods{}
		mods.Output = "hello"
		return mods, nil
	}

	stdout := captureStdout(t, func() {
		_, err := runChatTurn(context.Background(), "prompt", nil)
		require.NoError(t, err)
	})

	require.Equal(t, "hello", stdout)
}

func TestRunChatPrintsBannerAndPrompt(t *testing.T) {
	var output bytes.Buffer
	withChatTest(t, "/exit\n", func(_ *[]string) {
		chatOutput = &output

		require.NoError(t, runChat(context.Background(), nil, nil))

		got := output.String()
		require.Contains(t, got, "mods chat: type /exit or /quit to quit.")
		require.Contains(t, got, "mods> ")
	})
}

func TestRunChatRendersEnhancedUIOnlyToStderr(t *testing.T) {
	withChatTest(t, "", func(calls *[]string) {
		config.API = "openai"
		config.Model = "gpt-5"
		config.Role = "reviewer"
		IsErrorTTY = func() bool { return true }
		chatTerminalWidth = func() int { return 80 }
		prompts := []string{"/quit"}
		chatPromptInput = func() (string, bool, error) {
			prompt := prompts[0]
			prompts = prompts[1:]
			return prompt, false, nil
		}

		stdout := captureStdout(t, func() {
			require.NoError(t, runChat(context.Background(), []string{"hello"}, nil))
		})

		require.Empty(t, stdout)
		require.Equal(t, []string{"hello"}, *calls)
		got := ansi.Strip(chatOutput.(*bytes.Buffer).String())
		require.Contains(t, got, "MODS CHAT")
		require.Contains(t, got, "openai / gpt-5")
		require.Contains(t, got, "reviewer")
		require.Contains(t, got, "YOU")
		require.Contains(t, got, "hello")
		require.Contains(t, got, "MODS")
		require.Contains(t, got, "SAVED")
		require.Contains(t, got, "df31ae2")
		require.NotContains(t, got, "Session saved:")
	})
}

func TestChatBannerWrapsMetadataWithoutRepeatingActions(t *testing.T) {
	saveConfig := config
	t.Cleanup(func() { config = saveConfig })
	config.API = "anthropic"
	config.Model = "claude-sonnet"
	config.Role = "default"

	for _, width := range []int{28, 30, 60, 80, 120} {
		rendered := renderChatBanner(width)
		for _, line := range strings.Split(rendered, "\n") {
			require.LessOrEqual(t, lipgloss.Width(line), width, line)
		}
		plain := ansi.Strip(rendered)
		require.Contains(t, plain, "MODS CHAT")
		require.Contains(t, strings.Join(strings.Fields(plain), " "), "anthropic / claude-sonnet")
		require.NotContains(t, plain, "Ctrl+Enter")
		require.NotContains(t, plain, "/quit")
	}
}

func TestChatStylesFollowConfiguredTheme(t *testing.T) {
	saveConfig := config
	t.Cleanup(func() { config = saveConfig })

	tests := map[string]lipgloss.Color{
		"charm":      "#7D56F4",
		"dracula":    "#BD93F9",
		"catppuccin": "#CBA6F7",
		"base16":     "#7CAFC2",
		"unknown":    "#7D56F4",
	}
	for theme, accent := range tests {
		config.Theme = theme
		require.Equal(t, accent, makeChatStyles().interaction.Palette.Accent)
	}
}

func TestChatPromptEnterAddsNewlineAndCtrlJSubmits(t *testing.T) {
	model := newChatPromptModel()
	model = updateChatPrompt(t, model, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("hello")})
	model = updateChatPrompt(t, model, tea.KeyMsg{Type: tea.KeyEnter})
	model = updateChatPrompt(t, model, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("world")})
	model = updateChatPrompt(t, model, tea.KeyMsg{Type: tea.KeyCtrlJ})

	require.True(t, model.done)
	require.False(t, model.exit)
	require.Equal(t, "hello\nworld", model.prompt)
}

func TestChatPromptEnhancedCtrlEnterSubmits(t *testing.T) {
	model := newChatPromptModel()
	model.textarea.SetValue("hello")
	updated, _ := model.Update(testStringMessage(kittyKeyMessage("13;5u")))
	result := updated.(chatPromptModel)
	require.True(t, result.done)
	require.Equal(t, "hello", result.prompt)
}

func TestChatPromptCtrlSSubmits(t *testing.T) {
	model := newChatPromptModel()
	model.textarea.SetValue("hello")
	model = updateChatPrompt(t, model, tea.KeyMsg{Type: tea.KeyCtrlS})
	require.True(t, model.done)
	require.Equal(t, "hello", model.prompt)
}

func TestChatPromptEnhancedCtrlCExits(t *testing.T) {
	model := newChatPromptModel()
	updated, _ := model.Update(testStringMessage(kittyKeyMessage("99;5u")))
	result := updated.(chatPromptModel)
	require.True(t, result.done)
	require.True(t, result.exit)
}

func TestChatPromptNormalizesEnhancedEmacsKeys(t *testing.T) {
	tests := map[string]string{
		"97;5u":  "ctrl+a",
		"98;5u":  "ctrl+b",
		"100;5u": "ctrl+d",
		"101;5u": "ctrl+e",
		"102;5u": "ctrl+f",
		"104;5u": "ctrl+h",
		"107;5u": "ctrl+k",
		"110;5u": "ctrl+n",
		"112;5u": "ctrl+p",
		"116;5u": "ctrl+t",
		"117;5u": "ctrl+u",
		"118;5u": "ctrl+v",
		"119;5u": "ctrl+w",
		"121;5u": "ctrl+y",
	}
	for sequence, want := range tests {
		normalized := normalizeEnhancedChatKey(testStringMessage(kittyKeyMessage(sequence)))
		keyMsg, ok := normalized.(tea.KeyMsg)
		require.True(t, ok, sequence)
		require.Equal(t, want, keyMsg.String(), sequence)
	}
}

func TestChatPromptEmacsEditing(t *testing.T) {
	model := newChatPromptModel()
	model.textarea.SetValue("hello world")
	model = updateChatPrompt(t, model, testStringMessage(kittyKeyMessage("97;5u"))) // Ctrl+A
	model = updateChatPrompt(t, model, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("X")})
	require.Equal(t, "Xhello world", model.textarea.Value())

	model = updateChatPrompt(t, model, testStringMessage(kittyKeyMessage("101;5u"))) // Ctrl+E
	model = updateChatPrompt(t, model, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("Y")})
	require.Equal(t, "Xhello worldY", model.textarea.Value())

	model = updateChatPrompt(t, model, testStringMessage(kittyKeyMessage("119;5u"))) // Ctrl+W
	require.Equal(t, "Xhello ", model.textarea.Value())
	model = updateChatPrompt(t, model, testStringMessage(kittyKeyMessage("121;5u"))) // Ctrl+Y
	require.Equal(t, "Xhello worldY", model.textarea.Value())
	model = updateChatPrompt(t, model, testStringMessage(kittyKeyMessage("117;5u"))) // Ctrl+U
	require.Empty(t, model.textarea.Value())
}

func TestRunInteractiveChatPromptRecognizesKittyCtrlEnter(t *testing.T) {
	saveInput := chatInput
	saveOutput := chatOutput
	t.Cleanup(func() {
		chatInput = saveInput
		chatOutput = saveOutput
	})
	chatInput = strings.NewReader("hello\x1b[13;5u")
	chatOutput = &bytes.Buffer{}

	prompt, exit, err := runInteractiveChatPrompt()
	require.NoError(t, err)
	require.False(t, exit)
	require.Equal(t, "hello", prompt)
}

func TestRunInteractiveChatPromptRecognizesKittyCtrlS(t *testing.T) {
	saveInput := chatInput
	saveOutput := chatOutput
	t.Cleanup(func() {
		chatInput = saveInput
		chatOutput = saveOutput
	})
	chatInput = strings.NewReader("hello\x1b[115;5u")
	chatOutput = &bytes.Buffer{}

	prompt, exit, err := runInteractiveChatPrompt()
	require.NoError(t, err)
	require.False(t, exit)
	require.Equal(t, "hello", prompt)
}

func TestRunInteractiveChatPromptRecognizesKittyCtrlC(t *testing.T) {
	saveInput := chatInput
	saveOutput := chatOutput
	t.Cleanup(func() {
		chatInput = saveInput
		chatOutput = saveOutput
	})
	chatInput = strings.NewReader("\x1b[99;5u")
	chatOutput = &bytes.Buffer{}

	prompt, exit, err := runInteractiveChatPrompt()
	require.NoError(t, err)
	require.True(t, exit)
	require.Empty(t, prompt)
}

type testStringMessage string

func (m testStringMessage) String() string { return string(m) }

func TestChatPromptIgnoresBlankCtrlEnterAndCtrlCExits(t *testing.T) {
	model := newChatPromptModel()
	model = updateChatPrompt(t, model, tea.KeyMsg{Type: tea.KeyCtrlJ})
	require.False(t, model.done)

	model = updateChatPrompt(t, model, tea.KeyMsg{Type: tea.KeyCtrlC})
	require.True(t, model.done)
	require.True(t, model.exit)
	require.Empty(t, model.prompt)
}

func TestChatPromptResizesWithinBounds(t *testing.T) {
	model := newChatPromptModel()
	model = updateChatPrompt(t, model, tea.WindowSizeMsg{Width: 28, Height: 20})
	model.textarea.SetValue(strings.Repeat("a long wrapped line ", 12))
	model.resizeHeight()
	require.Equal(t, chatMaxHeight, model.textarea.Height())

	for _, line := range strings.Split(model.View(), "\n") {
		require.LessOrEqual(t, lipgloss.Width(line), 28)
	}

	model.textarea.SetValue("short")
	model.resizeHeight()
	require.Equal(t, chatMinHeight, model.textarea.Height())
}

func TestChatPromptTracksFullTerminalWidth(t *testing.T) {
	model := newChatPromptModel()
	for _, width := range []int{60, 120, 180, 72} {
		model = updateChatPrompt(t, model, tea.WindowSizeMsg{Width: width, Height: 30})
		require.Equal(t, width, model.width)

		widest := 0
		for _, line := range strings.Split(model.View(), "\n") {
			lineWidth := lipgloss.Width(line)
			require.LessOrEqual(t, lineWidth, width, line)
			widest = max(widest, lineWidth)
		}
		require.Equal(t, width, widest, "message input should fill the terminal width")
	}
}

func TestChatPromptTypingKeepsActionRowStable(t *testing.T) {
	model := newChatPromptModel()
	model = updateChatPrompt(t, model, tea.WindowSizeMsg{Width: 30, Height: 20})
	before := chatActionLine(model.View(), "Ctrl+Enter", "Send")
	model = updateChatPrompt(t, model, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("a")})
	after := chatActionLine(model.View(), "Ctrl+Enter", "Send")
	require.NotEqual(t, -1, before)
	require.Equal(t, before, after)
}

func TestChatPromptUsesTransparentEmptyEditorWithVisibleCursor(t *testing.T) {
	model := newChatPromptModel()
	visible := ansi.Strip(model.View())
	require.Contains(t, visible, "▏Type a message…")

	model.textarea.Cursor.Blink = true
	hidden := ansi.Strip(model.View())
	require.NotContains(t, hidden, "▏")
	require.Contains(t, hidden, "Type a message…")

	styles := makeChatStyles()
	require.Equal(t, styles.text.GetBackground(), model.textarea.FocusedStyle.Base.GetBackground())
}

func chatActionLine(view, keyText, label string) int {
	for i, line := range strings.Split(ansi.Strip(view), "\n") {
		if strings.Contains(line, keyText) && strings.Contains(line, label) {
			return i
		}
	}
	return -1
}

func updateChatPrompt(t *testing.T, model chatPromptModel, msg tea.Msg) chatPromptModel {
	t.Helper()
	updated, _ := model.Update(msg)
	result, ok := updated.(chatPromptModel)
	require.True(t, ok)
	return result
}

func withChatTest(t *testing.T, input string, fn func(calls *[]string)) {
	t.Helper()

	saveConfig := config
	saveIsInputTTY := IsInputTTY
	saveIsErrorTTY := IsErrorTTY
	saveChatTurn := chatTurn
	saveChatPromptInput := chatPromptInput
	saveChatTerminalWidth := chatTerminalWidth
	saveChatInput := chatInput
	saveChatOutput := chatOutput
	defer func() {
		config = saveConfig
		IsInputTTY = saveIsInputTTY
		IsErrorTTY = saveIsErrorTTY
		chatTurn = saveChatTurn
		chatPromptInput = saveChatPromptInput
		chatTerminalWidth = saveChatTerminalWidth
		chatInput = saveChatInput
		chatOutput = saveChatOutput
	}()

	config = Config{Chat: true}
	IsInputTTY = func() bool { return true }
	IsErrorTTY = func() bool { return false }
	chatInput = strings.NewReader(input)
	chatOutput = &bytes.Buffer{}
	calls := []string{}
	chatTurn = func(_ context.Context, prompt string, _ []tea.ProgramOption) (*Mods, error) {
		calls = append(calls, prompt)
		config.SessionWriteToID = "df31ae23ab8b75b5643c2f846c570997edc71333"
		return &Mods{}, nil
	}

	fn(&calls)
}
