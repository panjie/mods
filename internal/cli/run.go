package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/editor"
)

var canOpenReviewTTY = func() bool {
	path := "/dev/tty"
	flag := os.O_RDONLY
	if runtime.GOOS == "windows" {
		path = "CONIN$"
		flag = os.O_RDWR
	}
	f, err := os.OpenFile(path, flag, 0)
	if err != nil {
		debug.Printf("Review TTY unavailable: %v", err)
		return false
	}
	if err := f.Close(); err != nil {
		debug.Printf("Review TTY close failed: %v", err)
	}
	return true
}

var askInfoPrompt = askInfo

// buildTeaProgramOptions assembles the Bubble Tea options for the current
// invocation. Splitting this out of RunE keeps the entry point focused on
// orchestration; the tty/raw decision tree was previously interleaved with
// flag-validation code.
func buildTeaProgramOptions() []tea.ProgramOption {
	return teaProgramOptions(config.Raw)
}

// Explicit setup needs terminal input even when model output is configured raw.
func buildSetupProgramOptions() []tea.ProgramOption {
	return teaProgramOptions(false)
}

func teaProgramOptions(raw bool) []tea.ProgramOption {
	opts := []tea.ProgramOption{}
	config.InteractiveTTYAvailable = false

	if raw || !IsErrorTTY() {
		opts = append(opts, tea.WithInput(nil))
	} else if IsInputTTY() {
		config.InteractiveTTYAvailable = true
	} else if canOpenReviewTTY() {
		// Bubble Tea v2 opens the controlling TTY automatically when stdin is
		// redirected, so no explicit input option is required here.
		config.InteractiveTTYAvailable = true
	} else {
		opts = append(opts, tea.WithInput(nil))
	}

	if IsErrorTTY() && !raw && config.InteractiveTTYAvailable {
		opts = append(opts, tea.WithOutput(os.Stderr))
	} else {
		opts = append(opts, tea.WithoutRenderer())
	}
	return opts
}

// gatherInteractivePrompt covers the interactive prompts that RunE may trigger
// on a TTY: optional $EDITOR capture, API/model selection, and one-shot prompt
// entry through the same composer used by chat mode.
func gatherInteractivePrompt() error {
	if isNoArgs() && IsInputTTY() && config.OpenEditor {
		prompt, err := prefixFromEditor()
		if err != nil {
			return err
		}
		config.Prefix = prompt
	}

	if isNoArgs() && IsInputTTY() {
		if err := askInfoPrompt(); errors.Is(err, errSetupCanceled) {
			return modsError{
				Err:        err,
				ReasonText: "User canceled.",
			}
		} else if err != nil {
			return modsError{
				Err:        err,
				ReasonText: "Prompt failed.",
			}
		}
	}

	if isNoArgs() && IsInputTTY() {
		prompt, exit, err := readChatPromptOnce(nil)
		if err != nil {
			return err
		}
		if exit {
			return modsError{
				Err:        errSetupCanceled,
				ReasonText: "User canceled.",
			}
		}
		config.Prefix = RemoveWhitespace(prompt.Prompt)
		config.Media = prompt.Media
	}
	return nil
}

// maybePrintMissingAPIKeyHint surfaces a one-line hint when the user is
// starting a fresh prompt but has not configured an API key for the active
// provider. Ollama is exempt because it runs locally and does not need a
// key, and the hint is suppressed when stderr is not a TTY to avoid
// polluting piped output.
func maybePrintMissingAPIKeyHint() {
	if !isNoArgs() || HasAPIKey(&config) || config.API == "ollama" || !IsErrorTTY() {
		return
	}
	fmt.Fprintf(os.Stderr, "\n  No API key detected for %s.\n  Run %s to configure your provider.\n\n",
		config.API, StderrStyles().InlineCode.Render("mods --config"))
}

