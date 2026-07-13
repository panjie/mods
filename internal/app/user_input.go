package app

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"

	"charm.land/bubbles/v2/key"
	"charm.land/bubbles/v2/textarea"
	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"github.com/panjie/mods/internal/secrets"
	toolregistry "github.com/panjie/mods/internal/tools"
	"github.com/panjie/mods/internal/ui"
)

var errUserInputUnavailable = errors.New("interactive user input is unavailable")

type userInputResult struct {
	value string
	err   error
}

type userInputItem struct {
	req     toolregistry.UserInputRequest
	display userInputDisplay
	resp    chan userInputResult
}

type userInputDisplay struct {
	title    string
	tone     interactionTone
	headline string
	rows     []interactionRow
}

type userInputManager struct {
	mu       sync.Mutex
	ch       chan userInputItem
	pending  bool
	item     *userInputItem
	text     textarea.Model
	secret   textinput.Model
	selected int
	cfg      *Config
}

func newUserInputManager(cfg *Config) *userInputManager { return &userInputManager{cfg: cfg} }

func (u *userInputManager) available() bool {
	return u != nil && IsInputTTY() && u.cfg != nil && !u.cfg.Raw && !u.cfg.Minimal
}

func (u *userInputManager) snapshotChan() chan userInputItem {
	u.mu.Lock()
	defer u.mu.Unlock()
	return u.ch
}

func (u *userInputManager) startSession() tea.Cmd {
	if u == nil {
		return nil
	}
	u.mu.Lock()
	u.ch = make(chan userInputItem, 4)
	ch := u.ch
	u.mu.Unlock()
	return func() tea.Msg {
		item, ok := <-ch
		if !ok {
			return nil
		}
		return userInputStartMsg{item: item}
	}
}

func (u *userInputManager) pollCmd() tea.Cmd {
	ch := u.snapshotChan()
	return func() tea.Msg {
		if ch == nil {
			return nil
		}
		item, ok := <-ch
		if !ok {
			return nil
		}
		return userInputStartMsg{item: item}
	}
}

func (u *userInputManager) reset() {
	if u == nil {
		return
	}
	u.mu.Lock()
	if u.ch != nil {
		close(u.ch)
	}
	u.ch = nil
	u.mu.Unlock()
	u.pending = false
	u.item = nil
}

func (u *userInputManager) isPending() bool { return u != nil && u.pending && u.item != nil }

func (u *userInputManager) request(ctx context.Context, req toolregistry.UserInputRequest) (toolregistry.UserInputResponse, error) {
	return u.requestWithDisplay(ctx, req, userInputDisplay{})
}

func (u *userInputManager) requestWithDisplay(ctx context.Context, req toolregistry.UserInputRequest, display userInputDisplay) (toolregistry.UserInputResponse, error) {
	if !u.available() {
		return toolregistry.UserInputResponse{}, errUserInputUnavailable
	}
	ch := u.snapshotChan()
	if ch == nil {
		return toolregistry.UserInputResponse{}, errUserInputUnavailable
	}
	resp := make(chan userInputResult, 1)
	select {
	case ch <- userInputItem{req: req, display: display, resp: resp}:
	case <-ctx.Done():
		return toolregistry.UserInputResponse{}, ctx.Err()
	}
	select {
	case result := <-resp:
		if result.err != nil {
			return toolregistry.UserInputResponse{}, result.err
		}
		return toolregistry.UserInputResponse{Answer: result.value}, nil
	case <-ctx.Done():
		return toolregistry.UserInputResponse{}, ctx.Err()
	}
}

func (u *userInputManager) handleStartMsg(msg userInputStartMsg) {
	if u == nil {
		return
	}
	u.pending = true
	item := msg.item
	u.item = &item
	u.selected = 0
	if item.req.Kind == "secret" {
		u.secret = textinput.New()
		u.secret.EchoMode = textinput.EchoPassword
		u.secret.EchoCharacter = '•'
		u.secret.Placeholder = "Enter secret"
		u.secret.Prompt = ""
		u.secret.SetVirtualCursor(false)
		u.secret.Focus()
		return
	}
	u.text = textarea.New()
	u.text.Prompt = ""
	u.text.ShowLineNumbers = false
	u.text.SetHeight(1)
	u.text.SetVirtualCursor(false)
	u.text.KeyMap.InsertNewline = key.NewBinding(key.WithKeys("ctrl+j"), key.WithHelp("ctrl+j", "new line"))
	u.text.Focus()
}

func (u *userInputManager) finish(result userInputResult) tea.Cmd {
	u.item.resp <- result
	u.pending = false
	u.item = nil
	return u.pollCmd()
}

