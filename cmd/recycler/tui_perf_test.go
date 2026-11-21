package main

import (
	"fmt"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/wow-look-at-my/recycler"
	"github.com/wow-look-at-my/tml"
)

// largeBin opens the interface on a bin of n made-up items, with no recycle bin behind it.
func largeBin(t *testing.T, n int) *model {
	t.Helper()
	isolateTrash(t)
	m, err := newModel()
	require.NoError(t, err)
	now := time.Now()
	for i := range n {
		name := fmt.Sprintf("file-%06d.txt", i)
		m.items = append(m.items, recycler.Item{
			ID:           name,
			Name:         name,
			OriginalPath: filepath.Join("/home/someone/projects/a-fairly-long-directory-name", fmt.Sprint(i%97), name),
			DeletedAt:    now.Add(-time.Duration(i) * time.Minute),
			Size:         int64(i) * 1024,
		})
	}
	m.apply()
	m.settle()
	return m
}

// allocated is the bytes one frame of m allocates at the given size.
func allocated(m *model, width, height int) uint64 {
	m.frameOf(width, height)
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	m.frameOf(width, height)
	runtime.ReadMemStats(&after)
	return after.TotalAlloc - before.TotalAlloc
}

// A resize redraws the whole screen. A frame costs the rows on screen, not the rows in the bin.
func TestAFrameCostsTheScreenNotTheBin(t *testing.T) {
	small := allocated(largeBin(t, 200), 120, 40)
	large := allocated(largeBin(t, 20000), 120, 40)
	assert.Less(t, large, 2*small, "a frame of 20000 items allocated %d bytes, and one of 200 items %d", large, small)

	m := largeBin(t, 20000)
	assert.LessOrEqual(t, len(m.rows()), 40, "the table is handed more rows than the terminal is tall")

}

// frameBudget is one frame at a set number of frames a second.
const frameBudget = 16 * time.Millisecond

// A resize is the model taking the new size and the program drawing it. Each step of a drag must fit in a frame.
func TestAResizeFitsInAFrame(t *testing.T) {
	m := largeBin(t, 20000)
	p, err := tml.NewProgram(m.view, m, tml.ProgramOptions{NoInput: true})
	require.NoError(t, err)

	const steps = 40
	start := time.Now()
	for step := range steps {
		m.Update(p, tml.Resize{Width: 160 + step, Height: 50})
		_, err := m.view.Draw(m.Props(), m.width, m.height)
		require.NoError(t, err)
	}
	perFrame := time.Since(start) / steps
	assert.LessOrEqual(t, perFrame, frameBudget, "a resize of a 20000-item bin took %s", perFrame)
}

// The listing scrolls to its last row and keeps its header on screen.
func TestTheEndOfALongListingIsReachable(t *testing.T) {
	m := largeBin(t, 500)
	press(t, m, "end")
	out := ansi.Strip(m.frameOf(testWidth, 30))
	assert.Contains(t, out, "file-000499.txt")
	assert.Contains(t, out, "NAME", "the header scrolled away")
	assert.Equal(t, 499, m.selected)
}

// A taller terminal at the end of the listing shows more of it, rather than blank rows under the last item.
func TestGrowingAtTheEndFillsTheListing(t *testing.T) {
	m := largeBin(t, 500)
	press(t, m, "end")
	m.frameOf(testWidth, 50)
	require.Positive(t, m.viewRows)
	assert.Equal(t, 500-m.viewRows, m.offset, "the last screenful starts where it fills the viewport")
	out := ansi.Strip(m.frameOf(testWidth, 50))
	assert.Contains(t, out, fmt.Sprintf("file-%06d.txt", m.offset))
	assert.Contains(t, out, "file-000499.txt")
}

// A click names a row on screen, so it lands on the row under the pointer however far the listing scrolled.
func TestAClickWhileScrolledSelectsTheRowUnderIt(t *testing.T) {
	m := largeBin(t, 500)
	press(t, m, "end")
	x, y := rowPoint(t, m, 1)
	click(t, m, x, y, 0)
	assert.Equal(t, m.first()+1, m.selected)
}

// The wheel scrolls the listing without moving the selection, and stops at either end.
func TestTheWheelScrollsWithinTheListing(t *testing.T) {
	m := largeBin(t, 500)
	p, err := tml.NewProgram(m.view, m, tml.ProgramOptions{NoInput: true})
	require.NoError(t, err)
	wheel := func(delta int) { m.Update(p, tml.Event{Kind: tml.Scrolled, ID: "items", Delta: delta}) }

	wheel(-1)
	assert.Equal(t, 0, m.offset)
	wheel(3)
	assert.Equal(t, 3, m.offset)
	assert.Equal(t, 0, m.selected, "the wheel moved the selection")
	wheel(600)
	assert.Equal(t, 500-m.viewRows, m.offset)
}
