package app

import (
	"strings"
	"sync"
	"testing"

	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/glamour"
	"github.com/charmbracelet/lipgloss"
	"github.com/panjie/mods/internal/proto"
	"github.com/stretchr/testify/require"
)

func newRenderTestMods(t *testing.T, thought string) *Mods {
	t.Helper()
	m := &Mods{
		Config:       &Config{},
		Styles:       makeStyles(lipgloss.NewRenderer(nil)),
		state:        responseState,
		Thought:      thought,
		contentMutex: &sync.Mutex{},
		width:        40,
	}
	return m
}

func TestThoughtMarkdown(t *testing.T) {
	t.Run("renders a labelled blockquote and a horizontal rule", func(t *testing.T) {
		got := thoughtMarkdown("The user is asking about X.\nI should consider Y.")
		require.Contains(t, got, "> **💭 thinking**", "should include the labelled blockquote header")
		require.Contains(t, got, "> The user is asking about X.", "first body line should be quoted")
		require.Contains(t, got, "> I should consider Y.", "second body line should be quoted")
		require.Contains(t, got, "\n---\n", "should include a horizontal rule separating thought from answer")
	})

	t.Run("blank lines become bare quote markers", func(t *testing.T) {
		got := thoughtMarkdown("line one\n\nline three")
		require.Contains(t, got, ">\n", "blank thought lines should be rendered as bare > markers")
		require.Contains(t, got, "> line one")
		require.Contains(t, got, "> line three")
	})
}

func TestThoughtDisplayMarkdown(t *testing.T) {
	t.Run("renders a quiet blockquote without the raw thinking marker", func(t *testing.T) {
		got := thoughtDisplayMarkdown("The user is asking about X.\nI should consider Y.")
		require.Contains(t, got, "> _thinking_", "should include the quieter display header")
		require.Contains(t, got, "> The user is asking about X.")
		require.Contains(t, got, "> I should consider Y.")
		require.NotContains(t, got, "💭")
		require.NotContains(t, got, "**")
		require.NotContains(t, got, "\n---\n")
	})

	t.Run("blank lines become bare quote markers", func(t *testing.T) {
		got := thoughtDisplayMarkdown("line one\n\nline three")
		require.Contains(t, got, ">\n", "blank thought lines should be rendered as bare > markers")
		require.Contains(t, got, "> line one")
		require.Contains(t, got, "> line three")
		require.NotContains(t, got, "\n---\n")
	})
}

func TestFlushThought(t *testing.T) {
	t.Run("non-empty thought is appended to output as a blockquote", func(t *testing.T) {
		oldIsOutputTTY := isOutputTTY
		isOutputTTY = func() bool { return false }
		defer func() { isOutputTTY = oldIsOutputTTY }()

		m := newRenderTestMods(t, "deep thought\nsecond line")
		m.Config.Raw = true

		m.flushThought()

		require.True(t, m.thoughtFlushed)
		require.Contains(t, m.Output, "> **💭 thinking**")
		require.Contains(t, m.Output, "> deep thought")
		require.Contains(t, m.Output, "---")
	})

	t.Run("empty thought flushes nothing but marks flushed", func(t *testing.T) {
		oldIsOutputTTY := isOutputTTY
		isOutputTTY = func() bool { return false }
		defer func() { isOutputTTY = oldIsOutputTTY }()

		m := newRenderTestMods(t, "   \n\t ")
		m.Config.Raw = true

		m.flushThought()

		require.True(t, m.thoughtFlushed, "thoughtFlushed should be set so we do not retry")
		require.Empty(t, m.Output, "no blockquote should be emitted for an empty thought")
	})

	t.Run("thought is trimmed before rendering", func(t *testing.T) {
		oldIsOutputTTY := isOutputTTY
		isOutputTTY = func() bool { return false }
		defer func() { isOutputTTY = oldIsOutputTTY }()

		m := newRenderTestMods(t, "\n\n  actual thought  \n\n")
		m.Config.Raw = true

		m.flushThought()

		require.Contains(t, m.Output, "> actual thought")
	})

	t.Run("tty display uses quieter thinking markdown while raw output stays stable", func(t *testing.T) {
		oldIsOutputTTY := isOutputTTY
		oldExportedIsOutputTTY := IsOutputTTY
		isOutputTTY = func() bool { return true }
		IsOutputTTY = func() bool { return true }
		defer func() {
			isOutputTTY = oldIsOutputTTY
			IsOutputTTY = oldExportedIsOutputTTY
		}()

		r := lipgloss.NewRenderer(nil)
		gr, err := glamour.NewTermRenderer(glamour.WithStandardStyle("dark"))
		require.NoError(t, err)

		m := newRenderTestMods(t, "deep thought\nsecond line")
		m.renderer = r
		m.glam = gr
		m.glamViewport = viewport.New(80, 20)
		m.width = 80

		m.flushThought()

		require.True(t, m.thoughtFlushed)
		require.Contains(t, m.Output, "> **💭 thinking**")
		require.Contains(t, m.Output, "\n---\n")
		require.Contains(t, m.displayOutput, "> _thinking_")
		require.NotContains(t, m.displayOutput, "💭")
		require.NotContains(t, m.displayOutput, "**")
		require.NotContains(t, m.displayOutput, "\n---\n")
		require.Contains(t, m.glamOutput, "thinking")
		require.NotContains(t, m.glamOutput, "💭")
	})
}

