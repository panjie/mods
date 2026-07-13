package ui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
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
	Success lipgloss.Color
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

func MakeStyles(r *lipgloss.Renderer) (s Styles) {
	return MakeStylesWithTheme(r, "charm")
}

func MakeStylesWithTheme(r *lipgloss.Renderer, theme string) (s Styles) {
	const horizontalEdgePadding = 2
	s.AppName = r.NewStyle().Bold(true)
	s.CliArgs = r.NewStyle().Foreground(lipgloss.Color("#585858"))
	s.Comment = r.NewStyle().Foreground(lipgloss.Color("#757575"))
	s.CyclingChars = r.NewStyle().Foreground(lipgloss.Color("#FF87D7"))
	s.DebugHeader = r.NewStyle().Foreground(lipgloss.Color("#000000")).Background(lipgloss.Color("#FFD700")).Bold(true).Padding(0, 1).SetString("DEBUG")
	s.DebugDetails = r.NewStyle().Foreground(lipgloss.Color("#B8860B"))
	s.ErrorHeader = r.NewStyle().Foreground(lipgloss.Color("#F1F1F1")).Background(lipgloss.Color("#FF5F87")).Bold(true).Padding(0, 1).SetString("ERROR")
	s.ErrorDetails = s.Comment
	s.ErrPadding = r.NewStyle().Padding(0, horizontalEdgePadding)
	s.Flag = r.NewStyle().Foreground(lipgloss.AdaptiveColor{Light: "#00B594", Dark: "#3EEFCF"}).Bold(true)
	s.FlagComma = r.NewStyle().Foreground(lipgloss.AdaptiveColor{Light: "#5DD6C0", Dark: "#427C72"}).SetString(",")
	s.FlagDesc = s.Comment
	s.InlineCode = r.NewStyle().Foreground(lipgloss.Color("#FF5F87")).Background(lipgloss.Color("#3A3A3A")).Padding(0, 1)
	s.Link = r.NewStyle().Foreground(lipgloss.Color("#00AF87")).Underline(true)
	s.Quote = r.NewStyle().Foreground(lipgloss.AdaptiveColor{Light: "#FF71D0", Dark: "#FF78D2"})
	s.Pipe = r.NewStyle().Foreground(lipgloss.AdaptiveColor{Light: "#8470FF", Dark: "#745CFF"})
	s.SessionList = r.NewStyle().Padding(0, 1)
	s.ShaHash = s.Flag
	s.Timeago = r.NewStyle().Foreground(lipgloss.AdaptiveColor{Light: "#999", Dark: "#555"})
	s.Interaction = makeInteractionStyles(r, interactionPalette(theme))
	return s
}

func interactionPalette(theme string) InteractionPalette {
	switch strings.ToLower(strings.TrimSpace(theme)) {
	case "dracula":
		return InteractionPalette{
			Accent: "#BD93F9", Surface: "#343746", Text: "#F8F8F2", Muted: "#A6A6B5",
			Danger: "#FF5555", Warning: "#F1FA8C", Success: "#50FA7B",
		}
	case "catppuccin":
		return InteractionPalette{
			Accent: "#CBA6F7", Surface: "#313244", Text: "#CDD6F4", Muted: "#A6ADC8",
			Danger: "#F38BA8", Warning: "#F9E2AF", Success: "#A6E3A1",
		}
	case "base16":
		return InteractionPalette{
			Accent: "#7CAFC2", Surface: "#282828", Text: "#D8D8D8", Muted: "#B8B8B8",
			Danger: "#AB4642", Warning: "#F7CA88", Success: "#A1B56C",
		}
	default:
		return InteractionPalette{
			Accent: "#7D56F4", Surface: "#302B48", Text: "#F4F1FF", Muted: "#AAA3C7",
			Danger: "#FF5F87", Warning: "#FFD75F", Success: "#5FFFA2",
		}
	}
}

func makeInteractionStyles(r *lipgloss.Renderer, p InteractionPalette) InteractionStyles {
	return InteractionStyles{
		Palette: p,
		Panel: r.NewStyle().
			BorderStyle(lipgloss.ThickBorder()).
			BorderLeft(true).
			BorderForeground(p.Accent).
			PaddingLeft(1),
		Title:    r.NewStyle().Foreground(p.Accent).Bold(true),
		Meta:     r.NewStyle().Foreground(p.Muted),
		Body:     r.NewStyle().Foreground(p.Text),
		Label:    r.NewStyle().Foreground(p.Muted).Bold(true),
		Muted:    r.NewStyle().Foreground(p.Muted),
		Input:    r.NewStyle().Foreground(p.Text).Background(p.Surface).Padding(0, 1),
		Key:      r.NewStyle().Foreground(p.Accent).Background(p.Surface).Bold(true).Padding(0, 1),
		Action:   r.NewStyle().Foreground(p.Text),
		Selected: r.NewStyle().Foreground(p.Surface).Background(p.Accent).Bold(true).Padding(0, 1),
		Danger:   r.NewStyle().Foreground(p.Danger).Bold(true),
		Warning:  r.NewStyle().Foreground(p.Warning).Bold(true),
		Info:     r.NewStyle().Foreground(p.Accent).Bold(true),
		Success:  r.NewStyle().Foreground(p.Success).Bold(true),
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
	fmt.Println(lipgloss.JoinHorizontal(lipgloss.Center, outputHeader.String(), content))
}
