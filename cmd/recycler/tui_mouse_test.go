package main

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Clicking a row is the mouse's version of the arrow keys, and the table's header is not a row: a
// click.
func TestClickingARowSelectsIt(t *testing.T) {
	m, _ := binWith(t, "first.txt", "second.txt", "third.txt")
	require.Len(t, m.shown, 3)

	m.act("", "rows", tableHeaderLines+2)
	assert.Equal(t, 2, m.selected)

	m.act("", "rows", 0)
	assert.Equal(t, 2, m.selected, "a click on the header moved the cursor")
}

// The confirmation offers a yes and a no because restoring writes a file back outside the bin.
func TestTheConfirmationButtonsRestoreOrCancel(t *testing.T) {
	m, work := binWith(t, "notes.txt")
	restored := filepath.Join(work, "notes.txt")

	m.ask()
	require.True(t, m.asking)
	m.act("cancel", "", 0)
	assert.False(t, m.asking, "cancel left the confirmation open")
	assert.NoFileExists(t, restored, "cancel restored the item anyway")

	m.ask()
	require.True(t, m.asking)
	m.act("restore", "", 0)
	assert.False(t, m.asking)
	assert.FileExists(t, restored)
	assert.Empty(t, m.shown, "the restored item is still listed")
}

// A page is the rows the listing has room for under its header.
func TestAPageIsTheHeightOfTheListing(t *testing.T) {
	isolateTrash(t)
	empty, err := newModel()
	require.NoError(t, err)
	assert.Equal(t, 1, empty.page(), "an empty bin has no listing to page through")

	m, _ := binWith(t, "a.txt")
	short := m.page()
	m.frameOf(testWidth, 50)
	assert.Equal(t, short+20, m.page(), "twenty more lines of terminal are twenty more rows")
}