func TestCompletionOutputThoughtField(t *testing.T) {
	// Make sure the Update loop accumulates chunk.Thought into m.Thought and
	// flushes it on the first content chunk.
	oldIsOutputTTY := isOutputTTY
	isOutputTTY = func() bool { return false }
	defer func() { isOutputTTY = oldIsOutputTTY }()

	m := &Mods{
		Config:              &Config{},
		Styles:              makeStyles(lipgloss.NewRenderer(nil)),
		state:               requestState,
		contentMutex:        &sync.Mutex{},
		reasoningActive:     true,
		showOperationStatus: true,
		reviewer:            &toolReviewer{},
	}
	m.Config.Raw = true

	// A thought-only chunk should accumulate but not flush.
	_, _ = m.Update(streamEventMsg{
		kind:   streamEventChunk,
		chunk:  proto.Chunk{Thought: "step one\nstep two"},
		runner: newStreamRunner(staticStream{}, nil, func(err error) tea.Msg { return nil }),
	})
	require.Equal(t, "step one\nstep two", m.Thought)
	require.False(t, m.thoughtFlushed, "thought should not be flushed by a thought-only chunk")

	// The first content chunk should flush the thought ahead of the answer.
	_, _ = m.Update(streamEventMsg{
		kind:   streamEventChunk,
		chunk:  proto.Chunk{Content: "the answer"},
		runner: newStreamRunner(staticStream{}, nil, func(err error) tea.Msg { return nil }),
	})
	require.True(t, m.thoughtFlushed, "first content chunk should flush the thought")
	require.Contains(t, m.Output, "> **💭 thinking**")
	require.Contains(t, m.Output, "> step one")
	thoughtIdx := strings.Index(m.Output, "step one")
	answerIdx := strings.Index(m.Output, "the answer")
	require.Less(t, thoughtIdx, answerIdx, "thinking block should appear before the answer")
}

func TestCompletionOutputTrimsLeadingNewlineOfFirstAnswerChunk(t *testing.T) {
	oldIsOutputTTY := isOutputTTY
	isOutputTTY = func() bool { return false }
	defer func() { isOutputTTY = oldIsOutputTTY }()

	m := &Mods{
		Config:              &Config{},
		Styles:              makeStyles(lipgloss.NewRenderer(nil)),
		state:               requestState,
		contentMutex:        &sync.Mutex{},
		reasoningActive:     false,
		showOperationStatus: true,
		reviewer:            &toolReviewer{},
	}
	m.Config.Raw = true

	// A leftover leading newline (from a stripped </think>) must not start
	// the answer with a blank line.
	_, _ = m.Update(streamEventMsg{
		kind:   streamEventChunk,
		chunk:  proto.Chunk{Content: "\n你好"},
		runner: newStreamRunner(staticStream{}, nil, func(err error) tea.Msg { return nil }),
	})
	require.Equal(t, "你好", m.Output)
}

func TestCompletionOutputSeparatesResponsesAfterToolCall(t *testing.T) {
	m := &Mods{
		Config:              &Config{},
		Styles:              makeStyles(lipgloss.NewRenderer(nil)),
		state:               requestState,
		contentMutex:        &sync.Mutex{},
		showOperationStatus: true,
		reviewer:            &toolReviewer{},
	}
	m.Config.Raw = true
	errh := func(error) tea.Msg { return nil }

	_, _ = m.Update(streamEventMsg{
		kind:   streamEventChunk,
		chunk:  proto.Chunk{Content: "I'll check what's available and install GitHub CLI."},
		runner: newStreamRunner(staticStream{}, nil, errh),
	})
	_, _ = m.Update(streamEventMsg{
		kind:    streamEventToolCalls,
		results: []proto.ToolCallStatus{{Name: "shell_run"}},
		runner:  newStreamRunner(staticStream{}, nil, errh),
	})
	require.True(t, m.responseBoundaryPending)

	_, _ = m.Update(streamEventMsg{
		kind:   streamEventChunk,
		chunk:  proto.Chunk{Content: "\nThe installation seems to have succeeded."},
		runner: newStreamRunner(staticStream{}, nil, errh),
	})

	require.Equal(t, "I'll check what's available and install GitHub CLI.\n\nThe installation seems to have succeeded.", m.Output)
	require.False(t, m.responseBoundaryPending)
}