// dispatchPreTurnAction handles commands that do not need a model request.
// Keeping them ahead of Bubble Tea startup prevents terminal capability
// queries from leaking replies into the user's shell when the command exits.
func dispatchPreTurnAction(ctx context.Context, args []string) (bool, error) {
	if showSkillsDirs {
		printSkillsDirs(config.ResolveSkillsDirs())
		return true, nil
	}

	if config.Dirs {
		return true, runDirsAction(args)
	}

	if config.Settings {
		if config.SettingsImport {
			return true, runSettingsImportAction()
		}
		return true, runSettingsEditorAction()
	}

	if config.ResetSettings {
		return true, resetSettings()
	}

	if config.ListRoles {
		listRoles()
		return true, nil
	}
	if config.ListPrompts {
		listPrompts()
		return true, nil
	}
	if config.ListSkills {
		return true, listSkills(config.ResolveSkillsDirs())
	}
	if config.List {
		if err := ensureSessionDB(); err != nil {
			return true, err
		}
		id, err := listSessions(config.Raw)
		if err != nil || id == "" {
			return true, err
		}
		// Resume through the normal --continue prompt and model-turn path.
		config.List = false
		config.Continue = id
		config.ContinueLast = false
		return false, nil
	}

	if config.MCPList {
		listMCPServers(&config)
		return true, nil
	}

	if config.MCPListTools {
		ctx, cancel := context.WithTimeout(ctx, config.MCPTimeout)
		defer cancel()
		return true, listAllTools(ctx, &config)
	}

	return false, nil
}

// printSkillsDirs emits the effective scan roots one per line. Absolute paths
// make the output unambiguous and convenient for shell consumers while keeping
// missing configured directories visible.
func printSkillsDirs(dirs []string) {
	for _, dir := range dirs {
		if abs, err := filepath.Abs(dir); err == nil {
			dir = abs
		}
		fmt.Println(filepath.Clean(dir))
	}
}

// dispatchTurnResult validates and prints the result of a real model turn.
func dispatchTurnResult(mods *Mods) error {
	if mods.Input == "" && isNoArgs() {
		return modsError{
			ReasonText: "You haven't provided any prompt input.",
			Err: newUserErrorf(
				"You can give your prompt as arguments and/or pipe it from STDIN.\nExample: %s",
				StdoutStyles().InlineCode.Render("mods [PROMPT...]"),
			),
		}
	}

	// raw mode already prints the output, no need to print it again
	if IsOutputTTY() && !config.Raw {
		switch {
		case mods.RenderedOutput() != "":
			_, _ = lipgloss.Fprint(os.Stdout, mods.RenderedOutput())
		case mods.Output != "":
			_, _ = lipgloss.Fprint(os.Stdout, mods.Output)
		}
	}
	printInteractionStats(mods)

	if resultConfig(mods).SessionWriteToID != "" {
		return saveSession(mods)
	}

	return nil
}

// runDirsAction prints either a single requested path (config | sessions) or
// both with the same indentation as before. Extracted from RunE so the
// args inspection is no longer interleaved with the prompt path.
func runDirsAction(args []string) error {
	if len(args) > 0 {
		switch args[0] {
		case "config":
			fmt.Println(filepath.Dir(config.SettingsPath))
			return nil
		case "sessions":
			fmt.Println(config.SessionDir)
			return nil
		default:
			return modsError{
				Err:        newUserErrorf("unknown directory %q; use config or sessions", args[0]),
				ReasonText: "Could not print requested directory.",
			}
		}
	}
	const configurationLabel = "Configuration:"
	fmt.Printf("%s %s\n", configurationLabel, filepath.Dir(config.SettingsPath))
	fmt.Printf("%*s %s\n", len(configurationLabel), "Sessions:", config.SessionDir)
	return nil
}

// runSettingsEditorAction shells out to the user's $EDITOR to edit the
// settings file, then optionally reports the path that was written.
func runSettingsEditorAction() error {
	c, err := editor.Cmd("mods", config.SettingsPath)
	if err != nil {
		return modsError{
			Err:        err,
			ReasonText: "Could not edit your settings file.",
		}
	}
	c.Stdin = os.Stdin
	c.Stdout = os.Stdout
	c.Stderr = os.Stderr
	if err := c.Run(); err != nil {
		return modsError{
			Err: err,
			ReasonText: fmt.Sprintf(
				"Missing %s.",
				StderrStyles().InlineCode.Render("$EDITOR"),
			),
		}
	}

	fmt.Fprintln(os.Stderr, "Wrote config file to:", config.SettingsPath)
	return nil
}

func runSettingsImportAction() error {
	if err := MergeSettingsYAML(config.SettingsPath, []byte(config.SettingsYAML)); err != nil {
		return modsError{
			Err:        err,
			ReasonText: "Could not import settings into your configuration file.",
		}
	}

	fmt.Fprintln(os.Stderr, "Wrote config file to:", config.SettingsPath)
	return nil
}
