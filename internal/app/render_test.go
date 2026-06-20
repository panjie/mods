package app

import (
	"strings"
	"sync"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
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
	_, _ = m.Update(completionOutput{
		thought: "step one\nstep two",
		stream:  staticStream{},
		errh:    func(err error) tea.Msg { return nil },
	})
	require.Equal(t, "step one\nstep two", m.Thought)
	require.False(t, m.thoughtFlushed, "thought should not be flushed by a thought-only chunk")

	// The first content chunk should flush the thought ahead of the answer.
	_, _ = m.Update(completionOutput{
		content: "the answer",
		stream:  staticStream{},
		errh:    func(err error) tea.Msg { return nil },
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
	_, _ = m.Update(completionOutput{
		content: "\n你好",
		stream:  staticStream{},
		errh:    func(err error) tea.Msg { return nil },
	})
	require.Equal(t, "你好", m.Output)
}
