package cli

import (
	"fmt"
	"io"
	"os"
	"strings"

	"charm.land/bubbles/v2/key"
	"charm.land/bubbles/v2/list"
	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/atotto/clipboard"
	timeago "github.com/caarlos0/timea.go"
	"github.com/charmbracelet/x/ansi"
	"github.com/muesli/termenv"
	"github.com/panjie/mods/internal/proto"
	"github.com/panjie/mods/internal/ui"
)

// Session browser — an interactive TUI replacement for the old
// single-select picker. It lets users browse saved sessions, view a
// session's full transcript, copy an ID, and delete one or many sessions
// without leaving the interface.
//
// The browser is launched only on a fully interactive TTY (see
// listSessions); the machine-friendly tabular path (printList) is
// untouched.

const (
	browserTitleHeight  = 1
	browserFooterHeight = 1
)

var copyTextToClipboard = func(text string) {
	_ = clipboard.WriteAll(text)
	termenv.Copy(text)
}

type browserState int

const (
	stateBrowsing browserState = iota
	stateViewing
	stateConfirm
)

// --- styles ---------------------------------------------------------------
//
// Model-owned styles share the configured theme with chat and interaction
// panels. Background updates come from Bubble Tea, with no extra terminal I/O.

type browserStyles struct {
	titleBg, countBadge, markedBadge, status, markGlyph, selectedRow, markedRow,
	cursor, dangerTitle, confirmBox, empty, viewerTitle, viewerRule,
	roleUser, roleAssistant, roleSystem, highlight, selectedHighlight lipgloss.Style
}

func makeBrowserStyles(isDark bool) browserStyles {
	s := ui.MakeStylesWithTheme(config.Theme, isDark).Interaction
	return browserStyles{
		titleBg:           s.Title,
		countBadge:        s.Muted,
		markedBadge:       s.Info,
		status:            s.Muted,
		markGlyph:         s.Info,
		selectedRow:       lipgloss.NewStyle().Foreground(s.Palette.Text).Background(s.Palette.Surface),
		markedRow:         lipgloss.NewStyle(),
		cursor:            s.Info.Bold(true),
		dangerTitle:       s.Danger,
		confirmBox:        s.Panel.BorderForeground(s.Palette.Danger),
		empty:             s.Muted,
		viewerTitle:       s.Title,
		viewerRule:        s.Muted,
		roleUser:          s.Info.Bold(true),
		roleAssistant:     s.Title,
		roleSystem:        s.Muted,
		highlight:         s.Info.Underline(true),
		selectedHighlight: s.Selected.Padding(0),
	}
}

// --- list item ------------------------------------------------------------

// convItem adapts a Session to the bubbles/list.Item interface.
type convItem struct {
	conv Session
}

// FilterValue matches against title, id, model and api so the built-in "/"
// filter is useful for everything a user might search by.
func (i convItem) FilterValue() string {
	var sb strings.Builder
	sb.WriteString(i.conv.Title)
	sb.WriteByte(' ')
	sb.WriteString(i.conv.ID)
	if i.conv.Model != nil {
		sb.WriteByte(' ')
		sb.WriteString(*i.conv.Model)
	}
	if i.conv.API != nil {
		sb.WriteByte(' ')
		sb.WriteString(*i.conv.API)
	}
	return sb.String()
}

// --- delegate (row rendering) --------------------------------------------

// convDelegate renders each row and holds a back-pointer to the model so it
// can reflect live mark state. bubbles/list stores the delegate as an
// interface holding this pointer, so updates to the model are visible.
type convDelegate struct {
	b *browserModel
}

func (d *convDelegate) Height() int                             { return 2 }
func (d *convDelegate) Spacing() int                            { return 0 }
func (d *convDelegate) Update(_ tea.Msg, _ *list.Model) tea.Cmd { return nil }

