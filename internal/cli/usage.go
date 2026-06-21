package cli

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/muesli/termenv"
	"github.com/panjie/mods/internal/ui"
	"github.com/spf13/cobra"
	flag "github.com/spf13/pflag"
)

const (
	helpIntroSummary = "Mods is an AI command-line assistant for prompts, pipelines, and local work."
	helpIntroDetail  = "With built-in tools enabled, it can inspect and edit files, run shell commands, search the web, and keep conversations, with review controls for risky actions."
)

func useLine() string {
	appName := filepath.Base(os.Args[0])

	if ui.StdoutRenderer().ColorProfile() == termenv.TrueColor {
		appName = ui.MakeGradientText(ui.StdoutStyles().AppName, appName)
	}

	return fmt.Sprintf(
		"%s %s",
		appName,
		ui.StdoutStyles().CliArgs.Render("[OPTIONS] [PROMPT...]"),
	)
}

func usageFunc(cmd *cobra.Command) error {
	fmt.Printf("%s\n%s\n\n", helpIntroSummary, helpIntroDetail)
	fmt.Printf(
		"Usage:\n  %s\n\n",
		useLine(),
	)
	fmt.Println("Options:")
	showAll := config.HelpAll
	if showAll {
		printGroupedFlags(cmd)
	} else {
		printFlatFlags(cmd)
	}
	if !showAll {
		fmt.Printf(
			"\nUse %s to show advanced and configuration-first options.\n",
			ui.StdoutStyles().InlineCode.Render("--help-all"),
		)
	}
	if cmd.HasExample() {
		fmt.Printf(
			"\nExample:\n  %s\n  %s\n",
			ui.StdoutStyles().Comment.Render("# "+cmd.Example),
			cheapHighlighting(ui.StdoutStyles(), examples[cmd.Example]),
		)
	}

	return nil
}

func printFlatFlags(cmd *cobra.Command) {
	cmd.Flags().VisitAll(func(f *flag.Flag) {
		if !flagVisibleInUsage(f, false) {
			return
		}
		printFlag(f)
	})
}

func printGroupedFlags(cmd *cobra.Command) {
	groups := groupedUsageFlags(cmd.Flags(), true)
	first := true
	for _, category := range flagCategoryOrder {
		flags := groups[category]
		if len(flags) == 0 {
			continue
		}
		if !first {
			fmt.Println()
		}
		first = false
		fmt.Println(ui.StdoutStyles().Flag.Render(category))
		for _, f := range flags {
			printFlag(f)
		}
	}
}

func printFlag(f *flag.Flag) {
	if f.Shorthand == "" {
		fmt.Printf(
			"  %-44s %s\n",
			ui.StdoutStyles().Flag.Render("--"+f.Name),
			ui.StdoutStyles().FlagDesc.Render(f.Usage),
		)
		return
	}
	fmt.Printf(
		"  %s%s %-40s %s\n",
		ui.StdoutStyles().Flag.Render("-"+f.Shorthand),
		ui.StdoutStyles().FlagComma,
		ui.StdoutStyles().Flag.Render("--"+f.Name),
		ui.StdoutStyles().FlagDesc.Render(f.Usage),
	)
}
