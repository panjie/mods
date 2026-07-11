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
	chatInput       io.Reader = os.Stdin
	chatOutput      io.Writer = os.Stderr
	chatTurn                  = runChatTurn
	chatPromptInput           = runInteractiveChatPrompt
	oneTurn                   = runOneTurn
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
		if err := runChatMessage(ctx, firstPrompt, opts); err != nil {
			return err
		}
	}

	scanner := bufio.NewScanner(chatInput)
	for {
		prompt, exit, err := readChatPrompt(scanner)
		if err != nil {
			return err
		}
		if exit {
			return nil
		}
		if prompt == "" {
			continue
		}
		if isChatExit(prompt) {
			return nil
		}

		if err := runChatMessage(ctx, prompt, opts); err != nil {
			return err
		}
	}
}

func readChatPrompt(scanner *bufio.Scanner) (string, bool, error) {
	if IsErrorTTY() {
		prompt, exit, err := chatPromptInput()
		if err != nil {
			return "", false, modsError{Err: err, ReasonText: "Could not read chat input."}
		}
		return strings.TrimSpace(prompt), exit, nil
	}

	chatPrompt()
	if !scanner.Scan() {
		if err := scanner.Err(); err != nil {
			return "", false, modsError{Err: err, ReasonText: "Could not read chat input."}
		}
		return "", true, nil
	}
	return strings.TrimSpace(scanner.Text()), false, nil
}

func runChatMessage(ctx context.Context, prompt string, opts []tea.ProgramOption) error {
	renderChatUser(prompt)
	renderChatAssistant()
	if _, err := chatTurn(ctx, prompt, opts); err != nil {
		return err
	}
	renderChatSaved(config.SessionWriteToID)
	prepareNextChatTurn()
	return nil
}

func validateChatMode() error {
	if !config.Chat {
		return nil
	}
	if config.NoSave {
		return modsError{
			Err:        newUserErrorf("--chat requires session saving; remove --no-save"),
			ReasonText: "Chat mode requires session saving.",
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
	return config.List ||
		config.ListRoles ||
		config.ListPrompts ||
		config.ListSkills ||
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
	printTokenUsage(mods)
	if config.SessionWriteToID != "" {
		if _, _, err := persistSession(mods); err != nil {
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
	if config.SessionWriteToID == "" {
		return
	}
	config.Continue = config.SessionWriteToID
	config.ContinueLast = false
	config.SessionReadFromID = ""
	config.SessionWriteToID = ""
	config.SessionWriteToTitle = ""
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
	if !IsErrorTTY() {
		fmt.Fprintln(chatOutput, "mods chat: type /exit or /quit to quit.")
		return
	}
	fmt.Fprintln(chatOutput, renderChatBanner(chatTerminalWidth()))
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
