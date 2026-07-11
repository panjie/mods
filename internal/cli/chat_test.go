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
		require.Contains(t, got, "role: reviewer")
		require.Contains(t, got, "YOU\n")
		require.Contains(t, got, "hello")
		require.Contains(t, got, "MODS")
		require.Contains(t, got, "saved · df31ae2")
		require.NotContains(t, got, "Session saved:")
	})
}

func TestChatBannerHidesSecondaryMetadataWhenNarrow(t *testing.T) {
	saveConfig := config
	t.Cleanup(func() { config = saveConfig })
	config.API = "anthropic"
	config.Model = "claude-sonnet"
	config.Role = "default"

	wide := ansi.Strip(renderChatBanner(80))
	require.Contains(t, wide, "anthropic / claude-sonnet")
	require.NotContains(t, wide, "role:")

	narrow := ansi.Strip(renderChatBanner(30))
	require.Contains(t, narrow, "MODS CHAT")
	require.NotContains(t, narrow, "claude-sonnet")
	require.Contains(t, narrow, "Enter send")
}

func TestChatPromptEnterSubmitsAndCtrlJAddsNewline(t *testing.T) {
	model := newChatPromptModel()
	model = updateChatPrompt(t, model, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("hello")})
	model = updateChatPrompt(t, model, tea.KeyMsg{Type: tea.KeyCtrlJ})
	model = updateChatPrompt(t, model, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("world")})
	model = updateChatPrompt(t, model, tea.KeyMsg{Type: tea.KeyEnter})

	require.True(t, model.done)
	require.False(t, model.exit)
	require.Equal(t, "hello\nworld", model.prompt)
}

func TestChatPromptIgnoresBlankSubmitAndCtrlCExits(t *testing.T) {
	model := newChatPromptModel()
	model = updateChatPrompt(t, model, tea.KeyMsg{Type: tea.KeyEnter})
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
