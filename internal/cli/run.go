package cli

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/huh"
	"github.com/charmbracelet/x/editor"
)

// buildTeaProgramOptions assembles the Bubble Tea options for the current
// invocation. Splitting this out of RunE keeps the entry point focused on
// orchestration; the tty/raw decision tree was previously interleaved with
// flag-validation code.
func buildTeaProgramOptions() []tea.ProgramOption {
	opts := []tea.ProgramOption{}
	if !IsInputTTY() || config.Raw {
		opts = append(opts, tea.WithInput(nil))
	}
	if IsOutputTTY() && !config.Raw {
		opts = append(opts, tea.WithOutput(os.Stderr))
	} else {
		opts = append(opts, tea.WithoutRenderer())
	}
	return opts
}

// gatherInteractivePrompt covers the two interactive prompts that RunE may
// trigger when the user has not supplied any prompt arguments: the
// $EDITOR-driven prefix capture and the askInfo TUI for picking an API
// and model. Both paths only fire on a TTY.
func gatherInteractivePrompt() error {
	if isNoArgs() && IsInputTTY() && config.OpenEditor {
		prompt, err := prefixFromEditor()
		if err != nil {
			return err
		}
		config.Prefix = prompt
	}

	if (isNoArgs() || config.AskModel) && IsInputTTY() {
		if err := askInfo(); err != nil && err == huh.ErrUserAborted {
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

// dispatchOneShotActions executes the post-runOneTurn action chain: dirs,
// settings, reset, list-roles, list, mcp-list, delete, etc., and finally
// prints rendered/raw output for the regular prompt path. The chain is
// preserved in the same order as the previous inline switch so behaviour
// is unchanged; only the structure is now navigable per-action.
//
// Returning early from a matching action keeps the original semantics
// where a session-action flag short-circuits prompt output rendering.
func dispatchOneShotActions(ctx context.Context, args []string, mods *Mods) error {
	if config.Dirs {
		return runDirsAction(args)
	}

	if config.Settings {
		return runSettingsEditorAction()
	}

	if config.ResetSettings {
		return resetSettings()
	}

	if mods.Input == "" && isNoArgs() {
		return modsError{
			ReasonText: "You haven't provided any prompt input.",
			Err: newUserErrorf(
				"You can give your prompt as arguments and/or pipe it from STDIN.\nExample: %s",
				StdoutStyles().InlineCode.Render("mods [PROMPT...]"),
			),
		}
	}

	if config.ListRoles {
		listRoles()
		return nil
	}
	if config.ListPrompts {
		listPrompts()
		return nil
	}
	if config.List {
		return listSessions(config.Raw)
	}

	if config.MCPList {
		List(&config)
		return nil
	}

	if config.MCPListTools {
		ctx, cancel := context.WithTimeout(ctx, config.MCPTimeout)
		defer cancel()
		return listAllTools(ctx, &config)
	}

	// raw mode already prints the output, no need to print it again
	if IsOutputTTY() && !config.Raw {
		switch {
		case mods.RenderedOutput() != "":
			fmt.Print(mods.RenderedOutput())
		case mods.Output != "":
			fmt.Print(mods.Output)
		}
	}

	if config.SessionWriteToID != "" {
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
	fmt.Printf("Configuration: %s\n", filepath.Dir(config.SettingsPath))
	//nolint:mnd
	fmt.Printf("%*sSessions: %s\n", 8, " ", config.SessionDir)
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
	HideCommandWindow(c)
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

	if !config.Quiet {
		fmt.Fprintln(os.Stderr, "Wrote config file to:", config.SettingsPath)
	}
	return nil
}
