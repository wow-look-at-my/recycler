package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/wow-look-at-my/tml"
)

// screen renders a frame with the styling taken off, so an assertion is about.
func screen(t *testing.T, m *model) string {
	t.Helper()
	return ansi.Strip(m.frameOf(100, 30))
}

// press feeds keys through the same Update a terminal does. Painting between them is what sizes the viewport.
func press(t *testing.T, m *model, keys ...string) {
	t.Helper()
	for _, key := range keys {
		_, _ = m.Update(tml.KeyMsg(key))
		screen(t, m)
	}
}

// binWith recycles each named file and opens the interface on the result.
func binWith(t *testing.T, names ...string) (*model, string) {
	t.Helper()
	work := isolateTrash(t)
	for _, name := range names {
		path := writeFile(t, filepath.Join(work, name), "contents of "+name)
		_, err := run(t, "", "trash", path)
		require.NoError(t, err)
	}
	m, err := newModel()
	require.NoError(t, err)
	screen(t, m)
	return m, work
}

func TestTheInterfaceListsWhatIsInTheBin(t *testing.T) {
	m, work := binWith(t, "notes.md", "report.pdf")

	out := screen(t, m)
	assert.Contains(t, out, "recycle bin")
	assert.Contains(t, out, "2 items")
	assert.Contains(t, out, "NAME")
	assert.Contains(t, out, "notes.md")
	assert.Contains(t, out, "report.pdf")
	assert.Contains(t, out, work, "the directory each came from")
}

func TestAnEmptyBinSaysSoRatherThanDrawingAnEmptyTable(t *testing.T) {
	isolateTrash(t)
	m, err := newModel()
	require.NoError(t, err)

	assert.Contains(t, screen(t, m), "the recycle bin is empty")
}

// An item costs the row it is listed on and nothing more: the cursor is a bar the table draws across
// that row.
func TestMovingTheCursorMovesTheBarAndThePath(t *testing.T) {
	m, work := binWith(t, "first.txt", "second.txt")
	require.Len(t, m.shown, 2)

	top, _ := m.current()
	resting := m.frameOf(100, 30)

	press(t, m, "down")
	next, _ := m.current()

	require.NotEqual(t, top.ID, next.ID, "down moved to the other item")
	assert.Equal(t, 1, m.selected)
	assert.NotEqual(t, resting, m.frameOf(100, 30), "the bar moved with it")
	assert.Contains(t, screen(t, m), filepath.Join(work, next.Name), "and the path under the listing followed")
}

// The cursor stops at the ends rather than wrapping, so holding a key cannot.
func TestTheCursorStopsAtBothEnds(t *testing.T) {
	m, _ := binWith(t, "a.txt", "b.txt")

	press(t, m, "up", "up", "up")
	assert.Equal(t, 0, m.selected)

	press(t, m, "down", "down", "down")
	assert.Equal(t, len(m.shown)-1, m.selected)
}

func TestFilteringNarrowsTheListing(t *testing.T) {
	m, _ := binWith(t, "notes.md", "report.pdf")

	press(t, m, "/", "r", "e", "p")
	assert.True(t, m.filtering)
	require.Len(t, m.shown, 1)
	assert.Equal(t, "report.pdf", m.shown[0].Name)

	out := screen(t, m)
	assert.Contains(t, out, "1 of 2 items")
	assert.NotContains(t, out, "notes.md")
}

// A filter that matches nothing says so, rather than leaving a blank panel.
func TestAFilterMatchingNothingSaysSo(t *testing.T) {
	m, _ := binWith(t, "notes.md")

	press(t, m, "/", "z", "z", "z")
	assert.Contains(t, screen(t, m), `nothing matches "zzz"`)
}

func TestEscapeClearsTheFilter(t *testing.T) {
	m, _ := binWith(t, "notes.md", "report.pdf")

	press(t, m, "/", "r", "e", "p")
	require.Len(t, m.shown, 1)

	press(t, m, "esc")
	assert.False(t, m.filtering)
	assert.Empty(t, m.filter)
	assert.Len(t, m.shown, 2)
}

