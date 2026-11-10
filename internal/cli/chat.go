package cli

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"syscall"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/panjie/mods/internal/app"
	cfgpkg "github.com/panjie/mods/internal/config"
	"github.com/panjie/mods/internal/mcpclient"
)

type chatPromptResult = cfgpkg.TurnInput

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

	ctx, stop := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	defer stop()
	ctx = app.WithReviewSession(ctx)
	ctx, closePool := mcpclient.WithSessionPool(ctx)
	defer closePool() //nolint:errcheck
	session := &chatSession{options: cfgpkg.NewInvocationOptions(config), initial: config.InitialInput()}
	ctx = context.WithValue(ctx, chatSessionKey{}, session)
	chatBanner()
	history := []string{}
	firstPrompt := strings.TrimSpace(strings.Join(args, " "))
	if firstPrompt != "" {
		if err := runChatMessage(ctx, firstPrompt, opts); err != nil {
			return err
		}
		history = append(history, firstPrompt)
	}

	scanner := bufio.NewScanner(chatInput)
	for {
		prompt, exit, err := readChatPrompt(scanner, history)
		if err != nil {
			return err
		}
		if exit {
			return nil
		}
		if prompt.Prompt == "" && len(prompt.Media) == 0 {
			continue
		}

		if err := runChatInput(ctx, prompt, opts); err != nil {
			return err
		}
		history = append(history, prompt.Prompt)
	}
}

func readChatPrompt(scanner *bufio.Scanner, history []string) (chatPromptResult, bool, error) {
	if IsErrorTTY() {
		return readChatPromptOnce(history)
	}

	chatPrompt()
	if !scanner.Scan() {
		if err := scanner.Err(); err != nil {
			return chatPromptResult{}, false, modsError{Err: err, ReasonText: "Could not read chat input."}
		}
		return chatPromptResult{}, true, nil
	}
	return chatPromptResult{Prompt: strings.TrimSpace(scanner.Text())}, false, nil
}

func readChatPromptOnce(history []string) (chatPromptResult, bool, error) {
	if IsErrorTTY() {
		prompt, exit, err := chatPromptInput(history)
		if err != nil {
			return chatPromptResult{}, false, modsError{Err: err, ReasonText: "Could not read chat input."}
		}
		prompt.Prompt = strings.TrimSpace(prompt.Prompt)
		return prompt, exit, nil
	}

	scanner := bufio.NewScanner(chatInput)
	chatPrompt()
	if !scanner.Scan() {
		if err := scanner.Err(); err != nil {
			return chatPromptResult{}, false, modsError{Err: err, ReasonText: "Could not read chat input."}
		}
		return chatPromptResult{}, true, nil
	}
	return chatPromptResult{Prompt: strings.TrimSpace(scanner.Text())}, false, nil
}

func runChatMessage(ctx context.Context, prompt string, opts []tea.ProgramOption) error {
	return runChatInput(ctx, chatPromptResult{Prompt: prompt}, opts)
}

func runChatInput(ctx context.Context, input chatPromptResult, opts []tea.ProgramOption) error {
	prompt := input.Prompt
	display := prompt
	for _, media := range input.Media {
		display += "\n[" + media.Source + "]"
	}
	renderChatUser(display)
	renderChatAssistant()
	session, _ := ctx.Value(chatSessionKey{}).(*chatSession)
	if session != nil {
		ctx = session.next(ctx, input)
	}
	mods, err := chatTurn(ctx, prompt, opts)
	if err != nil {
		return err
	}
	cfg := resultConfig(mods)
	renderChatSaved(cfg.SessionWriteToID)
	if session != nil {
		session.id = cfg.SessionWriteToID
		session.started = true
		session.instructionsShown = cfg.ProjectInstructionsNoticeShown
	}
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

// hasChatSessionAction reports whether a flag that cannot be combined with
// --chat is selected. That set is declared as roleBlocksChat in the flag table.
func hasChatSessionAction() bool {
	return anyRoleSelected(roleBlocksChat)
}

func runChatTurn(ctx context.Context, prompt string, opts []tea.ProgramOption) (*Mods, error) {
	cfg := turnConfig(ctx).Clone()
	cfg.Prefix = RemoveWhitespace(prompt)
	ctx = context.WithValue(ctx, turnConfigKey{}, &cfg)
	mods, err := oneTurn(ctx, opts)
	if err != nil {
		return mods, err
	}
	printChatTurnOutput(mods)
	printTokenUsage(mods)
	if resultConfig(mods).SessionWriteToID != "" {
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
		_, _ = lipgloss.Fprint(os.Stdout, output)
		return
	}
	_, _ = lipgloss.Fprint(os.Stdout, mods.Output)
}

func chatBanner() {
	if !IsErrorTTY() {
		_, _ = lipgloss.Fprintln(chatOutput, "mods chat: press Ctrl+C to quit.")
		return
	}
	_, _ = lipgloss.Fprintln(chatOutput, renderChatBanner(chatTerminalWidth()))
}

func chatPrompt() {
	fmt.Fprint(chatOutput, "mods> ")
}

func runOneTurn(ctx context.Context, opts []tea.ProgramOption) (*Mods, error) {
	// Wrap ctx with signal handling so SIGINT/SIGTERM cancel the request
	// context that every provider HTTP call and tool call descends from. This
	// covers `kill` and non-TTY Ctrl+C; interactive Ctrl+C in raw mode is a
	// KeyMsg handled by Mods.quit(), which cancels the same context. Bubble
	// Tea's own signal handler is left enabled (we do not pass tea.WithContext,
	// so its internal context stays decoupled from this one) — the program
	// still exits on these signals, and we only add request cancellation on top
	// with no risk of double-handling.
	signalCtx, stop := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	defer stop()

	cfg := turnConfig(ctx).Clone()
	mods, err := newMods(signalCtx, &cfg, db, withSelfHelpCLI(registeredSelfHelpFlags))
	if err != nil {
		return nil, modsError{Err: err, ReasonText: "Couldn't start Bubble Tea program."}
	}

	p := tea.NewProgram(mods, opts...)
	var relay *debugRelay
	restoreDebugSink := func() {}
	if debug.Enabled() && config.InteractiveTTYAvailable && IsErrorTTY() && !config.Raw {
		relay = newDebugRelay(func(section string) {
			p.Send(tea.Println(section)())
		})
		restoreDebugSink = debug.SetManagedSink(relay.enqueue)
	}
	m, err := p.Run()
	restoreDebugSink()
	if relay != nil {
		relay.wait()
	}
	if err != nil {
		return nil, modsError{Err: err, ReasonText: "Couldn't start Bubble Tea program."}
	}

	mods = m.(*Mods)
	if mods.Error != nil {
		return mods, *mods.Error
	}
	return mods, nil
}
