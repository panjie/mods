package main

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"sync/atomic"

	"github.com/charmbracelet/mods/internal/proto"
)

var debugEnabled atomic.Bool

// SetDebugEnabled sets whether debug output is shown.
func SetDebugEnabled(enabled bool) {
	debugEnabled.Store(enabled)
}

func isDebugEnabled() bool {
	return debugEnabled.Load()
}

func debugPrintf(format string, args ...any) {
	if !debugEnabled.Load() {
		return
	}
	header := stderrStyles().DebugHeader.String()
	detail := stderrStyles().DebugDetails.Render(fmt.Sprintf(format, args...))
	fmt.Fprintf(os.Stderr, "\r %s %s\n", header, detail)
}

func debugPrintJSON(label string, v any) {
	if !debugEnabled.Load() {
		return
	}
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		debugPrintf("%s: <marshal error: %v>", label, err)
		return
	}
	output := string(b)
	const maxLen = 2000
	if len(output) > maxLen {
		output = output[:maxLen] + fmt.Sprintf("\n... (truncated, total %d chars)", len(b))
	}
	lines := strings.Split(output, "\n")
	debugPrintf("%s:", label)
	for _, line := range lines {
		if line != "" {
			detail := stderrStyles().DebugDetails.Render("  " + line)
			fmt.Fprintf(os.Stderr, "\r           %s\n", detail)
		}
	}
}

func truncateStr(s string, max int) string {
	if len(s) > max {
		return s[:max] + fmt.Sprintf("... (truncated, total %d chars)", len(s))
	}
	return s
}

func countTools(tools []proto.ToolSpec) int {
	return len(tools)
}
