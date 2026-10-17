package ui

import (
	"fmt"
	"time"

	tea "charm.land/bubbletea/v2"
)

const spinnerInterval = 100 * time.Millisecond

var spinnerFrames = [...]string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}

type spinnerTickMsg time.Time

func spinnerTick() tea.Cmd {
	return tea.Tick(spinnerInterval, func(t time.Time) tea.Msg { return spinnerTickMsg(t) })
}

// Anim keeps one quiet activity indicator and a human-readable phase label.
// All phases share the theme accent; color is not used to encode progress.
type Anim struct {
	start   time.Time
	elapsed time.Duration
	frame   int
	phase   SpinnerPhase
	Styles  Styles
}

func NewAnim(s Styles) Anim {
	return Anim{start: time.Now(), Styles: s}
}

func (a *Anim) SetPhase(p SpinnerPhase) {
	if a.phase == p {
		return
	}
	a.phase = p
	a.start = time.Now()
	a.elapsed = 0
}

func (Anim) Init() tea.Cmd { return spinnerTick() }

func (a Anim) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	if tick, ok := msg.(spinnerTickMsg); ok {
		a.frame = (a.frame + 1) % len(spinnerFrames)
		a.elapsed = max(0, time.Time(tick).Sub(a.start))
		return a, spinnerTick()
	}
	return a, nil
}

// Indicator is also used beside an existing operation label to avoid showing
// a second generic status for the same activity.
func (a Anim) Indicator() string {
	return a.Styles.Interaction.Info.Render(spinnerFrames[a.frame])
}

func (a Anim) View() tea.View {
	label := "Connecting"
	switch a.phase {
	case PhaseStreaming:
		label = "Generating"
	case PhaseTool:
		label = "Working"
	}
	suffix := ""
	if a.elapsed >= time.Second {
		suffix = fmt.Sprintf(" · %ds", int(a.elapsed.Seconds()))
	}
	return tea.NewView(a.Indicator() + a.Styles.Interaction.Muted.Render(" "+label+suffix))
}

type SpinnerPhase int

const (
	PhaseConnecting SpinnerPhase = iota
	PhaseStreaming
	PhaseTool
)