func (d *convDelegate) Render(w io.Writer, m list.Model, index int, item list.Item) {
	ci, ok := item.(convItem)
	if !ok {
		return
	}
	styles := d.b.textStyles
	width := m.Width()
	selected := index == m.Index()
	marked := d.b.marks[ci.conv.ID]

	// Cursor / mark column.
	cursor := " "
	if selected {
		if d.b.nerdGlyphs {
			cursor = d.b.styles.cursor.Render(ui.NerdChevron)
		} else {
			cursor = d.b.styles.cursor.Render("❯")
		}
	}
	glyph := " "
	if marked {
		if d.b.nerdGlyphs {
			glyph = d.b.styles.markGlyph.Render(ui.NerdBookmark)
		} else {
			glyph = d.b.styles.markGlyph.Render("◉")
		}
	}

	title := ci.conv.Title
	if title == "" {
		title = "(untitled)"
	}
	title = plainTranscriptContent(title)
	title = strings.Join(strings.Fields(title), " ")
	titleStyle := styles.AppName
	if !selected {
		titleStyle = titleStyle.Bold(false)
	}
	first := cursor + glyph + " " + titleStyle.Render(ansi.Truncate(title, max(1, width-3), "…"))
	meta := timeago.Of(ci.conv.UpdatedAt)
	if ci.conv.Model != nil && *ci.conv.Model != "" {
		meta += " · " + *ci.conv.Model
	}
	if width >= 70 {
		meta += " · " + ci.conv.ID[:min(ShortIDLength, len(ci.conv.ID))]
	}
	second := "   " + styles.Comment.Render(ansi.Truncate(strings.Join(strings.Fields(plainTranscriptContent(meta)), " "), max(1, width-3), "…"))
	lines := []string{first, second}
	for i, line := range lines {
		line = ansi.Truncate(line, width, "…")
		if selected {
			line = d.b.styles.selectedRow.Width(width).Render(line)
		}
		lines[i] = line
	}
	_, _ = io.WriteString(w, strings.Join(lines, "\n"))
}

// --- async messages ------------------------------------------------------

type loadedContentMsg struct {
	id            string
	request       uint64
	approvalCount int
	messages      []proto.Message
	err           error
}

type deletedMsg struct {
	count int
	err   error
	fresh []Session
}

// --- model ---------------------------------------------------------------

type browserModel struct {
	helpOpen                bool
	helpOffset              int
	previewID               string
	previewRequest          uint64
	previewText, previewErr string
	previewLoaded           bool
	db                      *DB
	width                   int
	height                  int

	list     list.Model
	delegate *convDelegate

	viewport            viewport.Model
	viewing             convItem
	viewerHeader        string
	viewerDoc           transcriptDocument
	viewerLayout        transcriptLayout
	viewerSearch        viewerSearch
	viewerMsgs          []proto.Message
	viewerLoaded        bool
	viewerRequest       uint64
	viewerApprovalCount int
	viewErr             string

	marks map[string]bool

	nerdGlyphs bool

	state          browserState
	continueID     string
	confirmTargets []convItem

	statusMsg  string // transient feedback, cleared on the next key
	styles     browserStyles
	textStyles ui.Styles
}

func newBrowserModel(sessions []Session, nerdGlyphs bool) *browserModel {
	items := make([]list.Item, 0, len(sessions))
	for _, c := range sessions {
		items = append(items, convItem{conv: c})
	}

	isDark := ui.StderrIsDark()
	m := &browserModel{
		db:         db,
		marks:      map[string]bool{},
		nerdGlyphs: nerdGlyphs,
		state:      stateBrowsing,
		width:      80, height: 24,
		styles:     makeBrowserStyles(isDark),
		textStyles: ui.MakeStylesWithTheme(config.Theme, isDark),
	}
	delegate := &convDelegate{b: m}
	m.delegate = delegate
	m.list = list.New(items, delegate, m.width, m.height-browserTitleHeight-browserFooterHeight)
	configureList(&m.list)
	m.resetViewerViewport(m.height - browserTitleHeight - browserFooterHeight)
	return m
}

