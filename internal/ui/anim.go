package ui

import (
	"math/rand"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/lucasb-eyer/go-colorful"
	"github.com/muesli/termenv"
)

const (
	charCyclingFPS  = time.Second / 22
	colorCycleFPS   = time.Second / 5
	maxCyclingChars = 120
)

var charRunes = []rune("0123456789abcdefABCDEF~!@#$£€%^&*()+=_")

type charState int

const (
	charInitialState charState = iota
	charCyclingState
)

// cyclingChar is a single animated character.
type cyclingChar struct {
	currentValue rune
	initialDelay time.Duration
}

func (c cyclingChar) randomRune() rune {
	return (charRunes)[rand.Intn(len(charRunes))] //nolint:gosec
}

func (c cyclingChar) state(start time.Time) charState {
	now := time.Now()
	if now.Before(start.Add(c.initialDelay)) {
		return charInitialState
	}
	return charCyclingState
}

type stepCharsMsg struct{}

func stepChars() tea.Cmd {
	return tea.Tick(charCyclingFPS, func(time.Time) tea.Msg {
		return stepCharsMsg{}
	})
}

type colorCycleMsg struct{}

func cycleColors() tea.Cmd {
	return tea.Tick(colorCycleFPS, func(time.Time) tea.Msg {
		return colorCycleMsg{}
	})
}

// anim is the model that manages the animation that displays while the
// output is being generated.
type Anim struct {
	start        time.Time
	size         int
	phase        SpinnerPhase
	renderer     *lipgloss.Renderer
	cyclingChars []cyclingChar
	ramp         []lipgloss.Style
	Styles       Styles
}

func NewAnim(cyclingCharsSize uint, r *lipgloss.Renderer, s Styles) Anim {
	// #nosec G115
	n := int(cyclingCharsSize)
	if n > maxCyclingChars {
		n = maxCyclingChars
	}

	c := Anim{
		start:    time.Now(),
		size:     n,
		phase:    PhaseConnecting,
		renderer: r,
		Styles:   s,
	}
	c.rebuildRamp()

	makeDelay := func(a int32, b time.Duration) time.Duration {
		return time.Duration(rand.Int31n(a)) * (time.Millisecond * b) //nolint:gosec
	}

	makeInitialDelay := func() time.Duration {
		return makeDelay(8, 60) //nolint:mnd
	}

	// Characters that cycle forever.
	c.cyclingChars = make([]cyclingChar, n)

	for i := range c.cyclingChars {
		c.cyclingChars[i] = cyclingChar{
			initialDelay: makeInitialDelay(),
		}
	}

	return c
}

// SetPhase switches the animation's color palette. It is a no-op when the
// phase is unchanged, so callers can safely invoke it every frame. The ramp
// is rebuilt (in truecolor mode) from the new phase's gradient endpoints.
func (a *Anim) SetPhase(p SpinnerPhase) {
	if a.phase == p {
		return
	}
	a.phase = p
	a.rebuildRamp()
}

// rebuildRamp (re)builds the color-cycling ramp from the current phase's
// gradient. It is built for any profile that supports color (TrueColor,
// ANSI256, ANSI); lipgloss/termenv downsample the hex endpoints to the detected
// profile on render, so WSL/256-color terminals still get a colored spinner
// even when COLORTERM isn't set. Only truly monochrome (Ascii) profiles skip it.
// The slice is allocated at 2x capacity so Update can rotate it seamlessly.
func (a *Anim) rebuildRamp() {
	n := a.size
	a.ramp = nil
	const minRampSize = 3
	if n < minRampSize || a.renderer == nil || a.renderer.ColorProfile() == termenv.Ascii {
		return
	}
	startHex, endHex := a.phase.gradient()
	ramp := gradientRamp(n, startHex, endHex)
	a.ramp = make([]lipgloss.Style, n, n*2) //nolint:mnd
	for i, color := range ramp {
		a.ramp[i] = a.renderer.NewStyle().Foreground(color)
	}
	a.ramp = append(a.ramp, reverse(a.ramp)...) // reverse and append for color cycling
}

// Init initializes the animation.
func (Anim) Init() tea.Cmd {
	return tea.Batch(stepChars(), cycleColors())
}

// Update handles messages.
func (a Anim) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg.(type) {
	case stepCharsMsg:
		a.updateChars(&a.cyclingChars)
		return a, stepChars()
	case colorCycleMsg:
		const minColorCycleSize = 2
		if len(a.ramp) < minColorCycleSize {
			return a, nil
		}
		a.ramp = append(a.ramp[1:], a.ramp[0])
		return a, cycleColors()
	default:
		return a, nil
	}
}

func (a *Anim) updateChars(chars *[]cyclingChar) {
	for i, c := range *chars {
		switch c.state(a.start) {
		case charInitialState:
			(*chars)[i].currentValue = '.'
		case charCyclingState:
			(*chars)[i].currentValue = c.randomRune()
		}
	}
}

// View renders the animation.
func (a Anim) View() string {
	var b strings.Builder

	for i, c := range a.cyclingChars {
		if len(a.ramp) > i {
			b.WriteString(a.ramp[i].Render(string(c.currentValue)))
			continue
		}
		b.WriteRune(c.currentValue)
	}

	return b.String()
}

// Default gradient endpoints (the Connecting-phase palette). Kept as
// constants so MakeGradientText and any default callers share one source.
const (
	defaultGradientStart = "#F967DC"
	defaultGradientEnd   = "#6B50FF"
)

// SpinnerPhase selects the color palette the animation renders in. The phase
// is derived at render time from the app's runtime state (see app.spinnerPhase)
// and pushed into the Anim via SetPhase; only the palette changes, the cycling
// mechanism is identical across phases.
type SpinnerPhase int

const (
	PhaseConnecting SpinnerPhase = iota // warming up / waiting for first token
	PhaseStreaming                      // model is emitting text (incl. thinking)
	PhaseTool                           // a tool / web_search is executing
)

// gradient returns the [start, end] hex colors for this phase's ramp.
func (p SpinnerPhase) gradient() (start, end string) {
	switch p {
	case PhaseStreaming:
		return "#3DDC97", "#3DC6DC"
	case PhaseTool:
		return "#F5A524", "#FF6B35"
	default:
		return defaultGradientStart, defaultGradientEnd
	}
}

func gradientRamp(length int, startHex, endHex string) []lipgloss.Color {
	var (
		c        = make([]lipgloss.Color, length)
		start, _ = colorful.Hex(startHex)
		end, _   = colorful.Hex(endHex)
	)
	for i := 0; i < length; i++ {
		step := start.BlendLuv(end, float64(i)/float64(length))
		c[i] = lipgloss.Color(step.Hex())
	}
	return c
}

func makeGradientRamp(length int) []lipgloss.Color {
	return gradientRamp(length, defaultGradientStart, defaultGradientEnd)
}

func MakeGradientText(baseStyle lipgloss.Style, str string) string {
	const minSize = 3
	if len(str) < minSize {
		return str
	}
	b := strings.Builder{}
	runes := []rune(str)
	for i, c := range makeGradientRamp(len(str)) {
		b.WriteString(baseStyle.Foreground(c).Render(string(runes[i])))
	}
	return b.String()
}

func reverse[T any](in []T) []T {
	out := make([]T, len(in))
	copy(out, in[:])
	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
		out[i], out[j] = out[j], out[i]
	}
	return out
}
