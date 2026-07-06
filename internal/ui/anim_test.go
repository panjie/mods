package ui

import (
	"io"
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"
	"github.com/stretchr/testify/require"
)

func trueColorRenderer() *lipgloss.Renderer {
	r := lipgloss.NewRenderer(io.Discard)
	r.SetColorProfile(termenv.TrueColor)
	return r
}

// ansi256Renderer simulates a 256-color terminal (e.g. WSL under Windows
// Terminal when COLORTERM isn't propagated): termenv reports ANSI256, which is
// below TrueColor but still color-capable.
func ansi256Renderer() *lipgloss.Renderer {
	r := lipgloss.NewRenderer(io.Discard)
	r.SetColorProfile(termenv.ANSI256)
	return r
}

// monochromeRenderer simulates a terminal with no color support at all.
func monochromeRenderer() *lipgloss.Renderer {
	r := lipgloss.NewRenderer(io.Discard)
	r.SetColorProfile(termenv.Ascii)
	return r
}

func TestSpinnerPhaseGradient(t *testing.T) {
	cases := []struct {
		phase            SpinnerPhase
		wantStart, wantEnd string
	}{
		{PhaseConnecting, defaultGradientStart, defaultGradientEnd},
		{PhaseStreaming, "#3DDC97", "#3DC6DC"},
		{PhaseTool, "#F5A524", "#FF6B35"},
	}
	seen := map[string]bool{}
	for _, c := range cases {
		start, end := c.phase.gradient()
		require.Equal(t, c.wantStart, start, "phase %d start color", c.phase)
		require.Equal(t, c.wantEnd, end, "phase %d end color", c.phase)
		// Each phase must have a distinct palette.
		key := start + end
		require.False(t, seen[key], "phase %d palette duplicates another phase", c.phase)
		seen[key] = true
	}
}

func TestNewAnimRampForColorProfiles(t *testing.T) {
	t.Run("truecolor builds a doubled ramp and emits color", func(t *testing.T) {
		const size = 6
		a := NewAnim(size, trueColorRenderer(), Styles{})
		require.Len(t, a.ramp, size*2, "ramp is doubled (forward + reversed) for color cycling")
		view := a.View()
		require.NotEmpty(t, view, "cycling chars render even before ticks")
		require.Contains(t, view, "\x1b[", "truecolor must emit ANSI color escapes")
	})

	t.Run("256-color profile also builds the ramp and emits color", func(t *testing.T) {
		const size = 6
		a := NewAnim(size, ansi256Renderer(), Styles{})
		require.Len(t, a.ramp, size*2,
			"the ramp must be built for any color profile so WSL/256-color terminals still get color")
		view := a.View()
		require.NotEmpty(t, view)
		require.Contains(t, view, "\x1b[", "256-color must downsample and emit ANSI color escapes")
	})

	t.Run("below min ramp size renders plain chars", func(t *testing.T) {
		a := NewAnim(2, trueColorRenderer(), Styles{}) // minRampSize == 3
		require.Empty(t, a.ramp, "no ramp below the minimum visible width")
		require.NotEmpty(t, a.View())
		require.NotContains(t, a.View(), "\x1b[", "no color styling without a ramp")
	})

	t.Run("monochrome leaves the ramp empty and emits no escapes", func(t *testing.T) {
		a := NewAnim(6, monochromeRenderer(), Styles{})
		require.Empty(t, a.ramp, "only truly colorless (Ascii) profiles skip the ramp")
		view := a.View()
		require.NotEmpty(t, view)
		require.NotContains(t, view, "\x1b[", "Ascii must not emit color escapes")
		// Sanity: the monochrome view is just the raw cycling chars.
		require.True(t, strings.IndexAny(view, "\x1b") == -1)
	})
}

func TestSetPhaseRebuildsRamp(t *testing.T) {
	const size = 6
	a := NewAnim(size, trueColorRenderer(), Styles{})
	require.Equal(t, PhaseConnecting, a.phase)
	require.Len(t, a.ramp, size*2)

	// Switching phase rebuilds the ramp with the new palette and records the phase.
	a.SetPhase(PhaseStreaming)
	require.Equal(t, PhaseStreaming, a.phase)
	require.Len(t, a.ramp, size*2, "rebuilt ramp keeps the doubled layout")
	require.NotEmpty(t, a.View())

	// Switching to yet another phase keeps the ramp consistent.
	a.SetPhase(PhaseTool)
	require.Equal(t, PhaseTool, a.phase)
	require.Len(t, a.ramp, size*2)
}

func TestSetPhaseNoOpWhenUnchanged(t *testing.T) {
	const size = 6
	a := NewAnim(size, trueColorRenderer(), Styles{})
	a.SetPhase(PhaseTool)
	before := append([]lipgloss.Style(nil), a.ramp...)

	// Calling SetPhase with the current phase must be a no-op: the ramp is
	// not rebuilt, so its contents are byte-for-byte identical.
	a.SetPhase(PhaseTool)
	require.Equal(t, before, a.ramp)
	require.Equal(t, PhaseTool, a.phase)
}
