// tui_tree.go holds node and the tree the browser lists the bin as.

package main

import (
	"cmp"
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/wow-look-at-my/recycler"
)

// A nodeKind is what one row of the listing stands for.
type nodeKind int

const (
	// nodeItem is one recycled item.
	nodeItem nodeKind = iota
	// nodeDir is a directory items were recycled from.
	nodeDir
	// nodeVersions is every item recycled from one path.
	nodeVersions
)

// A node is one row of the listing. Item is the recycled item of a nodeItem.
type node struct {
	recycler.Item
	kind nodeKind
	// key names the row across a reload, so the selection and an open group stay where they were.
	key   string
	depth int
	open  bool
	// items is every recycled item the row stands for. An action on the row acts on all of them.
	items []recycler.Item
}

func (n node) group() bool { return n.kind != nodeItem }

func itemNode(item recycler.Item, depth int) node {
	return node{Item: item, key: "i:" + item.ID, depth: depth, items: []recycler.Item{item}}
}

// groupNode sums items into a row of the given kind.
func groupNode(kind nodeKind, key, name, path string, depth int, items []recycler.Item) node {
	var newest time.Time
	var size int64
	for _, item := range items {
		if item.DeletedAt.After(newest) {
			newest = item.DeletedAt
		}
		if item.Size > 0 {
			size += item.Size
		}
	}
	return node{
		Item: recycler.Item{Name: name, OriginalPath: path, DeletedAt: newest, Size: size, IsDir: kind == nodeDir},
		kind: kind, key: key, depth: depth, items: items,
	}
}

// A dirEntry is one directory of the tree while it is built.
type dirEntry struct {
	name  string
	path  string
	dirs  map[string]*dirEntry
	files map[string][]recycler.Item
	// flat keeps each item a row of its own, for items with no recorded path, which share no file.
	flat bool
}

func newDirEntry(name, path string) *dirEntry {
	return &dirEntry{name: name, path: path, dirs: map[string]*dirEntry{}, files: map[string][]recycler.Item{}}
}

// all is every item under d, in the order they were added.
func (d *dirEntry) all() []recycler.Item {
	var out []recycler.Item
	for _, items := range d.files {
		out = append(out, items...)
	}
	for _, sub := range d.dirs {
		out = append(out, sub.all()...)
	}
	return out
}

// notRecorded is the group of items whose original location nothing recorded.
const notRecorded = "(not recorded)"

// buildTree lists items as directories, the files recycled from each, and the versions of each file.
func (m *model) buildTree(items []recycler.Item) []node {
	roots := map[string]*dirEntry{}
	unknown := newDirEntry(notRecorded, "")
	unknown.flat = true
	for _, item := range items {
		if item.OriginalPath == "" {
			unknown.files[item.ID] = append(unknown.files[item.ID], item)
			continue
		}
		path := filepath.Clean(item.OriginalPath)
		vol := filepath.VolumeName(path)
		root := vol + string(filepath.Separator)
		d := roots[root]
		if d == nil {
			d = newDirEntry(root, root)
			roots[root] = d
		}
		parts := strings.Split(strings.Trim(path[len(vol):], string(filepath.Separator)), string(filepath.Separator))
		for _, part := range parts[:len(parts)-1] {
			sub := d.dirs[part]
			if sub == nil {
				sub = newDirEntry(part, filepath.Join(d.path, part))
				d.dirs[part] = sub
			}
			d = sub
		}
		base := parts[len(parts)-1]
		d.files[base] = append(d.files[base], item)
	}

	var tops [][]node
	for _, root := range roots {
		tops = append(tops, m.dirRows(root, 0))
	}
	if len(unknown.files) > 0 {
		tops = append(tops, m.dirRows(unknown, 0))
	}
	return m.joinSorted(tops)
}

// dirRows is the row of d and, when it is open, the rows under it. A directory that holds nothing. However, one
// directory shares its row with it, so a deep path takes a row rather than a row per level.
func (m *model) dirRows(d *dirEntry, depth int) []node {
	label := d.name
	for !d.flat && len(d.files) == 0 && len(d.dirs) == 1 {
		for _, sub := range d.dirs {
			d = sub
		}
		label = filepath.Join(label, d.name)
	}
	items := d.all()
	slices.SortStableFunc(items, m.compare)
	head := groupNode(nodeDir, "d:"+d.path, label, d.path, depth, items)
	if d.flat {
		head.key = "d:" + notRecorded
	}
	head.open = m.isOpen(head)
	if !head.open {
		return []node{head}
	}

	var dirs, files [][]node
	for _, sub := range d.dirs {
		dirs = append(dirs, m.dirRows(sub, depth+1))
	}
	for _, versions := range d.files {
		files = append(files, m.fileRows(versions, depth+1))
	}
	return append([]node{head}, append(m.joinSorted(dirs), m.joinSorted(files)...)...)
}

