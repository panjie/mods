package ui

import (
	"image/color"
	"math"

	"charm.land/lipgloss/v2"
)

// contrastRatio uses WCAG relative luminance, including the sRGB transfer
// function. It applies to the concrete foreground/background pairs we render.
func contrastRatio(a, b color.Color) float64 {
	luminance := func(c color.Color) float64 {
		r, g, b, _ := c.RGBA()
		linear := func(v uint32) float64 {
			x := float64(v) / 65535
			if x <= 0.04045 {
				return x / 12.92
			}
			return math.Pow((x+0.055)/1.055, 2.4)
		}
		return 0.2126*linear(r) + 0.7152*linear(g) + 0.0722*linear(b)
	}
	x, y := luminance(a), luminance(b)
	if x < y {
		x, y = y, x
	}
	return (x + 0.05) / (y + 0.05)
}
func contrastForeground(background color.Color) color.Color {
	black, white := lipgloss.Color("#000000"), lipgloss.Color("#FFFFFF")
	if contrastRatio(black, background) > contrastRatio(white, background) {
		return black
	}
	return white
}
func interactionPalette(theme string, isDark bool) InteractionPalette {
	p := rawInteractionPalette(theme, isDark)
	// Both transparent body text and text inside input/key surfaces must remain
	// legible. Keep theme hues, moving only as far as needed toward black/white.
	background := lipgloss.Color("#FFFFFF")
	target := uint32(0)
	if isDark {
		background = lipgloss.Color("#181818")
		target = 65535
	}
	adjust := func(c color.Color) color.Color {
		if contrastRatio(c, background) >= 4.5 && contrastRatio(c, p.Surface) >= 4.5 {
			return c
		}
		r, g, b, _ := c.RGBA()
		for i := 1; i <= 100; i++ {
			blend := func(v uint32) uint16 { return uint16((int(v)*(100-i) + int(target)*i) / 100) }
			mixed := color.RGBA64{R: blend(r), G: blend(g), B: blend(b), A: 65535}
			if contrastRatio(mixed, background) >= 4.5 && contrastRatio(mixed, p.Surface) >= 4.5 {
				return mixed
			}
		}
		return c
	}
	p.Text = adjust(p.Text)
	p.Muted = adjust(p.Muted)
	p.Danger = adjust(p.Danger)
	p.Warning = adjust(p.Warning)
	p.Success = adjust(p.Success)
	return p
}
