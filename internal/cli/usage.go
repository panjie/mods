package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/panjie/mods/internal/ui"
	"github.com/spf13/cobra"
	flag "github.com/spf13/pflag"
	"golang.org/x/term"
)

const (
	helpIntroSummary = "Mods is an AI command-line assistant for prompts, pipelines, and local work."
	helpIntroDetail  = "With built-in tools enabled, it can inspect and edit files, run shell commands, and search the web, with review controls for writes. Conversations can be saved and resumed."
)

func useLine() string {
	appName := filepath.Base(os.Args[0])

	appName = ui.StdoutStyles().AppName.Render(appName)

	return fmt.Sprintf(
		"%s %s",
		appName,
		ui.StdoutStyles().CliArgs.Render("[OPTIONS] [PROMPT...]"),
	)
}

func usageFunc(cmd *cobra.Command) error {
	_, _ = lipgloss.Fprintf(os.Stdout, "%s\n%s\n\n", ansi.Wrap(helpIntroSummary, helpWidth(), ""), ansi.Wrap(helpIntroDetail, helpWidth(), ""))
	_, _ = lipgloss.Fprintf(os.Stdout,
		"Usage:\n  %s\n\n",
		useLine(),
	)
	_, _ = lipgloss.Fprintln(os.Stdout, ansi.Wrap("Quick start:\n  mods --config          Configure a provider\n  mods --chat            Start a conversation\n  mods \"Explain this code\"\n  cat main.go | mods \"Review this file\"\n  mods --list-sessions   Browse saved sessions\n  mods --image photo.jpg \"Describe this image\"\n", helpWidth(), ""))
	_, _ = lipgloss.Fprintln(os.Stdout, "Options:")
	printGroupedFlags(cmd)
	if cmd.HasExample() {
		_, _ = lipgloss.Fprintf(os.Stdout,
			"\nExample:\n  %s\n  %s\n",
			ui.StdoutStyles().Comment.Render("# "+cmd.Example),
			cheapHighlighting(ui.StdoutStyles(), examples[cmd.Example]),
		)
	}

	return nil
}

func printGroupedFlags(cmd *cobra.Command) {
	groups := groupedUsageFlags(cmd.Flags())
	categories := make([]string, 0, len(flagCategorySpecs)+1)
	for _, category := range flagCategorySpecs {
		categories = append(categories, category.Name)
	}
	categories = append(categories, flagCategoryOther)
	for _, category := range categories {
		if len(groups[category]) == 0 {
			continue
		}
		_, _ = lipgloss.Fprintln(os.Stdout, "\n"+ui.StdoutStyles().AppName.Render(category))
		for _, f := range groups[category] {
			printFlag(f)
		}
	}
}

var helpWidth = func() int {
	width, _, err := term.GetSize(int(os.Stdout.Fd()))
	if err != nil || width < 1 {
		return 96
	}
	return width
}

func printFlag(f *flag.Flag) {
	_, _ = lipgloss.Fprintln(os.Stdout, renderFlag(f, helpWidth()))
}

func renderFlag(f *flag.Flag, width int) string {
	styles := ui.StdoutStyles()
	name := "--" + f.Name + flagValueSuffix(f)
	if f.Shorthand != "" {
		name = "-" + f.Shorthand + ", " + name
	}
	description := f.Usage
	width = max(8, width)
	labelWidth := 28
	if width < 70 || ansi.StringWidth(name) > labelWidth {
		return ansi.Wrap("  "+styles.Flag.Render(name), width, "") + "\n" +
			indentHelp(ansi.Wrap(styles.FlagDesc.Render(description), max(1, width-4), ""), 4)
	}
	return "  " + styles.Flag.Render(name) + strings.Repeat(" ", labelWidth-ansi.StringWidth(name)+2) +
		strings.ReplaceAll(ansi.Wrap(styles.FlagDesc.Render(description), max(1, width-labelWidth-4), ""), "\n", "\n"+strings.Repeat(" ", labelWidth+4))
}

func indentHelp(text string, spaces int) string {
	indent := strings.Repeat(" ", spaces)
	return indent + strings.ReplaceAll(text, "\n", "\n"+indent)
}

// flagValueSuffix describes required and optional values without exposing
// internal defaults such as the settings editor sentinel.
func flagValueSuffix(f *flag.Flag) string {
	if f.Name == "web-search" {
		return "[=BOOL]"
	}
	if f.Value.Type() == "bool" {
		return ""
	}
	value := strings.ToUpper(f.Value.Type())
	for _, category := range flagCategorySpecs {
		for _, spec := range category.Flags {
			if spec.Name == f.Name && spec.ValueName != "" {
				value = spec.ValueName
			}
		}
	}
	if f.Name == "skills-dirs" {
		return " [DIR]"
	}
	if f.NoOptDefVal != "" {
		return "[=" + value + "]"
	}
	return " " + value
}