// configureList strips the bubbles/list chrome (we render our own) and
// rebinds the keymap so single-letter navigation keys don't collide with the
// browser's actions (notably "d" for delete and "c" for continue).
func configureList(l *list.Model) {
	l.SetShowTitle(false)
	l.SetShowFilter(false)
	l.SetShowStatusBar(false)
	l.SetShowPagination(false)
	l.SetShowHelp(false)
	l.SetFilteringEnabled(true)
	l.FilterInput.SetVirtualCursor(false)

	km := list.DefaultKeyMap()
	// Rebind the page-navigation keys: the defaults ("d","f","l","h","b","u")
	// collide with the browser's action vocabulary (d=delete) and read as
	// cluttered. Keep the discoverable arrow/pgup/pgdn/home/end set.
	km.PrevPage = key.NewBinding(key.WithKeys("pgup"), key.WithHelp("pgup", "prev page"))
	km.NextPage = key.NewBinding(key.WithKeys("pgdown"), key.WithHelp("pgdn", "next page"))
	km.GoToStart = key.NewBinding(key.WithKeys("home", "g"), key.WithHelp("g", "top"))
	km.GoToEnd = key.NewBinding(key.WithKeys("end", "G"), key.WithHelp("G", "bottom"))
	l.KeyMap = km
	// The browser owns quit semantics (q / esc / ctrl+c). Disabling the
	// list's built-in quit bindings also prevents "esc" from quitting when
	// the user only meant to clear an applied filter.
	l.DisableQuitKeybindings()
}

func (m *browserModel) Init() tea.Cmd { return tea.RequestBackgroundColor }

func (m *browserModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.BackgroundColorMsg:
		m.styles = makeBrowserStyles(msg.IsDark())
		m.textStyles = ui.MakeStylesWithTheme(config.Theme, msg.IsDark())
		m.list.Styles = list.DefaultStyles(msg.IsDark())
		m.list.FilterInput.SetVirtualCursor(false)
		m.applyViewerStyles()
		return m, nil
	case tea.WindowSizeMsg:
		anchor := m.captureViewerAnchor()
		m.width, m.height = msg.Width, msg.Height
		m.list.SetSize(m.width, m.bodyHeight())
		if m.state == stateViewing && m.viewing.conv.ID != "" {
			m.viewerHeader = m.buildViewerHeader(m.viewing)
		}
		headerH := lipgloss.Height(m.viewerHeader)
		m.viewport.SetWidth(m.width)
		m.viewport.SetHeight(max(1, m.height-headerH-browserFooterHeight))
		m.viewerSearch.resize(m.width)
		if m.state == stateViewing && m.viewerDoc.content != "" {
			m.rebuildViewerLayout()
			m.restoreViewerAnchor(anchor)
		}
		return m, m.refreshPreview()

	case previewLoadedMsg:
		m.receivePreview(msg)
		return m, nil
	case loadedContentMsg:
		return m.handleLoaded(msg)

	case deletedMsg:
		model, cmd := m.handleDeleted(msg)
		return model, tea.Batch(cmd, m.refreshPreview())

	case tea.MouseWheelMsg:
		if m.state == stateViewing {
			return m.updateViewerMouse(msg)
		}
		return m, nil

	case tea.KeyMsg:
		// ctrl+c always quits, regardless of sub-state.
		if msg.String() == "ctrl+c" {
			return m, tea.Quit
		}
		if m.helpOpen {
			switch msg.String() {
			case "esc", "q", "?":
				m.helpOpen = false
			case "down", "j":
				m.helpOffset++
			case "up", "k":
				m.helpOffset = max(0, m.helpOffset-1)
			}
			return m, nil
		}
		if msg.String() == "?" && m.state != stateConfirm && m.list.FilterState() != list.Filtering && !m.viewerSearch.active {
			m.helpOpen, m.helpOffset = true, 0
			return m, nil
		}
		// Any key dismisses a transient status message.
		if m.statusMsg != "" {
			m.statusMsg = ""
		}
		switch m.state {
		case stateConfirm:
			return m.updateConfirm(msg)
		case stateViewing:
			model, cmd := m.updateViewer(msg)
			return model, tea.Batch(cmd, m.refreshPreview())
		default:
			model, cmd := m.updateBrowsing(msg)
			return model, tea.Batch(cmd, m.refreshPreview())
		}
	}

	// Non-key messages (spinner ticks, etc.) are forwarded to the list while
	// browsing so its built-in components keep working.
	if m.state == stateBrowsing {
		nl, cmd := m.list.Update(msg)
		m.list = nl
		return m, tea.Batch(cmd, m.refreshPreview())
	}
	return m, nil
}

func (m *browserModel) bodyHeight() int {
	h := m.height - browserTitleHeight - browserFooterHeight
	if h < 1 {
		return 1
	}
	return h
}

// --- browsing ------------------------------------------------------------

