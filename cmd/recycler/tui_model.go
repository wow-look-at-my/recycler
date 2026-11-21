// tui_model.go holds sortKey.

package main

import (
	"cmp"
	"fmt"
	"io/fs"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/wow-look-at-my/go-containers/set"
	"github.com/wow-look-at-my/recycler"
	"github.com/wow-look-at-my/tml"
	"github.com/wow-look-at-my/tml/inspect"
	"github.com/wow-look-at-my/tml/sema"
)

const cellSep = "\x1f"

// A sortKey is a column the listing can be in order of. The values are the table's column indices.
type sortKey int

const (
	sortName sortKey = iota
	sortDeleted
	sortSize
	sortFrom
)

var columnTitles = []string{"NAME", "DELETED", "SIZE", "FROM"}

// askKind is the question a confirmation asks.
type askKind int

const (
	askNone askKind = iota
	askRestore
	askDelete
)

// A menuEntry is one line of the context menu.
type menuEntry struct {
	label string
	run   func()
}

type model struct {
	view *tml.View

	items    []recycler.Item
	shown    []recycler.Item
	selected int
	// offset is the first row on screen, and viewRows how many rows the listing shows under its header.
	offset   int
	viewRows int

	// marks holds the IDs of the marked items.
	marks map[string]bool
	// anchor is where a shift+click range starts.
	anchor int

	sortBy     sortKey
	descending bool

	filter    string
	filtering bool

	ask       askKind
	prompting bool
	prompt    string

	menu         []menuEntry
	menuX, menuY int
	menuSelected int

	// focusNext is a control to move the keyboard to once it is on screen.
	focusNext string

	// lastClick and lastRow tell a double click from clicks.
	lastClick time.Time
	lastRow   int

	status      string
	statusStyle string

	width, height int
	quitting      bool
}

// doubleClick is the longest gap between the clicks of a double click.
const doubleClick = 400 * time.Millisecond

func newModel() (*model, error) {
	ui, err := fs.Sub(uiFS, "ui")
	if err != nil {
		return nil, err
	}
	view, err := tml.Load(ui, "app.tml", tml.Options{Dark: true})
	if err != nil {
		return nil, err
	}
	// The arrows walk the rows, so the ring is left with tab.
	view.UI().SetKeyMap(tml.KeyMap{
		Next:     []string{"tab"},
		Prev:     []string{"shift+tab"},
		Activate: []string{"enter", "space"},
	})

	m := &model{
		view: view, statusStyle: "muted", width: 96, height: 30,
		marks: map[string]bool{}, sortBy: sortDeleted, descending: true, lastRow: -1,
	}
	m.reload()
	m.settle()
	return m, nil
}

// reload re-reads the bin. Listing skips a directory it cannot read, so a failure here is the whole listing failing.
func (m *model) reload() {
	items, err := recycler.List()
	if err != nil {
		m.say(err.Error(), "danger")
		return
	}
	m.items = items
	present := set.New[string]()
	for _, item := range items {
		present.Add(item.ID)
	}
	for id := range m.marks {
		if !present.Contains(id) {
			delete(m.marks, id)
		}
	}
	m.apply()
}

// apply narrows the listing to what the filter matches, puts it in order. It keeps the selection on the item it
// was on when that item is still listed.
func (m *model) apply() {
	keep, _ := m.current()
	m.shown = m.shown[:0]
	needle := strings.ToLower(m.filter)
	for _, item := range m.items {
		if needle == "" || strings.Contains(strings.ToLower(item.Name), needle) ||
			strings.Contains(strings.ToLower(item.OriginalPath), needle) {
			m.shown = append(m.shown, item)
		}
	}
	slices.SortStableFunc(m.shown, m.compare)
	if keep.ID != "" {
		if at := slices.IndexFunc(m.shown, func(it recycler.Item) bool { return it.ID == keep.ID }); at >= 0 {
			m.selected = at
		}
	}
	m.selected = clamp(m.selected, 0, len(m.shown)-1)
	m.follow()
}

// compare orders a pair of items by the sort column, and by ID where the column ties, so the order is stable.
func (m *model) compare(a, b recycler.Item) int {
	var by int
	switch m.sortBy {
	case sortName:
		by = cmp.Compare(strings.ToLower(a.Name), strings.ToLower(b.Name))
	case sortDeleted:
		by = a.DeletedAt.Compare(b.DeletedAt)
	case sortSize:
		by = cmp.Compare(a.Size, b.Size)
	case sortFrom:
		by = cmp.Compare(strings.ToLower(originDir(a)), strings.ToLower(originDir(b)))
	}
	if m.descending {
		by = -by
	}
	if by == 0 {
		by = cmp.Compare(a.ID, b.ID)
	}
	return by
}

