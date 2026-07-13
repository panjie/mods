package ui

import (
	"testing"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/muesli/termenv"
	"github.com/stretchr/testify/require"
)

func TestInteractionThemePalettes(t *testing.T) {
	tests := map[string]lipgloss.Color{
		"charm":      "#7D56F4",
		"dracula":    "#BD93F9",
		"catppuccin": "#CBA6F7",
		"base16":     "#7CAFC2",
	}
	for theme, accent := range tests {
		t.Run(theme, func(t *testing.T) {
			styles := MakeStylesWithTheme(lipgloss.NewRenderer(nil), theme)
			require.Equal(t, accent, styles.Interaction.Palette.Accent)
			require.NotEqual(t, styles.Interaction.Palette.Danger, styles.Interaction.Palette.Warning)
		})
	}
}

func TestInteractionUnknownThemeFallsBackToCharm(t *testing.T) {
	renderer := lipgloss.NewRenderer(nil)
	unknown := MakeStylesWithTheme(renderer, "unknown").Interaction.Palette
	charm := MakeStylesWithTheme(renderer, "charm").Interaction.Palette
	require.Equal(t, charm, unknown)
}

func TestInteractionSelectedStateUsesThemeAccent(t *testing.T) {
	renderer := lipgloss.NewRenderer(nil)
	renderer.SetColorProfile(termenv.TrueColor)
	styles := MakeStylesWithTheme(renderer, "dracula").Interaction
	require.NotEqual(t, styles.Selected.Render("Y Allow"), styles.Action.Render("Y Allow"))
	require.Contains(t, styles.Selected.Render("Y Allow"), "\x1b[")
}

func TestInteractionSuccessStateUsesThemeSuccessColor(t *testing.T) {
	renderer := lipgloss.NewRenderer(nil)
	renderer.SetColorProfile(termenv.TrueColor)
	styles := MakeStylesWithTheme(renderer, "catppuccin").Interaction
	rendered := RenderInteractionPanel(styles, 40, InteractionPanel{
		Title: "Saved",
		Tone:  InteractionToneSuccess,
	})
	require.Contains(t, rendered, "\x1b[")
	require.Contains(t, ansi.Strip(rendered), "SAVED")
}