func (m *browserModel) updateBrowsing(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	filtering := m.list.FilterState() == list.Filtering
	s := msg.String()

	// Quit / filter-clear keys. When the filter input is focused we defer to
	// the list so "q"/"esc" behave as the user expects (typed text / cancel).
	if !filtering {
		switch s {
		case "q":
			return m, tea.Quit
		case "esc":
			if m.list.FilterState() == list.FilterApplied {
				break // let the list clear the applied filter
			}
			return m, tea.Quit
		}
	}

	// Browser actions are only available when not typing into the filter.
	if !filtering {
		switch s {
		case "r":
			m.previewID = ""
			return m, m.refreshPreview()
		case "enter":
			return m.openViewer()
		case "d":
			if ci, ok := m.focused(); ok {
				return m.startConfirm([]convItem{ci})
			}
		case "D":
			return m.startConfirm(m.markedOrFocused())
		case "c":
			return m.continueFocused()
		case " ", "space":
			return m.toggleMark()
		}
	}

	nl, cmd := m.list.Update(msg)
	m.list = nl
	// An enabled but inactive filter bar otherwise leaves an empty title row.
	m.list.SetShowFilter(m.list.FilterState() == list.Filtering)
	return m, cmd
}

func (m *browserModel) focused() (convItem, bool) {
	sel := m.list.SelectedItem()
	if sel == nil {
		return convItem{}, false
	}
	ci, ok := sel.(convItem)
	return ci, ok
}

func (m *browserModel) markedOrFocused() []convItem {
	if len(m.marks) == 0 {
		if ci, ok := m.focused(); ok {
			return []convItem{ci}
		}
		return nil
	}
	// Preserve the on-screen order so the confirm overlay reads top-to-bottom.
	out := make([]convItem, 0, len(m.marks))
	for _, it := range m.list.Items() {
		ci, ok := it.(convItem)
		if !ok {
			continue
		}
		if m.marks[ci.conv.ID] {
			out = append(out, ci)
		}
	}
	return out
}

func (m *browserModel) toggleMark() (tea.Model, tea.Cmd) {
	ci, ok := m.focused()
	if !ok {
		return m, nil
	}
	id := ci.conv.ID
	if m.marks[id] {
		delete(m.marks, id)
		m.statusMsg = "unmarked " + id[:ShortIDLength]
	} else {
		m.marks[id] = true
		m.statusMsg = "marked " + id[:ShortIDLength]
	}
	// Advance to the next row so a user can mark (or unmark) many sessions in
	// quick succession, like a checkbox list. CursorDown clamps at the last
	// item, so this is a no-op when the focused row is already at the bottom.
	m.list.CursorDown()
	return m, nil
}

func (m *browserModel) continueFocused() (tea.Model, tea.Cmd) {
	ci, ok := m.focused()
	if !ok {
		return m, nil
	}
	m.continueID = ci.conv.ID
	return m, tea.Quit
}

func (m *browserModel) startConfirm(targets []convItem) (tea.Model, tea.Cmd) {
	if len(targets) == 0 {
		m.statusMsg = "nothing selected — press d to delete the focused item"
		return m, nil
	}
	m.confirmTargets = targets
	m.state = stateConfirm
	return m, nil
}

// --- viewing -------------------------------------------------------------

func (m *browserModel) openViewer() (tea.Model, tea.Cmd) {
	ci, ok := m.focused()
	if !ok {
		return m, nil
	}
	m.state = stateViewing
	m.viewing = ci
	m.viewErr = ""
	m.viewerDoc = transcriptDocument{}
	m.viewerLayout = transcriptLayout{}
	m.viewerSearch = newViewerSearch(m.width)
	m.viewerMsgs = nil
	m.viewerLoaded = false
	m.viewerRequest++
	m.viewerApprovalCount = 0
	m.viewerHeader = m.buildViewerHeader(ci)
	headerH := lipgloss.Height(m.viewerHeader)
	m.resetViewerViewport(m.height - headerH - browserFooterHeight)
	return m, m.loadContent(ci.conv.ID, m.viewerRequest)
}

