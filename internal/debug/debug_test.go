package debug

import (
	"bytes"
	"fmt"
	"strings"
	"sync"
	"testing"
	"unicode/utf8"

	"github.com/stretchr/testify/require"
)

func captureDebug(t *testing.T) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	restore := SetOutputForTest(&buf)
	SetEnabled(true)
	t.Cleanup(func() {
		SetEnabled(false)
		restore()
	})
	return &buf
}

func TestPrintSectionLayoutAndPrettyJSON(t *testing.T) {
	buf := captureDebug(t)
	Print(Section{
		Title:  "tool · turn 1 · round 1 · call 1/1",
		Fields: []Field{{Label: "call", Value: "→ fs_read [call_1]"}, {Label: "status", Value: "success · 18ms"}},
		Blocks: []Block{Arguments([]byte(`{"path":"README.md"}`)), Result("")},
	})

	output := buf.String()
	require.NotContains(t, output, "\x1b[")
	require.Contains(t, output, "DEBUG tool · turn 1 · round 1 · call 1/1")
	require.Contains(t, output, "call    → fs_read [call_1]")
	require.Contains(t, output, "arguments · 20 B\n    {\n      \"path\": \"README.md\"")
	require.Contains(t, output, "result · 0 B\n    <empty>")
}

func TestPayloadTruncationPreservesUTF8HeadAndTail(t *testing.T) {
	value := strings.Repeat("你", 7000) + "TAIL"
	block := Result(value)
	require.True(t, utf8.ValidString(block.Value))
	require.Contains(t, block.Meta, "truncated")
	require.Contains(t, block.Value, "bytes omitted")
	require.True(t, strings.HasSuffix(block.Value, "TAIL"))
	require.LessOrEqual(t, len(block.Value), resultLimit+100)
}

func TestPrintIsAtomicAcrossConcurrentSections(t *testing.T) {
	buf := captureDebug(t)
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		i := i
		wg.Add(1)
		go func() {
			defer wg.Done()
			marker := fmt.Sprintf("marker-%02d", i)
			Print(Section{Title: marker, Blocks: []Block{{Label: "payload", Value: marker + "\n" + marker}}})
		}()
	}
	wg.Wait()

	output := buf.String()
	for i := 0; i < 20; i++ {
		marker := fmt.Sprintf("marker-%02d", i)
		expected := "DEBUG " + marker + "\n  payload\n    " + marker + "\n    " + marker
		require.Contains(t, output, expected)
	}
}

func TestDisabledDebugWritesNothing(t *testing.T) {
	var buf bytes.Buffer
	restore := SetOutputForTest(&buf)
	t.Cleanup(restore)
	SetEnabled(false)
	Print(Section{Title: "hidden"})
	require.Empty(t, buf.String())
}

func TestManagedSinkReceivesOneCursorFreeSection(t *testing.T) {
	buf := captureDebug(t)
	var sections []string
	restoreSink := SetManagedSink(func(section string) {
		sections = append(sections, section)
	})
	t.Cleanup(restoreSink)

	Print(Section{
		Title:  "tool · call",
		Fields: []Field{{Label: "status", Value: "success"}},
		Blocks: []Block{{Label: "result", Value: "line one\nline two"}},
	})

	require.Empty(t, buf.String())
	require.Equal(t, []string{
		"DEBUG tool · call\n  status  success\n  result\n    line one\n    line two",
	}, sections)
	require.NotContains(t, sections[0], "\r")
}

// A relayed section taller than the terminal would make Bubble Tea's
// insertAbove pre-scroll a screenful of blank rows into scrollback, so the
// managed sink receives a clamped head/tail view. Direct (non-managed) output
// keeps the full payload.
func TestManagedSinkClampsOversizedSections(t *testing.T) {
	buf := captureDebug(t)
	lines := make([]string, 80)
	for i := range lines {
		lines[i] = fmt.Sprintf("line-%02d", i)
	}
	payload := strings.Join(lines, "\n")
	var sections []string
	restoreSink := SetManagedSink(func(section string) {
		sections = append(sections, section)
	})

	Print(Section{Title: "tool · call", Blocks: []Block{{Label: "result", Value: payload}}})
	restoreSink()

	require.Empty(t, buf.String())
	require.Len(t, sections, 1)
	relayed := strings.Split(sections[0], "\n")
	require.Len(t, relayed, managedSectionHead+managedSectionTail+1)
	require.Contains(t, sections[0], "lines omitted")
	require.Contains(t, sections[0], "line-00")
	require.Contains(t, sections[0], "line-79")
	require.NotContains(t, sections[0], "line-40")

	Print(Section{Title: "tool · call", Blocks: []Block{{Label: "result", Value: payload}}})
	require.Contains(t, buf.String(), "line-40")
}
