package app

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"unicode/utf8"

	"charm.land/bubbles/v2/key"
	"charm.land/bubbles/v2/textarea"
	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/panjie/mods/internal/secrets"
	toolregistry "github.com/panjie/mods/internal/tools"
	"github.com/panjie/mods/internal/ui"
)

var errUserInputUnavailable = errors.New("interactive user input is unavailable")

type userInputResult struct {
	value  string
	values []string
	form   map[string]string
	err    error
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

// userFormFieldState holds the per-field editor for one field of a kind=form
// request. Only the editor matching Field.Kind is populated.
type userFormFieldState struct {
	field    toolregistry.UserInputField
	text     textarea.Model
	secret   textinput.Model
	selected int
}

type userInputManager struct {
	mu          sync.Mutex
	ch          chan userInputItem
	sessionDone chan struct{}
	pending     bool
	item        *userInputItem
	text        textarea.Model
	secret      textinput.Model
	selected    int
	checked     []bool
	formFields  []userFormFieldState
	focus       int
	cfg         *Config
}

func newUserInputManager(cfg *Config) *userInputManager { return &userInputManager{cfg: cfg} }

func (u *userInputManager) available() bool {
	return u != nil && IsInputTTY() && u.cfg != nil && !u.cfg.Raw && !u.cfg.Minimal
}

func (u *userInputManager) snapshotSession() (chan userInputItem, <-chan struct{}) {
	u.mu.Lock()
	defer u.mu.Unlock()
	return u.ch, u.sessionDone
}

func (u *userInputManager) stopSessionLocked() {
	if u.sessionDone != nil {
		close(u.sessionDone)
	}
	u.ch = nil
	u.sessionDone = nil
}

func (u *userInputManager) startSession() tea.Cmd {
	if u == nil {
		return nil
	}
	u.mu.Lock()
	u.stopSessionLocked()
	u.ch = make(chan userInputItem, 4)
	u.sessionDone = make(chan struct{})
	ch := u.ch
	done := u.sessionDone
	u.mu.Unlock()
	return func() tea.Msg {
		select {
		case item := <-ch:
			return userInputStartMsg{item: item}
		case <-done:
			return nil
		}
	}
}

func (u *userInputManager) pollCmd() tea.Cmd {
	ch, done := u.snapshotSession()
	return func() tea.Msg {
		if ch == nil {
			return nil
		}
		select {
		case item := <-ch:
			return userInputStartMsg{item: item}
		case <-done:
			return nil
		}
	}
}

func (u *userInputManager) reset() {
	if u == nil {
		return
	}
	u.mu.Lock()
	u.stopSessionLocked()
	u.mu.Unlock()
	u.pending = false
	u.item = nil
	u.checked = nil
	u.formFields = nil
	u.focus = 0
}

func (u *userInputManager) isPending() bool { return u != nil && u.pending && u.item != nil }

func (u *userInputManager) request(ctx context.Context, req toolregistry.UserInputRequest) (toolregistry.UserInputResponse, error) {
	return u.requestWithDisplay(ctx, req, userInputDisplay{})
}

func (u *userInputManager) requestWithDisplay(ctx context.Context, req toolregistry.UserInputRequest, display userInputDisplay) (toolregistry.UserInputResponse, error) {
	if !u.available() {
		return toolregistry.UserInputResponse{}, errUserInputUnavailable
	}
	ch, done := u.snapshotSession()
	if ch == nil {
		return toolregistry.UserInputResponse{}, errUserInputUnavailable
	}
	resp := make(chan userInputResult, 1)
	select {
	case ch <- userInputItem{req: req, display: display, resp: resp}:
	case <-done:
		return toolregistry.UserInputResponse{}, errUserInputUnavailable
	case <-ctx.Done():
		return toolregistry.UserInputResponse{}, ctx.Err()
	}
	select {
	case result := <-resp:
		if result.err != nil {
			return toolregistry.UserInputResponse{}, result.err
		}
		if req.Kind == "multiselect" {
			return toolregistry.UserInputResponse{Answers: result.values}, nil
		}
		return toolregistry.UserInputResponse{Answer: result.value}, nil
	case <-done:
		return toolregistry.UserInputResponse{}, errUserInputUnavailable
	case <-ctx.Done():
		return toolregistry.UserInputResponse{}, ctx.Err()
	}
}

// requestForm runs a kind=form prompt and returns the raw per-field answers
// keyed by Field.Key. Secret post-processing (secrets.Put + ref swap) is the
// caller's responsibility, mirroring the single-secret flow.
func (u *userInputManager) requestForm(ctx context.Context, req toolregistry.UserInputRequest) (map[string]string, error) {
	if !u.available() {
		return nil, errUserInputUnavailable
	}
	ch, done := u.snapshotSession()
	if ch == nil {
		return nil, errUserInputUnavailable
	}
	resp := make(chan userInputResult, 1)
	select {
	case ch <- userInputItem{req: req, resp: resp}:
	case <-done:
		return nil, errUserInputUnavailable
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	select {
	case result := <-resp:
		if result.err != nil {
			return nil, result.err
		}
		return result.form, nil
	case <-done:
		return nil, errUserInputUnavailable
	case <-ctx.Done():
		return nil, ctx.Err()
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
	u.checked = nil
	u.focus = 0
	u.formFields = nil
	if item.req.Kind == "form" {
		u.formFields = make([]userFormFieldState, len(item.req.Fields))
		for i, f := range item.req.Fields {
			state := userFormFieldState{field: f}
			switch f.Kind {
			case "secret":
				state.secret = newTextinputSecret()
			case "select":
				state.selected = 0
			default:
				state.text = newTextareaSingleLine(f.Multiline)
			}
			u.formFields[i] = state
		}
		u.focusFormEditor(0)
		return
	}
	if item.req.Kind == "multiselect" {
		u.checked = make([]bool, len(item.req.Options))
		return
	}
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
	u.text = newTextareaSingleLine(false)
	u.text.Focus()
}

func newTextinputSecret() textinput.Model {
	ti := textinput.New()
	ti.EchoMode = textinput.EchoPassword
	ti.EchoCharacter = '•'
	ti.Placeholder = "Enter secret"
	ti.Prompt = ""
	ti.SetVirtualCursor(false)
	return ti
}

func newTextareaSingleLine(multiline bool) textarea.Model {
	ta := textarea.New()
	ta.Prompt = ""
	ta.ShowLineNumbers = false
	ta.SetHeight(1)
	ta.SetVirtualCursor(false)
	if multiline {
		ta.KeyMap.InsertNewline = key.NewBinding(key.WithKeys("ctrl+j"), key.WithHelp("ctrl+j", "new line"))
	} else {
		ta.KeyMap.InsertNewline = key.NewBinding(key.WithDisabled())
	}
	return ta
}

// focusFormEditor focuses the field at the given index and blurs any other
// editable editor. Select fields have no focused editor; their value is
// changed via arrow keys.
func (u *userInputManager) focusFormEditor(i int) {
	if i < 0 || i >= len(u.formFields) {
		return
	}
	for j := range u.formFields {
		switch u.formFields[j].field.Kind {
		case "secret":
			if j == i {
				u.formFields[j].secret.Focus()
			} else {
				u.formFields[j].secret.Blur()
			}
		case "text":
			if j == i {
				u.formFields[j].text.Focus()
			} else {
				u.formFields[j].text.Blur()
			}
		}
	}
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
	if req.Kind == "form" {
		return u.handleFormKey(msg)
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
	if req.Kind == "multiselect" {
		choiceCount := len(req.Options) + 1 // Include the virtual "Select all" row.
		switch msg.String() {
		case "up":
			u.selected = (u.selected - 1 + choiceCount) % choiceCount
		case "down", "tab":
			u.selected = (u.selected + 1) % choiceCount
		case " ", "space":
			if u.selected == 0 {
				u.toggleAll()
			} else {
				optionIndex := u.selected - 1
				u.checked[optionIndex] = !u.checked[optionIndex]
			}
		case "a", "A":
			u.toggleAll()
		case "enter":
			values := u.selectedValues(req.Options)
			if len(values) == 0 {
				return true, nil
			}
			return true, u.finish(userInputResult{values: values})
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

func (u *userInputManager) toggleAll() {
	selectAll := !u.allChecked()
	for i := range u.checked {
		u.checked[i] = selectAll
	}
}

func (u *userInputManager) allChecked() bool {
	if len(u.checked) == 0 {
		return false
	}
	for _, checked := range u.checked {
		if !checked {
			return false
		}
	}
	return true
}

func (u *userInputManager) anyChecked() bool {
	for _, checked := range u.checked {
		if checked {
			return true
		}
	}
	return false
}

func (u *userInputManager) selectedValues(options []string) []string {
	values := make([]string, 0, len(options))
	for i, option := range options {
		if i < len(u.checked) && u.checked[i] {
			values = append(values, option)
		}
	}
	return values
}

// handleFormKey dispatches keys for a kind=form prompt. Tab/Shift+Tab cycles
// focus between fields, Enter submits the whole form once every required
// field has a value, and other keys are routed to the focused field's editor.
func (u *userInputManager) handleFormKey(msg tea.KeyMsg) (bool, tea.Cmd) {
	switch msg.String() {
	case "tab":
		u.focus = (u.focus + 1) % len(u.formFields)
		u.focusFormEditor(u.focus)
		return true, nil
	case "shift+tab":
		u.focus = (u.focus - 1 + len(u.formFields)) % len(u.formFields)
		u.focusFormEditor(u.focus)
		return true, nil
	case "enter":
		values := make(map[string]string, len(u.formFields))
		for i := range u.formFields {
			v := u.fieldValue(i)
			if v == "" {
				// Block submit silently while a required field is empty.
				return true, nil
			}
			values[u.formFields[i].field.Key] = v
		}
		return true, u.finish(userInputResult{form: values})
	}
	state := &u.formFields[u.focus]
	var cmd tea.Cmd
	switch state.field.Kind {
	case "secret":
		state.secret, cmd = state.secret.Update(msg)
	case "select":
		switch msg.String() {
		case "left", "up":
			state.selected = (state.selected - 1 + len(state.field.Options)) % len(state.field.Options)
		case "right", "down":
			state.selected = (state.selected + 1) % len(state.field.Options)
		}
	default:
		state.text, cmd = state.text.Update(msg)
	}
	return true, cmd
}

// fieldValue returns the trimmed current value of the i-th form field, or the
// empty string if it is unfilled.
func (u *userInputManager) fieldValue(i int) string {
	if i < 0 || i >= len(u.formFields) {
		return ""
	}
	state := &u.formFields[i]
	switch state.field.Kind {
	case "secret":
		return strings.TrimSpace(state.secret.Value())
	case "select":
		if state.selected < 0 || state.selected >= len(state.field.Options) {
			return ""
		}
		return state.field.Options[state.selected]
	default:
		return strings.TrimSpace(state.text.Value())
	}
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
		if req.Kind == "form" {
			for _, f := range req.Fields {
				if f.Kind == "secret" {
					display.title = "Authentication required"
					display.tone = interactionToneDanger
					break
				}
			}
		}
		if req.Target.Tool != "" {
			display.rows = append(display.rows, interactionRow{Label: "Target", Value: req.Target.Tool + req.Target.Path})
		}
	}
	panel := interactionPanel{
		Title: display.title, Tone: display.tone, Headline: display.headline, Rows: display.rows,
	}
	innerWidth := interactionPanelInnerWidth(styles, width)
	if req.Kind == "form" {
		return u.renderFormBody(panel, innerWidth, styles, width)
	}
	switch req.Kind {
	case "select":
		options := make([]interactionAction, len(req.Options))
		for i, option := range req.Options {
			options[i] = interactionAction{Key: "›", Label: option, Selected: i == u.selected}
		}
		panel.Choices = options
		panel.Actions = []interactionAction{{Key: "↑ ↓/Tab", Label: "Navigate"}, {Key: "Enter", Label: "Select"}, {Key: "Esc", Label: "Cancel"}}
	case "multiselect":
		options := make([]interactionAction, len(req.Options)+1)
		selectAllCheckbox := "[ ]"
		if u.allChecked() {
			selectAllCheckbox = "[x]"
		} else if u.anyChecked() {
			selectAllCheckbox = "[-]"
		}
		options[0] = interactionAction{Key: selectAllCheckbox, Label: "Select all", Selected: u.selected == 0}
		for i, option := range req.Options {
			checkbox := "[ ]"
			if i < len(u.checked) && u.checked[i] {
				checkbox = "[x]"
			}
			options[i+1] = interactionAction{Key: checkbox, Label: option, Selected: i+1 == u.selected}
		}
		panel.Choices = options
		panel.StackChoices = true
		panel.Actions = []interactionAction{{Key: "↑ ↓/Tab", Label: "Navigate"}, {Key: "Space", Label: "Toggle"}, {Key: "A", Label: "Toggle all"}, {Key: "Enter", Label: "Submit"}, {Key: "Esc", Label: "Cancel"}}
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

const formLabelWidth = 12

// renderFormBody lays out every form field on its own body line. Only the
// focused text/secret field carries the real terminal cursor; select fields
// show their current option with angle-bracket markers instead.
func (u *userInputManager) renderFormBody(panel interactionPanel, innerWidth int, styles ui.InteractionStyles, width int) ui.CursorView {
	body := make([]string, 0, len(u.formFields))
	var cursor *tea.Cursor
	cursorBody := -1
	for i := range u.formFields {
		state := &u.formFields[i]
		focused := i == u.focus
		line, lineCursor := renderFormLineEdit(state, focused, innerWidth, styles)
		body = append(body, line)
		if lineCursor != nil {
			cursor = lineCursor
			cursorBody = i
		}
	}
	panel.Body = body
	panel.Cursor = cursor
	panel.CursorBody = cursorBody
	actions := []interactionAction{
		{Key: "Tab/Shift+Tab", Label: "Move"},
		{Key: "Enter", Label: "Submit"},
		{Key: "Esc", Label: "Cancel"},
	}
	if len(u.formFields) > 0 && u.formFields[u.focus].field.Kind == "select" {
		actions = append([]interactionAction{{Key: "← →", Label: "Change"}}, actions...)
	}
	panel.Actions = actions
	return ui.RenderInteractionPanelView(styles, width, panel)
}

// renderFormLineEdit renders one form field row. Focused text/secret fields
// return a real terminal cursor; everything else returns nil.
func renderFormLineEdit(state *userFormFieldState, focused bool, innerWidth int, styles ui.InteractionStyles) (string, *tea.Cursor) {
	label := padFormLabel(state.field.Label, formLabelWidth)
	prefix := label + " "
	switch state.field.Kind {
	case "secret":
		if focused {
			prefix = focusedFormPrefix(state.field.Label, styles.Input)
			prefixWidth := lipgloss.Width(prefix)
			contentWidth := max(1, innerWidth-prefixWidth-styles.Input.GetHorizontalFrameSize())
			state.secret.SetWidth(max(1, contentWidth-1))
			view := ui.NewCursorView(state.secret.View(), state.secret.Cursor()).
				InStyle(styles.Input).
				Translate(prefixWidth, 0)
			return prefix + view.Content, view.Cursor
		}
		return prefix + styles.Muted.Render(renderMaskedValue(state.secret.Value())), nil
	case "select":
		current := ""
		if len(state.field.Options) > 0 && state.selected >= 0 && state.selected < len(state.field.Options) {
			current = state.field.Options[state.selected]
		}
		if focused {
			return prefix + styles.Input.Render("› "+current+"  ← →"), nil
		}
		return prefix + styles.Muted.Render(current), nil
	default: // text
		if focused {
			prefix = focusedFormPrefix(state.field.Label, styles.Input)
			prefixWidth := lipgloss.Width(prefix)
			contentWidth := max(1, innerWidth-prefixWidth-styles.Input.GetHorizontalFrameSize())
			state.text.SetWidth(contentWidth)
			view := ui.NewCursorView(state.text.View(), state.text.Cursor()).
				InStyle(styles.Input).
				Translate(prefixWidth, 0)
			return prefix + view.Content, view.Cursor
		}
		val := strings.TrimSpace(state.text.Value())
		if val == "" {
			val = state.field.Placeholder
		}
		if val == "" {
			return prefix + styles.Muted.Render(""), nil
		}
		return prefix + styles.Muted.Render(val), nil
	}
}

func focusedFormPrefix(label string, inputStyle lipgloss.Style) string {
	normalWidth := lipgloss.Width(padFormLabel(label, formLabelWidth)) + 1
	inputLeftFrame := inputStyle.GetMarginLeft() + inputStyle.GetBorderLeftSize() + inputStyle.GetPaddingLeft()
	width := normalWidth - inputLeftFrame
	if width <= 0 {
		return ""
	}
	const marker = "›"
	labelWidth := lipgloss.Width(label)
	markerWidth := lipgloss.Width(marker)
	if labelWidth+markerWidth > width {
		return padFormLabel(label, width)
	}
	return label + strings.Repeat(" ", width-labelWidth-markerWidth) + marker
}

func renderMaskedValue(value string) string {
	if value == "" {
		return ""
	}
	return strings.Repeat("•", utf8.RuneCountInString(value))
}

func padFormLabel(label string, width int) string {
	n := lipgloss.Width(label)
	if n >= width {
		return label
	}
	return label + strings.Repeat(" ", width-n)
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
	if req.Kind == "form" {
		return m.handleFormInput(ctx, req)
	}
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

// handleFormInput validates every secret target up front, runs the form UI,
// then stores each secret via secrets.Put and assembles the keyed response.
func (m *Mods) handleFormInput(ctx context.Context, req toolregistry.UserInputRequest) (toolregistry.UserInputResponse, error) {
	if m.Config.Plan {
		for _, f := range req.Fields {
			if f.Kind == "secret" {
				return toolregistry.UserInputResponse{}, fmt.Errorf("secrets cannot be requested during plan mode")
			}
		}
	}
	for _, f := range req.Fields {
		if f.Kind != "secret" {
			continue
		}
		tool, ok := m.currentToolRegistry.Tool(f.Target.Tool)
		if !ok || (tool.Kind != toolregistry.ToolKindMCP && tool.Kind != toolregistry.ToolKindShell) {
			return toolregistry.UserInputResponse{}, fmt.Errorf("secret target must be an available MCP or shell tool")
		}
	}
	values, err := m.userInput.requestForm(ctx, req)
	if err != nil {
		return toolregistry.UserInputResponse{}, err
	}
	form := make(map[string]toolregistry.FieldResponse, len(req.Fields))
	for _, f := range req.Fields {
		v := values[f.Key]
		if f.Kind == "secret" {
			ref, err := m.secrets.Put(v, secrets.Target{Tool: f.Target.Tool, Path: f.Target.Path})
			if err != nil {
				return toolregistry.UserInputResponse{}, err
			}
			form[f.Key] = toolregistry.FieldResponse{SecretRef: ref}
		} else {
			form[f.Key] = toolregistry.FieldResponse{Answer: v}
		}
	}
	return toolregistry.UserInputResponse{Form: form}, nil
}
