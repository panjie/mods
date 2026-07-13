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
	helpIntroDetail  = "With built-in tools enabled, it can inspect and edit files, run shell commands, search the web, and keep sessions, with review controls for risky actions."
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
	printGroupedFlags(cmd)
	if cmd.HasExample() {
		fmt.Printf(
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
	description := ui.StdoutStyles().FlagDesc.Render(f.Usage)
	if flagIsAdvanced(f) {
		description += " " + ui.StdoutStyles().Comment.Render("[advanced]")
	}
	if f.Shorthand == "" {
		fmt.Printf(
			"  %-44s %s\n",
			ui.StdoutStyles().Flag.Render("--"+f.Name),
			description,
		)
		return
	}
	fmt.Printf(
		"  %s%s %-40s %s\n",
		ui.StdoutStyles().Flag.Render("-"+f.Shorthand),
		ui.StdoutStyles().FlagComma,
		ui.StdoutStyles().Flag.Render("--"+f.Name),
		description,
	)
}
