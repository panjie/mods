package huh

import (
	"image/color"
	"testing"

	"charm.land/lipgloss/v2"
	catppuccin "github.com/catppuccin/go"
)

// Option text must flip polarity with the terminal background so form choices
// stay readable on light terminals.
func TestThemeOptionTextFollowsBackgroundPolarity(t *testing.T) {
	cases := []struct {
		name      string
		theme     func(bool) *Styles
		lightText color.Color
		darkText  color.Color
	}{
		{"charm", ThemeCharm, lipgloss.Color("235"), lipgloss.Color("252")},
		{"dracula", ThemeDracula, lipgloss.Color("#2E2F3E"), lipgloss.Color("#f8f8f2")},
		{"base16", ThemeBase16, lipgloss.Color("0"), lipgloss.Color("7")},
		{"catppuccin", ThemeCatppuccin, catppuccin.Latte.Text(), catppuccin.Mocha.Text()},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			light := tc.theme(false)
			if got := light.Focused.Option.GetForeground(); got != tc.lightText {
				t.Errorf("light Option = %v, want %v", got, tc.lightText)
			}
			if got := light.Focused.UnselectedOption.GetForeground(); got != tc.lightText {
				t.Errorf("light UnselectedOption = %v, want %v", got, tc.lightText)
			}
			if got := tc.theme(true).Focused.Option.GetForeground(); got != tc.darkText {
				t.Errorf("dark Option = %v, want %v", got, tc.darkText)
			}
		})
	}
}

func TestThemeBase16AppliesTextInputStyles(t *testing.T) {
	styles := ThemeBase16(true)
	if got := styles.Focused.TextInput.Prompt.GetForeground(); got != lipgloss.Color("3") {
		t.Errorf("base16 prompt = %v, want %v", got, lipgloss.Color("3"))
	}
	if got := styles.Focused.TextInput.Placeholder.GetForeground(); got != lipgloss.Color("8") {
		t.Errorf("base16 placeholder = %v, want %v", got, lipgloss.Color("8"))
	}
	if got := styles.Focused.TextInput.Cursor.GetForeground(); got != lipgloss.Color("5") {
		t.Errorf("base16 cursor = %v, want %v", got, lipgloss.Color("5"))
	}
}
