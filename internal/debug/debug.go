package debug

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"sync/atomic"

	"charm.land/lipgloss/v2"
	"github.com/panjie/mods/internal/textutil"
	"github.com/panjie/mods/internal/ui"
)

var debugEnabled atomic.Bool

// SetDebugEnabled sets whether debug output is shown.
func SetEnabled(enabled bool) {
	debugEnabled.Store(enabled)
}

func Enabled() bool {
	return debugEnabled.Load()
}

func Printf(format string, args ...any) {
	if !debugEnabled.Load() {
		return
	}
	header := ui.StderrStyles().DebugHeader.String()
	detail := ui.StderrStyles().DebugDetails.Render(fmt.Sprintf(format, args...))
	_, _ = lipgloss.Fprintf(os.Stderr, "\r %s %s\n", header, detail)
}

func PrintJSON(label string, v any) {
	if !debugEnabled.Load() {
		return
	}
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		Printf("%s: <marshal error: %v>", label, err)
		return
	}
	output := string(b)
	const maxLen = 2000
	if len(output) > maxLen {
		output = textutil.TruncateUTF8Bytes(output, maxLen) + fmt.Sprintf("\n... (truncated, total %d bytes)", len(b))
	}
	lines := strings.Split(output, "\n")
	Printf("%s:", label)
	for _, line := range lines {
		if line != "" {
			detail := ui.StderrStyles().DebugDetails.Render("  " + line)
			_, _ = lipgloss.Fprintf(os.Stderr, "\r           %s\n", detail)
		}
	}
}

func Truncate(s string, max int) string {
	if len(s) > max {
		return textutil.TruncateUTF8Bytes(s, max) + fmt.Sprintf("... (truncated, total %d bytes)", len(s))
	}
	return s
}

type Facade struct{}

var FacadeInstance Facade

func (Facade) SetEnabled(enabled bool)           { SetEnabled(enabled) }
func (Facade) Printf(format string, args ...any) { Printf(format, args...) }
func (Facade) Enabled() bool                     { return Enabled() }
func (Facade) Truncate(s string, max int) string { return Truncate(s, max) }
