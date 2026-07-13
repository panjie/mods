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
	"github.com/muesli/reflow/wordwrap"
	"github.com/muesli/termenv"
	"github.com/panjie/mods/internal/proto"
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

type browserState int

const (
	stateBrowsing browserState = iota
	stateViewing
	stateConfirm
)

// --- styles ---------------------------------------------------------------
//
// The data semantics (ID / muted meta / relative time) reuse StdoutStyles so
// the browser reads as part of mods. The chrome (title bar, footer, overlay)
// uses a dedicated palette anchored on the mods brand purple (#6C50FF) with a
// pink accent for marks and a warm red for destructive actions.

type browserStyles struct {
	titleBg, countBadge, markedBadge, status, markGlyph, selectedRow, markedRow,
	cursor, dangerTitle, confirmBox, empty, viewerTitle, viewerRule,
	roleUser, roleAssistant, roleSystem lipgloss.Style
}

func makeBrowserStyles(isDark bool) browserStyles {
	lightDark := lipgloss.LightDark(isDark)
	return browserStyles{
		titleBg: lipgloss.NewStyle().
			Background(lipgloss.Color("#6C50FF")).
			Foreground(lipgloss.Color("#FFFFFF")).
			Bold(true),

		countBadge: lipgloss.NewStyle().
			Background(lipgloss.Color("#4A3B9F")).
			Foreground(lipgloss.Color("#E0DDFF")).
			Bold(true),

		markedBadge: lipgloss.NewStyle().
			Foreground(lightDark(lipgloss.Color("#C0268E"), lipgloss.Color("#FF87D7"))).
			Bold(true),

		status: lipgloss.NewStyle().
			Foreground(lightDark(lipgloss.Color("#00B594"), lipgloss.Color("#3EECF0"))).
			Bold(true),

		markGlyph: lipgloss.NewStyle().
			Foreground(lightDark(lipgloss.Color("#C0268E"), lipgloss.Color("#FF87D7"))).
			Bold(true),

		selectedRow: lipgloss.NewStyle().
			Background(lightDark(lipgloss.Color("#EFEBFE"), lipgloss.Color("#241F3D"))),

		markedRow: lipgloss.NewStyle().
			Background(lightDark(lipgloss.Color("#FBF0F8"), lipgloss.Color("#2A1E29"))),

		cursor: lipgloss.NewStyle().
			Foreground(lightDark(lipgloss.Color("#6C50FF"), lipgloss.Color("#9D86FF"))).
			Bold(true),

		dangerTitle: lipgloss.NewStyle().
			Background(lipgloss.Color("#FF5F87")).
			Foreground(lipgloss.Color("#FFFFFF")).
			Bold(true),

		confirmBox: lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(lightDark(lipgloss.Color("#C0268E"), lipgloss.Color("#FF5F87"))).
			Padding(0, 2),

		empty: lipgloss.NewStyle().
			Foreground(lightDark(lipgloss.Color("#757575"), lipgloss.Color("#8A8A8A"))).
			Italic(true),

		viewerTitle: lipgloss.NewStyle().Bold(true),
		viewerRule: lipgloss.NewStyle().
			Foreground(lightDark(lipgloss.Color("#E0E0E0"), lipgloss.Color("#333333"))),

		roleUser: lipgloss.NewStyle().
			Foreground(lightDark(lipgloss.Color("#6C50FF"), lipgloss.Color("#9D86FF"))).
			Bold(true),
		roleAssistant: lipgloss.NewStyle().
			Foreground(lightDark(lipgloss.Color("#00B594"), lipgloss.Color("#3EECF0"))).
			Bold(true),
		roleSystem: lipgloss.NewStyle().
			Foreground(lightDark(lipgloss.Color("#757575"), lipgloss.Color("#8A8A8A"))).
			Italic(true),
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

func (d *convDelegate) Height() int                             { return 1 }
func (d *convDelegate) Spacing() int                            { return 0 }
func (d *convDelegate) Update(_ tea.Msg, _ *list.Model) tea.Cmd { return nil }

func (d *convDelegate) Render(w io.Writer, m list.Model, index int, item list.Item) {
	ci, ok := item.(convItem)
	if !ok {
		return
	}
	styles := StdoutStyles()
	width := m.Width()
	selected := index == m.Index()
	marked := d.b.marks[ci.conv.ID]

	// Cursor / mark column.
	cursor := " "
	if selected {
		cursor = d.b.styles.cursor.Render("❯")
	}
	glyph := " "
	if marked {
		glyph = d.b.styles.markGlyph.Render("◉")
	}

	id := styles.ShaHash.Render(ci.conv.ID[:ShortIDLength])

	// Secondary meta: model · api.
	var metaParts []string
	if ci.conv.Model != nil && *ci.conv.Model != "" {
		metaParts = append(metaParts, *ci.conv.Model)
	}
	if ci.conv.API != nil && *ci.conv.API != "" {
		metaParts = append(metaParts, *ci.conv.API)
	}
	meta := ""
	if len(metaParts) > 0 {
		meta = styles.Comment.Render("  · " + strings.Join(metaParts, " · "))
	}

	timeStr := styles.Timeago.Render(timeago.Of(ci.conv.UpdatedAt))

	// Layout:  cursor glyph id  TITLE  meta   <pad>   timeago
	// Reserve room for the title, truncating it so the row never wraps.
	fixedLeft := lipgloss.Width(cursor) + 1 + lipgloss.Width(glyph) + 1 +
		lipgloss.Width(id) + 2
	fixedRight := lipgloss.Width(meta) + lipgloss.Width(timeStr) + 1
	titleMax := width - fixedLeft - fixedRight
	title := ci.conv.Title
	if titleMax < 1 {
		titleMax = 1
	}
	if len([]rune(title)) > titleMax {
		title = truncateRune(title, titleMax)
	}
	if title == "" {
		title = "(untitled)"
	}
	titleStyle := styles.SessionList
	if selected {
		titleStyle = titleStyle.Bold(true)
	}
	titleRendered := titleStyle.Render(title)

	left := cursor + glyph + " " + id + "  " + titleRendered + meta
	pad := width - lipgloss.Width(left) - lipgloss.Width(timeStr)
	if pad < 1 {
		pad = 1
	}
	line := left + strings.Repeat(" ", pad) + timeStr

	switch {
	case selected:
		line = d.b.styles.selectedRow.Render(line)
	case marked:
		line = d.b.styles.markedRow.Render(line)
	}
	_, _ = io.WriteString(w, line)
}

// --- async messages ------------------------------------------------------

type loadedContentMsg struct {
	id      string
	content string
	err     error
}

type deletedMsg struct {
	count int
	err   error
	fresh []Session
}

// --- model ---------------------------------------------------------------

type browserModel struct {
	db     *DB
	width  int
	height int

	list     list.Model
	delegate *convDelegate

	viewport      viewport.Model
	viewing       convItem
	viewerHeader  string
	rawContent    string
	viewerContent string
	viewerLoaded  bool
	viewErr       string

	marks map[string]bool

	state          browserState
	confirmTargets []convItem

	statusMsg string // transient feedback, cleared on the next key
	styles    browserStyles
}

func newBrowserModel(sessions []Session) *browserModel {
	items := make([]list.Item, 0, len(sessions))
	for _, c := range sessions {
		items = append(items, convItem{conv: c})
	}

	m := &browserModel{
		db:    db,
		marks: map[string]bool{},
		state: stateBrowsing,
		width: 80, height: 24,
		styles: makeBrowserStyles(true),
	}
	delegate := &convDelegate{b: m}
	m.delegate = delegate
	m.list = list.New(items, delegate, m.width, m.height-browserTitleHeight-browserFooterHeight)
	configureList(&m.list)
	m.viewport = viewport.New(viewport.WithWidth(m.width), viewport.WithHeight(m.height-browserTitleHeight-browserFooterHeight))
	return m
}

// configureList strips the bubbles/list chrome (we render our own) and
// rebinds the keymap so single-letter navigation keys don't collide with the
// browser's actions (notably "d" for delete and "c" for copy).
func configureList(l *list.Model) {
	l.SetShowTitle(false)
	l.SetShowFilter(true)
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
		m.list.Styles = list.DefaultStyles(msg.IsDark())
		m.list.FilterInput.SetVirtualCursor(false)
		return m, nil
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		m.list.SetSize(m.width, m.bodyHeight())
		headerH := lipgloss.Height(m.viewerHeader)
		m.viewport.SetWidth(m.width)
		m.viewport.SetHeight(max(1, m.height-headerH-browserFooterHeight))
		if m.rawContent != "" && m.state == stateViewing {
			m.viewerContent = renderTranscript(m.rawContent, m.width)
			m.viewport.SetContent(m.viewerContent)
		}
		return m, nil

	case loadedContentMsg:
		return m.handleLoaded(msg)

	case deletedMsg:
		return m.handleDeleted(msg)

	case tea.KeyMsg:
		// ctrl+c always quits, regardless of sub-state.
		if msg.String() == "ctrl+c" {
			return m, tea.Quit
		}
		// Any key dismisses a transient status message.
		if m.statusMsg != "" {
			m.statusMsg = ""
		}
		switch m.state {
		case stateConfirm:
			return m.updateConfirm(msg)
		case stateViewing:
			return m.updateViewer(msg)
		default:
			return m.updateBrowsing(msg)
		}
	}

	// Non-key messages (spinner ticks, etc.) are forwarded to the list while
	// browsing so its built-in components keep working.
	if m.state == stateBrowsing {
		nl, cmd := m.list.Update(msg)
		m.list = nl
		return m, cmd
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
		case "enter":
			return m.openViewer()
		case "d":
			if ci, ok := m.focused(); ok {
				return m.startConfirm([]convItem{ci})
			}
		case "D":
			return m.startConfirm(m.markedOrFocused())
		case "c":
			return m.copyFocused()
		case " ", "space":
			return m.toggleMark()
		}
	}

	nl, cmd := m.list.Update(msg)
	m.list = nl
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

func (m *browserModel) copyFocused() (tea.Model, tea.Cmd) {
	ci, ok := m.focused()
	if !ok {
		return m, nil
	}
	_ = clipboard.WriteAll(ci.conv.ID)
	termenv.Copy(ci.conv.ID)
	m.statusMsg = "copied " + ci.conv.ID
	return m, nil
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
	m.viewerContent = ""
	m.rawContent = ""
	m.viewerLoaded = false
	m.viewerHeader = m.buildViewerHeader(ci)
	headerH := lipgloss.Height(m.viewerHeader)
	m.viewport = viewport.New(viewport.WithWidth(m.width), viewport.WithHeight(max(1, m.height-headerH-browserFooterHeight)))
	return m, m.loadContent(ci.conv.ID)
}

func (m *browserModel) closeViewer() {
	m.state = stateBrowsing
	m.viewing = convItem{}
	m.viewerHeader = ""
	m.viewerContent = ""
	m.rawContent = ""
	m.viewerLoaded = false
	m.viewErr = ""
}

func (m *browserModel) loadContent(id string) tea.Cmd {
	return func() tea.Msg {
		var msgs []proto.Message
		if err := m.db.ReadMessages(id, &msgs); err != nil {
			return loadedContentMsg{id: id, err: err}
		}
		return loadedContentMsg{id: id, content: proto.Session(msgs).String()}
	}
}

func (m *browserModel) handleLoaded(msg loadedContentMsg) (tea.Model, tea.Cmd) {
	m.viewerLoaded = true
	if msg.err != nil {
		m.viewErr = msg.err.Error()
		m.viewerContent = ""
		return m, nil
	}
	m.rawContent = msg.content
	m.viewerContent = renderTranscript(msg.content, m.width)
	m.viewport.SetContent(m.viewerContent)
	m.viewport.GotoTop()
	m.viewErr = ""
	return m, nil
}

func (m *browserModel) updateViewer(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "q", "esc":
		m.closeViewer()
		return m, nil
	case "j", "down":
		m.viewport.ScrollDown(1)
		return m, nil
	case "k", "up":
		m.viewport.ScrollUp(1)
		return m, nil
	case "pgdown", "f":
		m.viewport.PageDown()
		return m, nil
	case "pgup", "b":
		m.viewport.PageUp()
		return m, nil
	case "g":
		m.viewport.GotoTop()
		return m, nil
	case "G":
		m.viewport.GotoBottom()
		return m, nil
	}
	nvp, cmd := m.viewport.Update(msg)
	m.viewport = nvp
	return m, cmd
}

