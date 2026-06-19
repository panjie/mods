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

func useLine() string {
	appName := filepath.Base(os.Args[0])

	if ui.StdoutRenderer().ColorProfile() == termenv.TrueColor {
		appName = ui.MakeGradientText(ui.StdoutStyles().AppName, appName)
	}

	return fmt.Sprintf(
		"%s %s",
		appName,
		ui.StdoutStyles().CliArgs.Render("[OPTIONS] [PREFIX TERM]"),
	)
}

func usageFunc(cmd *cobra.Command) error {
	fmt.Printf(
		"Usage:\n  %s\n\n",
		useLine(),
	)
	fmt.Println("Options:")
	cmd.Flags().VisitAll(func(f *flag.Flag) {
		if f.Hidden {
			return
		}
		if f.Shorthand == "" {
			fmt.Printf(
				"  %-44s %s\n",
				ui.StdoutStyles().Flag.Render("--"+f.Name),
				ui.StdoutStyles().FlagDesc.Render(f.Usage),
			)
		} else {
			fmt.Printf(
				"  %s%s %-40s %s\n",
				ui.StdoutStyles().Flag.Render("-"+f.Shorthand),
				ui.StdoutStyles().FlagComma,
				ui.StdoutStyles().Flag.Render("--"+f.Name),
				ui.StdoutStyles().FlagDesc.Render(f.Usage),
			)
		}
	})
	if cmd.HasExample() {
		fmt.Printf(
			"\nExample:\n  %s\n  %s\n",
			ui.StdoutStyles().Comment.Render("# "+cmd.Example),
			cheapHighlighting(ui.StdoutStyles(), examples[cmd.Example]),
		)
	}

	return nil
}
