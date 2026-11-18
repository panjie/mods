package app

import (
	"errors"
	"testing"

	tea "charm.land/bubbletea/v2"
	toolregistry "github.com/panjie/mods/internal/tools"
	"github.com/stretchr/testify/require"
)

func newPasteTestModel(kind string) (*Mods, chan userInputResult) {
	u := newUserInputManager(&Config{})
	response := make(chan userInputResult, 1)
	u.handleStartMsg(userInputStartMsg{item: userInputItem{
		req: toolregistry.UserInputRequest{
			Question: "Password", Kind: kind,
			Fields: []toolregistry.UserInputField{
				{Key: "password", Label: "Password", Kind: "secret"},
				{Key: "username", Label: "Username", Kind: "text"},
			},
		},
		resp: response,
	}})
	return &Mods{Config: &Config{}, userInput: u}, response
}

func TestUserInputPasswordPaste(t *testing.T) {
	original := userInputReadClipboard
	t.Cleanup(func() { userInputReadClipboard = original })
	const password = "密碼-pasted-token"
	for _, kind := range []string{"secret", "form"} {
		for _, event := range []struct {
			name string
			msg  tea.Msg
		}{
			{"terminal", tea.PasteMsg{Content: password}},
			{"ctrl-v", tea.KeyPressMsg{Code: 'v', Mod: tea.ModCtrl}},
			{"ctrl-shift-v", tea.KeyPressMsg{Code: 'v', Mod: tea.ModCtrl | tea.ModShift}},
			{"shift-insert", tea.KeyPressMsg{Code: tea.KeyInsert, Mod: tea.ModShift}},
		} {
			t.Run(kind+"/"+event.name, func(t *testing.T) {
				reads := 0
				userInputReadClipboard = func() (string, error) {
					reads++
					return password, nil
				}
				m, response := newPasteTestModel(kind)
				u := m.userInput
				// Paste into the middle of an existing value, preserving both sides.
				m.Update(tea.KeyPressMsg{Code: 'x', Text: "xx"})
				m.Update(tea.KeyPressMsg{Code: tea.KeyLeft})
				_, cmd := m.Update(event.msg)
				if event.name != "terminal" {
					require.NotNil(t, cmd)
					m.Update(cmd())
					require.Equal(t, 1, reads)
				} else {
					require.Zero(t, reads, "terminal paste must use the supplied text")
				}
				require.True(t, u.isPending(), "paste must not submit the password")
				view := u.render(100, makeStyles(true).Interaction)
				require.NotContains(t, view, password)
				require.Contains(t, view, "•")
				require.Contains(t, view, "Paste")
				if kind == "form" {
					require.Empty(t, u.formFields[1].text.Value())
					m.Update(tea.KeyPressMsg{Code: tea.KeyTab})
					m.Update(tea.PasteMsg{Content: "alice"})
				}
				m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
				require.Len(t, response, 1, "Enter must submit the pasted value")
				got := <-response
				require.NoError(t, got.err)
				if kind == "form" {
					require.Equal(t, "x"+password+"x", got.form["password"])
					require.Equal(t, "alice", got.form["username"])
				} else {
					require.Equal(t, "x"+password+"x", got.value)
				}
			})
		}
	}
}

func TestUserInputPasswordPasteIgnoresStaleResults(t *testing.T) {
	original := userInputReadClipboard
	t.Cleanup(func() { userInputReadClipboard = original })
	userInputReadClipboard = func() (string, error) { return "private-token", nil }
	for _, change := range []string{"focus", "cancel", "new-request", "reset"} {
		t.Run(change, func(t *testing.T) {
			m, _ := newPasteTestModel("form")
			u := m.userInput
			_, cmd := m.Update(tea.KeyPressMsg{Code: 'v', Mod: tea.ModCtrl})
			require.NotNil(t, cmd)
			switch change {
			case "focus":
				m.Update(tea.KeyPressMsg{Code: tea.KeyTab})
			case "cancel":
				m.Update(tea.KeyPressMsg{Code: tea.KeyEsc})
			case "new-request":
				u.handleStartMsg(userInputStartMsg{item: userInputItem{
					req:  toolregistry.UserInputRequest{Question: "Public text", Kind: "text"},
					resp: make(chan userInputResult, 1),
				}})
			case "reset":
				u.reset()
			}
			m.Update(cmd())
			require.Empty(t, u.secret.Value())
			require.Empty(t, u.text.Value())
			for _, field := range u.formFields {
				require.Empty(t, field.secret.Value())
				require.Empty(t, field.text.Value())
			}
		})
	}
}

func TestUserInputPasswordPasteReadFailure(t *testing.T) {
	original := userInputReadClipboard
	t.Cleanup(func() { userInputReadClipboard = original })
	userInputReadClipboard = func() (string, error) {
		return "partial-secret", errors.New("clipboard unavailable")
	}
	m, _ := newPasteTestModel("secret")
	m.Update(tea.PasteMsg{Content: "existing"})
	_, cmd := m.Update(tea.KeyPressMsg{Code: 'v', Mod: tea.ModCtrl})
	require.NotNil(t, cmd)
	m.Update(cmd())
	require.Equal(t, "existing", m.userInput.secret.Value())
	require.True(t, m.userInput.isPending())
}
