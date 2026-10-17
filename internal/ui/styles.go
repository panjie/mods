package ui

import (
	"image/color"
	"strings"

	"charm.land/lipgloss/v2"
)

type Styles struct {
	AppName,
	CliArgs,
	Comment,
	DebugHeader,
	DebugDetails,
	ErrorHeader,
	ErrorDetails,
	ErrPadding,
	Flag,
	FlagComma,
	FlagDesc,
	InlineCode,
	Link,
	Pipe,
	Quote,
	SessionList,
	ShaHash,
	Timeago lipgloss.Style
	Interaction InteractionStyles
}

type InteractionPalette struct {
	Accent,
	Surface,
	Text,
	Muted,
	Danger,
	Warning,
	Success color.Color
}

type InteractionStyles struct {
	Palette  InteractionPalette
	Panel    lipgloss.Style
	Title    lipgloss.Style
	Meta     lipgloss.Style
	Body     lipgloss.Style
	Label    lipgloss.Style
	Muted    lipgloss.Style
	Input    lipgloss.Style
	Key      lipgloss.Style
	Action   lipgloss.Style
	Selected lipgloss.Style
	Danger   lipgloss.Style
	Warning  lipgloss.Style
	Info     lipgloss.Style
	Success  lipgloss.Style
}

func MakeStyles(isDark bool) (s Styles) {
	return MakeStylesWithTheme("charm", isDark)
}

func MakeStylesWithTheme(theme string, isDark bool) (s Styles) {
	const horizontalEdgePadding = 2
	s.Interaction = makeInteractionStyles(interactionPalette(theme, isDark))
	i := s.Interaction
	s.AppName = i.Title
	s.CliArgs = i.Muted
	s.Comment = i.Muted
	s.DebugHeader = i.Muted.Bold(true).SetString("Debug")
	s.DebugDetails = i.Muted
	s.ErrorHeader = i.Danger.SetString("Error")
	s.ErrorDetails = i.Body
	s.ErrPadding = lipgloss.NewStyle().Padding(0, horizontalEdgePadding)
	s.Flag = i.Info
	s.FlagComma = i.Muted.SetString(",")
	s.FlagDesc = i.Body
	s.InlineCode = i.Info
	s.Link = i.Info.Underline(true)
	s.Quote = i.Body
	s.Pipe = i.Muted
	s.SessionList = lipgloss.NewStyle().Padding(0, 1)
	s.ShaHash = i.Muted
	s.Timeago = i.Muted

	return s
}

