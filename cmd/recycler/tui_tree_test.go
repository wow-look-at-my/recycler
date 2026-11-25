package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/wow-look-at-my/recycler"
)

// treeBin recycles each path, relative to a work directory, in order. A path named twice is recycled twice, so the bin
// holds a version of it for each time. The interface opens on the result in its default view.
func treeBin(t *testing.T, paths ...string) (*model, string) {
	t.Helper()
	work := isolateTrash(t)
	for _, rel := range paths {
		path := writeFile(t, filepath.Join(work, rel), "contents of "+rel)
		_, err := run(t, "", "trash", path)
		require.NoError(t, err)
	}
	m, err := newModel()
	require.NoError(t, err)
	screen(t, m)
	return m, work
}

// rowNamed is the index of the listed row whose label is name.
func rowNamed(t *testing.T, m *model, name string) int {
	t.Helper()
	for i, n := range m.shown {
		if n.Name == name {
			return i
		}
	}
	require.FailNow(t, "no row is named "+name)
	return -1
}

func selectRow(t *testing.T, m *model, name string) node {
	t.Helper()
	m.selected = rowNamed(t, m, name)
	m.follow()
	return m.shown[m.selected]
}

// The browser opens on the tree: a directory row, and under it a row for each file recycled from it.
func TestTheBinOpensAsATreeOfDirectories(t *testing.T) {
	m, work := treeBin(t, "src/main.go", "src/util.go", "docs/readme.md")
	require.True(t, m.tree)

	top := m.shown[0]
	assert.Equal(t, nodeDir, top.kind)
	assert.Equal(t, work, top.Name, "the directories down to the first fork share one row")
	assert.Len(t, top.items, 3)

	src := m.shown[rowNamed(t, m, "src")]
	assert.Equal(t, 1, src.depth)
	assert.Len(t, src.items, 2)
	main := m.shown[rowNamed(t, m, "main.go")]
	assert.Equal(t, 2, main.depth)

	out := screen(t, m)
	assert.Contains(t, out, "src/ (2)")
	assert.Contains(t, out, "docs/ (1)")
	assert.Contains(t, out, "main.go")
}

// Every item recycled from one path is one row, and that row stands for each version.
func TestTheVersionsOfAFileShareARow(t *testing.T) {
	m, work := treeBin(t, "notes.md", "notes.md", "notes.md", "other.txt")

	notes := m.shown[rowNamed(t, m, "notes.md")]
	assert.Equal(t, nodeVersions, notes.kind)
	assert.False(t, notes.open, "the versions start closed")
	assert.Len(t, notes.items, 3)
	assert.Equal(t, filepath.Join(work, "notes.md"), notes.OriginalPath)
	assert.Contains(t, screen(t, m), "notes.md (3 versions)")

	selectRow(t, m, "notes.md")
	press(t, m, "right")
	at := m.selected
	assert.True(t, m.shown[at].open, "right opens the row")
	require.Greater(t, len(m.shown), at+3)
	for _, version := range m.shown[at+1 : at+4] {
		assert.Equal(t, nodeItem, version.kind, "each version is a row of its own")
		assert.Equal(t, notes.depth+1, version.depth)
	}
	press(t, m, "left")
	assert.False(t, m.shown[m.selected].open, "left closes it again")
}

// Delete on a versions row deletes every version, and nothing else.
func TestDeletingAVersionsRowDeletesEveryVersion(t *testing.T) {
	noAgent(t)
	m, _ := treeBin(t, "notes.md", "notes.md", "other.txt")

	selectRow(t, m, "notes.md")
	press(t, m, "d")
	require.Equal(t, askDelete, m.ask)
	assert.Contains(t, screen(t, m), "Delete all 2 versions of notes.md permanently?")
	press(t, m, "y")

	items, err := recycler.List()
	require.NoError(t, err)
	require.Len(t, items, 1)
	assert.Equal(t, "other.txt", items[0].Name)
}

// Delete on a directory row deletes everything recycled from under it, closed or open, and nothing from beside it.
func TestDeletingADirectoryRowDeletesEverythingFromIt(t *testing.T) {
	noAgent(t)
	m, work := treeBin(t, "build/a.o", "build/obj/b.o", "build/obj/b.o", "keep.txt")

	selectRow(t, m, "build")
	press(t, m, "left")
	require.False(t, m.shown[m.selected].open, "a closed directory still stands for what it holds")
	press(t, m, "d")
	assert.Contains(t, screen(t, m), "Delete all 3 items from "+filepath.Join(work, "build")+" permanently?")
	press(t, m, "y")

	items, err := recycler.List()
	require.NoError(t, err)
	require.Len(t, items, 1)
	assert.Equal(t, "keep.txt", items[0].Name)
}

// A restore of a directory row puts back everything in it.
func TestRestoringADirectoryRowRestoresEverythingInIt(t *testing.T) {
	m, work := treeBin(t, "src/a.go", "src/b.go", "keep.txt")

	selectRow(t, m, "src")
	press(t, m, "enter", "y")
	assert.FileExists(t, filepath.Join(work, "src", "a.go"))
	assert.FileExists(t, filepath.Join(work, "src", "b.go"))
	assert.NoFileExists(t, filepath.Join(work, "keep.txt"))
}

// Space on a group marks every item in it, and the row says how much of it is marked.
func TestMarkingAGroupMarksWhatItHolds(t *testing.T) {
	m, _ := treeBin(t, "src/a.go", "src/b.go", "keep.txt")

	selectRow(t, m, "a.go")
	press(t, m, "space")
	assert.Contains(t, screen(t, m), "◐ ", "a group with some of it marked says so")

	selectRow(t, m, "src")
	press(t, m, "space")
	src := m.shown[rowNamed(t, m, "src")]
	for _, item := range src.items {
		assert.True(t, m.marks[item.ID])
	}
	assert.Equal(t, markAll, m.markState(src))
	assert.Equal(t, "2 marked", m.markedLabel())
}