func (m *browserModel) closeViewer() {
	m.previewID = ""
	m.state = stateBrowsing
	m.viewing = convItem{}
	m.viewerHeader = ""
	m.viewerDoc = transcriptDocument{}
	m.viewerLayout = transcriptLayout{}
	m.viewerSearch = viewerSearch{}
	m.viewerMsgs = nil
	m.viewerLoaded = false
	m.viewerApprovalCount = 0
	m.viewErr = ""
}

func (m *browserModel) loadContent(id string, request uint64) tea.Cmd {
	return func() tea.Msg {
		result := loadedContentMsg{id: id, request: request}
		var msgs []proto.Message
		if err := m.db.ReadMessages(id, &msgs); err != nil {
			result.err = err
			return result
		}
		rules, err := m.db.ApprovalRules(id)
		result.messages = msgs
		result.approvalCount = len(rules)
		result.err = err
		return result
	}
}

func (m *browserModel) handleLoaded(msg loadedContentMsg) (tea.Model, tea.Cmd) {
	if m.state != stateViewing || msg.id != m.viewing.conv.ID || msg.request != m.viewerRequest {
		return m, nil
	}
	m.viewerLoaded = true
	if msg.err != nil {
		m.viewErr = msg.err.Error()
		m.viewerDoc = transcriptDocument{}
		m.viewerMsgs = nil
		return m, nil
	}
	m.viewerMsgs = append([]proto.Message(nil), msg.messages...)
	m.viewerApprovalCount = msg.approvalCount
	m.viewErr = ""
	m.viewerHeader = m.buildViewerHeader(m.viewing)
	m.viewport.SetHeight(max(1, m.height-lipgloss.Height(m.viewerHeader)-browserFooterHeight))
	m.viewerDoc = buildTranscriptDocument(msg.messages)
	m.viewport.SetContent(m.viewerDoc.content)
	m.rebuildViewerLayout()
	m.viewport.SetYOffset(0)
	return m, nil
}

func (m *browserModel) updateViewer(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.viewerSearch.active {
		return m.updateViewerSearch(msg)
	}
	switch msg.String() {
	case "r":
		if m.viewErr != "" {
			return m.openViewer()
		}
		return m, nil
	case "q", "esc":
		m.closeViewer()
		return m, nil
	case "/":
		if m.viewerLoaded && m.viewerDoc.content != "" {
			return m, m.beginViewerSearch()
		}
		return m, nil
	case "n":
		m.moveViewerHighlight(1)
		return m, nil
	case "N":
		m.moveViewerHighlight(-1)
		return m, nil
	case "w":
		m.toggleViewerWrap()
		return m, nil
	case "c":
		m.continueID = m.viewing.conv.ID
		return m, tea.Quit
	case "C":
		return m.copyViewedSession()
	case "j", "down":
		m.scrollViewer(1)
		return m, nil
	case "k", "up":
		m.scrollViewer(-1)
		return m, nil
	case "pgdown", "f":
		m.scrollViewer(m.viewport.Height())
		return m, nil
	case "pgup", "b":
		m.scrollViewer(-m.viewport.Height())
		return m, nil
	case "g":
		m.viewport.SetYOffset(0)
		return m, nil
	case "G":
		m.viewport.SetYOffset(max(0, m.viewport.TotalLineCount()-m.viewport.Height()))
		return m, nil
	case "h", "left":
		if !m.viewport.SoftWrap {
			m.viewport.ScrollLeft(viewerHorizontalStep)
		}
		return m, nil
	case "l", "right":
		if !m.viewport.SoftWrap {
			m.viewport.ScrollRight(viewerHorizontalStep)
		}
		return m, nil
	}
	return m, nil
}

func (m *browserModel) copyViewedSession() (tea.Model, tea.Cmd) {
	if !m.viewerLoaded {
		m.statusMsg = "session is still loading"
		return m, nil
	}
	if m.viewErr != "" {
		m.statusMsg = "copy failed: session did not load"
		return m, nil
	}
	text := formatSessionCopy(m.viewing.conv, m.viewerMsgs, m.viewerApprovalCount)
	copyTextToClipboard(text)
	m.statusMsg = fmt.Sprintf("copied session (%d chars)", len([]rune(text)))
	return m, nil
}