func (m *browserModel) buildViewerHeader(ci convItem) string {
	styles := StdoutStyles()
	title := m.styles.viewerTitle.Render(ci.conv.Title)
	id := styles.ShaHash.Render(ci.conv.ID)

	var parts []string
	if ci.conv.Model != nil && *ci.conv.Model != "" {
		parts = append(parts, *ci.conv.Model)
	}
	if ci.conv.API != nil && *ci.conv.API != "" {
		parts = append(parts, *ci.conv.API)
	}
	parts = append(parts, ci.conv.UpdatedAt.Format("2006-01-02 15:04"))
	meta := styles.Comment.Render(strings.Join(parts, "  ·  "))

	line1 := title + "  " + id
	rule := m.styles.viewerRule.Render(strings.Repeat("─", m.width))
	return lipgloss.JoinVertical(lipgloss.Left, line1, meta, rule)
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
	view := tea.NewView(content)
	view.AltScreen = true
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
	}
	return view
}

func (m *browserModel) viewList() string {
	title := m.viewTitle()
	footer := m.viewFooter()
	m.list.SetSize(m.width, m.bodyHeight())
	body := m.list.View()
	if len(m.list.Items()) == 0 {
		body = m.emptyState(m.width, m.bodyHeight())
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
			StdoutStyles().Comment.Render("press esc to go back")
		body = lipgloss.Place(m.width, viewportH, lipgloss.Center, lipgloss.Center, hint)
	case !m.viewerLoaded:
		body = lipgloss.Place(m.width, viewportH, lipgloss.Center, lipgloss.Center,
			m.styles.empty.Render("Loading…"))
	case m.viewerContent == "":
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
		right = m.styles.markedBadge.Render(fmt.Sprintf("◉ %d marked", len(m.marks)))
	}

	bar := left
	if right != "" {
		gap := m.width - lipgloss.Width(left) - lipgloss.Width(right)
		if gap < 1 {
			gap = 1
		}
		bar = left + strings.Repeat(" ", gap) + right
	}
	return m.styles.titleBg.Width(m.width).Render(bar)
}

