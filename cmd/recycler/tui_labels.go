// tui_labels.go holds the text the view shows: the questions, the header labels, the hints and the cells.

package main

import (
	"fmt"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"github.com/wow-look-at-my/recycler"
)

// question is what the open confirmation says: its title, the question, the line under it, and its button.
func (m *model) question() (title, question, detail, label string) {
	targets := m.targets()
	what := m.describeTargets()
	switch m.ask {
	case askDelete:
		return "Delete permanently", "Delete " + what + " permanently?",
			"This cannot be undone: the bin is the only copy.", "Delete"
	default:
		detail = "to their original locations"
		if len(targets) == 1 {
			detail = "to " + targets[0].OriginalPath
		}
		if unknown := countUnknown(targets); unknown > 0 && len(targets) > 1 {
			detail += fmt.Sprintf(" (%d with no recorded location stay)", unknown)
		}
		return "Restore", "Restore " + what + "?", detail, "Restore"
	}
}

// describeTargets names what an action applies to, the way a person picked it.
func (m *model) describeTargets() string {
	targets := m.targets()
	n, ok := m.current()
	if !ok || len(targets) < 2 || !slices.Equal(targets, n.items) {
		return describe(targets)
	}
	switch n.kind {
	case nodeDir:
		return fmt.Sprintf("all %d items from %s", len(targets), fromCell(n))
	case nodeVersions:
		return fmt.Sprintf("all %d versions of %s", len(targets), n.Name)
	}
	return describe(targets)
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
	for _, item := range m.matched {
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
	keys := "enter restore · space mark · shift+↑↓ mark a range · a mark all · t restore to · d delete · "
	if m.tree {
		keys += "←→ close/open · "
	}
	keys += "v tree/flat · "
	if len(m.disks) > 1 || m.disk != "" {
		keys += "f disk · "
	}
	return keys + "s sort · S reverse · / filter · m menu · r reload · q quit · right-click for a menu, click a heading to sort"
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

// rows are the rows on screen as the table's delimited cells. The terminal's height bounds the viewport, so it is
// enough rows before the first layout at a new size has measured the viewport.
func (m *model) rows() []string {
	shown := m.shown[m.first():min(len(m.shown), m.first()+m.height)]
	rows := make([]string, 0, len(shown))
	for _, n := range shown {
		rows = append(rows, strings.Join(m.cells(n), cellSep))
	}
	return rows
}

// cells is the text of each column of a row.
func (m *model) cells(n node) []string {
	return []string{
		m.nameCell(n),
		n.DeletedAt.Local().Format("2006-01-02 15:04"),
		humanSize(n.Size),
		fromCell(n),
	}
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
	switch {
	case m.filter != "":
		return "nothing matches " + strconv.Quote(m.filter)
	case m.disk != "":
		return "nothing in the bin sits on " + m.disk
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
