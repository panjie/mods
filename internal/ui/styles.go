package ui

import (
	"image/color"
	"os"
	"strings"

	"charm.land/lipgloss/v2"
)

type Styles struct {
	AppName,
	CliArgs,
	Comment,
	CyclingChars,
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
	lightDark := lipgloss.LightDark(isDark)
	s.AppName = lipgloss.NewStyle().Bold(true)
	s.CliArgs = lipgloss.NewStyle().Foreground(lipgloss.Color("#585858"))
	s.Comment = lipgloss.NewStyle().Foreground(lipgloss.Color("#757575"))
	s.CyclingChars = lipgloss.NewStyle().Foreground(lipgloss.Color("#FF87D7"))
	s.DebugHeader = lipgloss.NewStyle().Foreground(lipgloss.Color("#000000")).Background(lipgloss.Color("#FFD700")).Bold(true).Padding(0, 1).SetString("DEBUG")
	s.DebugDetails = lipgloss.NewStyle().Foreground(lipgloss.Color("#B8860B"))
	s.ErrorHeader = lipgloss.NewStyle().Foreground(lipgloss.Color("#F1F1F1")).Background(lipgloss.Color("#FF5F87")).Bold(true).Padding(0, 1).SetString("ERROR")
	s.ErrorDetails = s.Comment
	s.ErrPadding = lipgloss.NewStyle().Padding(0, horizontalEdgePadding)
	s.Flag = lipgloss.NewStyle().Foreground(lightDark(lipgloss.Color("#00B594"), lipgloss.Color("#3EEFCF"))).Bold(true)
	s.FlagComma = lipgloss.NewStyle().Foreground(lightDark(lipgloss.Color("#5DD6C0"), lipgloss.Color("#427C72"))).SetString(",")
	s.FlagDesc = s.Comment
	s.InlineCode = lipgloss.NewStyle().Foreground(lipgloss.Color("#FF5F87")).Background(lipgloss.Color("#3A3A3A")).Padding(0, 1)
	s.Link = lipgloss.NewStyle().Foreground(lipgloss.Color("#00AF87")).Underline(true)
	s.Quote = lipgloss.NewStyle().Foreground(lightDark(lipgloss.Color("#FF71D0"), lipgloss.Color("#FF78D2")))
	s.Pipe = lipgloss.NewStyle().Foreground(lightDark(lipgloss.Color("#8470FF"), lipgloss.Color("#745CFF")))
	s.SessionList = lipgloss.NewStyle().Padding(0, 1)
	s.ShaHash = s.Flag
	s.Timeago = lipgloss.NewStyle().Foreground(lightDark(lipgloss.Color("#999"), lipgloss.Color("#555")))
	s.Interaction = makeInteractionStyles(interactionPalette(theme))
	return s
}

func interactionPalette(theme string) InteractionPalette {
	switch strings.ToLower(strings.TrimSpace(theme)) {
	case "dracula":
		return InteractionPalette{
			Accent: lipgloss.Color("#BD93F9"), Surface: lipgloss.Color("#343746"), Text: lipgloss.Color("#F8F8F2"), Muted: lipgloss.Color("#A6A6B5"),
			Danger: lipgloss.Color("#FF5555"), Warning: lipgloss.Color("#F1FA8C"), Success: lipgloss.Color("#50FA7B"),
		}
	case "catppuccin":
		return InteractionPalette{
			Accent: lipgloss.Color("#CBA6F7"), Surface: lipgloss.Color("#313244"), Text: lipgloss.Color("#CDD6F4"), Muted: lipgloss.Color("#A6ADC8"),
			Danger: lipgloss.Color("#F38BA8"), Warning: lipgloss.Color("#F9E2AF"), Success: lipgloss.Color("#A6E3A1"),
		}
	case "base16":
		return InteractionPalette{
			Accent: lipgloss.Color("#7CAFC2"), Surface: lipgloss.Color("#282828"), Text: lipgloss.Color("#D8D8D8"), Muted: lipgloss.Color("#B8B8B8"),
			Danger: lipgloss.Color("#AB4642"), Warning: lipgloss.Color("#F7CA88"), Success: lipgloss.Color("#A1B56C"),
		}
	default:
		return InteractionPalette{
			Accent: lipgloss.Color("#7D56F4"), Surface: lipgloss.Color("#302B48"), Text: lipgloss.Color("#F4F1FF"), Muted: lipgloss.Color("#AAA3C7"),
			Danger: lipgloss.Color("#FF5F87"), Warning: lipgloss.Color("#FFD75F"), Success: lipgloss.Color("#5FFFA2"),
		}
	}
}

func makeInteractionStyles(p InteractionPalette) InteractionStyles {
	return InteractionStyles{
		Palette: p,
		Panel: lipgloss.NewStyle().
			BorderStyle(lipgloss.ThickBorder()).
			BorderLeft(true).
			BorderForeground(p.Accent).
			PaddingLeft(1),
		Title:    lipgloss.NewStyle().Foreground(p.Accent).Bold(true),
		Meta:     lipgloss.NewStyle().Foreground(p.Muted),
		Body:     lipgloss.NewStyle().Foreground(p.Text),
		Label:    lipgloss.NewStyle().Foreground(p.Muted).Bold(true),
		Muted:    lipgloss.NewStyle().Foreground(p.Muted),
		Input:    lipgloss.NewStyle().Foreground(p.Text).Background(p.Surface).Padding(0, 1),
		Key:      lipgloss.NewStyle().Foreground(p.Accent).Background(p.Surface).Bold(true).Padding(0, 1),
		Action:   lipgloss.NewStyle().Foreground(p.Text),
		Selected: lipgloss.NewStyle().Foreground(p.Surface).Background(p.Accent).Bold(true).Padding(0, 1),
		Danger:   lipgloss.NewStyle().Foreground(p.Danger).Bold(true),
		Warning:  lipgloss.NewStyle().Foreground(p.Warning).Bold(true),
		Info:     lipgloss.NewStyle().Foreground(p.Accent).Bold(true),
		Success:  lipgloss.NewStyle().Foreground(p.Success).Bold(true),
	}
}

// action messages

const defaultAction = "WROTE"

var outputHeader = lipgloss.NewStyle().Foreground(lipgloss.Color("#F1F1F1")).Background(lipgloss.Color("#6C50FF")).Bold(true).Padding(0, 1).MarginRight(1)

func PrintConfirmation(action, content string) {
	if action == "" {
		action = defaultAction
	}
	outputHeader = outputHeader.SetString(strings.ToUpper(action))
	_, _ = lipgloss.Fprintln(os.Stdout, lipgloss.JoinHorizontal(lipgloss.Center, outputHeader.String(), content))
}
