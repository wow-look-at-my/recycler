// tui_model.go holds sortKey.

package main

import (
	"cmp"
	"fmt"
	"io/fs"
	"maps"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/wow-look-at-my/go-containers/set"
	"github.com/wow-look-at-my/recycler"
	"github.com/wow-look-at-my/tml"
	"github.com/wow-look-at-my/tml/cell"
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

	items []recycler.Item
	// matched is the items the filter and the disk let through, in the listing's order.
	matched []recycler.Item
	// shown is the rows of the listing: one for each item, or the tree of directories and versions.
	shown    []node
	selected int

	// tree lists the bin by directory, and flipped holds the groups opened or closed against their default.
	tree    bool
	flipped map[string]bool

	// widths is the widest cell of each column over every row, so a scroll does not resize the columns.
	widths []string

	// diskOf is the disk each item takes space on.
	diskOf   map[string]string
	disks    []string
	disk     string
	diskText string

	// extending is true while shift and an arrow grow the marks from anchor. extendBase is the marks it started from.
	extending  bool
	extendBase map[string]bool
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
		tree: true, flipped: map[string]bool{},
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
	m.readDisks()
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
	m.matched = m.matched[:0]
	needle := strings.ToLower(m.filter)
	for _, item := range m.items {
		if m.disk != "" && m.diskOf[item.ID] != m.disk {
			continue
		}
		if needle == "" || strings.Contains(strings.ToLower(item.Name), needle) ||
			strings.Contains(strings.ToLower(item.OriginalPath), needle) {
			m.matched = append(m.matched, item)
		}
	}
	slices.SortStableFunc(m.matched, m.compare)
	if m.tree {
		m.shown = m.buildTree(m.matched)
	} else {
		m.shown = make([]node, len(m.matched))
		for i, item := range m.matched {
			m.shown[i] = itemNode(item, 0)
		}
	}
	if keep.key != "" {
		if at := slices.IndexFunc(m.shown, func(n node) bool { return n.key == keep.key }); at >= 0 {
			m.selected = at
		}
	}
	m.selected = clamp(m.selected, 0, len(m.shown)-1)
	m.follow()
	m.widths = m.measureColumns()
	m.diskText = m.diskLabel()
}

// measureColumns is the widest cell of each column over every row, not the
// rows on screen.
func (m *model) measureColumns() []string {
	widths := make([]int, len(columnTitles))
	for i, title := range columnTitles {
		// The sorted heading carries an arrow.
		widths[i] = cell.Width(title) + 2
	}
	for _, n := range m.shown {
		for i, text := range m.cells(n) {
			widths[i] = max(widths[i], cell.Width(text))
		}
	}
	out := make([]string, len(widths))
	for i, w := range widths {
		out[i] = strconv.Itoa(w)
	}
	return out
}

// setTree lists the bin as a tree, or as one row for each item.
func (m *model) setTree(tree bool) {
	m.tree = tree
	m.apply()
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

// current is the row the cursor sits on, when the listing has any.
func (m *model) current() (node, bool) {
	if m.selected < 0 || m.selected >= len(m.shown) {
		return node{}, false
	}
	return m.shown[m.selected], true
}

// targets is what an action applies to: the marked items in listing order, or every item of the selected row when
// nothing is marked. A directory row is everything recycled from under it, and a versions row is every version.
func (m *model) targets() []recycler.Item {
	var marked []recycler.Item
	for _, item := range m.matched {
		if m.marks[item.ID] {
			marked = append(marked, item)
		}
	}
	if len(marked) > 0 {
		return marked
	}
	if n, ok := m.current(); ok {
		return n.items
	}
	return nil
}

func (m *model) move(delta int) {
	m.selected = clamp(m.selected+delta, 0, len(m.shown)-1)
	m.follow()
}

// toggleMark marks every item of row i, or unmarks them when every one is already marked.
func (m *model) toggleMark(i int) {
	if i < 0 || i >= len(m.shown) {
		return
	}
	n := m.shown[i]
	all := m.markState(n) == markAll
	for _, item := range n.items {
		if all {
			delete(m.marks, item.ID)
		} else {
			m.marks[item.ID] = true
		}
	}
}

// markRange marks every item of the rows from one index of the listing to another.
func (m *model) markRange(from, to int) {
	if from > to {
		from, to = to, from
	}
	for i := max(0, from); i <= min(to, len(m.shown)-1); i++ {
		for _, item := range m.shown[i].items {
			m.marks[item.ID] = true
		}
	}
}

// extend moves the cursor and marks the rows from where the shift started to where the cursor is now. Going back
// over a row takes its mark off again, unless the row was marked before the shift started.
func (m *model) extend(delta int) {
	if !m.extending {
		m.extending, m.extendBase, m.anchor = true, maps.Clone(m.marks), m.selected
	}
	m.move(delta)
	m.marks = maps.Clone(m.extendBase)
	m.markRange(m.anchor, m.selected)
}

// markAll marks every listed item, or clears the marks when every listed item is already marked.
func (m *model) markAll() {
	all := len(m.matched) > 0
	for _, item := range m.matched {
		all = all && m.marks[item.ID]
	}
	if all {
		clear(m.marks)
		return
	}
	for _, item := range m.matched {
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
		"count":       sema.StringValue(countLabel(len(m.matched), len(m.items))),
		"size":        sema.StringValue(totalLabel(m.matched)),
		"disk":        sema.StringValue(m.diskText),
		"widths":      sema.ListValue(m.widths),
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
		"promptText":   sema.StringValue("Restore " + m.describeTargets() + " into:"),
	}
}

// first is the index of the first row on screen.
func (m *model) first() int {
	return clamp(m.offset, 0, len(m.shown))
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
