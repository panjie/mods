package app

import (
	"io"
	"os"
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
		thinkActive:         true,
		showOperationStatus: true,
		reviewer:            &toolReviewer{},
	}
	m.Config.Raw = true

	// A thought-only chunk should accumulate but not flush.
	_, _ = m.Update(streamEventMsg{
		kind:   streamEventChunk,
		chunk:  proto.Chunk{Thought: "step one\nstep two"},
		runner: newStreamRunner(staticStream{}, nil, nil, func(err error) tea.Msg { return nil }),
	})
	require.Equal(t, "step one\nstep two", m.Thought)
	require.False(t, m.thoughtFlushed, "thought should not be flushed by a thought-only chunk")

	// The first content chunk should flush the thought ahead of the answer.
	_, _ = m.Update(streamEventMsg{
		kind:   streamEventChunk,
		chunk:  proto.Chunk{Content: "the answer"},
		runner: newStreamRunner(staticStream{}, nil, nil, func(err error) tea.Msg { return nil }),
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
		thinkActive:         false,
		showOperationStatus: true,
		reviewer:            &toolReviewer{},
	}
	m.Config.Raw = true

	// A leftover leading newline (from a stripped </think>) must not start
	// the answer with a blank line.
	_, _ = m.Update(streamEventMsg{
		kind:   streamEventChunk,
		chunk:  proto.Chunk{Content: "\n你好"},
		runner: newStreamRunner(staticStream{}, nil, nil, func(err error) tea.Msg { return nil }),
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
		runner: newStreamRunner(staticStream{}, nil, nil, errh),
	})
	_, _ = m.Update(streamEventMsg{
		kind:    streamEventToolCalls,
		results: []proto.ToolCallStatus{{Name: "shell_run"}},
		runner:  newStreamRunner(staticStream{}, nil, nil, errh),
	})
	require.True(t, m.responseBoundaryPending)

	_, _ = m.Update(streamEventMsg{
		kind:   streamEventChunk,
		chunk:  proto.Chunk{Content: "\nThe installation seems to have succeeded."},
		runner: newStreamRunner(staticStream{}, nil, nil, errh),
	})

	require.Equal(t, "I'll check what's available and install GitHub CLI.\n\nThe installation seems to have succeeded.", m.Output)
	require.False(t, m.responseBoundaryPending)
}

func TestSessionOutputFlushesForNonTTY(t *testing.T) {
	oldIsOutputTTY := IsOutputTTY
	IsOutputTTY = func() bool { return false }
	t.Cleanup(func() { IsOutputTTY = oldIsOutputTTY })

	db := testDB(t)
	id := newSessionID()
	require.NoError(t, db.SaveSession(
		id,
		"show flush",
		"openai",
		"gpt-4",
		[]proto.Message{{Role: proto.RoleUser, Content: "show me"}},
		nil,
	))

	m := &Mods{
		Config:       &Config{SessionReadFromID: id},
		db:           db,
		contentMutex: &sync.Mutex{},
		reviewer:     &toolReviewer{},
	}
	msg := m.readFromSession()()
	require.IsType(t, streamEventMsg{}, msg)
	_, _ = m.Update(msg)

	output := captureStdout(t, func() { _ = m.View() })
	require.Contains(t, output, "**User**: show me")
}

// TestRenderWithOperationDropsSpinnerDuringPreOutputReview locks in fix ②:
// when a tool approval is pending and the model hasn't emitted any text yet,
// the "Generating…" spinner must not appear above the approval prompt (it's
// misleading — we're waiting on the user, not generating). Once there is model
// output, it's shown above the prompt as context.
func TestRenderWithOperationDropsSpinnerDuringPreOutputReview(t *testing.T) {
	pendingReviewer := &toolReviewer{
		reviewMode:    ReviewAuto,
		reviewPending: true,
		reviewItem: &toolReviewItem{
			name: "shell_run",
			args: []byte(`{"command":"ls"}`),
			resp: make(chan reviewResponse, 1),
		},
	}
	m := &Mods{
		Config:              &Config{},
		Styles:              makeStyles(lipgloss.NewRenderer(nil)),
		state:               responseState,
		contentMutex:        &sync.Mutex{},
		width:               60,
		showOperationStatus: true,
		reviewer:            pendingReviewer,
	}

	t.Run("no model output yet: spinner dropped, review prompt shown", func(t *testing.T) {
		m.responseOutputStarted = false
		got := m.renderWithOperation("✶ Generating...")
		require.NotContains(t, got, "Generating", "spinner must not appear above the approval prompt")
		require.Contains(t, got, "Review:")
	})

	t.Run("model output present: output kept above the review prompt", func(t *testing.T) {
		m.responseOutputStarted = true
		got := m.renderWithOperation("partial answer so far")
		require.Contains(t, got, "partial answer so far")
		require.Contains(t, got, "Review:")
	})
}

func TestRenderWithOperationSuppressesSpinnerDuringToolRun(t *testing.T) {
	m := &Mods{
		Config:              &Config{},
		Styles:              makeStyles(lipgloss.NewRenderer(nil)),
		state:               responseState,
		contentMutex:        &sync.Mutex{},
		width:               60,
		showOperationStatus: true,
		reviewer:            &toolReviewer{},
	}
	m.setActiveOperation("Run: find . -name '*.go'")

	t.Run("no model output yet: spinner dropped, only the tool label shows", func(t *testing.T) {
		m.responseOutputStarted = false
		got := m.renderWithOperation("✶ Generating...")
		require.NotContains(t, got, "Generating", "spinner must not appear while a tool is running")
		require.Contains(t, got, "Run: find")
	})

	t.Run("model output present: output kept above the tool label", func(t *testing.T) {
		m.responseOutputStarted = true
		got := m.renderWithOperation("partial answer so far")
		require.Contains(t, got, "partial answer so far")
		require.Contains(t, got, "Run: find")
	})
}

func captureStdout(tb testing.TB, fn func()) string {
	tb.Helper()

	old := os.Stdout
	r, w, err := os.Pipe()
	require.NoError(tb, err)
	os.Stdout = w
	defer func() { os.Stdout = old }()

	fn()
	require.NoError(tb, w.Close())
	out, err := io.ReadAll(r)
	require.NoError(tb, err)
	return string(out)
}
