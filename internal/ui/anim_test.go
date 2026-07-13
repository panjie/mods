package ui

import (
	"testing"

	"charm.land/lipgloss/v2"
	"github.com/stretchr/testify/require"
)

func TestSpinnerPhaseGradient(t *testing.T) {
	cases := []struct {
		phase              SpinnerPhase
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

func TestNewAnimBuildsRendererIndependentRamp(t *testing.T) {
	t.Run("builds a doubled ramp and emits color", func(t *testing.T) {
		const size = 6
		a := NewAnim(size, Styles{})
		require.Len(t, a.ramp, size*2, "ramp is doubled (forward + reversed) for color cycling")
		view := a.View().Content
		require.NotEmpty(t, view, "cycling chars render even before ticks")
		require.Contains(t, view, "\x1b[")
	})

	t.Run("below min ramp size renders plain chars", func(t *testing.T) {
		a := NewAnim(2, Styles{}) // minRampSize == 3
		require.Empty(t, a.ramp, "no ramp below the minimum visible width")
		require.NotEmpty(t, a.View().Content)
		require.NotContains(t, a.View().Content, "\x1b[", "no color styling without a ramp")
	})
}

func TestSetPhaseRebuildsRamp(t *testing.T) {
	const size = 6
	a := NewAnim(size, Styles{})
	require.Equal(t, PhaseConnecting, a.phase)
	require.Len(t, a.ramp, size*2)

	// Switching phase rebuilds the ramp with the new palette and records the phase.
	a.SetPhase(PhaseStreaming)
	require.Equal(t, PhaseStreaming, a.phase)
	require.Len(t, a.ramp, size*2, "rebuilt ramp keeps the doubled layout")
	require.NotEmpty(t, a.View().Content)

	// Switching to yet another phase keeps the ramp consistent.
	a.SetPhase(PhaseTool)
	require.Equal(t, PhaseTool, a.phase)
	require.Len(t, a.ramp, size*2)
}

func TestSetPhaseNoOpWhenUnchanged(t *testing.T) {
	const size = 6
	a := NewAnim(size, Styles{})
	a.SetPhase(PhaseTool)
	before := append([]lipgloss.Style(nil), a.ramp...)

	// Calling SetPhase with the current phase must be a no-op: the ramp is
	// not rebuilt, so its contents are byte-for-byte identical.
	a.SetPhase(PhaseTool)
	require.Equal(t, before, a.ramp)
	require.Equal(t, PhaseTool, a.phase)
}
