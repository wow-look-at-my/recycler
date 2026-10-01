package main

import (
	"bytes"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/wow-look-at-my/recycler"
)

// A filesystem the sweep could not bring back up is named a single time, and
// named again when it recovers. Reporting it every poll would bury it; never
// reporting the recovery leaves the reader watching a problem that went away.
func TestPressureIsNamedOnceAndItsRecoveryOnce(t *testing.T) {
	short := recycler.Pressure{Dir: "/data/.Trash", Avail: 1 << 20, Target: 3 << 20, Total: 100 << 20}
	seen := map[string]bool{}

	var out bytes.Buffer
	reportPressureChanges(&out, []recycler.Pressure{short}, seen)
	assert.Contains(t, out.String(), "/data/.Trash")
	assert.True(t, seen["/data/.Trash"], "a reported filesystem was not remembered")

	out.Reset()
	reportPressureChanges(&out, []recycler.Pressure{short}, seen)
	assert.Empty(t, out.String(), "the same pressure was reported twice")

	out.Reset()
	reportPressureChanges(&out, nil, seen)
	assert.Contains(t, out.String(), "/data/.Trash is back above its target")
	assert.Empty(t, seen, "a recovered filesystem is still remembered, so it cannot be reported again")

	out.Reset()
	reportPressureChanges(&out, nil, seen)
	assert.Empty(t, out.String(), "a recovery was reported twice")
}

// What a sweep could not give back decides how the line ends: bytes that failed
// to go are a different problem from a bin with nothing left in it.
func TestThePressureLineSaysWhyTheSweepFellShort(t *testing.T) {
	empty := recycler.Pressure{Dir: "/data/.Trash", Avail: 1 << 20, Target: 3 << 20, Total: 100 << 20}
	line := pressureLine(empty)
	assert.Contains(t, line, "cannot keep /data/.Trash above its target")
	assert.Contains(t, line, "nothing recycled is left to give back")

	stranded := empty
	stranded.Stranded = 2 << 20
	line = pressureLine(stranded)
	assert.Contains(t, line, "would not go")
	assert.NotContains(t, line, "nothing recycled is left")
}

// The daemon's own summary of a filesystem carries every number it judged on,
// which is what makes a log line answerable without the daemon to hand.
func TestAPressureSpellsOutEveryNumberItJudgedOn(t *testing.T) {
	p := recycler.Pressure{Dir: "/data/.Trash", Avail: 1 << 20, Target: 3 << 20, Total: 100 << 20}
	said := p.String()
	assert.Contains(t, said, "/data/.Trash")
	assert.Contains(t, said, "1048576")
	assert.Contains(t, said, "2097152", "the shortfall is what the reader acts on")
	assert.Contains(t, said, "3145728")
	assert.Contains(t, said, "104857600")
}
