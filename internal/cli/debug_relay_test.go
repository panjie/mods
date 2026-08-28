package cli

import (
	"bytes"
	"strings"
	"sync"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	debugpkg "github.com/panjie/mods/internal/debug"
	"github.com/stretchr/testify/require"
)

type debugRelayTriggerMsg struct{}
type debugRelayQuitMsg struct{}

type debugRelayModel struct{}

type lockedBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *lockedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *lockedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

func (debugRelayModel) Init() tea.Cmd {
	return func() tea.Msg { return debugRelayTriggerMsg{} }
}

func (m debugRelayModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg.(type) {
	case debugRelayTriggerMsg:
		debugpkg.Print(debugpkg.Section{Title: "inside update", Fields: []debugpkg.Field{{Label: "status", Value: "safe"}}})
		return m, tea.Tick(20*time.Millisecond, func(time.Time) tea.Msg { return debugRelayQuitMsg{} })
	case debugRelayQuitMsg:
		return m, tea.Quit
	default:
		return m, nil
	}
}

func (debugRelayModel) View() tea.View {
	return tea.NewView("live model output")
}

func TestDebugRelayIsNonBlockingAndOrdered(t *testing.T) {
	firstStarted := make(chan struct{})
	releaseFirst := make(chan struct{})
	var mu sync.Mutex
	var got []string
	relay := newDebugRelay(func(section string) {
		if section == "first" {
			close(firstStarted)
			<-releaseFirst
		}
		mu.Lock()
		got = append(got, section)
		mu.Unlock()
	})

	relay.enqueue("first")
	<-firstStarted
	secondQueued := make(chan struct{})
	go func() {
		relay.enqueue("second")
		close(secondQueued)
	}()
	<-secondQueued
	close(releaseFirst)
	relay.wait()

	mu.Lock()
	defer mu.Unlock()
	require.Equal(t, []string{"first", "second"}, got)
}

func TestDebugRelaySafelyPrintsFromBubbleTeaUpdate(t *testing.T) {
	var output lockedBuffer
	program := tea.NewProgram(
		debugRelayModel{},
		tea.WithInput(nil),
		tea.WithOutput(&output),
		tea.WithoutSignals(),
		tea.WithFPS(120),
	)
	relay := newDebugRelay(func(section string) {
		program.Send(tea.Println(section)())
	})
	restoreSink := debugpkg.SetManagedSink(relay.enqueue)
	debugpkg.SetEnabled(true)
	t.Cleanup(func() {
		debugpkg.SetEnabled(false)
		restoreSink()
	})

	_, err := program.Run()
	restoreSink()
	relay.wait()

	require.NoError(t, err)
	require.Equal(t, 1, strings.Count(output.String(), "DEBUG inside update"), output.String())
	require.Contains(t, output.String(), "  status  safe")
}
