package main

import (
	"bytes"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/wow-look-at-my/recycler"
)

// A filesystem the sweep cannot bring back is named a single time, not on
// every poll, and named again when it recovers.
func TestAPressureIsNamedOnceAndAgainOnRecovery(t *testing.T) {
	seen := map[string]bool{}
	full := []recycler.Pressure{{Dir: "/bin", Avail: 100, Target: 500}}

	var first bytes.Buffer
	reportPressureChanges(&first, full, seen)
	assert.Contains(t, first.String(), "/bin")
	assert.True(t, seen["/bin"], "the directory is remembered as reported")

	var again bytes.Buffer
	reportPressureChanges(&again, full, seen)
	assert.Empty(t, again.String(), "the same pressure was reported twice")

	var recovered bytes.Buffer
	reportPressureChanges(&recovered, nil, seen)
	assert.Contains(t, recovered.String(), "/bin is back above its target")
	assert.False(t, seen["/bin"], "a recovered directory is forgotten")
}

// The line says how short the sweep fell, and whether anything recycled is
// left. Nothing left to give back is the louder of both: the disk is full of
// live data and the bin cannot help.
func TestThePressureLineSaysWhetherAnythingIsLeftToGiveBack(t *testing.T) {
	short := pressureLine(recycler.Pressure{Dir: "/bin", Avail: 100, Target: 500})
	assert.Contains(t, short, "/bin")
	assert.Contains(t, short, "nothing recycled is left to give back")

	stranded := pressureLine(recycler.Pressure{Dir: "/bin", Avail: 100, Target: 500, Stranded: 4096})
	assert.Contains(t, stranded, "would not go")
	assert.False(t, strings.Contains(stranded, "nothing recycled is left"),
		"a sweep that stranded bytes did have something to give back")
}
