package cli

import (
	"fmt"
	"os"
	"path/filepath"

	"charm.land/lipgloss/v2"
	"github.com/panjie/mods/internal/ui"
	"github.com/spf13/cobra"
	flag "github.com/spf13/pflag"
)

const (
	helpIntroSummary = "Mods is an AI command-line assistant for prompts, pipelines, and local work."
	helpIntroDetail  = "With built-in tools enabled, it can inspect and edit files, run shell commands, search the web, and keep sessions, with review controls for risky actions."
)

func useLine() string {
	appName := filepath.Base(os.Args[0])

	appName = ui.MakeGradientText(ui.StdoutStyles().AppName, appName)

	return fmt.Sprintf(
		"%s %s",
		appName,
		ui.StdoutStyles().CliArgs.Render("[OPTIONS] [PROMPT...]"),
	)
}

func usageFunc(cmd *cobra.Command) error {
	_, _ = lipgloss.Fprintf(os.Stdout, "%s\n%s\n\n", helpIntroSummary, helpIntroDetail)
	_, _ = lipgloss.Fprintf(os.Stdout,
		"Usage:\n  %s\n\n",
		useLine(),
	)
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
	first := true
	categoryOrder := make([]string, 0, len(flagCategorySpecs)+1)
	for _, category := range flagCategorySpecs {
		categoryOrder = append(categoryOrder, category.Name)
	}
	categoryOrder = append(categoryOrder, flagCategoryOther)
	for _, category := range categoryOrder {
		flags := groups[category]
		if len(flags) == 0 {
			continue
		}
		if !first {
			_, _ = lipgloss.Fprintln(os.Stdout)
		}
		first = false
		_, _ = lipgloss.Fprintln(os.Stdout, ui.StdoutStyles().Flag.Render(category))
		for _, f := range flags {
			printFlag(f)
		}
	}
}

func printFlag(f *flag.Flag) {
	description := ui.StdoutStyles().FlagDesc.Render(f.Usage)
	if flagIsAdvanced(f) {
		description += " " + ui.StdoutStyles().Comment.Render("[advanced]")
	}
	if f.Shorthand == "" {
		_, _ = lipgloss.Fprintf(os.Stdout,
			"  %-44s %s\n",
			ui.StdoutStyles().Flag.Render("--"+f.Name),
			description,
		)
		return
	}
	_, _ = lipgloss.Fprintf(os.Stdout,
		"  %s%s %-40s %s\n",
		ui.StdoutStyles().Flag.Render("-"+f.Shorthand),
		ui.StdoutStyles().FlagComma,
		ui.StdoutStyles().Flag.Render("--"+f.Name),
		description,
	)
}
