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
	"github.com/charmbracelet/x/ansi"
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
	missingKey  string
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
	u.missingKey = ""
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
				state.text.Placeholder = f.Placeholder
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
	if item.req.Kind == "select" {
		u.checked = make([]bool, len(item.req.Options))
		if len(u.checked) > 0 {
			u.checked[0] = true
		}
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
	// The default cursor-line style paints a solid background across the
	// whole line; interaction panels style inputs themselves, so drop it.
	styles := ta.Styles()
	styles.Focused.CursorLine = lipgloss.NewStyle()
	styles.Blurred.CursorLine = lipgloss.NewStyle()
	ta.SetStyles(styles)
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
		n := len(req.Options)
		switch msg.String() {
		case "up":
			u.selected = (u.selected - 1 + n) % n
		case "down", "tab":
			u.selected = (u.selected + 1) % n
		case " ", "space":
			if len(u.checked) == n {
				for i := range u.checked {
					u.checked[i] = i == u.selected
				}
			}
		case "enter":
			for i, checked := range u.checked {
				if checked {
					return true, u.finish(userInputResult{value: req.Options[i]})
				}
			}
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

// choiceRows renders the option list as one row per line. Radio rows use
// "(•)"/"( )" markers (single choice); checkbox rows use "[x]"/"[ ]". When
// includeSelectAll is set, a virtual "Select all" row is prepended and
// u.selected is offset accordingly (multiselect only). With the
// nerd-font-glyphs setting enabled, radio and checkbox rows use single-cell
// Nerd Font glyphs instead.
func (u *userInputManager) choiceRows(includeSelectAll, radio bool) []interactionAction {
	nerd := u.cfg != nil && u.cfg.NerdFontGlyphs
	offset := 0
	capacity := len(u.item.req.Options)
	if includeSelectAll {
		offset = 1
		capacity++
	}
	options := make([]interactionAction, 0, capacity)
	if includeSelectAll {
		selectAllCheckbox := "[ ]"
		if u.allChecked() {
			selectAllCheckbox = "[x]"
		} else if u.anyChecked() {
			selectAllCheckbox = "[-]"
		}
		if nerd {
			selectAllCheckbox = ui.NerdCheckOff
			if u.allChecked() {
				selectAllCheckbox = ui.NerdCheckOn
			} else if u.anyChecked() {
				selectAllCheckbox = ui.NerdCheckMid
			}
		}
		options = append(options, interactionAction{Key: selectAllCheckbox, Label: "Select all", Selected: u.selected == 0})
	}
	for i, option := range u.item.req.Options {
		on, off := "(•)", "( )"
		if !radio {
			on, off = "[x]", "[ ]"
		}
		if nerd {
			if radio {
				on, off = ui.NerdRadioOn, ui.NerdRadioOff
			} else {
				on, off = ui.NerdCheckOn, ui.NerdCheckOff
			}
		}
		marker := off
		if i < len(u.checked) && u.checked[i] {
			marker = on
		}
		options = append(options, interactionAction{Key: marker, Label: option, Selected: i+offset == u.selected})
	}
	return options
}

// handleFormKey dispatches keys for a kind=form prompt. Tab/Shift+Tab cycles
// focus between fields, Enter submits the whole form once every required
// field has a value (flagging the first empty field until the next keypress),
// and other keys are routed to the focused field's editor.
func (u *userInputManager) handleFormKey(msg tea.KeyMsg) (bool, tea.Cmd) {
	switch msg.String() {
	case "tab":
		u.missingKey = ""
		u.focus = (u.focus + 1) % len(u.formFields)
		u.focusFormEditor(u.focus)
		return true, nil
	case "shift+tab":
		u.missingKey = ""
		u.focus = (u.focus - 1 + len(u.formFields)) % len(u.formFields)
		u.focusFormEditor(u.focus)
		return true, nil
	case "enter":
		values := make(map[string]string, len(u.formFields))
		for i := range u.formFields {
			v := u.fieldValue(i)
			if v == "" {
				// Flag the first empty field, move focus to it so the user
				// lands in the field blocking submission, and keep the flag
				// until the next keypress.
				u.missingKey = u.formFields[i].field.Key
				u.focus = i
				u.focusFormEditor(i)
				return true, nil
			}
			values[u.formFields[i].field.Key] = v
		}
		u.missingKey = ""
		return true, u.finish(userInputResult{form: values})
	}
	u.missingKey = ""
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
		display.title = "Input"
		display.tone = interactionToneInfo
		display.headline = req.Question
		if req.Kind == "secret" {
			display.title = "Credentials"
			display.tone = interactionToneDanger
		}
		if req.Kind == "form" {
			for _, f := range req.Fields {
				if f.Kind == "secret" {
					display.title = "Credentials"
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
	panel.Headline = clampInteractionHeadline(panel.Headline, innerWidth, 2)
	if req.Kind == "form" {
		return u.renderFormBody(panel, innerWidth, styles, width)
	}
	switch req.Kind {
	case "select":
		panel.Choices = u.choiceRows(false, true)
		panel.StackChoices = true
		panel.Actions = []interactionAction{{Key: "↑ ↓/Tab", Label: "Navigate"}, {Key: "Enter", Label: "Confirm"}, {Key: "Esc", Label: "Cancel"}}
	case "multiselect":
		panel.Choices = u.choiceRows(true, false)
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
		actions := []interactionAction{{Key: "Enter", Label: "Submit"}, {Key: "Esc", Label: "Cancel"}}
		if req.Multiline {
			actions = append([]interactionAction{{Key: "Ctrl+J", Label: "New line"}}, actions...)
		}
		panel.Actions = actions
	}
	return ui.RenderInteractionPanelView(styles, width, panel)
}

const (
	formLabelColMin = 8
	formLabelColMax = 16
	stackedIndent   = "  ›"
)

// clampInteractionHeadline bounds a model-supplied headline to maxLines
// wrapped lines with an ellipsis, so an overlong question cannot flood the
// panel (validation caps it too; this is defense in depth).
func clampInteractionHeadline(headline string, width, maxLines int) string {
	if headline == "" || width <= 1 || maxLines <= 0 {
		return headline
	}
	lines := strings.Split(ansi.Hardwrap(headline, width, false), "\n")
	if len(lines) <= maxLines {
		return headline
	}
	lines = lines[:maxLines]
	last := len(lines) - 1
	if lipgloss.Width(lines[last]) < width-1 {
		lines[last] += "…"
	} else {
		lines[last] = ansi.Truncate(lines[last], width-1, "…")
	}
	return strings.Join(lines, "\n")
}

// formLabelColumn sizes the inline label column from the actual labels,
// reserving room for the focus marker and the input frame so the widest
// label still renders inline. Clamped between formLabelColMin and
// formLabelColMax so short forms stay compact while wide (e.g. CJK) labels
// still get room.
func formLabelColumn(fields []userFormFieldState, inputStyle lipgloss.Style) int {
	maxWidth := 0
	for i := range fields {
		if w := lipgloss.Width(fields[i].field.Label); w > maxWidth {
			maxWidth = w
		}
	}
	inputLeftFrame := inputStyle.GetMarginLeft() + inputStyle.GetBorderLeftSize() + inputStyle.GetPaddingLeft()
	return min(max(maxWidth+1+inputLeftFrame, formLabelColMin), formLabelColMax)
}

// renderFormBody lays out every form field on one or two body lines. Labels
// wider than the label column stack the label on its own line above an
// indented editor so nothing hard-wraps mid-word. Only the focused text/secret
// field carries the real terminal cursor; select fields show their current
// option with angle-bracket markers instead.
func (u *userInputManager) renderFormBody(panel interactionPanel, innerWidth int, styles ui.InteractionStyles, width int) ui.CursorView {
	labelCol := formLabelColumn(u.formFields, styles.Input)
	body := make([]string, 0, len(u.formFields)*2)
	var cursor *tea.Cursor
	cursorBody := -1
	for i := range u.formFields {
		state := &u.formFields[i]
		focused := i == u.focus
		missing := u.missingKey != "" && state.field.Key == u.missingKey
		lines, lineCursor, cursorLine := renderFormLineEdit(state, focused, missing, innerWidth, labelCol, styles)
		if lineCursor != nil {
			cursor = lineCursor
			cursorBody = len(body) + cursorLine
		}
		body = append(body, lines...)
	}
	panel.Body = body
	panel.Cursor = cursor
	panel.CursorBody = cursorBody
	actions := []interactionAction{
		{Key: "Tab/Shift+Tab", Label: "Move"},
		{Key: "Enter", Label: "Submit"},
		{Key: "Esc", Label: "Cancel"},
	}
	if len(u.formFields) > 0 {
		field := u.formFields[u.focus].field
		if field.Kind == "select" {
			actions = append([]interactionAction{{Key: "← →", Label: "Change"}}, actions...)
		} else if field.Multiline {
			actions = append([]interactionAction{{Key: "Ctrl+J", Label: "New line"}}, actions...)
		}
	}
	panel.Actions = actions
	return ui.RenderInteractionPanelView(styles, width, panel)
}

// renderFormLineEdit renders one form field as one line (label inline) or two
// lines (label stacked above an indented editor). Focused text/secret fields
// return a real terminal cursor positioned on the editor line; everything
// else returns nil. lineOffset positions the cursor when the editor renders
// on a later line of the field's block.
func renderFormLineEdit(state *userFormFieldState, focused, missing bool, innerWidth, labelCol int, styles ui.InteractionStyles) ([]string, *tea.Cursor, int) {
	label := state.field.Label
	inputLeftFrame := styles.Input.GetMarginLeft() + styles.Input.GetBorderLeftSize() + styles.Input.GetPaddingLeft()
	if lipgloss.Width(label)+1 > labelCol+1-inputLeftFrame {
		return renderFormStackedField(state, focused, missing, innerWidth, styles)
	}
	prefix := padFormLabel(label, labelCol) + " "
	requiredSuffix := formRequiredSuffix(missing, styles)
	switch state.field.Kind {
	case "secret":
		if focused {
			return renderFormSecretEditor(&state.secret, focusedFormPrefix(label, labelCol, styles.Input), 0, innerWidth, styles, requiredSuffix)
		}
		value := renderMaskedValue(state.secret.Value())
		return []string{prefix + renderFormValueSlot(value, missing, styles)}, nil, -1
	case "select":
		current := formSelectCurrent(state)
		if focused {
			return []string{focusedFormPrefix(label, labelCol, styles.Input) + " " + formEditorStyle(styles).Render(current+"  ← →")}, nil, -1
		}
		return []string{prefix + styles.Muted.Render(current)}, nil, -1
	default: // text
		if focused {
			return renderFormTextEditor(&state.text, focusedFormPrefix(label, labelCol, styles.Input), 0, innerWidth, styles, requiredSuffix)
		}
		val := strings.TrimSpace(state.text.Value())
		if val == "" && !missing {
			val = state.field.Placeholder
		}
		return []string{prefix + renderFormValueSlot(val, missing, styles)}, nil, -1
	}
}

// renderFormStackedField renders a field whose label is wider than the label
// column: the label goes on its own line and the editor or value is indented
// below it, so long (e.g. CJK) labels never hard-wrap mid-word.
func renderFormStackedField(state *userFormFieldState, focused, missing bool, innerWidth int, styles ui.InteractionStyles) ([]string, *tea.Cursor, int) {
	labelLine := ansi.Truncate(state.field.Label, max(1, innerWidth), "…")
	requiredSuffix := formRequiredSuffix(missing, styles)
	switch state.field.Kind {
	case "secret":
		if focused {
			lines, cursor, lineOffset := renderFormSecretEditor(&state.secret, stackedIndent, 1, innerWidth, styles, requiredSuffix)
			return append([]string{labelLine}, lines...), cursor, lineOffset
		}
		value := renderMaskedValue(state.secret.Value())
		return []string{labelLine, "  " + renderFormValueSlot(value, missing, styles)}, nil, -1
	case "select":
		current := formSelectCurrent(state)
		if focused {
			return []string{labelLine, stackedIndent + " " + formEditorStyle(styles).Render(current+"  ← →")}, nil, -1
		}
		return []string{labelLine, "  " + styles.Muted.Render(current)}, nil, -1
	default: // text
		if focused {
			lines, cursor, lineOffset := renderFormTextEditor(&state.text, stackedIndent, 1, innerWidth, styles, requiredSuffix)
			return append([]string{labelLine}, lines...), cursor, lineOffset
		}
		val := strings.TrimSpace(state.text.Value())
		if val == "" && !missing {
			val = state.field.Placeholder
		}
		return []string{labelLine, "  " + renderFormValueSlot(val, missing, styles)}, nil, -1
	}
}

// renderFormTextEditor renders a focused single-line textarea after prefix on
// the field's lineOffset line and returns its real terminal cursor. A
// non-empty suffix (required marker) is appended inside the line budget.
func renderFormTextEditor(editor *textarea.Model, prefix string, lineOffset, innerWidth int, styles ui.InteractionStyles, suffix string) ([]string, *tea.Cursor, int) {
	prefixWidth := lipgloss.Width(prefix)
	suffixWidth := lipgloss.Width(suffix)
	contentWidth := max(1, innerWidth-prefixWidth-1-suffixWidth)
	editor.SetWidth(contentWidth)
	view := ui.NewCursorView(editor.View(), editor.Cursor()).
		InStyle(formEditorStyle(styles)).
		Translate(prefixWidth+1, lineOffset)
	return []string{prefix + " " + view.Content + suffix}, view.Cursor, lineOffset
}

// renderFormSecretEditor renders a focused masked textinput after prefix on
// the field's lineOffset line. One trailing cell is reserved for the input's
// virtual cursor so typing cannot wrap the line.
func renderFormSecretEditor(editor *textinput.Model, prefix string, lineOffset, innerWidth int, styles ui.InteractionStyles, suffix string) ([]string, *tea.Cursor, int) {
	prefixWidth := lipgloss.Width(prefix)
	suffixWidth := lipgloss.Width(suffix)
	contentWidth := max(1, innerWidth-prefixWidth-1-suffixWidth)
	editor.SetWidth(max(1, contentWidth-1))
	view := ui.NewCursorView(editor.View(), editor.Cursor()).
		InStyle(formEditorStyle(styles)).
		Translate(prefixWidth+1, lineOffset)
	return []string{prefix + " " + view.Content + suffix}, view.Cursor, lineOffset
}

// formRequiredSuffix builds the warning marker appended to the focused editor
// of the field currently blocking submission.
func formRequiredSuffix(missing bool, styles ui.InteractionStyles) string {
	if !missing {
		return ""
	}
	return " " + styles.Warning.Render("· required")
}

// renderFormValueSlot renders the unfocused value area of a form field. An
// empty flagged field points at itself with a warning marker instead of the
// usual muted value or placeholder.
func renderFormValueSlot(value string, missing bool, styles ui.InteractionStyles) string {
	if value == "" && missing {
		return styles.Warning.Render("· required")
	}
	return styles.Muted.Render(value)
}

func formSelectCurrent(state *userFormFieldState) string {
	if len(state.field.Options) > 0 && state.selected >= 0 && state.selected < len(state.field.Options) {
		return state.field.Options[state.selected]
	}
	return ""
}

// formEditorStyle renders focused form input content with the input
// foreground only, so the row keeps the panel's base background.
func formEditorStyle(styles ui.InteractionStyles) lipgloss.Style {
	return lipgloss.NewStyle().Foreground(styles.Input.GetForeground())
}

func focusedFormPrefix(label string, labelCol int, inputStyle lipgloss.Style) string {
	normalWidth := labelCol + 1
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
		title:    "Credentials",
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