func (m *browserModel) viewFooter() string {
	var pairs [][2]string
	switch m.state {
	case stateViewing:
		total := m.viewport.TotalLineCount()
		pct := int(m.viewport.ScrollPercent() * 100) //nolint:mnd
		pos := fmt.Sprintf("%d%% of %d lines", pct, total)
		pairs = [][2]string{
			{"↑↓/jk", "scroll"},
			{"g/G", "top/bottom"},
			{"q / esc", "back"},
			{pos, ""},
		}
	case stateConfirm:
		pairs = [][2]string{
			{"y", "confirm"},
			{"n / esc", "cancel"},
		}
	default:
		pairs = [][2]string{
			{"↑↓/jk", "move"},
			{"/", "filter"},
			{"enter", "view"},
			{"space", "mark"},
			{"d", "delete"},
			{"D", "delete marked"},
			{"c", "copy"},
			{"q", "quit"},
		}
	}
	return renderHelp(m.width, pairs)
}

func (m *browserModel) viewConfirmPanel() string {
	styles := StdoutStyles()
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
		rows = append(rows, fmt.Sprintf("  %s  %s", id, truncateRune(t.conv.Title, titleMax)))
	}
	body := strings.Join(rows, "\n")
	note := styles.Comment.Render("this cannot be undone")
	actions := styles.Flag.Render("y") + " " + styles.Comment.Render("confirm") +
		"    " + styles.Flag.Render("n / esc") + " " + styles.Comment.Render("cancel")

	content := head + "\n" + body + "\n\n" + note + "\n\n" + actions
	return m.styles.confirmBox.Render(content)
}

