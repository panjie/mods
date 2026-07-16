package app

import (
	"io"
	"os"
	"strings"
	"sync"
	"testing"

	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"
	"charm.land/glamour/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/panjie/mods/internal/proto"
	"github.com/stretchr/testify/require"
)

func newRenderTestMods(t *testing.T, thought string) *Mods {
	t.Helper()
	m := &Mods{
		Config:       &Config{},
		Styles:       makeStyles(true),
		state:        responseState,
		Thought:      thought,
		contentMutex: &sync.Mutex{},
		width:        40,
	}
	return m
}

func TestRenderMarkdownUsesConfiguredRendererWithoutMutatingOutput(t *testing.T) {
	gr, err := glamour.NewTermRenderer(
		glamour.WithStandardStyle("notty"),
		glamour.WithWordWrap(30),
	)
	require.NoError(t, err)
	m := &Mods{
		glam: gr,
		outputRenderer: outputRenderer{
			Output:     "original",
			glamOutput: "rendered original",
		},
	}

	got, err := m.RenderMarkdown("# Skills\n\n- **alpha** — a description with enough words to wrap\n")

	require.NoError(t, err)
	require.Contains(t, got, "Skills")
	require.Contains(t, got, "alpha")
	for _, line := range strings.Split(strings.TrimSpace(got), "\n") {
		require.LessOrEqual(t, lipgloss.Width(line), 30)
	}
	require.Equal(t, "original", m.Output)
	require.Equal(t, "rendered original", m.glamOutput)
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

func TestThoughtDisplayBlock(t *testing.T) {
	t.Run("renders an interaction panel without the raw thinking marker", func(t *testing.T) {
		got := thoughtDisplayBlock(makeStyles(true).Interaction, 60, "The user is asking about X.\nI should consider Y.")
		plain := ansi.Strip(got)

		require.Contains(t, got, "┃")
		require.Contains(t, plain, "THINKING")
		require.Contains(t, plain, "-t")
		require.Contains(t, plain, "The user is asking about X.")
		require.Contains(t, plain, "I should consider Y.")
		require.NotContains(t, plain, "💭")
		require.NotContains(t, plain, "**")
		require.NotContains(t, plain, "---")
	})

	t.Run("blank lines are preserved in the panel body", func(t *testing.T) {
		got := thoughtDisplayBlock(makeStyles(true).Interaction, 60, "line one\n\nline three")
		plain := ansi.Strip(got)

		require.Contains(t, plain, "line one")
		require.Contains(t, plain, "line three")
		require.NotContains(t, plain, "---")
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

	t.Run("tty display uses interaction panel while raw output stays stable", func(t *testing.T) {
		oldIsOutputTTY := isOutputTTY
		oldExportedIsOutputTTY := IsOutputTTY
		isOutputTTY = func() bool { return true }
		IsOutputTTY = func() bool { return true }
		defer func() {
			isOutputTTY = oldIsOutputTTY
			IsOutputTTY = oldExportedIsOutputTTY
		}()

		gr, err := glamour.NewTermRenderer(glamour.WithStandardStyle("dark"))
		require.NoError(t, err)

		m := newRenderTestMods(t, "deep thought\nsecond line")
		m.glam = gr
		m.glamViewport = viewport.New(viewport.WithWidth(80), viewport.WithHeight(20))
		m.width = 80

		m.flushThought()

		require.True(t, m.thoughtFlushed)
		require.Contains(t, m.Output, "> **💭 thinking**")
		require.Contains(t, m.Output, "\n---\n")
		require.NotContains(t, m.displayOutput, "> _thinking_")
		require.Contains(t, m.displayOutput, "MODS_DISPLAY_BLOCK_")
		require.NotContains(t, m.displayOutput, "💭")
		require.NotContains(t, m.displayOutput, "**")
		require.NotContains(t, m.displayOutput, "\n---\n")
		plain := ansi.Strip(m.glamOutput)
		require.Contains(t, m.glamOutput, "┃")
		require.Contains(t, plain, "THINKING")
		require.Contains(t, plain, "deep thought")
		require.NotContains(t, plain, "MODS_DISPLAY_BLOCK_")
		require.NotContains(t, m.glamOutput, "💭")
	})
}

func TestReplaceDisplayBlocks(t *testing.T) {
	m := &Mods{}
	marker := m.nextDisplayBlockMarker("┃ THINKING\n┃ body")

	got := m.replaceDisplayBlocks("before\n" + marker + "\nafter")

	require.Equal(t, "before\n┃ THINKING\n┃ body\nafter", got)
}

func TestCompletionOutputThoughtField(t *testing.T) {
	// Make sure the Update loop accumulates chunk.Thought into m.Thought and
	// flushes it on the first content chunk.
	oldIsOutputTTY := isOutputTTY
	isOutputTTY = func() bool { return false }
	defer func() { isOutputTTY = oldIsOutputTTY }()

	m := &Mods{
		Config:              &Config{},
		Styles:              makeStyles(true),
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
		Styles:              makeStyles(true),
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
		Styles:              makeStyles(true),
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
// the spinner must not appear above the approval prompt (it's
// misleading — we're waiting on the user, not generating). Once there is model
// output, it's shown above the prompt as context.
func TestRenderWithOperationDropsSpinnerDuringPreOutputReview(t *testing.T) {
	oldTTY := IsOutputTTY
	IsOutputTTY = func() bool { return true }
	t.Cleanup(func() { IsOutputTTY = oldTTY })

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
		Styles:              makeStyles(true),
		state:               responseState,
		contentMutex:        &sync.Mutex{},
		width:               60,
		showOperationStatus: true,
		reviewer:            pendingReviewer,
		anim:                staticModel("animating"),
	}

	t.Run("no model output yet: spinner paused, review prompt shown", func(t *testing.T) {
		m.responseOutputStarted = false
		got := m.renderWithOperation("")
		require.NotContains(t, got, "animating", "spinner must not appear above the approval prompt")
		require.Contains(t, got, "REVIEW REQUIRED")
	})

	t.Run("model output present: output kept above the review prompt", func(t *testing.T) {
		m.responseOutputStarted = true
		got := m.renderWithOperation("partial answer so far")
		require.Contains(t, got, "partial answer so far")
		require.Contains(t, got, "REVIEW REQUIRED")
		require.NotContains(t, got, "animating", "spinner stays paused while approval is pending")
	})
}

func TestRenderWithOperationShowsSpinnerAndToolLabel(t *testing.T) {
	oldTTY := IsOutputTTY
	IsOutputTTY = func() bool { return true }
	t.Cleanup(func() { IsOutputTTY = oldTTY })

	m := &Mods{
		Config:              &Config{},
		Styles:              makeStyles(true),
		state:               responseState,
		contentMutex:        &sync.Mutex{},
		width:               60,
		showOperationStatus: true,
		reviewer:            &toolReviewer{},
		anim:                staticModel("animating"),
	}
	m.setActiveOperation("Run: find . -name '*.go'")

	t.Run("no model output yet: spinner and tool label both shown", func(t *testing.T) {
		m.responseOutputStarted = false
		got := m.renderWithOperation("")
		require.Contains(t, got, "animating", "spinner stays on while a tool is running")
		require.Contains(t, got, "Run: find")
	})

	t.Run("model output present: output kept above spinner and tool label", func(t *testing.T) {
		m.responseOutputStarted = true
		got := m.renderWithOperation("partial answer so far")
		require.Contains(t, got, "partial answer so far")
		require.Contains(t, got, "animating")
		require.Contains(t, got, "Run: find")
	})

	t.Run("hide-tool-status hides the label but keeps the spinner", func(t *testing.T) {
		m.Config.HideToolStatus = true
		m.responseOutputStarted = false
		got := m.renderWithOperation("")
		require.Contains(t, got, "animating", "spinner stays on even with --hide-tool-status")
		require.NotContains(t, got, "Run: find", "tool label hidden by --hide-tool-status")
	})
}

func TestOperationStatusLineShowsRunningShellBadge(t *testing.T) {
	m := &Mods{
		Config:              &Config{},
		Styles:              makeStyles(true),
		width:               80,
		showOperationStatus: true,
		reviewer:            &toolReviewer{},
	}
	m.setActiveOperation("Shell - go test ./... - last: ok")

	got := m.operationStatusLine()

	require.Equal(t, "RUNNING Shell - go test ./... - last: ok", strings.Join(strings.Fields(ansi.Strip(got)), " "))
	require.Contains(t, got, "\x1b[")
}

func TestOperationStatusLineStylesShellCompletion(t *testing.T) {
	m := &Mods{
		Config: &Config{},
		Styles: makeStyles(true),
		width:  80,
	}
	m.setActiveOperation("✗ Shell - npm test (exit 1)")

	got := m.operationStatusLine()
	require.Equal(t, "FAILED Shell - npm test (exit 1)", strings.Join(strings.Fields(ansi.Strip(got)), " "))
	require.Contains(t, got, "\x1b[")
}

func TestStyleToolResultLineUsesMutedBodyAndColoredMarker(t *testing.T) {
	m := &Mods{Styles: makeStyles(true)}

	success, ok := m.styleToolResultLine("  │ ✓ fs_read_file: path=mods.go")
	require.True(t, ok)
	require.Equal(t, "  │ ✓ fs_read_file: path=mods.go", ansi.Strip(success))
	require.Contains(t, success, "\x1b[")
	require.Contains(t, success, "38;2;143;209;158")
	require.Contains(t, success, "38;2;117;117;117")

	failure, ok := m.styleToolResultLine("  │ ✗ fs_delete_file: path=mods.go · failed")
	require.True(t, ok)
	require.Equal(t, "  │ ✗ fs_delete_file: path=mods.go · failed", ansi.Strip(failure))
	require.Contains(t, failure, "38;2;231;154;162")
}

func TestViewShowsReviewBannerWhenStdoutIsNotTTYButReviewInputIsAvailable(t *testing.T) {
	oldOutputTTY := IsOutputTTY
	IsOutputTTY = func() bool { return false }
	t.Cleanup(func() { IsOutputTTY = oldOutputTTY })

	m := &Mods{
		Config:       &Config{InteractiveTTYAvailable: true},
		Styles:       makeStyles(true),
		state:        responseState,
		contentMutex: &sync.Mutex{},
		width:        60,
		reviewer: &toolReviewer{
			reviewAvailabilityKnown:    true,
			interactiveReviewAvailable: true,
			reviewPending:              true,
			reviewItem: &toolReviewItem{
				name: "fs_write_file",
				args: []byte(`{"path":"out.txt","content":"x"}`),
				resp: make(chan reviewResponse, 1),
			},
		},
	}
	m.appendToOutput("partial answer")

	var view string
	stdout := captureStdout(t, func() { view = m.View().Content })

	require.Equal(t, "partial answer", stdout)
	require.Contains(t, view, "REVIEW REQUIRED")
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
