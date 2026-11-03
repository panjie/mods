package cli

import (
	"strings"

	"charm.land/bubbles/v2/textarea"
	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
)

// promptArea gives the multiline editor a horizontally scrolling view when
// wrapping is disabled. Logical newlines and editing bindings stay in textarea.
type promptArea struct {
	textarea.Model
	wrap  bool
	width int
}

func newPromptArea(wrap bool) promptArea {
	m := promptArea{Model: textarea.New(), wrap: wrap, width: chatDefaultWidth}
	m.MaxWidth = 0
	return m
}

func (m *promptArea) fit() {
	w := max(1, m.width)
	if !m.wrap {
		for _, line := range strings.Split(m.Value(), "\n") {
			w = max(w, ansi.StringWidth(line)+4)
		}
	}
	m.Model.SetWidth(w)
}

func (m *promptArea) SetWidth(width int)        { m.width = width; m.fit() }
func (m *promptArea) SetValue(value string)     { m.Model.SetValue(value); m.fit() }
func (m *promptArea) InsertString(value string) { m.Model.InsertString(value); m.fit() }

func (m promptArea) Update(msg tea.Msg) (promptArea, tea.Cmd) {
	var cmd tea.Cmd
	m.Model, cmd = m.Model.Update(msg)
	m.fit()
	return m, cmd
}

func (m promptArea) offset() int {
	if m.wrap {
		return 0
	}
	return max(0, m.LineInfo().CharOffset-max(1, m.width)+1)
}

func (m promptArea) View() string {
	view := m.Model.View()
	if m.wrap {
		return view
	}
	lines := strings.Split(view, "\n")
	offset := m.offset()
	for i, line := range lines {
		lines[i] = ansi.Cut(line, offset, offset+max(1, m.width))
	}
	return strings.Join(lines, "\n")
}

func (m promptArea) Cursor() *tea.Cursor {
	cursor := m.Model.Cursor()
	if cursor != nil {
		cursor.Position.X -= m.offset()
	}
	return cursor
}
