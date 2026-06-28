package cli

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

var (
	chatInput  io.Reader = os.Stdin
	chatOutput io.Writer = os.Stderr
	chatTurn             = runChatTurn
	oneTurn              = runOneTurn
)

func runChat(ctx context.Context, args []string, opts []tea.ProgramOption) error {
	if err := validateChatMode(); err != nil {
		return err
	}
	if !IsInputTTY() {
		return modsError{
			Err:        newUserErrorf("--chat requires an interactive terminal"),
			ReasonText: "Chat mode requires an interactive terminal.",
		}
	}

	chatBanner()
	firstPrompt := strings.TrimSpace(strings.Join(args, " "))
	if firstPrompt != "" {
		if _, err := chatTurn(ctx, firstPrompt, opts); err != nil {
			return err
		}
		prepareNextChatTurn()
	}

	scanner := bufio.NewScanner(chatInput)
	for {
		chatPrompt()
		if !scanner.Scan() {
			if err := scanner.Err(); err != nil {
				return modsError{Err: err, ReasonText: "Could not read chat input."}
			}
			return nil
		}

		prompt := strings.TrimSpace(scanner.Text())
		if prompt == "" {
			continue
		}
		if isChatExit(prompt) {
			return nil
		}

		if _, err := chatTurn(ctx, prompt, opts); err != nil {
			return err
		}
		prepareNextChatTurn()
	}
}

func validateChatMode() error {
	if !config.Chat {
		return nil
	}
	if config.NoCache {
		return modsError{
			Err:        newUserErrorf("--chat requires conversation caching; remove --no-cache"),
			ReasonText: "Chat mode requires conversation caching.",
		}
	}
	if hasChatSessionAction() {
		return modsError{
			Err:        newUserErrorf("--chat cannot be combined with list, show, delete, settings, config, dirs, or MCP listing actions"),
			ReasonText: "Chat mode cannot be combined with one-shot session actions.",
		}
	}
	return nil
}

func hasChatSessionAction() bool {
	return config.Show != "" ||
		config.ShowLast ||
		config.List ||
		config.ListRoles ||
		config.ListPrompts ||
		len(config.Delete) > 0 ||
		config.DeleteOlderThan != 0 ||
		config.Settings ||
		config.ConfigSetup ||
		config.ResetSettings ||
		config.Dirs ||
		config.MCPList ||
		config.MCPListTools
}

func runChatTurn(ctx context.Context, prompt string, opts []tea.ProgramOption) (*Mods, error) {
	config.Prefix = RemoveWhitespace(prompt)
	mods, err := oneTurn(ctx, opts)
	if err != nil {
		return mods, err
	}
	printChatTurnOutput(mods)
	if config.CacheWriteToID != "" {
		if err := saveConversation(mods); err != nil {
			return mods, err
		}
	}
	return mods, nil
}

func printChatTurnOutput(mods *Mods) {
	if mods == nil || !IsOutputTTY() || config.Raw {
		return
	}
	if output := mods.RenderedOutput(); output != "" {
		fmt.Print(output)
		return
	}
	fmt.Print(mods.Output)
}

func prepareNextChatTurn() {
	if config.CacheWriteToID == "" {
		return
	}
	config.Continue = config.CacheWriteToID
	config.ContinueLast = false
	config.Title = ""
	config.CacheReadFromID = ""
	config.CacheWriteToID = ""
	config.CacheWriteToTitle = ""
}

func isChatExit(input string) bool {
	switch strings.TrimSpace(input) {
	case "/exit", "/quit":
		return true
	default:
		return false
	}
}

func chatBanner() {
	if config.Quiet {
		return
	}
	fmt.Fprintln(chatOutput, "mods chat: type /exit or /quit to quit.")
}

func chatPrompt() {
	fmt.Fprint(chatOutput, "mods> ")
}

func runOneTurn(ctx context.Context, opts []tea.ProgramOption) (*Mods, error) {
	mods, err := newMods(ctx, StderrRenderer(), &config, db)
	if err != nil {
		return nil, modsError{Err: err, ReasonText: "Couldn't start Bubble Tea program."}
	}

	p := tea.NewProgram(mods, opts...)
	m, err := p.Run()
	if err != nil {
		return nil, modsError{Err: err, ReasonText: "Couldn't start Bubble Tea program."}
	}

	mods = m.(*Mods)
	if mods.Error != nil {
		return mods, *mods.Error
	}
	return mods, nil
}
