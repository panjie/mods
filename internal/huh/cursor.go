package huh

import (
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

type cursorProvider interface {
	Cursor() *tea.Cursor
}

func cloneCursor(cursor *tea.Cursor) *tea.Cursor {
	if cursor == nil {
		return nil
	}
	copy := *cursor
	return &copy
}

func translateCursor(cursor *tea.Cursor, x, y int) *tea.Cursor {
	cursor = cloneCursor(cursor)
	if cursor != nil {
		cursor.X += x
		cursor.Y += y
	}
	return cursor
}

func cursorAfterPrefix(cursor *tea.Cursor, prefix string) *tea.Cursor {
	cursor = cloneCursor(cursor)
	if cursor == nil || prefix == "" {
		return cursor
	}
	if idx := strings.LastIndexByte(prefix, '\n'); idx >= 0 {
		cursor.Y += strings.Count(prefix, "\n")
		cursor.X += lipgloss.Width(prefix[idx+1:])
	} else {
		cursor.X += lipgloss.Width(prefix)
	}
	return cursor
}

func cursorInStyle(cursor *tea.Cursor, style lipgloss.Style) *tea.Cursor {
	return translateCursor(cursor,
		style.GetMarginLeft()+style.GetBorderLeftSize()+style.GetPaddingLeft(),
		style.GetMarginTop()+style.GetBorderTopSize()+style.GetPaddingTop(),
	)
}
