package main

import (
	"path/filepath"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
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

// The frame counter is what animates the interface.
func TestATickAdvancesTheFrameAndSchedulesTheNext(t *testing.T) {
	m, _ := binWith(t, "notes.txt")
	before := m.frame

	_, cmd := m.Update(tick(time.Now()))
	assert.Equal(t, before+1, m.frame)
	assert.NotNil(t, cmd, "the tick scheduled no successor")
	assert.NotNil(t, m.Init(), "the interface starts without a tick")
}

// The view is what reaches the terminal, and it has to ask for motion events: without them a click
// never.
func TestTheViewAsksForMouseMotion(t *testing.T) {
	m, _ := binWith(t, "notes.txt")
	assert.Equal(t, tea.MouseModeAllMotion, m.View().MouseMode)
}

// A page is however tall the last frame made the listing.
func TestAPageIsTheHeightOfThePaintedListing(t *testing.T) {
	isolateTrash(t)
	unpainted, err := newModel()
	require.NoError(t, err)
	assert.Equal(t, 1, unpainted.page())

	painted, _ := binWith(t, "a.txt", "b.txt", "c.txt", "d.txt")
	assert.Equal(t, len(painted.shown), painted.page(), "a page is the rows the last frame drew")
}