// sortOn puts the listing in order of a column. The column it is already in order of turns around.
func (m *model) sortOn(key sortKey) {
	if key == m.sortBy {
		m.descending = !m.descending
	} else {
		// A date or a size reads best largest first, and a name from A.
		m.sortBy, m.descending = key, key == sortDeleted || key == sortSize
	}
	m.apply()
}

func (m *model) sortTo(key sortKey, descending bool) {
	m.sortBy, m.descending = key, descending
	m.apply()
}

// follow scrolls the selected row into view.
func (m *model) follow() {
	if m.viewRows <= 0 {
		return
	}
	switch {
	case m.selected < m.offset:
		m.offset = m.selected
	case m.selected >= m.offset+m.viewRows:
		m.offset = m.selected - m.viewRows + 1
	}
	m.offset = max(0, m.offset)
}

// settle lays the frame out to learn how many rows the listing shows, and
// keeps the scroll inside the listing.
func (m *model) settle() {
	if rows, ok := m.listingRows(); ok && rows != m.viewRows {
		m.viewRows = rows
		m.follow()
	}
	m.offset = clamp(m.offset, 0, len(m.shown)-m.viewRows)
	if m.focusNext != "" && m.view.UI().Focus(m.focusNext) {
		m.focusNext = ""
	}
}

// listingRows is how many rows the listing shows under its header at the current size. An empty bin has no listing.
func (m *model) listingRows() (int, bool) {
	box, err := m.view.Layout(m.Props(), m.width, m.height)
	if err != nil {
		return 0, false
	}
	items := inspect.Find(box, "items")
	if items == nil {
		return 0, false
	}
	return max(0, items.Content.H-tableHeaderLines), true
}

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

// targets is what an action applies to: the marked items in listing order, or the selected item when nothing is
// marked.
func (m *model) targets() []recycler.Item {
	var marked []recycler.Item
	for _, item := range m.shown {
		if m.marks[item.ID] {
			marked = append(marked, item)
		}
	}
	if len(marked) > 0 {
		return marked
	}
	if item, ok := m.current(); ok {
		return []recycler.Item{item}
	}
	return nil
}

func (m *model) move(delta int) {
	m.selected = clamp(m.selected+delta, 0, len(m.shown)-1)
	m.follow()
}

// toggleMark marks or unmarks the item at index i of the listing.
func (m *model) toggleMark(i int) {
	if i < 0 || i >= len(m.shown) {
		return
	}
	id := m.shown[i].ID
	if m.marks[id] {
		delete(m.marks, id)
	} else {
		m.marks[id] = true
	}
}

// markRange marks every item from one index of the listing to another.
func (m *model) markRange(from, to int) {
	if from > to {
		from, to = to, from
	}
	for i := max(0, from); i <= min(to, len(m.shown)-1); i++ {
		m.marks[m.shown[i].ID] = true
	}
}

// markAll marks every listed item, or clears the marks when every listed item is already marked.
func (m *model) markAll() {
	all := len(m.shown) > 0
	for _, item := range m.shown {
		all = all && m.marks[item.ID]
	}
	if all {
		clear(m.marks)
		return
	}
	for _, item := range m.shown {
		m.marks[item.ID] = true
	}
}

// page is a screenful of rows.
func (m *model) page() int {
	return max(1, m.viewRows)
}

func (m *model) render() string {
	out, err := m.view.Render(m.Props(), m.width, m.height)
	if err != nil {
		return "tml: " + err.Error()
	}
	return out
}

// Props is what the view draws from. The program reads it before every frame.
func (m *model) Props() tml.Props {
	item, has := m.current()
	origin := item.OriginalPath
	if origin == "" && has {
		origin = "unknown"
	}
	title, question, detail, label := m.question()

	labels := make([]string, len(m.menu))
	for i, entry := range m.menu {
		labels[i] = entry.label
	}

	return tml.Props{
		"count":       sema.StringValue(countLabel(len(m.shown), len(m.items))),
		"size":        sema.StringValue(totalLabel(m.shown)),
		"status":      sema.StringValue(m.status),
		"statusStyle": sema.StringValue(m.statusStyle),
		"marked":      sema.StringValue(m.markedLabel()),
		"sortLabel":   sema.StringValue(m.sortLabel()),
		"hints":       sema.StringValue(m.hints()),

		"filter":      sema.StringValue(m.filter),
		"cursor":      sema.StringValue(strconv.Itoa(len(m.filter))),
		"filtering":   sema.BoolValue(m.filtering),
		"filterShown": sema.BoolValue(m.filtering || m.filter != ""),

		"columns":   sema.ListValue(m.columns()),
		"rows":      sema.ListValue(m.rows()),
		"selected":  sema.StringValue(strconv.Itoa(m.selected - m.first())),
		"offset":    sema.StringValue(strconv.Itoa(m.offset)),
		"lines":     sema.StringValue(strconv.Itoa(len(m.shown) + tableHeaderLines)),
		"empty":     sema.BoolValue(len(m.shown) == 0),
		"emptyText": sema.StringValue(m.emptyText()),

		"origin": sema.StringValue(origin),

		"menuOpen":     sema.BoolValue(len(m.menu) > 0),
		"menuX":        sema.StringValue(strconv.Itoa(m.menuX)),
		"menuY":        sema.StringValue(strconv.Itoa(m.menuY)),
		"menuItems":    sema.ListValue(labels),
		"menuSelected": sema.StringValue(strconv.Itoa(m.menuSelected)),

		"askPlain":   sema.BoolValue(m.ask == askRestore),
		"askDanger":  sema.BoolValue(m.ask == askDelete),
		"askTitle":   sema.StringValue(title),
		"ask":        sema.StringValue(question),
		"askTo":      sema.StringValue(detail),
		"askLabel":   sema.StringValue(label),
		"askVariant": sema.StringValue("primary"),

		"prompting":    sema.BoolValue(m.prompting),
		"prompt":       sema.StringValue(m.prompt),
		"promptCursor": sema.StringValue(strconv.Itoa(len(m.prompt))),
		"promptText":   sema.StringValue("Restore " + describe(m.targets()) + " into:"),
	}
}

