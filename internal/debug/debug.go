package debug

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"unicode/utf8"

	"github.com/panjie/mods/internal/textutil"
	"github.com/panjie/mods/internal/ui"
)

const (
	argumentLimit = 4 * 1024
	argumentTail  = 1 * 1024
	resultLimit   = 16 * 1024
	resultTail    = 4 * 1024
)

var (
	debugEnabled atomic.Bool
	outputMu     sync.Mutex
	output       io.Writer = os.Stderr
	plainOutput  bool
	managedSink  func(string)
)

// Field is one aligned key/value row in a debug section.
type Field struct {
	Label string
	Value string
}

// Block is a labelled, indented multi-line payload in a debug section.
type Block struct {
	Label string
	Meta  string
	Value string
}

// Section is one atomically-written debug event. Title should identify the
// lifecycle scope (startup, turn, model, tool, approval, session, warning).
type Section struct {
	Title  string
	Fields []Field
	Blocks []Block
}

// SetEnabled sets whether debug output is shown.
func SetEnabled(enabled bool) {
	debugEnabled.Store(enabled)
}

func Enabled() bool {
	return debugEnabled.Load()
}

// Print writes a complete section while holding one lock, so concurrent tool
// calls cannot interleave their multi-line payloads.
func Print(section Section) {
	if !debugEnabled.Load() {
		return
	}
	outputMu.Lock()
	defer outputMu.Unlock()

	styled := ui.IsErrorTTY() && !plainOutput
	managed := managedSink != nil
	lines := formatSection(section, styled, managed)
	if managed {
		managedSink(strings.Join(lines, "\n"))
		return
	}
	_, _ = fmt.Fprintln(output, strings.Join(lines, "\n"))
}

func formatSection(section Section, styled, managed bool) []string {
	styles := ui.StderrStyles()
	header := "DEBUG"
	title := section.Title
	if styled {
		header = styles.DebugHeader.String()
		title = styles.DebugDetails.Render(title)
	}
	headerPrefix := ""
	detailPrefix := ""
	if styled {
		headerPrefix = " "
		if !managed {
			headerPrefix = "\r "
			detailPrefix = "\r"
		}
	}
	lines := []string{fmt.Sprintf("%s%s %s", headerPrefix, header, title)}
	width := 0
	for _, field := range section.Fields {
		if len(field.Label) > width {
			width = len(field.Label)
		}
	}
	for _, field := range section.Fields {
		lines = append(lines, formatDetail(fmt.Sprintf("  %-*s  %s", width, field.Label, field.Value), styled, detailPrefix))
	}
	for _, block := range section.Blocks {
		label := block.Label
		if block.Meta != "" {
			label += " · " + block.Meta
		}
		lines = append(lines, formatDetail("  "+label, styled, detailPrefix))
		value := block.Value
		if value == "" {
			value = "<empty>"
		}
		for _, line := range strings.Split(value, "\n") {
			lines = append(lines, formatDetail("    "+line, styled, detailPrefix))
		}
	}
	return lines
}

func formatDetail(value string, styled bool, prefix string) string {
	if styled {
		value = ui.StderrStyles().DebugDetails.Render(value)
	}
	return prefix + value
}

// Arguments formats a tool argument payload. Small JSON documents are
// indented; oversized payloads retain both their beginning and end.
func Arguments(data []byte) Block {
	return payloadBlock("arguments", data, argumentLimit, argumentTail, true)
}

// Result formats a tool result payload with a wider output-oriented limit.
func Result(value string) Block {
	return payloadBlock("result", []byte(value), resultLimit, resultTail, false)
}

func payloadBlock(label string, data []byte, limit, tail int, prettyJSON bool) Block {
	rawLen := len(data)
	value := string(data)
	meta := formatBytes(rawLen)
	if rawLen > limit {
		head := limit - tail
		prefix := validPrefix(data, head)
		suffix := validSuffix(data, tail)
		omitted := rawLen - len(prefix) - len(suffix)
		value = string(prefix) + fmt.Sprintf("\n... (%d bytes omitted; total %d bytes) ...\n", omitted, rawLen) + string(suffix)
		meta += " · truncated"
	} else if prettyJSON && rawLen > 0 {
		var pretty bytes.Buffer
		if json.Indent(&pretty, data, "", "  ") == nil {
			value = pretty.String()
		}
	}
	return Block{Label: label, Meta: meta, Value: value}
}