func (u *userInputManager) handleKey(msg tea.KeyMsg) (bool, tea.Cmd) {
	if !u.isPending() {
		return false, nil
	}
	req := u.item.req
	if msg.String() == "esc" {
		return true, u.finish(userInputResult{err: fmt.Errorf("user canceled input")})
	}
	if msg.String() == "ctrl+c" {
		return false, nil
	}
	if req.Kind == "select" {
		switch msg.String() {
		case "left", "up":
			u.selected = (u.selected - 1 + len(req.Options)) % len(req.Options)
		case "right", "down", "tab":
			u.selected = (u.selected + 1) % len(req.Options)
		case "enter":
			return true, u.finish(userInputResult{value: req.Options[u.selected]})
		}
		return true, nil
	}
	if msg.String() == "enter" {
		value := strings.TrimSpace(u.text.Value())
		if req.Kind == "secret" {
			value = strings.TrimSpace(u.secret.Value())
		}
		if value == "" {
			return true, nil
		}
		return true, u.finish(userInputResult{value: value})
	}
	var cmd tea.Cmd
	if req.Kind == "secret" {
		u.secret, cmd = u.secret.Update(msg)
	} else {
		u.text, cmd = u.text.Update(msg)
	}
	return true, cmd
}

func (u *userInputManager) render(width int, styles ui.InteractionStyles) string {
	return u.renderView(width, styles).Content
}

func (u *userInputManager) renderView(width int, styles ui.InteractionStyles) ui.CursorView {
	if !u.isPending() {
		return ui.CursorView{}
	}
	if width <= 0 {
		width = 80
	}
	req := u.item.req
	display := u.item.display
	if display.title == "" {
		display.title = "Input required"
		display.tone = interactionToneInfo
		display.headline = req.Question
		if req.Kind == "secret" {
			display.title = "Authentication required"
			display.tone = interactionToneDanger
		}
		if req.Target.Tool != "" {
			display.rows = append(display.rows, interactionRow{Label: "Target", Value: req.Target.Tool + req.Target.Path})
		}
	}
	panel := interactionPanel{
		Title: display.title, Tone: display.tone, Headline: display.headline, Rows: display.rows,
	}
	innerWidth := interactionPanelInnerWidth(styles, width)
	switch req.Kind {
	case "select":
		options := make([]interactionAction, len(req.Options))
		for i, option := range req.Options {
			options[i] = interactionAction{Key: "›", Label: option, Selected: i == u.selected}
		}
		panel.Choices = options
		panel.Actions = []interactionAction{{Key: "↑ ↓/Tab", Label: "Navigate"}, {Key: "Enter", Label: "Select"}, {Key: "Esc", Label: "Cancel"}}
	case "secret":
		contentWidth := max(1, innerWidth-styles.Input.GetHorizontalFrameSize()-2)
		// textinput renders an additional cursor cell when the value is
		// non-empty and the cursor is at the end. Reserve that cell so typing
		// cannot wrap the input row and push the action row downward.
		u.secret.SetWidth(max(1, contentWidth-1))
		input := ui.NewCursorView("› "+u.secret.View(), u.secret.Cursor()).Translate(2, 0).InStyle(styles.Input)
		panel.Body = []string{input.Content}
		panel.Cursor = input.Cursor
		panel.Actions = []interactionAction{{Key: "Enter", Label: "Submit"}, {Key: "Esc", Label: "Cancel"}}
	default:
		contentWidth := max(1, innerWidth-styles.Input.GetHorizontalFrameSize()-2)
		u.text.SetWidth(contentWidth)
		input := ui.NewCursorView("› "+u.text.View(), u.text.Cursor()).Translate(2, 0).InStyle(styles.Input)
		panel.Body = []string{input.Content}
		panel.Cursor = input.Cursor
		panel.Actions = []interactionAction{{Key: "Enter", Label: "Submit"}, {Key: "Ctrl+J", Label: "New line"}, {Key: "Esc", Label: "Cancel"}}
	}
	return ui.RenderInteractionPanelView(styles, width, panel)
}

func (m *Mods) handleSudoPrompt(ctx context.Context, prompt, command string) (string, error) {
	question := strings.TrimSpace(prompt)
	if question == "" {
		question = "sudo password"
	}
	resp, err := m.userInput.requestWithDisplay(ctx, toolregistry.UserInputRequest{
		Question: question,
		Kind:     "secret",
	}, userInputDisplay{
		title:    "Authentication required",
		tone:     interactionToneDanger,
		headline: "sudo needs elevated privileges",
		rows:     []interactionRow{{Label: "Command", Value: command}},
	})
	if err != nil {
		return "", err
	}
	return resp.Answer, nil
}

func (m *Mods) handleUserInput(ctx context.Context, req toolregistry.UserInputRequest) (toolregistry.UserInputResponse, error) {
	if req.Kind == "secret" {
		if m.Config.Plan {
			return toolregistry.UserInputResponse{}, fmt.Errorf("secrets cannot be requested during plan mode")
		}
		tool, ok := m.currentToolRegistry.Tool(req.Target.Tool)
		if !ok || (tool.Kind != toolregistry.ToolKindMCP && tool.Kind != toolregistry.ToolKindShell) {
			return toolregistry.UserInputResponse{}, fmt.Errorf("secret target must be an available MCP or shell tool")
		}
	}
	resp, err := m.userInput.request(ctx, req)
	if err != nil {
		return resp, err
	}
	if req.Kind != "secret" {
		return resp, nil
	}
	ref, err := m.secrets.Put(resp.Answer, secrets.Target{Tool: req.Target.Tool, Path: req.Target.Path})
	if err != nil {
		return toolregistry.UserInputResponse{}, err
	}
	return toolregistry.UserInputResponse{SecretRef: ref}, nil
}
