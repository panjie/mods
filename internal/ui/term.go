package ui

import (
	"os"
	"sync"

	"github.com/charmbracelet/lipgloss"
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

var StdoutRenderer = sync.OnceValue(func() *lipgloss.Renderer {
	return lipgloss.DefaultRenderer()
})

var StdoutStyles = sync.OnceValue(func() Styles {
	return MakeStyles(StdoutRenderer())
})

var StderrRenderer = sync.OnceValue(func() *lipgloss.Renderer {
	return lipgloss.NewRenderer(os.Stderr, termenv.WithColorCache(true))
})

var StderrStyles = sync.OnceValue(func() Styles {
	return MakeStyles(StderrRenderer())
})
