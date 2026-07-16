package cli

import (
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/charmbracelet/x/ansi"
	"golang.org/x/term"
)

const (
	defaultListOutputWidth = 100
	maxListOutputWidth     = 120
	minFlexibleColumnWidth = 24
	listColumnGap          = "  "
)

type listColumn struct {
	Header   string
	Flexible bool
}

type listView struct {
	Title   string
	Meta    []string
	Columns []listColumn
	Rows    [][]string
	Empty   string
	Summary string
}

var listOutputWidth = func() int {
	width, _, err := term.GetSize(int(os.Stdout.Fd()))
	if err != nil || width <= 0 {
		return defaultListOutputWidth
	}
	return min(width, maxListOutputWidth)
}

func printListView(view listView) {
	styled := IsOutputTTY() && !config.Raw
	_, _ = io.WriteString(os.Stdout, renderListView(view, listOutputWidth(), styled))
}

func renderListView(view listView, width int, styled bool) string {
	if width <= 0 {
		width = defaultListOutputWidth
	}
	width = min(width, maxListOutputWidth)

	var sb strings.Builder
	writeListTitle(&sb, view.Title, styled)
	for _, line := range view.Meta {
		writeListMeta(&sb, line, styled)
	}
	if len(view.Meta) > 0 {
		sb.WriteByte('\n')
	}

	if len(view.Rows) == 0 {
		empty := view.Empty
		if empty == "" {
			empty = "No items found."
		}
		if styled {
			empty = StdoutStyles().Comment.Italic(true).Render(empty)
		}
		sb.WriteString(empty)
		sb.WriteString("\n\n")
	} else {
		widths, flexible, wide := listColumnWidths(view, width)
		if wide {
			renderWideListRows(&sb, view, widths, flexible, styled)
		} else {
			renderCompactListRows(&sb, view, widths, flexible, width, styled)
		}
		sb.WriteByte('\n')
	}

	summary := view.Summary
	if styled {
		summary = StdoutStyles().Comment.Render(summary)
	}
	sb.WriteString(summary)
	sb.WriteByte('\n')
	return sb.String()
}

func writeListTitle(sb *strings.Builder, title string, styled bool) {
	if styled {
		title = StdoutStyles().Flag.Bold(true).Render(title)
	}
	sb.WriteString(title)
	sb.WriteString("\n\n")
}

func writeListMeta(sb *strings.Builder, line string, styled bool) {
	if styled {
		line = StdoutStyles().Comment.Render(line)
	}
	sb.WriteString(line)
	sb.WriteByte('\n')
}

func listColumnWidths(view listView, outputWidth int) ([]int, int, bool) {
	widths := make([]int, len(view.Columns))
	flexible := -1
	for i, column := range view.Columns {
		widths[i] = ansi.StringWidth(column.Header)
		if column.Flexible {
			flexible = i
		}
	}
	for _, row := range view.Rows {
		for i := range min(len(row), len(widths)) {
			widths[i] = max(widths[i], ansi.StringWidth(row[i]))
		}
	}

	if flexible < 0 {
		return widths, flexible, true
	}

	fixedWidth := (len(widths) - 1) * len(listColumnGap)
	for i, columnWidth := range widths {
		if i != flexible {
			fixedWidth += columnWidth
		}
	}
	available := outputWidth - fixedWidth
	if available < minFlexibleColumnWidth {
		return widths, flexible, false
	}
	widths[flexible] = min(widths[flexible], available)
	return widths, flexible, true
}

func renderWideListRows(sb *strings.Builder, view listView, widths []int, flexible int, styled bool) {
	headings := make([]string, len(view.Columns))
	for i, column := range view.Columns {
		headings[i] = column.Header
	}
	writeListRow(sb, headings, widths, true, styled)

	for _, row := range view.Rows {
		wrapped := []string{""}
		if flexible >= 0 && flexible < len(row) {
			wrapped = wrapListText(row[flexible], widths[flexible])
		}
		for lineIndex, descriptionLine := range wrapped {
			line := make([]string, len(view.Columns))
			if lineIndex == 0 {
				copy(line, row)
			}
			if flexible >= 0 {
				line[flexible] = descriptionLine
			}
			writeListRow(sb, line, widths, false, styled)
		}
	}
}

func renderCompactListRows(
	sb *strings.Builder,
	view listView,
	widths []int,
	flexible int,
	outputWidth int,
	styled bool,
) {
	fixedHeadings := make([]string, 0, len(view.Columns)-1)
	fixedWidths := make([]int, 0, len(view.Columns)-1)
	for i, column := range view.Columns {
		if i == flexible {
			continue
		}
		fixedHeadings = append(fixedHeadings, column.Header)
		fixedWidths = append(fixedWidths, widths[i])
	}
	writeListRow(sb, fixedHeadings, fixedWidths, true, styled)

	descriptionWidth := max(1, outputWidth-2)
	for _, row := range view.Rows {
		fixedCells := make([]string, 0, len(row)-1)
		for i, cell := range row {
			if i != flexible {
				fixedCells = append(fixedCells, cell)
			}
		}
		writeListRow(sb, fixedCells, fixedWidths, false, styled)
		if flexible < 0 || flexible >= len(row) || row[flexible] == "" {
			continue
		}
		for _, line := range wrapListText(row[flexible], descriptionWidth) {
			sb.WriteString("  ")
			sb.WriteString(line)
			sb.WriteByte('\n')
		}
	}
}

func writeListRow(sb *strings.Builder, cells []string, widths []int, header, styled bool) {
	for i, cell := range cells {
		if i > 0 {
			sb.WriteString(listColumnGap)
		}
		if i < len(cells)-1 {
			cell = padListCell(cell, widths[i])
		}
		if styled {
			switch {
			case header:
				cell = StdoutStyles().Comment.Bold(true).Render(cell)
			case i == 0 && strings.TrimSpace(cell) != "":
				cell = StdoutStyles().AppName.Bold(true).Render(cell)
			}
		}
		sb.WriteString(cell)
	}
	sb.WriteByte('\n')
}

func padListCell(value string, width int) string {
	return value + strings.Repeat(" ", max(0, width-ansi.StringWidth(value)))
}

func wrapListText(value string, width int) []string {
	if value == "" {
		return []string{""}
	}
	return strings.Split(ansi.Wrap(value, max(1, width), " "), "\n")
}

func listCount(count int, singular, plural string) string {
	label := plural
	if count == 1 {
		label = singular
	}
	return fmt.Sprintf("%d %s", count, label)
}