// fileRows is the row of the items recycled from one path. One item is a row. Several are a group of versions.
func (m *model) fileRows(versions []recycler.Item, depth int) []node {
	if len(versions) == 1 {
		return []node{itemNode(versions[0], depth)}
	}
	path := versions[0].OriginalPath
	head := groupNode(nodeVersions, "v:"+path, filepath.Base(path), path, depth, versions)
	head.open = m.isOpen(head)
	if !head.open {
		return []node{head}
	}
	rows := []node{head}
	for _, item := range versions {
		rows = append(rows, itemNode(item, depth+1))
	}
	return rows
}

// joinSorted puts sibling subtrees in the listing's order by their first row, and joins them.
func (m *model) joinSorted(subtrees [][]node) []node {
	slices.SortStableFunc(subtrees, func(a, b []node) int {
		if by := m.compare(a[0].Item, b[0].Item); by != 0 {
			return by
		}
		return cmp.Compare(a[0].key, b[0].key)
	})
	var out []node
	for _, rows := range subtrees {
		out = append(out, rows...)
	}
	return out
}

// isOpen says whether a group shows its rows.
func (m *model) isOpen(n node) bool {
	return (n.kind == nodeDir) != m.flipped[n.key]
}

// setOpen opens or closes the group on the selected row.
func (m *model) setOpen(open bool) bool {
	n, ok := m.current()
	if !ok || !n.group() || n.open == open {
		return false
	}
	m.flipped[n.key] = !m.flipped[n.key]
	m.apply()
	return true
}

// collapse closes the selected group, or moves to the group the selected row is in.
func (m *model) collapse() {
	if !m.tree || m.setOpen(false) {
		return
	}
	n, ok := m.current()
	if !ok {
		return
	}
	for i := m.selected - 1; i >= 0; i-- {
		if m.shown[i].depth < n.depth {
			m.selected = i
			m.follow()
			return
		}
	}
}

// expand opens the selected group, or moves into the group when it is already open.
func (m *model) expand() {
	if !m.tree || m.setOpen(true) {
		return
	}
	if n, ok := m.current(); ok && n.group() && n.open {
		m.move(1)
	}
}

// nameCell is the NAME column of a row: its mark, its depth, whether it is open, and its label.
func (m *model) nameCell(n node) string {
	// The table trims a cell's blanks, so an unmarked row needs a glyph of its own to stay aligned.
	mark := "○ "
	switch m.markState(n) {
	case markAll:
		mark = "● "
	case markSome:
		mark = "◐ "
	}
	if !m.tree {
		return mark + n.Name
	}
	arrow := "  "
	if n.group() {
		arrow = "▸ "
		if n.open {
			arrow = "▾ "
		}
	}
	label := n.Name
	switch n.kind {
	case nodeDir:
		if n.OriginalPath != "" && !strings.HasSuffix(label, string(filepath.Separator)) {
			label += string(filepath.Separator)
		}
		label += fmt.Sprintf(" (%d)", len(n.items))
	case nodeVersions:
		label += fmt.Sprintf(" (%d versions)", len(n.items))
	}
	return mark + strings.Repeat("  ", n.depth) + arrow + label
}

// arrowAt reports whether x, counted from the left of the table, lands on the open or closed arrow of a row.
func (m *model) arrowAt(n node, x int) bool {
	at := 2 + 2*n.depth
	return m.tree && n.group() && x >= at && x < at+2
}

// fromCell is the FROM column of a row: the directory a row came from, or the path of a directory row.
func fromCell(n node) string {
	if n.kind == nodeDir {
		if n.OriginalPath == "" {
			return notRecorded
		}
		return n.OriginalPath
	}
	return originDir(n.Item)
}

type markLevel int

const (
	markNone markLevel = iota
	markSome
	markAll
)

// markState says how many of the items a row stands for are marked.
func (m *model) markState(n node) markLevel {
	marked := 0
	for _, item := range n.items {
		if m.marks[item.ID] {
			marked++
		}
	}
	switch {
	case marked == 0:
		return markNone
	case marked == len(n.items):
		return markAll
	}
	return markSome
}