// interactionPalette maps a theme name to a background-adaptive palette. Dark
// values keep each theme's identity; light values darken text, muted, and
// pastel status hues so panels stay readable on light terminal backgrounds.
func rawInteractionPalette(theme string, isDark bool) InteractionPalette {
	lightDark := lipgloss.LightDark(isDark)
	switch strings.ToLower(strings.TrimSpace(theme)) {
	case "dracula":
		return InteractionPalette{
			Accent:  lightDark(lipgloss.Color("#7C4DD8"), lipgloss.Color("#BD93F9")),
			Surface: lightDark(lipgloss.Color("#E9E8F2"), lipgloss.Color("#343746")),
			Text:    lightDark(lipgloss.Color("#2E2F3E"), lipgloss.Color("#F8F8F2")),
			Muted:   lightDark(lipgloss.Color("#63657A"), lipgloss.Color("#A6A6B5")),
			Danger:  lightDark(lipgloss.Color("#D22B4B"), lipgloss.Color("#FF5555")),
			Warning: lightDark(lipgloss.Color("#8A7A00"), lipgloss.Color("#F1FA8C")),
			Success: lightDark(lipgloss.Color("#0C8B46"), lipgloss.Color("#50FA7B")),
		}
	case "catppuccin":
		// Light values come from the official Catppuccin Latte flavour.
		return InteractionPalette{
			Accent:  lightDark(lipgloss.Color("#8839EF"), lipgloss.Color("#CBA6F7")),
			Surface: lightDark(lipgloss.Color("#CCD0DA"), lipgloss.Color("#313244")),
			Text:    lightDark(lipgloss.Color("#4C4F69"), lipgloss.Color("#CDD6F4")),
			Muted:   lightDark(lipgloss.Color("#6C6F85"), lipgloss.Color("#A6ADC8")),
			Danger:  lightDark(lipgloss.Color("#D20F39"), lipgloss.Color("#F38BA8")),
			Warning: lightDark(lipgloss.Color("#DF8E1D"), lipgloss.Color("#F9E2AF")),
			Success: lightDark(lipgloss.Color("#40A02B"), lipgloss.Color("#A6E3A1")),
		}
	case "base16":
		return InteractionPalette{
			Accent:  lightDark(lipgloss.Color("#3D7E99"), lipgloss.Color("#7CAFC2")),
			Surface: lightDark(lipgloss.Color("#DCDCDC"), lipgloss.Color("#282828")),
			Text:    lightDark(lipgloss.Color("#2B2B2B"), lipgloss.Color("#D8D8D8")),
			Muted:   lightDark(lipgloss.Color("#686868"), lipgloss.Color("#B8B8B8")),
			Danger:  lipgloss.Color("#AB4642"),
			Warning: lightDark(lipgloss.Color("#A07B2F"), lipgloss.Color("#F7CA88")),
			Success: lightDark(lipgloss.Color("#647B36"), lipgloss.Color("#A1B56C")),
		}
	default:
		return InteractionPalette{
			Accent:  lightDark(lipgloss.Color("#5A56E0"), lipgloss.Color("#7D56F4")),
			Surface: lightDark(lipgloss.Color("#EDE9FE"), lipgloss.Color("#302B48")),
			Text:    lightDark(lipgloss.Color("#28203E"), lipgloss.Color("#F4F1FF")),
			Muted:   lightDark(lipgloss.Color("#6E6592"), lipgloss.Color("#AAA3C7")),
			Danger:  lightDark(lipgloss.Color("#D63468"), lipgloss.Color("#FF5F87")),
			Warning: lightDark(lipgloss.Color("#A16207"), lipgloss.Color("#FFD75F")),
			Success: lightDark(lipgloss.Color("#0B8A5E"), lipgloss.Color("#5FFFA2")),
		}
	}
}

func makeInteractionStyles(p InteractionPalette) InteractionStyles {
	return InteractionStyles{
		Palette: p,
		Panel: lipgloss.NewStyle().
			BorderStyle(lipgloss.NormalBorder()).
			BorderLeft(true).
			BorderForeground(p.Accent).
			PaddingLeft(1),
		Title:    lipgloss.NewStyle().Bold(true),
		Meta:     lipgloss.NewStyle().Foreground(p.Muted),
		Body:     lipgloss.NewStyle(),
		Label:    lipgloss.NewStyle().Foreground(p.Muted),
		Muted:    lipgloss.NewStyle().Foreground(p.Muted),
		Input:    lipgloss.NewStyle().Padding(0, 1),
		Key:      lipgloss.NewStyle().Bold(true).Padding(0, 1),
		Action:   lipgloss.NewStyle().Foreground(p.Muted),
		Selected: lipgloss.NewStyle().Foreground(contrastForeground(p.Accent)).Background(p.Accent).Bold(true).Padding(0, 1),
		Danger:   lipgloss.NewStyle().Foreground(p.Danger).Bold(true),
		Warning:  lipgloss.NewStyle().Foreground(p.Warning),
		Info:     lipgloss.NewStyle().Foreground(p.Accent),
		Success:  lipgloss.NewStyle().Foreground(p.Success),
	}
}

// MakeInteractionStyles shares terminal-aware styling across interactive models.
// Until the terminal reports its background, unpaired text uses its default
// foreground rather than assuming a dark terminal.
func MakeInteractionStyles(theme string, isDark, known bool) InteractionStyles {
	p := interactionPalette(theme, isDark)
	if !known {
		p.Text = nil
		p.Muted = nil
	}
	s := makeInteractionStyles(p)
	if !known {
		s.Input = s.Input.UnsetBackground()
	}
	return s
}