func (m *browserModel) buildViewerHeader(ci convItem) string {
	styles := m.textStyles
	title := m.styles.viewerTitle.Render(ansi.Truncate(strings.Join(strings.Fields(plainTranscriptContent(ci.conv.Title)), " "), max(1, m.width), "…"))
	id := styles.ShaHash.Render(ci.conv.ID)

	var parts []string
	if ci.conv.Model != nil && *ci.conv.Model != "" {
		parts = append(parts, *ci.conv.Model)
	}
	if ci.conv.API != nil && *ci.conv.API != "" {
		parts = append(parts, *ci.conv.API)
	}
	parts = append(parts, ci.conv.UpdatedAt.Format("2006-01-02 15:04"))
	if m.viewerLoaded && m.viewErr == "" {
		parts = append(parts, fmt.Sprintf("%d messages · %d saved approval rules (not expanded)", len(m.viewerMsgs), m.viewerApprovalCount))
	}
	meta := styles.Comment.Render(strings.Join(parts, "  ·  "))

	line1 := title + "\n" + ansi.Truncate(id, max(1, m.width), "…")
	rule := m.styles.viewerRule.Render(strings.Repeat("─", m.width))
	return lipgloss.NewStyle().Width(max(1, m.width)).Render(lipgloss.JoinVertical(lipgloss.Left, line1, ansi.Truncate(meta, max(1, m.width), "…"), rule))
}

// --- confirm / delete ----------------------------------------------------

func (m *browserModel) updateConfirm(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "y", "Y", "enter":
		targets := m.confirmTargets
		m.confirmTargets = nil
		m.state = stateBrowsing
		m.statusMsg = fmt.Sprintf("deleting %d…", len(targets))
		return m, m.deleteCmd(targets)
	case "n", "N", "esc":
		m.confirmTargets = nil
		m.state = stateBrowsing
		return m, nil
	}
	return m, nil
}

func (m *browserModel) deleteCmd(targets []convItem) tea.Cmd {
	return func() tea.Msg {
		var firstErr error
		for _, t := range targets {
			if err := m.db.Delete(t.conv.ID); err != nil {
				if firstErr == nil {
					firstErr = err
				}
				continue
			}
			_ = removeLegacySessionFile(t.conv.ID) // best-effort cleanup
		}
		fresh, listErr := m.db.List()
		if listErr != nil && firstErr == nil {
			firstErr = listErr
		}
		return deletedMsg{count: len(targets), err: firstErr, fresh: fresh}
	}
}

func (m *browserModel) handleDeleted(msg deletedMsg) (tea.Model, tea.Cmd) {
	items := make([]list.Item, 0, len(msg.fresh))
	alive := make(map[string]bool, len(msg.fresh))
	for _, c := range msg.fresh {
		items = append(items, convItem{conv: c})
		alive[c.ID] = true
	}
	// Prune marks for sessions that no longer exist.
	for id := range m.marks {
		if !alive[id] {
			delete(m.marks, id)
		}
	}
	switch {
	case len(items) == 0:
		m.statusMsg = "no sessions left"
	case msg.err != nil:
		m.statusMsg = "delete failed: " + msg.err.Error()
	default:
		m.statusMsg = fmt.Sprintf("deleted %d session%s", msg.count, plural(msg.count))
	}
	return m, m.list.SetItems(items)
}

// --- view ----------------------------------------------------------------

func (m *browserModel) View() tea.View {
	if m.width == 0 {
		view := tea.NewView("Starting…")
		view.AltScreen = true
		return view
	}
	var content string
	switch m.state {
	case stateViewing:
		content = m.viewViewer()
	case stateConfirm:
		content = m.viewConfirm()
	default:
		content = m.viewList()
	}
	if m.helpOpen {
		content = m.viewKeyboardHelp()
	}
	view := tea.NewView(content)
	view.AltScreen = true
	if m.helpOpen {
		return view
	}
	if m.state == stateBrowsing && m.list.FilterState() == list.Filtering {
		if cursor := m.list.FilterInput.Cursor(); cursor != nil {
			copy := *cursor
			copy.X += m.list.Styles.TitleBar.GetMarginLeft() +
				m.list.Styles.TitleBar.GetBorderLeftSize() +
				m.list.Styles.TitleBar.GetPaddingLeft()
			copy.Y += browserTitleHeight +
				m.list.Styles.TitleBar.GetMarginTop() +
				m.list.Styles.TitleBar.GetBorderTopSize() +
				m.list.Styles.TitleBar.GetPaddingTop()
			view.Cursor = &copy
		}
	} else if m.state == stateViewing && m.viewerSearch.active {
		if cursor := m.viewerSearch.input.Cursor(); cursor != nil {
			copy := *cursor
			copy.Y += max(0, m.height-browserFooterHeight)
			view.Cursor = &copy
		}
	}
	return view
}

