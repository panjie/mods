package huh

import (
	"testing"

	tea "charm.land/bubbletea/v2"
)

func initializedCursorForm(t *testing.T, layout Layout, fields ...Field) *Form {
	t.Helper()
	form := NewForm(NewGroup(fields...)).WithLayout(layout).WithWidth(60)
	return batchUpdate(form, form.Init()).(*Form)
}

func TestInputAndTextRealCursor(t *testing.T) {
	input := NewInput().Title("Name")
	form := initializedCursorForm(t, LayoutDefault, input)
	requireCursor(t, form.Cursor())
	input.textinput.SetValue("中文")
	input.textinput.CursorEnd()
	if form.Cursor().X <= 0 {
		t.Fatalf("expected cursor X > 0, got %d", form.Cursor().X)
	}

	text := NewText().Title("Message").Lines(3)
	form = initializedCursorForm(t, LayoutDefault, text)
	text.textarea.SetValue("第一行\n第二行")
	text.textarea.CursorEnd()
	requireCursor(t, form.Cursor())
	if form.Cursor().Y <= 0 {
		t.Fatalf("expected cursor Y > 0, got %d", form.Cursor().Y)
	}
}

func TestFilterCursorOnlyWhileFiltering(t *testing.T) {
	field := NewSelect[string]().Title("Pick").Options(NewOptions("one", "two")...)
	form := initializedCursorForm(t, LayoutDefault, field)
	requireNoCursor(t, form.Cursor())
	field.setFiltering(true)
	_ = field.filter.Focus()
	requireCursor(t, form.Cursor())
	field.setFiltering(false)
	requireNoCursor(t, form.Cursor())
}

func TestBuiltInLayoutsTranslateCursor(t *testing.T) {
	for name, layout := range map[string]Layout{
		"default": LayoutDefault,
		"stack":   LayoutStack,
		"columns": LayoutColumns(2),
		"grid":    LayoutGrid(1, 2),
	} {
		t.Run(name, func(t *testing.T) {
			form := NewForm(
				NewGroup(NewInput().Title("First")),
				NewGroup(NewInput().Title("Second")),
			).WithLayout(layout).WithWidth(80)
			form = batchUpdate(form, form.Init()).(*Form)
			requireCursor(t, form.Cursor())
		})
	}
}

func TestCursorHiddenAfterCompletion(t *testing.T) {
	form := initializedCursorForm(t, LayoutDefault, NewInput().Title("Name"))
	form.State = StateCompleted
	requireNoCursor(t, form.Cursor())
}

func TestCursorTracksWideTextAndMovement(t *testing.T) {
	input := NewInput().Title("Name")
	form := initializedCursorForm(t, LayoutDefault, input)
	input.textinput.SetValue("中文ab")
	input.textinput.CursorEnd()
	end := form.Cursor()
	requireCursor(t, end)
	_, _ = input.Update(tea.KeyPressMsg{Code: tea.KeyLeft})
	left := form.Cursor()
	requireCursor(t, left)
	if left.X >= end.X {
		t.Fatalf("expected cursor to move left: before=%d after=%d", end.X, left.X)
	}
}

func requireCursor(t *testing.T, cursor *tea.Cursor) {
	t.Helper()
	if cursor == nil {
		t.Fatal("expected real cursor")
	}
}

func requireNoCursor(t *testing.T, cursor *tea.Cursor) {
	t.Helper()
	if cursor != nil {
		t.Fatalf("expected hidden cursor, got %+v", cursor)
	}
}
