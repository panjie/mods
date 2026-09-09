package ui

import (
	"fmt"
	"image/color"
	"testing"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/muesli/termenv"
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

// TestInteractionPalettesAdaptToTerminalBackground guards readability of panel
// text on both terminal polarities: body/muted text and status hues must be
// dark on light backgrounds and light on dark backgrounds.
func TestInteractionPalettesAdaptToTerminalBackground(t *testing.T) {
	for _, theme := range []string{"charm", "dracula", "catppuccin", "base16", "unknown"} {
		t.Run(theme, func(t *testing.T) {
			light := MakeStylesWithTheme(theme, false).Interaction.Palette
			dark := MakeStylesWithTheme(theme, true).Interaction.Palette
			require.NotEqual(t, light, dark, "palette must react to the terminal background")

			roles := map[string]struct{ light, dark color.Color }{
				"Text":    {light.Text, dark.Text},
				"Muted":   {light.Muted, dark.Muted},
				"Warning": {light.Warning, dark.Warning},
				"Success": {light.Success, dark.Success},
			}
			for name, role := range roles {
				require.Less(t, colorLightness(t, role.light), 0.5, "light-mode %s must stay readable on light backgrounds", name)
				require.Greater(t, colorLightness(t, role.dark), 0.5, "dark-mode %s must stay readable on dark backgrounds", name)
			}
		})
	}
}

func colorLightness(t *testing.T, c color.Color) float64 {
	t.Helper()
	r, g, b, _ := c.RGBA()
	hex := fmt.Sprintf("#%02X%02X%02X", uint8(r>>8), uint8(g>>8), uint8(b>>8))
	_, _, lightness := termenv.ConvertToRGB(termenv.RGBColor(hex)).Hsl()
	return lightness
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

func TestInteractionContrastPairs(t *testing.T) {
	for _, theme := range []string{"charm", "dracula", "catppuccin", "base16", "unknown"} {
		for _, dark := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/%t", theme, dark), func(t *testing.T) {
				s := MakeStylesWithTheme(theme, dark).Interaction
				p := s.Palette
				bg := lipgloss.Color("#FFFFFF")
				if dark {
					bg = lipgloss.Color("#181818")
				}
				for name, fg := range map[string]color.Color{"text": p.Text, "muted": p.Muted, "danger": p.Danger, "warning": p.Warning, "success": p.Success} {
					require.GreaterOrEqual(t, contrastRatio(fg, bg), 4.5, name)
					require.GreaterOrEqual(t, contrastRatio(fg, p.Surface), 4.5, name+" on surface")
				}
				require.GreaterOrEqual(t, contrastRatio(s.Selected.GetForeground(), s.Selected.GetBackground()), 4.5, "selected")
				require.GreaterOrEqual(t, contrastRatio(s.Key.GetForeground(), s.Key.GetBackground()), 4.5, "key label")
			})
		}
	}
}
func TestInteractionUnknownBackgroundUsesTerminalForeground(t *testing.T) {
	s := MakeInteractionStyles("charm", true, false)
	require.Nil(t, s.Palette.Text)
	require.Nil(t, s.Palette.Muted)
	require.Equal(t, "Title", ansi.Strip(s.Title.Render("Title")))
}
