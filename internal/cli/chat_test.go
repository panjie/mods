package cli

import (
	"bytes"
	"context"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
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

func withChatTest(t *testing.T, input string, fn func(calls *[]string)) {
	t.Helper()

	saveConfig := config
	saveIsInputTTY := IsInputTTY
	saveChatTurn := chatTurn
	saveChatInput := chatInput
	saveChatOutput := chatOutput
	defer func() {
		config = saveConfig
		IsInputTTY = saveIsInputTTY
		chatTurn = saveChatTurn
		chatInput = saveChatInput
		chatOutput = saveChatOutput
	}()

	config = Config{Chat: true}
	IsInputTTY = func() bool { return true }
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