func validPrefix(data []byte, max int) []byte {
	if len(data) <= max {
		return data
	}
	end := max
	for end > 0 && !utf8.Valid(data[:end]) {
		end--
	}
	return data[:end]
}

func validSuffix(data []byte, max int) []byte {
	if len(data) <= max {
		return data
	}
	start := len(data) - max
	for start < len(data) && !utf8.Valid(data[start:]) {
		start++
	}
	return data[start:]
}

func formatBytes(size int) string {
	if size < 1024 {
		return fmt.Sprintf("%d B", size)
	}
	if size < 1024*1024 {
		return fmt.Sprintf("%.1f KiB", float64(size)/1024)
	}
	return fmt.Sprintf("%.1f MiB", float64(size)/(1024*1024))
}

// Printf remains available for low-frequency diagnostics that do not belong
// to a larger lifecycle event.
func Printf(format string, args ...any) {
	if !debugEnabled.Load() {
		return
	}
	message := fmt.Sprintf(format, args...)
	Print(Section{Title: inferScope(message) + " · " + message})
}

func inferScope(message string) string {
	lower := strings.ToLower(strings.TrimSpace(message))
	switch {
	case strings.HasPrefix(lower, "api"), strings.HasPrefix(lower, "request"), strings.HasPrefix(lower, "think"), strings.HasPrefix(lower, "token"):
		return "model"
	case strings.HasPrefix(lower, "tool"), strings.HasPrefix(lower, "web search"):
		return "tool"
	case strings.HasPrefix(lower, "review"), strings.HasPrefix(lower, "requestapproval"), strings.HasPrefix(lower, "command assessment"), strings.HasPrefix(lower, "assesscommand"):
		return "approval"
	case strings.HasPrefix(lower, "session"), strings.HasPrefix(lower, "stdin"), strings.HasPrefix(lower, "images"):
		return "session"
	case strings.HasPrefix(lower, "skills"), strings.HasPrefix(lower, "prompt"), strings.HasPrefix(lower, "openai protocol"), strings.HasPrefix(lower, "registerfilesystem"):
		return "startup"
	default:
		return "warning"
	}
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
	Print(Section{Title: "diagnostic", Blocks: []Block{payloadBlock(label, b, 2000, 500, false)}})
}

func Truncate(s string, max int) string {
	if len(s) > max {
		return textutil.TruncateUTF8Bytes(s, max) + fmt.Sprintf("... (truncated, total %d bytes)", len(s))
	}
	return s
}

// SetOutputForTest replaces the debug sink and returns a restore function.
// It is intentionally test-oriented; production output remains stderr.
func SetOutputForTest(w io.Writer) func() {
	outputMu.Lock()
	previous := output
	previousPlain := plainOutput
	output = w
	plainOutput = true
	outputMu.Unlock()
	return func() {
		outputMu.Lock()
		output = previous
		plainOutput = previousPlain
		outputMu.Unlock()
	}
}

// SetManagedSink routes each complete debug section through a terminal owner
// such as Bubble Tea. The sink receives one newline-delimited string per
// section without carriage-return cursor control. Call the returned function
// after the terminal owner stops to restore direct stderr output.
func SetManagedSink(sink func(string)) func() {
	outputMu.Lock()
	previous := managedSink
	managedSink = sink
	outputMu.Unlock()
	var once sync.Once
	return func() {
		once.Do(func() {
			outputMu.Lock()
			managedSink = previous
			outputMu.Unlock()
		})
	}
}

type Facade struct{}

var FacadeInstance Facade

func (Facade) SetEnabled(enabled bool)           { SetEnabled(enabled) }
func (Facade) Printf(format string, args ...any) { Printf(format, args...) }
func (Facade) Print(section Section)             { Print(section) }
func (Facade) Arguments(data []byte) Block       { return Arguments(data) }
func (Facade) Result(value string) Block         { return Result(value) }
func (Facade) Enabled() bool                     { return Enabled() }
func (Facade) Truncate(s string, max int) string { return Truncate(s, max) }
func (Facade) SetManagedSink(sink func(string)) func() {
	return SetManagedSink(sink)
}
