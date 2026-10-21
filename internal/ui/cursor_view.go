package ui

import (
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

// CursorView keeps rendered content and its optional real terminal cursor
// together while parent layouts add frames or preceding content.
type CursorView struct {
	Content string
	Cursor  *tea.Cursor
}

func NewCursorView(content string, cursor *tea.Cursor) CursorView {
	return CursorView{Content: content, Cursor: cloneCursor(cursor)}
}

func (v CursorView) Translate(x, y int) CursorView {
	if v.Cursor != nil {
		v.Cursor.X += x
		v.Cursor.Y += y
	}
	return v
}

// InStyle renders a view through a Lip Gloss style and accounts for the
// style's left/top margin, border, and padding.
func (v CursorView) InStyle(style lipgloss.Style) CursorView {
	v.Content = style.Render(v.Content)
	if v.Cursor != nil {
		v.Cursor.X += style.GetMarginLeft() + style.GetBorderLeftSize() + style.GetPaddingLeft()
		v.Cursor.Y += style.GetMarginTop() + style.GetBorderTopSize() + style.GetPaddingTop()
	}
	return v
}

func (v CursorView) TeaView() tea.View {
	view := tea.NewView(v.Content)
	view.Cursor = cloneCursor(v.Cursor)
	return view
}

func cloneCursor(cursor *tea.Cursor) *tea.Cursor {
	if cursor == nil {
		return nil
	}
	copy := *cursor
	return &copy
}
