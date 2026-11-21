package main

import (
	"io"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/wow-look-at-my/tml"
	"github.com/wow-look-at-my/tml/input"
)

// pointer sends pointer input the way the program does: to the model first, then through the view's UI, whose
// events go back to the model. A frame is painted after each, so the geometry the next one lands on is current.
func pointer(t *testing.T, m *model, msgs ...input.Mouse) {
	t.Helper()
	p, err := tml.NewProgram(m.view, m, tml.ProgramOptions{NoInput: true, Output: io.Discard})
	require.NoError(t, err)
	for _, msg := range msgs {
		if !m.Update(p, msg) {
			for _, event := range m.view.UI().Update(msg) {
				m.Update(p, event)
			}
		}
		screen(t, m)
	}
}

func click(t *testing.T, m *model, x, y int, mod input.Mod) {
	t.Helper()
	pointer(t, m,
		input.Mouse{Action: input.Press, Button: input.ButtonLeft, X: x, Y: y, Mod: mod},
		input.Mouse{Action: input.Release, Button: input.ButtonLeft, X: x, Y: y, Mod: mod})
}

func rightClick(t *testing.T, m *model, x, y int) {
	t.Helper()
	pointer(t, m,
		input.Mouse{Action: input.Press, Button: input.ButtonRight, X: x, Y: y},
		input.Mouse{Action: input.Release, Button: input.ButtonRight, X: x, Y: y})
}

// rowPoint is a point on the listing's row on screen, under the heading and its rule.
func rowPoint(t *testing.T, m *model, row int) (int, int) {
	t.Helper()
	rows, ok := m.view.UI().Target("rows")
	require.True(t, ok, "the table is on screen")
	return rows.Box.X + 1, rows.Box.Y + tableHeaderLines + row
}

// headingPoint is a point on the heading of a column, found by its title in the drawn frame.
func headingPoint(t *testing.T, m *model, title string) (int, int) {
	t.Helper()
	rows, ok := m.view.UI().Target("rows")
	require.True(t, ok)
	line := strings.Split(screen(t, m), "\n")[rows.Box.Y]
	at := strings.Index(line, title)
	require.GreaterOrEqual(t, at, 0, "%q is a heading", title)
	return len([]rune(line[:at])), rows.Box.Y
}

// menuPoint is a point on line i of the open context menu.
func menuPoint(t *testing.T, m *model, i int) (int, int) {
	t.Helper()
	menu, ok := m.view.UI().Target("menuList")
	require.True(t, ok, "the menu is on screen")
	return menu.Rect.X + 1, menu.Rect.Y + i
}

// Clicking a row is the mouse's version of the arrow keys.
func TestClickingARowSelectsIt(t *testing.T) {
	m, _ := binWith(t, "first.txt", "second.txt", "third.txt")
	require.Len(t, m.shown, 3)

	x, y := rowPoint(t, m, 2)
	click(t, m, x, y, 0)
	assert.Equal(t, 2, m.selected)
}

// A click on a heading sorts on its column, and a second click turns the order around.
func TestClickingAHeadingSorts(t *testing.T) {
	m, _ := binWith(t, "b.txt", "a.txt", "c.txt")

	x, y := headingPoint(t, m, "NAME")
	click(t, m, x, y, 0)
	assert.Equal(t, sortName, m.sortBy)
	assert.False(t, m.descending)
	assert.Equal(t, "a.txt", m.shown[0].Name)

	x, y = headingPoint(t, m, "NAME")
	click(t, m, x, y, 0)
	assert.True(t, m.descending)
	assert.Equal(t, "c.txt", m.shown[0].Name)
}

// Ctrl+click marks a row or takes the mark away, and shift+click marks every row from the last click.
func TestModifierClicksMark(t *testing.T) {
	m, _ := binWith(t, "a.txt", "b.txt", "c.txt", "d.txt")

	x, y := rowPoint(t, m, 0)
	click(t, m, x, y, input.Ctrl)
	assert.Len(t, m.marks, 1)
	click(t, m, x, y, input.Ctrl)
	assert.Empty(t, m.marks, "a second ctrl+click takes the mark away")

	click(t, m, x, y, 0)
	x, y = rowPoint(t, m, 2)
	click(t, m, x, y, input.Shift)
	assert.Len(t, m.marks, 3, "rows 0 to 2")
	assert.Equal(t, 2, m.selected)
}

// A double click restores, after the confirmation.
func TestADoubleClickAsksToRestore(t *testing.T) {
	m, _ := binWith(t, "notes.txt")

	x, y := rowPoint(t, m, 0)
	click(t, m, x, y, 0)
	assert.Equal(t, askNone, m.ask, "one click only selects")
	click(t, m, x, y, 0)
	assert.Equal(t, askRestore, m.ask)
}

