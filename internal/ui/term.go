package ui

import (
	"os"
	"strconv"
	"strings"
	"sync"

	"github.com/mattn/go-isatty"
	"github.com/muesli/termenv"
)

var IsInputTTY = sync.OnceValue(func() bool {
	return isatty.IsTerminal(os.Stdin.Fd())
})

var IsOutputTTY = sync.OnceValue(func() bool {
	return isatty.IsTerminal(os.Stdout.Fd())
})

var IsErrorTTY = sync.OnceValue(func() bool {
	return isatty.IsTerminal(os.Stderr.Fd())
})

var StdoutStyles = sync.OnceValue(func() Styles {
	return MakeStyles(staticBackgroundIsDark(os.Getenv("COLORFGBG")))
})

var StderrIsDark = sync.OnceValue(func() bool {
	return staticBackgroundIsDark(os.Getenv("COLORFGBG"))
})

var StderrStyles = sync.OnceValue(func() Styles {
	return MakeStyles(StderrIsDark())
})

// staticBackgroundIsDark infers the background without terminal I/O. Static
// CLI output must never issue OSC queries: unlike a long-running interactive
// TUI, a short-lived command can exit before the terminal reply is consumed,
// leaving the shell to echo it as visible escape text.
func staticBackgroundIsDark(colorFGBG string) bool {
	parts := strings.Split(colorFGBG, ";")
	value := strings.TrimSpace(parts[len(parts)-1])
	index, err := strconv.Atoi(value)
	if err != nil || index < 0 || index > 255 {
		return true
	}

	var color termenv.Color = termenv.ANSI256Color(index)
	if index < 16 {
		color = termenv.ANSIColor(index)
	}
	_, _, lightness := termenv.ConvertToRGB(color).Hsl()
	return lightness < 0.5
}
