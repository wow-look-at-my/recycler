package main

import (
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"path/filepath"
	"strconv"
	"strings"
	"time"
	"unicode"

	tea "charm.land/bubbletea/v2"
	"github.com/spf13/cobra"
	"github.com/wow-look-at-my/recycler"
	"github.com/wow-look-at-my/tml"
	"github.com/wow-look-at-my/tml/sema"
)

//go:embed ui
var uiFS embed.FS

// cellSep joins a row's cells for <Table>, which splits them again. Every printable delimiter is legal in a file name.
const cellSep = "\x1f"

var tuiCmd = &cobra.Command{
	Use:   "tui",
	Short: "Browse the recycle bin on a full screen",
	Long: `Open the recycle bin in a terminal interface: pick an item, read where it
came from, and restore it.

Running "recycler" with no arguments on a terminal opens the same interface.
Nothing here deletes anything: restoring is the only thing that takes an item
out of the bin.`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error { return runTUI() },
}

func init() {
	rootCmd.AddCommand(tuiCmd)
}

type model struct {
	view *tml.View

	items    []recycler.Item
	shown    []recycler.Item
	selected int
	offset   int

	filter    string
	filtering bool

	asking bool

	status      string
	statusStyle string
	frame       int

	width, height int
	quitting      bool
}

type tick time.Time

func ticker() tea.Cmd {
	return tea.Tick(120*time.Millisecond, func(t time.Time) tea.Msg { return tick(t) })
}

func newModel() (*model, error) {
	ui, err := fs.Sub(uiFS, "ui")
	if err != nil {
		return nil, err
	}
	view, err := tml.Load(ui, "app.tml", tml.Options{Dark: true})
	if err != nil {
		return nil, err
	}
	// The arrows walk the rows, so the ring is left with tab. Enter belongs to the popup and never reaches the ring.
	view.UI().SetKeyMap(tml.KeyMap{
		Next:     []string{"tab"},
		Prev:     []string{"shift+tab"},
		Activate: []string{"enter", "space"},
	})

	m := &model{view: view, statusStyle: "muted", width: 96, height: 30}
	m.reload()
	return m, nil
}

func (m *model) Init() tea.Cmd { return ticker() }

// reload re-reads the bin. Listing skips a directory it cannot read, so a failure here is the whole listing failing.
func (m *model) reload() {
	items, err := recycler.List()
	if err != nil {
		m.say(err.Error(), "danger")
		return
	}
	m.items = items
	m.apply()
}

// apply narrows the listing to what the filter matches and keeps the selection on a row that still exists.
func (m *model) apply() {
	m.shown = m.shown[:0]
	needle := strings.ToLower(m.filter)
	for _, item := range m.items {
		if needle == "" || strings.Contains(strings.ToLower(item.Name), needle) ||
			strings.Contains(strings.ToLower(item.OriginalPath), needle) {
			m.shown = append(m.shown, item)
		}
	}
	m.selected = clamp(m.selected, 0, len(m.shown)-1)
	m.follow()
}

// follow scrolls the selected row into view. A table draws its header and a rule above the rows, so a row sits that
// much further down than its index. How tall the viewport is comes from where the last frame put it.
func (m *model) follow() {
	target, ok := m.view.UI().Target("items")
	if !ok || target.Rect.H <= 0 {
		return
	}
	line := m.selected + tableHeaderLines
	switch {
	case line < m.offset:
		m.offset = line
	case line >= m.offset+target.Rect.H:
		m.offset = line - target.Rect.H + 1
	}
	m.offset = max(0, m.offset)
}

// tableHeaderLines is the header and the rule under it, which every row is drawn below.
const tableHeaderLines = 2

func (m *model) say(text, style string) {
	m.status, m.statusStyle = text, style
}

// current is the item the cursor sits on, when the listing has any.
func (m *model) current() (recycler.Item, bool) {
	if m.selected < 0 || m.selected >= len(m.shown) {
		return recycler.Item{}, false
	}
	return m.shown[m.selected], true
}

func (m *model) move(delta int) {
	m.selected = clamp(m.selected+delta, 0, len(m.shown)-1)
	m.follow()
}

