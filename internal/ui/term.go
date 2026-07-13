package ui

import (
	"os"
	"sync"

	"charm.land/lipgloss/v2"
	"github.com/mattn/go-isatty"
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
	isDark := true
	if IsOutputTTY() && IsInputTTY() {
		isDark = lipgloss.HasDarkBackground(os.Stdin, os.Stdout)
	}
	return MakeStyles(isDark)
})

var StderrIsDark = sync.OnceValue(func() bool {
	isDark := true
	if IsErrorTTY() && IsInputTTY() {
		isDark = lipgloss.HasDarkBackground(os.Stdin, os.Stderr)
	}
	return isDark
})

var StderrStyles = sync.OnceValue(func() Styles {
	return MakeStyles(StderrIsDark())
})
