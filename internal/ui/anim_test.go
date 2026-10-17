package ui

import (
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/require"
)

func TestActivityIndicatorPhases(t *testing.T) {
	a := NewAnim(MakeStyles(true))
	require.NotNil(t, a.Init())
	for _, tc := range []struct {
		phase SpinnerPhase
		label string
	}{
		{PhaseConnecting, "Connecting"},
		{PhaseStreaming, "Generating"},
		{PhaseTool, "Working"},
	} {
		a.SetPhase(tc.phase)
		m, cmd := a.Update(spinnerTickMsg(a.start.Add(3 * time.Second)))
		a = m.(Anim)
		require.NotNil(t, cmd, "ticks must continue across phase transitions")
		require.Contains(t, ansi.Strip(a.View().Content), tc.label+" · 3s")
		require.Equal(t, 1, ansi.StringWidth(a.Indicator()))
		a.SetPhase(tc.phase)
		require.Contains(t, ansi.Strip(a.View().Content), "3s", "rendering the same phase must not reset elapsed time")
	}
	a.SetPhase(PhaseConnecting)
	require.NotContains(t, ansi.Strip(a.View().Content), "3s")
}

func TestActivityIndicatorAdvancesWithoutGrowing(t *testing.T) {
	a := NewAnim(Styles{})
	first := a.Indicator()
	for i := 0; i < len(spinnerFrames); i++ {
		m, cmd := a.Update(spinnerTickMsg(a.start.Add(time.Duration(i+1) * spinnerInterval)))
		a = m.(Anim)
		require.NotNil(t, cmd)
		require.Equal(t, 1, ansi.StringWidth(a.Indicator()))
		if i == 0 {
			require.NotEqual(t, first, a.Indicator())
		}
	}
	require.Equal(t, first, a.Indicator())
	_, cmd := a.Update(struct{}{})
	require.Nil(t, cmd, "unrelated events must not start another tick chain")
}