func (m *browserModel) viewList() string {
	title := m.viewTitle()
	footer := m.viewFooter()
	m.list.SetSize(m.listWidth(), m.bodyHeight())
	body := m.list.View()
	if len(m.list.Items()) == 0 {
		body = m.emptyState(m.width, m.bodyHeight())
	}
	if m.splitPreview() && len(m.list.Items()) > 0 {
		left := lipgloss.NewStyle().Width(m.listWidth()).Height(m.bodyHeight()).Render(body)
		divider := m.styles.viewerRule.Render(strings.TrimSuffix(strings.Repeat("│\n", m.bodyHeight()), "\n"))
		right := m.viewPreview(max(1, m.width-m.listWidth()-3), m.bodyHeight())
		body = lipgloss.JoinHorizontal(lipgloss.Top, left, lipgloss.NewStyle().Padding(0, 1).Render(divider), right)
	}
	return lipgloss.JoinVertical(lipgloss.Left, title, body, footer)
}

func (m *browserModel) viewConfirm() string {
	title := m.viewTitle()
	panel := m.viewConfirmPanel()
	bodyH := m.height - browserTitleHeight - lipgloss.Height(panel)
	if bodyH < 1 {
		bodyH = 1
	}
	m.list.SetSize(m.width, bodyH)
	body := m.list.View()
	if len(m.list.Items()) == 0 {
		body = m.emptyState(m.width, bodyH)
	}
	return lipgloss.JoinVertical(lipgloss.Left, title, body, panel)
}

func (m *browserModel) viewViewer() string {
	header := m.viewerHeader
	headerH := lipgloss.Height(header)
	viewportH := m.height - headerH - browserFooterHeight
	if viewportH < 1 {
		viewportH = 1
	}
	m.viewport.SetHeight(viewportH)

	var body string
	switch {
	case m.viewErr != "":
		hint := m.styles.dangerTitle.Render(" couldn't load session ") + "\n\n" +
			m.styles.empty.Render(m.viewErr) + "\n\n" +
			m.textStyles.Comment.Render("press r to retry · esc to go back")
		body = lipgloss.NewStyle().Width(m.width).MaxHeight(viewportH).Render(ansi.Wrap(hint, m.width, ""))
	case !m.viewerLoaded:
		body = lipgloss.Place(m.width, viewportH, lipgloss.Center, lipgloss.Center,
			m.styles.empty.Render("Loading…"))
	case m.viewerDoc.content == "":
		body = lipgloss.Place(m.width, viewportH, lipgloss.Center, lipgloss.Center,
			m.styles.empty.Render("This session has no messages."))
	default:
		body = m.viewport.View()
	}
	return lipgloss.JoinVertical(lipgloss.Left, header, body, m.viewFooter())
}

func (m *browserModel) viewTitle() string {
	left := " Sessions " +
		m.styles.countBadge.Render(fmt.Sprintf(" %d ", len(m.list.Items())))

	var right string
	switch {
	case m.statusMsg != "":
		right = m.styles.status.Render(m.statusMsg)
	case len(m.marks) > 0:
		markGlyph := "◉"
		if m.nerdGlyphs {
			markGlyph = ui.NerdBookmark
		}
		right = m.styles.markedBadge.Render(fmt.Sprintf("%s %d marked", markGlyph, len(m.marks)))
	}

	bar := left
	if right != "" {
		gap := m.width - lipgloss.Width(left) - lipgloss.Width(right)
		if gap < 1 {
			gap = 1
		}
		bar = left + strings.Repeat(" ", gap) + right
	}
	return m.styles.titleBg.Width(m.width).Render(ansi.Truncate(bar, m.width, "…"))
}

