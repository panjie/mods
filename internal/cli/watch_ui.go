package cli

import (
	"context"
	"fmt"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/panjie/mods/internal/watch"
)

type watchTickMsg struct{}
type watchEventMsg struct {
	event watch.Event
	err   error
}
type watchWaitModel struct {
	pending *watchEventMsg
	ctx     context.Context
	manager *watch.Manager
	event   watch.Event
	err     error
	done    bool
}

func watchTick() tea.Cmd {
	return tea.Tick(time.Second, func(time.Time) tea.Msg { return watchTickMsg{} })
}
func (m *watchWaitModel) Init() tea.Cmd {
	return tea.Batch(watchTick(), func() tea.Msg { e, err := m.manager.Next(m.ctx); return watchEventMsg{e, err} })
}
func (m *watchWaitModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case watchEventMsg:
		if m.manager.Status().Paused && msg.err == nil {
			m.pending = &msg
			return m, nil
		}
		m.event, m.err, m.done = msg.event, msg.err, true
		return m, tea.Quit
	case watchTickMsg:
		if err := m.ctx.Err(); err != nil {
			m.err = err
			m.done = true
			return m, tea.Quit
		}
		return m, watchTick()
	case tea.KeyPressMsg:
		switch msg.String() {
		case "space", " ":
			m.manager.Pause(!m.manager.Status().Paused)
			if !m.manager.Status().Paused && m.pending != nil {
				m.event, m.err, m.done = m.pending.event, m.pending.err, true
				return m, tea.Quit
			}
		case "ctrl+c", "esc", "q":
			m.manager.Stop()
			m.err = context.Canceled
			m.done = true
			return m, tea.Quit
		}
	}
	return m, nil
}
func (m *watchWaitModel) View() tea.View {
	if m.done {
		return tea.NewView("")
	}
	s := m.manager.Status()
	state := s.State
	if s.Paused {
		state = "paused (collection continues)"
	}
	last := "none"
	if !s.LastOutput.IsZero() {
		last = s.LastOutput.Format(time.RFC3339)
	}
	// Source names may contain terminal controls; quote and strip before display.
	return tea.NewView(fmt.Sprintf("Watching %q — %s\nLast output: %s · batches: %d · buffered: %d bytes · omitted: %d\nSpace pauses/resumes actions · Ctrl+C stops\n", ansi.Strip(s.Source), state, last, s.Batches, s.BufferedBytes, s.OmittedBytes))
}
func waitForWatchEvent(ctx context.Context, m *watch.Manager, opts []tea.ProgramOption) (watch.Event, error) {
	if config.Raw || !config.InteractiveTTYAvailable || !IsErrorTTY() {
		return m.Next(ctx)
	}
	waitCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	model := &watchWaitModel{ctx: waitCtx, manager: m}
	p := tea.NewProgram(model, opts...)
	if _, err := p.Run(); err != nil {
		return watch.Event{}, err
	}
	if !model.done {
		return watch.Event{}, context.Canceled
	}
	return model.event, model.err
}