// ask opens the confirmation. Restoring writes a file back outside the bin, so it is worth a keystroke of its own.
func (m *model) ask() {
	item, ok := m.current()
	if !ok {
		return
	}
	if item.OriginalPath == "" {
		m.say("original location unknown: restore it with recycler restore --to PATH", "warn")
		return
	}
	m.asking = true
}

func (m *model) restore() {
	m.asking = false
	item, ok := m.current()
	if !ok {
		return
	}
	restored, err := recycler.Restore(item.ID)
	if err != nil {
		switch {
		case errors.Is(err, recycler.ErrExists):
			m.say("something is already at "+item.OriginalPath, "warn")
		case errors.Is(err, recycler.ErrUnknownOrigin):
			m.say("original location unknown: restore it with recycler restore --to PATH", "warn")
		default:
			m.say(err.Error(), "danger")
		}
		return
	}
	m.say("restored "+restored, "ok")
	m.reload()
}

func (m *model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		return m, nil
	case tick:
		m.frame++
		return m, ticker()
	case tea.KeyPressMsg:
		if cmd, handled := m.hotkey(msg); handled {
			return m, cmd
		}
	}

	for _, event := range m.view.UI().Update(msg) {
		switch event.Kind {
		case tml.Activated:
			m.act(event.Action, event.ID, event.Y)
		case tml.Scrolled:
			if event.ID == "items" {
				m.offset = max(0, m.offset+event.Delta)
			}
		}
	}
	if m.quitting {
		return m, tea.Quit
	}
	return m, nil
}

func (m *model) act(action, id string, row int) {
	switch {
	case action == "restore":
		m.restore()
	case action == "cancel":
		m.asking = false
	case id == "rows" && row >= tableHeaderLines:
		m.selected = clamp(row-tableHeaderLines+m.offset, 0, len(m.shown)-1)
	}
}

func (m *model) hotkey(msg tea.KeyPressMsg) (tea.Cmd, bool) {
	switch msg.String() {
	case "ctrl+c":
		return tea.Quit, true
	case "esc":
		return m.escape()
	}
	if m.asking {
		return m.answer(msg.String())
	}
	if m.filtering {
		return m.typing(msg)
	}
	return m.browsing(msg)
}

func (m *model) escape() (tea.Cmd, bool) {
	switch {
	case m.asking:
		m.asking = false
	case m.filtering:
		m.filtering, m.filter = false, ""
		m.apply()
	default:
		return tea.Quit, true
	}
	return nil, true
}

func (m *model) answer(key string) (tea.Cmd, bool) {
	switch key {
	case "enter", "y":
		m.restore()
		return nil, true
	case "n":
		m.asking = false
		return nil, true
	}
	return nil, false
}

func (m *model) typing(msg tea.KeyPressMsg) (tea.Cmd, bool) {
	switch msg.String() {
	case "enter":
		m.filtering = false
		return nil, true
	case "backspace":
		if m.filter != "" {
			m.filter = m.filter[:len(m.filter)-1]
			m.apply()
		}
		return nil, true
	case "up", "down":
		return m.browsing(msg)
	}
	if r := msg.Code; unicode.IsPrint(r) && msg.Mod == 0 {
		m.filter += string(r)
		m.apply()
		return nil, true
	}
	return nil, false
}

func (m *model) browsing(msg tea.KeyPressMsg) (tea.Cmd, bool) {
	switch msg.String() {
	case "q":
		return tea.Quit, true
	case "up", "k":
		m.move(-1)
	case "down", "j":
		m.move(1)
	case "pgup":
		m.move(-m.page())
	case "pgdown":
		m.move(m.page())
	case "home", "g":
		m.move(-len(m.shown))
	case "end", "G":
		m.move(len(m.shown))
	case "enter":
		m.ask()
	case "/":
		m.filtering = true
		m.view.UI().Focus("filter")
	case "r":
		m.reload()
		m.say("reloaded", "muted")
	default:
		return nil, false
	}
	return nil, true
}

// page is a screenful of rows, taken from where the last frame put the viewport.
func (m *model) page() int {
	if target, ok := m.view.UI().Target("items"); ok && target.Rect.H > tableHeaderLines {
		return target.Rect.H - tableHeaderLines
	}
	return 1
}

