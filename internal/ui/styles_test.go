package ui

import (
	"image/color"
	"testing"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/require"
)

func TestInteractionThemePalettes(t *testing.T) {
	tests := map[string]color.Color{
		"charm":      lipgloss.Color("#7D56F4"),
		"dracula":    lipgloss.Color("#BD93F9"),
		"catppuccin": lipgloss.Color("#CBA6F7"),
		"base16":     lipgloss.Color("#7CAFC2"),
	}
	for theme, accent := range tests {
		t.Run(theme, func(t *testing.T) {
			styles := MakeStylesWithTheme(theme, true)
			require.Equal(t, accent, styles.Interaction.Palette.Accent)
			require.NotEqual(t, styles.Interaction.Palette.Danger, styles.Interaction.Palette.Warning)
		})
	}
}

func TestInteractionUnknownThemeFallsBackToCharm(t *testing.T) {
	unknown := MakeStylesWithTheme("unknown", true).Interaction.Palette
	charm := MakeStylesWithTheme("charm", true).Interaction.Palette
	require.Equal(t, charm, unknown)
}

func TestInteractionSelectedStateUsesThemeAccent(t *testing.T) {
	styles := MakeStylesWithTheme("dracula", true).Interaction
	require.NotEqual(t, styles.Selected.Render("Y Allow"), styles.Action.Render("Y Allow"))
	require.Contains(t, styles.Selected.Render("Y Allow"), "\x1b[")
}

func TestInteractionSuccessStateUsesThemeSuccessColor(t *testing.T) {
	styles := MakeStylesWithTheme("catppuccin", true).Interaction
	rendered := RenderInteractionPanel(styles, 40, InteractionPanel{
		Title: "Saved",
		Tone:  InteractionToneSuccess,
	})
	require.Contains(t, rendered, "\x1b[")
	require.Contains(t, ansi.Strip(rendered), "SAVED")
}
