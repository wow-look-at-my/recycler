package main

import (
	"bytes"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/wow-look-at-my/recycler"
)

// A daemon that repeats an unchanged complaint on every poll buries the change
// that matters, so a filesystem is named when it goes under and when it comes
// back, and not in between.
func TestAFilesystemIsNamedWhenItGoesUnderAndWhenItRecovers(t *testing.T) {
	p := recycler.Pressure{Dir: "/bin", Avail: 100, Target: 400, Total: 4000}
	seen := map[string]bool{}

	var first bytes.Buffer
	reportPressureChanges(&first, []recycler.Pressure{p}, seen)
	assert.Contains(t, first.String(), "cannot keep /bin above its target")
	assert.True(t, seen["/bin"])

	var again bytes.Buffer
	reportPressureChanges(&again, []recycler.Pressure{p}, seen)
	assert.Empty(t, again.String())

	var recovered bytes.Buffer
	reportPressureChanges(&recovered, nil, seen)
	assert.Equal(t, "/bin is back above its target\n", recovered.String())
	assert.Empty(t, seen)
}

// The two endings say different things: bytes the sweep tried to give back and
// could not, against a bin that has nothing left to give.
func TestAPressureLineSaysWhetherAnythingRecycledIsLeft(t *testing.T) {
	stranded := pressureLine(recycler.Pressure{Dir: "/bin", Avail: 100, Target: 400, Stranded: 50})
	assert.Contains(t, stranded, "300 B short")
	assert.Contains(t, stranded, "50 B of recycled items would not go")

	empty := pressureLine(recycler.Pressure{Dir: "/bin", Avail: 100, Target: 400})
	assert.Contains(t, empty, "nothing recycled is left to give back")
}
