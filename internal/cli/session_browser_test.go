package cli

import (
	"testing"
	"time"

	"charm.land/bubbles/v2/list"
	tea "charm.land/bubbletea/v2"
	"github.com/stretchr/testify/require"
)

func TestSessionBrowserFilterUsesRealCursor(t *testing.T) {
	model := newBrowserModel([]Session{{
		ID:        "df31ae23ab8b75b5643c2f846c570997edc71333",
		Title:     "中文会话",
		UpdatedAt: time.Now(),
	}})
	_, _ = model.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	_, _ = model.Update(tea.KeyPressMsg{Code: '/', Text: "/"})
	require.Equal(t, list.Filtering, model.list.FilterState())
	view := model.View()
	require.True(t, view.AltScreen)
	require.NotNil(t, view.Cursor)

	before := view.Cursor.Position
	for _, r := range "中文" {
		_, _ = model.Update(tea.KeyPressMsg{Code: r, Text: string(r)})
	}
	after := model.View()
	require.NotNil(t, after.Cursor)
	require.NotEqual(t, before, after.Cursor.Position)

	_, _ = model.Update(tea.KeyPressMsg{Code: tea.KeyEsc})
	require.Nil(t, model.View().Cursor)
}

func TestSessionBrowserDoesNotQueryTerminalOutsideBubbleTea(t *testing.T) {
	saved := StdoutStyles
	t.Cleanup(func() { StdoutStyles = saved })
	StdoutStyles = func() Styles {
		t.Fatal("session browser called standalone terminal style detection")
		return Styles{}
	}

	model := newBrowserModel([]Session{{
		ID:        "df31ae23ab8b75b5643c2f846c570997edc71333",
		Title:     "terminal probe regression",
		UpdatedAt: time.Now(),
	}})
	_, _ = model.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	require.NotEmpty(t, model.View().Content)
	require.NotEmpty(t, model.buildViewerHeader(convItem{conv: Session{
		ID:        "df31ae23ab8b75b5643c2f846c570997edc71333",
		Title:     "terminal probe regression",
		UpdatedAt: time.Now(),
	}}))

	model.state = stateConfirm
	model.confirmTargets = []convItem{{conv: Session{
		ID:        "df31ae23ab8b75b5643c2f846c570997edc71333",
		Title:     "terminal probe regression",
		UpdatedAt: time.Now(),
	}}}
	require.NotEmpty(t, model.View().Content)

	model.state = stateViewing
	model.viewErr = "load failed"
	model.viewerHeader = "session\nmetadata\nrule"
	require.NotEmpty(t, model.View().Content)
}
