package main

import (
	"bytes"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/wow-look-at-my/recycler"
)

// A daemon that repeats an unchanged complaint on every poll buries the change
// that matters. A filesystem is named when it goes under and when it comes
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

// Both endings say different things: bytes the sweep tried to give back and
// could not, against a bin that has nothing left to give.
func TestAPressureLineSaysWhetherAnythingRecycledIsLeft(t *testing.T) {
	stranded := pressureLine(recycler.Pressure{Dir: "/bin", Avail: 100, Target: 400, Stranded: 50})
	assert.Contains(t, stranded, "300 B short")
	assert.Contains(t, stranded, "50 B of recycled items would not go")

	empty := pressureLine(recycler.Pressure{Dir: "/bin", Avail: 100, Target: 400})
	assert.Contains(t, empty, "nothing recycled is left to give back")
}

// String carries every number a filesystem was judged on. A reader of the log
// line can act on it with no daemon left to ask.
func TestAPressureSpellsOutTheNumbersItJudgedOn(t *testing.T) {
	said := recycler.Pressure{Dir: "/bin", Avail: 100, Target: 400, Total: 4000}.String()
	assert.Contains(t, said, "/bin")
	assert.Contains(t, said, "100 bytes available")
	assert.Contains(t, said, "300 short", "the shortfall is what the reader acts on")
	assert.Contains(t, said, "400 byte target")
	assert.Contains(t, said, "4000 byte filesystem")
}