// A click on the arrow of a group opens or closes it.
func TestClickingTheArrowOpensAGroup(t *testing.T) {
	m, _ := treeBin(t, "notes.md", "notes.md")

	i := rowNamed(t, m, "notes.md")
	_, y := rowPoint(t, m, i-m.first())
	left, _ := viewport(t, m)
	x := left + 2 + 2*m.shown[i].depth
	click(t, m, x, y, 0)
	assert.True(t, m.shown[i].open, "the arrow opened the row")
}

// v turns the tree into one row for each item, and back.
func TestVSwitchesBetweenTheTreeAndOneRowPerItem(t *testing.T) {
	m, _ := treeBin(t, "src/a.go", "b.go")
	press(t, m, "v")
	assert.False(t, m.tree)
	assert.Len(t, m.shown, 2)
	press(t, m, "v")
	assert.True(t, m.tree)
	assert.Greater(t, len(m.shown), 2)
}

// shift and an arrow mark every row the cursor passes over, and going back takes the mark off the row it left.
func TestShiftAndAnArrowMarkARange(t *testing.T) {
	m, _ := binWith(t, "a.txt", "b.txt", "c.txt", "d.txt")

	press(t, m, "shift+down", "shift+down")
	assert.Equal(t, 2, m.selected)
	assert.Equal(t, "3 marked", m.markedLabel())

	press(t, m, "shift+up")
	assert.Equal(t, "2 marked", m.markedLabel(), "going back unmarks the row it left")
	assert.False(t, m.marks[m.shown[2].ID])

	press(t, m, "down", "shift+down")
	assert.Equal(t, "4 marked", m.markedLabel(), "a new range adds to the marks already there")
}

// A range started over a group takes everything in it.
func TestShiftRangeTakesWholeGroups(t *testing.T) {
	m, _ := treeBin(t, "src/a.go", "src/b.go", "keep.txt")
	m.selected = 0
	press(t, m, "shift+down")
	assert.Equal(t, "3 marked", m.markedLabel(), "the top directory holds every item")
}

// readDisks learns the disk of each item from the bin it sits in.
func TestEachItemIsOnTheDiskOfItsBin(t *testing.T) {
	m, _ := binWith(t, "a.txt", "b.txt")
	require.Len(t, m.disks, 1)
	want := diskOf(filepath.Dir(m.items[0].ID))
	assert.Equal(t, want, m.disks[0])
	for _, item := range m.items {
		assert.Equal(t, want, m.diskOf[item.ID])
	}
	assert.Empty(t, m.diskText, "one disk needs no filter")
}

// f narrows the listing to one disk at a time, then back to every disk.
func TestFNarrowsTheListingToOneDisk(t *testing.T) {
	m := largeBin(t, 10)
	m.disks = []string{"/mnt/a", "/mnt/b"}
	for i, item := range m.items {
		m.diskOf[item.ID] = m.disks[i%2]
	}
	m.apply()

	press(t, m, "f")
	assert.Equal(t, "/mnt/a", m.disk)
	assert.Len(t, m.matched, 5)
	for _, item := range m.matched {
		assert.Equal(t, "/mnt/a", m.diskOf[item.ID])
	}
	out := screen(t, m)
	assert.Contains(t, out, "5 of 10 items")
	assert.Contains(t, out, "disk /mnt/a")

	press(t, m, "f")
	assert.Equal(t, "/mnt/b", m.disk)
	press(t, m, "f")
	assert.Empty(t, m.disk)
	assert.Len(t, m.matched, 10)
	assert.Contains(t, screen(t, m), "2 disks")
}

// A filter to a disk with nothing on it says so rather than looking like an empty bin.
func TestAnEmptyDiskSaysSo(t *testing.T) {
	m := largeBin(t, 2)
	m.disk = "/nowhere"
	m.apply()
	assert.Equal(t, "nothing in the bin sits on /nowhere", m.emptyText())
}

// The columns keep their place as the listing scrolls, however long or short the names on screen are.
func TestScrollingKeepsTheColumnsInPlace(t *testing.T) {
	isolateTrash(t)
	m, err := newModel()
	require.NoError(t, err)
	now := time.Now()
	for i := range 200 {
		name := "f.txt"
		if i == 150 {
			name = "a-file-whose-name-runs-on-for-quite-a-while.txt"
		}
		m.items = append(m.items, recycler.Item{
			ID: fmt.Sprint(i), Name: name, OriginalPath: filepath.Join("/x", fmt.Sprint(i), name),
			DeletedAt: now.Add(-time.Duration(i) * time.Minute), Size: int64(i) << (i % 30),
		})
	}
	m.setTree(false)

	// heading is where each heading starts. The scrollbar shares the line, and its thumb moves on purpose.
	heading := func() []int {
		m.frameOf(160, 30)
		_, y := viewport(t, m)
		line := strings.Split(ansi.Strip(m.frameOf(160, 30)), "\n")[y]
		var at []int
		for _, title := range columnTitles {
			at = append(at, strings.Index(line, title))
		}
		return at
	}
	m.frameOf(160, 30)
	top := heading()
	press(t, m, "end")
	require.Positive(t, m.offset)
	assert.Equal(t, top, heading(), "the headings moved when the listing scrolled")
}

// A missing bin directory names itself as its disk rather than failing.
func TestDiskOfAMissingDirectoryIsTheDirectory(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "gone")
	require.NoError(t, os.RemoveAll(missing))
	assert.Equal(t, missing, diskOf(missing))
}