// A right click on a row selects it and opens a menu there, and a click on an entry runs it.
func TestARightClickOpensTheItemMenu(t *testing.T) {
	m, _ := binWith(t, "a.txt", "b.txt")

	x, y := rowPoint(t, m, 1)
	rightClick(t, m, x, y)
	require.NotEmpty(t, m.menu, "the menu opened")
	assert.Equal(t, 1, m.selected, "the row under the click is the one the menu acts on")
	out := screen(t, m)
	for _, entry := range []string{"Restore", "Restore to…", "Mark", "Delete permanently…"} {
		assert.Contains(t, out, entry)
	}

	mx, my := menuPoint(t, m, 2)
	click(t, m, mx, my, 0)
	assert.Empty(t, m.menu, "running an entry closes the menu")
	assert.True(t, m.marks[m.shown[1].ID], "the Mark entry marked the row")
}

// A right click on a heading offers both orders of its column.
func TestARightClickOnAHeadingOffersSorting(t *testing.T) {
	m, _ := binWith(t, "a.txt")

	x, y := headingPoint(t, m, "SIZE")
	rightClick(t, m, x, y)
	require.Len(t, m.menu, 2)
	assert.Contains(t, screen(t, m), "Sort by size, descending")

	mx, my := menuPoint(t, m, 1)
	click(t, m, mx, my, 0)
	assert.Equal(t, sortSize, m.sortBy)
	assert.True(t, m.descending)
}

// A click outside the menu closes it and does nothing else.
func TestAClickOutsideTheMenuClosesIt(t *testing.T) {
	m, _ := binWith(t, "a.txt", "b.txt")

	x, y := rowPoint(t, m, 0)
	rightClick(t, m, x, y)
	require.NotEmpty(t, m.menu)

	x, y = rowPoint(t, m, 1)
	click(t, m, x, y, 0)
	assert.Empty(t, m.menu)
	assert.Equal(t, 0, m.selected, "the click that closed the menu did not also select")
}

// The menu opens from the keyboard too, and the arrows and enter drive it.
func TestTheMenuKeyOpensTheMenu(t *testing.T) {
	m, _ := binWith(t, "a.txt")

	press(t, m, "m")
	require.NotEmpty(t, m.menu)
	press(t, m, "down", "enter")
	assert.Empty(t, m.menu)
	assert.True(t, m.prompting, "the second entry is Restore to…")

	press(t, m, "esc", "m", "esc")
	assert.Empty(t, m.menu)
	assert.False(t, m.quitting, "esc closed the menu rather than quitting")
}

// The wheel over the listing scrolls it.
func TestTheWheelScrollsTheListing(t *testing.T) {
	m := largeBin(t, 200)
	screen(t, m)
	x, y := rowPoint(t, m, 3)

	pointer(t, m, input.Mouse{Action: input.Wheel, Button: input.WheelDown, X: x, Y: y})
	pointer(t, m, input.Mouse{Action: input.Wheel, Button: input.WheelDown, X: x, Y: y})
	assert.Equal(t, 2, m.offset)
	pointer(t, m, input.Mouse{Action: input.Wheel, Button: input.WheelUp, X: x, Y: y})
	assert.Equal(t, 1, m.offset)
}

// Dragging the scrollbar's thumb to the bottom scrolls to the end, and back to the top scrolls to the start.
func TestDraggingTheScrollbarScrolls(t *testing.T) {
	m := largeBin(t, 200)
	screen(t, m)
	items, ok := m.view.UI().Target("items")
	require.True(t, ok)
	bar := items.Box.X + items.Box.W - 1
	bottom := items.Box.Y + items.Box.H - 1

	pointer(t, m,
		input.Mouse{Action: input.Press, Button: input.ButtonLeft, X: bar, Y: items.Box.Y},
		input.Mouse{Action: input.Motion, Button: input.ButtonLeft, X: bar, Y: bottom})
	assert.Equal(t, len(m.shown)-m.viewRows, m.offset, "the thumb at the bottom is the end of the listing")

	pointer(t, m,
		input.Mouse{Action: input.Motion, Button: input.ButtonLeft, X: bar, Y: items.Box.Y},
		input.Mouse{Action: input.Release, Button: input.ButtonLeft, X: bar, Y: items.Box.Y})
	assert.Equal(t, 0, m.offset)
}

// The confirmation offers a yes and a no because restoring writes a file back outside the bin.
func TestTheConfirmationButtonsRestoreOrCancel(t *testing.T) {
	m, work := binWith(t, "notes.txt")
	restored := filepath.Join(work, "notes.txt")

	m.askRestore()
	require.Equal(t, askRestore, m.ask)
	m.activated(tml.Event{Kind: tml.Activated, Action: "cancel"})
	assert.Equal(t, askNone, m.ask, "cancel left the confirmation open")
	assert.NoFileExists(t, restored, "cancel restored the item anyway")

	m.askRestore()
	m.activated(tml.Event{Kind: tml.Activated, Action: "confirm"})
	assert.Equal(t, askNone, m.ask)
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
