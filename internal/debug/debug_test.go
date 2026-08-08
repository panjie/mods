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