// --- helpers -------------------------------------------------------------

func renderHelp(width int, pairs [][2]string) string {
	styles := StdoutStyles()
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
	if w := lipgloss.Width(s); w < width {
		s += strings.Repeat(" ", width-w)
	}
	return s
}

func (m *browserModel) emptyState(width, height int) string {
	msg := "No sessions.\n\nPress q to exit."
	return lipgloss.Place(width, height, lipgloss.Center, lipgloss.Center,
		m.styles.empty.Render(msg))
}

// renderTranscript colorizes the role prefixes that proto.Session emits
// and word-wraps to the viewport width for comfortable reading. It is kept
// lightweight on purpose: a faithful, readable transcript beats a heavy
// markdown pass here.
func renderTranscript(raw string, width int) string {
	if width < 10 { //nolint:mnd
		width = 10
	}
	s := raw
	styles := makeBrowserStyles(true)
	s = strings.ReplaceAll(s, "**System**: ", styles.roleSystem.Render("system")+": ")
	s = strings.ReplaceAll(s, "**User**: ", styles.roleUser.Render("user")+": ")
	s = strings.ReplaceAll(s, "**Assistant**: ", styles.roleAssistant.Render("assistant")+": ")
	return wordwrap.String(s, width)
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

func plural(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}

// --- launcher ------------------------------------------------------------

// runSessionBrowser starts the interactive browser over the supplied
// sessions. It is only invoked on a fully interactive TTY.
func runSessionBrowser(sessions []Session) error {
	program := tea.NewProgram(
		newBrowserModel(sessions),
		tea.WithOutput(os.Stderr),
	)
	if _, err := program.Run(); err != nil {
		return err
	}
	return nil
}