func (m *model) View() tea.View {
	view := tea.NewView(m.render())
	view.MouseMode = tea.MouseModeAllMotion
	return view
}

func (m *model) render() string {
	out, err := m.view.Render(m.props(), m.width, m.height)
	if err != nil {
		return "tml: " + err.Error()
	}
	return out
}

func (m *model) props() tml.Props {
	item, has := m.current()
	origin := item.OriginalPath
	if origin == "" && has {
		origin = "unknown"
	}

	props := tml.Props{
		"count":       sema.StringValue(countLabel(len(m.shown), len(m.items))),
		"size":        sema.StringValue(totalLabel(m.shown)),
		"status":      sema.StringValue(m.status),
		"statusStyle": sema.StringValue(m.statusStyle),
		"frame":       sema.StringValue(strconv.Itoa(m.frame)),

		"filter":    sema.StringValue(m.filter),
		"cursor":    sema.StringValue(strconv.Itoa(len(m.filter))),
		"filtering": sema.BoolValue(m.filtering),

		"rows":      sema.ListValue(m.rows()),
		"selected":  sema.StringValue(strconv.Itoa(m.selected)),
		"offset":    sema.StringValue(strconv.Itoa(m.offset)),
		"empty":     sema.BoolValue(len(m.shown) == 0),
		"emptyText": sema.StringValue(m.emptyText()),

		"name":     sema.StringValue(item.Name),
		"origin":   sema.StringValue(origin),
		"deleted":  sema.StringValue(item.DeletedAt.Local().Format("2006-01-02 15:04:05")),
		"itemSize": sema.StringValue(humanSize(item.Size)),
		"id":       sema.StringValue(item.ID),
		"kind":     sema.StringValue(kindLabel(item)),

		"asking": sema.BoolValue(m.asking),
		"ask":    sema.StringValue("Restore " + item.Name + "?"),
		"askTo":  sema.StringValue("to " + origin),
	}
	return props
}

// rows are the listing as the table's delimited cells. The name leads and the directory trails, because a cut falls on
// the end of the row: the directory is the part the panel below spells out in full, and the name is the part that has
// to survive a narrow terminal.
func (m *model) rows() []string {
	rows := make([]string, 0, len(m.shown))
	for _, item := range m.shown {
		rows = append(rows, strings.Join([]string{
			item.Name,
			item.DeletedAt.Local().Format("2006-01-02 15:04"),
			humanSize(item.Size),
			originDir(item),
		}, cellSep))
	}
	return rows
}

// originDir is the directory an item was recycled from, or a note that nothing recorded it.
func originDir(item recycler.Item) string {
	if item.OriginalPath == "" {
		return "(not recorded)"
	}
	return filepath.Dir(item.OriginalPath)
}

func (m *model) emptyText() string {
	if m.filter != "" {
		return "nothing matches " + strconv.Quote(m.filter)
	}
	return "the recycle bin is empty"
}

func countLabel(shown, total int) string {
	if shown == total {
		return fmt.Sprintf("%d items", total)
	}
	return fmt.Sprintf("%d of %d items", shown, total)
}

// totalLabel sums what the platform could measure. An item of unknown size is left out rather than counted as nothing.
func totalLabel(items []recycler.Item) string {
	var total int64
	unknown := 0
	for _, item := range items {
		if item.Size == recycler.SizeUnknown {
			unknown++
			continue
		}
		total += item.Size
	}
	if unknown > 0 {
		return fmt.Sprintf("%s + %d of unknown size", humanSize(total), unknown)
	}
	return humanSize(total)
}

func kindLabel(item recycler.Item) string {
	if item.IsDir {
		return "directory"
	}
	return "file"
}

func clamp(n, low, high int) int {
	if high < low {
		return low
	}
	return max(low, min(n, high))
}

func runTUI() error {
	if !recycler.Available() {
		return recycler.ErrUnsupported
	}
	m, err := newModel()
	if err != nil {
		return err
	}
	_, err = tml.Run(m)
	return err
}

// frameOf renders a frame at a fixed size, for a test that has no terminal.
func (m *model) frameOf(width, height int) string {
	m.width, m.height = width, height
	return m.render()
}