// Restoring is the only thing that takes an item out of the bin, and it asks before it writes a file back.
func TestRestoreAsksAndThenPutsTheFileBack(t *testing.T) {
	m, work := binWith(t, "notes.md")
	original := filepath.Join(work, "notes.md")
	require.NoFileExists(t, original)

	press(t, m, "enter")
	assert.True(t, m.asking, "enter asks rather than restoring straight away")
	assert.Contains(t, screen(t, m), "Restore notes.md?")

	press(t, m, "enter")
	assert.False(t, m.asking)
	assert.FileExists(t, original, "the item went back where it came from")
	assert.Empty(t, m.shown, "and left the bin")
	assert.Equal(t, "restored "+original, m.status)
	assert.Contains(t, screen(t, m), "restored")
}

func TestCancellingTheAskLeavesTheItemInTheBin(t *testing.T) {
	m, work := binWith(t, "notes.md")

	press(t, m, "enter", "esc")
	assert.False(t, m.asking)
	assert.NoFileExists(t, filepath.Join(work, "notes.md"))
	assert.Len(t, m.shown, 1)
}

// Nothing is overwritten: a file that took the original location back is reported rather than clobbered.
func TestRestoreRefusesToOverwriteWhatTookThePlaceBack(t *testing.T) {
	m, work := binWith(t, "notes.md")
	original := writeFile(t, filepath.Join(work, "notes.md"), "something else entirely")

	press(t, m, "enter", "y")

	assert.Equal(t, "something is already at "+original, m.status)
	assert.Contains(t, screen(t, m), "something is already at")
	assert.Len(t, m.shown, 1, "the item stayed in the bin")

	kept, err := os.ReadFile(original)
	require.NoError(t, err)
	assert.Equal(t, "something else entirely", string(kept), "the file in the way was left alone")
}

// Reloading picks up what another program recycled while the interface was on screen.
func TestReloadingPicksUpNewItems(t *testing.T) {
	m, work := binWith(t, "notes.md")
	require.Len(t, m.shown, 1)

	path := writeFile(t, filepath.Join(work, "later.txt"), "later")
	_, err := run(t, "", "trash", path)
	require.NoError(t, err)

	press(t, m, "r")
	assert.Len(t, m.shown, 2)
	assert.Contains(t, screen(t, m), "later.txt")
}

// A path holding the character the table splits on still arrives as a cell of its own.
func TestAPathHoldingTheDelimiterStaysOneCell(t *testing.T) {
	m, _ := binWith(t, "a|piped|name.txt")

	assert.Contains(t, screen(t, m), "a|piped|name.txt")
	require.Len(t, m.rows(), 1)
	assert.Len(t, strings.Split(m.rows()[0], cellSep), 4, "the row is the columns the table declares, no more")
}

// q quits while browsing and types while filtering, or the filter could never contain the letter.
func TestQTypesIntoTheFilterRatherThanQuitting(t *testing.T) {
	m, _ := binWith(t, "quarterly.txt", "notes.md")

	press(t, m, "/", "q")
	assert.Equal(t, "q", m.filter)
	assert.False(t, m.quitting, "q typed rather than quit")
	assert.Contains(t, screen(t, m), "quarterly.txt", "and the filter still matched on it")
}

// The interface has no way to destroy anything, which is the package's own invariant.
func TestTheInterfaceOffersNoWayToDeleteAnything(t *testing.T) {
	m, _ := binWith(t, "notes.md")

	// "deleted" is a column and a reassurance, so the words checked here are the ones that would name an action.
	out := strings.ToLower(screen(t, m))
	for _, word := range []string{"purge", "empty the", "shred", "wipe", "permanently", "destroy"} {
		assert.NotContains(t, out, word, "the interface offers %q", word)
	}

	// Every key that does anything at all, held against the bin: none of them may remove the item.
	for _, key := range []string{"d", "x", "D", "delete", "backspace", "p", "e", "w", "ctrl+d"} {
		press(t, m, key)
		assert.Len(t, m.shown, 1, "%q took the item out of the bin", key)
	}
}