// question is what the open confirmation says: its title, the question, the line under it, and its button.
func (m *model) question() (title, question, detail, label string) {
	targets := m.targets()
	switch m.ask {
	case askDelete:
		return "Delete permanently", "Delete " + describe(targets) + " permanently?",
			"This cannot be undone: the bin is the only copy.", "Delete"
	default:
		detail = "to their original locations"
		if len(targets) == 1 {
			detail = "to " + targets[0].OriginalPath
		}
		if unknown := countUnknown(targets); unknown > 0 && len(targets) > 1 {
			detail += fmt.Sprintf(" (%d with no recorded location stay)", unknown)
		}
		return "Restore", "Restore " + describe(targets) + "?", detail, "Restore"
	}
}

// describe names what an action applies to: an item by name, several by how many there are.
func describe(items []recycler.Item) string {
	if len(items) == 1 {
		return items[0].Name
	}
	return fmt.Sprintf("%d items", len(items))
}

func countUnknown(items []recycler.Item) int {
	n := 0
	for _, item := range items {
		if item.OriginalPath == "" {
			n++
		}
	}
	return n
}

func (m *model) markedLabel() string {
	n := 0
	for _, item := range m.shown {
		if m.marks[item.ID] {
			n++
		}
	}
	if n == 0 {
		return ""
	}
	return fmt.Sprintf("%d marked", n)
}

func (m *model) sortLabel() string {
	order := "ascending"
	if m.descending {
		order = "descending"
	}
	return "sorted by " + strings.ToLower(columnTitles[m.sortBy]) + ", " + order
}

// hints are the keys that do something in the state the interface is in.
func (m *model) hints() string {
	switch {
	case len(m.menu) > 0:
		return "↑↓ choose · enter runs · esc closes"
	case m.prompting:
		return "type a directory · enter restores there · esc cancels"
	case m.ask != askNone:
		return "enter confirms · tab moves · esc cancels"
	case m.filtering:
		return "type to filter · ↑↓ move · enter keeps the filter · esc clears it"
	}
	return "enter restore · space mark · a mark all · t restore to · d delete · s sort · S reverse · " +
		"/ filter · m menu · r reload · q quit · right-click for a menu, click a heading to sort"
}

// columns are the table's headings, the sorted one wearing an arrow.
func (m *model) columns() []string {
	out := slices.Clone(columnTitles)
	arrow := " ▲"
	if m.descending {
		arrow = " ▼"
	}
	out[m.sortBy] += arrow
	return out
}

// first is the index of the first row on screen.
func (m *model) first() int {
	return clamp(m.offset, 0, len(m.shown))
}

// rows are the rows on screen as the table's delimited cells. The terminal's height bounds the viewport, so it is
// enough rows before the first layout at a new size has measured the viewport.
func (m *model) rows() []string {
	shown := m.shown[m.first():min(len(m.shown), m.first()+m.height)]
	rows := make([]string, 0, len(shown))
	for _, item := range shown {
		// The table trims a cell's blanks, so an unmarked row needs a glyph of its own to stay aligned.
		mark := "○ "
		if m.marks[item.ID] {
			mark = "● "
		}
		rows = append(rows, strings.Join([]string{
			mark + item.Name,
			item.DeletedAt.Local().Format("2006-01-02 15:04"),
			humanSize(item.Size),
			originDir(item),
		}, cellSep))
	}
	return rows
}

// originDir is the directory an item was recycled from, or a nothing recorded
// it.
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

func clamp(n, low, high int) int {
	if high < low {
		return low
	}
	return max(low, min(n, high))
}

// frameOf renders a frame at a fixed size, for a test that has no terminal. It resizes the way the program does.
func (m *model) frameOf(width, height int) string {
	if width != m.width || height != m.height {
		m.width, m.height = width, height
		m.settle()
	}
	return m.render()
}