func (m *browserModel) viewFooter() string {
	var pairs [][2]string
	switch m.state {
	case stateViewing:
		if m.viewerSearch.active {
			return m.viewerSearch.view(m.width)
		}
		total := m.viewport.TotalLineCount()
		pct := int(m.viewport.ScrollPercent() * 100) //nolint:mnd
		pos := fmt.Sprintf("%d%% of %d lines", pct, total)
		pairs = [][2]string{
			{m.viewerSearch.summary(pos), ""},
			{"?", "keys"},
			{"esc", "back"},
			{"↑↓/jk", "scroll"},
			{"/", "find"},
			{"n/N", "match"},
			{"c", "continue"},
			{"C", "copy"},
			{"w", m.viewerWrapLabel()},
			{"q / esc", "back"},
		}
	case stateConfirm:
		pairs = [][2]string{
			{"y", "confirm"},
			{"n / esc", "cancel"},
		}
	default:
		pairs = [][2]string{
			{"?", "keys"},
			{"q", "quit"},
			{"↑↓/jk", "move"},
			{"/", "filter"},
			{"enter", "view"},
			{"space", "mark"},
			{"d", "delete"},
			{"D", "delete marked"},
			{"c", "continue"},
		}
	}
	return m.renderHelp(pairs)
}

func (m *browserModel) viewConfirmPanel() string {
	styles := m.textStyles
	n := len(m.confirmTargets)
	head := m.styles.dangerTitle.Render(fmt.Sprintf(" Delete %d session%s? ", n, plural(n)))

	const maxShow = 5 //nolint:mnd
	var rows []string
	for i, t := range m.confirmTargets {
		if i >= maxShow {
			rows = append(rows, "  "+styles.Comment.Render(
				fmt.Sprintf("…and %d more", n-maxShow)))
			break
		}
		id := styles.ShaHash.Render(t.conv.ID[:ShortIDLength])
		titleMax := m.width - 16 //nolint:mnd
		if titleMax < 8 {        //nolint:mnd
			titleMax = 8
		}
		rows = append(rows, fmt.Sprintf("  %s  %s", id, truncateDisplay(t.conv.Title, titleMax)))
	}
	body := strings.Join(rows, "\n")
	note := styles.Comment.Render("this cannot be undone")
	actions := styles.Flag.Render("y") + " " + styles.Comment.Render("confirm") +
		"    " + styles.Flag.Render("n / esc") + " " + styles.Comment.Render("cancel")

	content := head + "\n" + body + "\n\n" + note + "\n\n" + actions
	return m.styles.confirmBox.Render(content)
}

// --- helpers -------------------------------------------------------------

func (m *browserModel) renderHelp(pairs [][2]string) string {
	styles := m.textStyles
	var segs []string
	for _, p := range pairs {
		if p[0] == "" {
			continue
		}
		seg := styles.Flag.Render(p[0])
		if p[1] != "" {
			seg += " " + styles.Comment.Render(p[1])
		}
		segs = append(segs, seg)
	}
	s := strings.Join(segs, "   ")
	if m.width > 0 && lipgloss.Width(s) > m.width {
		s = ansi.Truncate(s, m.width, "…")
	}
	if w := lipgloss.Width(s); w < m.width {
		s += strings.Repeat(" ", m.width-w)
	}
	return s
}

func (m *browserModel) emptyState(width, height int) string {
	msg := "No sessions.\n\nPress q to exit."
	return lipgloss.Place(width, height, lipgloss.Center, lipgloss.Center,
		m.styles.empty.Render(msg))
}

func truncateRune(s string, max int) string {
	if max <= 0 {
		return ""
	}
	r := []rune(s)
	if len(r) <= max {
		return s
	}
	if max == 1 {
		return "…"
	}
	return string(r[:max-1]) + "…"
}

func truncateDisplay(s string, max int) string {
	if max <= 0 {
		return ""
	}
	return ansi.Truncate(s, max, "…")
}

func plural(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}

// --- launcher ------------------------------------------------------------

// runSessionBrowser starts the interactive browser over the supplied
// sessions. It is only invoked on a fully interactive TTY.
func runSessionBrowser(sessions []Session, nerdGlyphs bool) (string, error) {
	program := tea.NewProgram(
		newBrowserModel(sessions, nerdGlyphs),
		tea.WithOutput(os.Stderr),
	)
	result, err := program.Run()
	if err != nil {
		return "", err
	}
	return result.(*browserModel).continueID, nil
}
