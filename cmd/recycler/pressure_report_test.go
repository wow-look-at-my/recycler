package main

import (
	"bytes"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/wow-look-at-my/recycler"
)

func under(dir string, stranded uint64) recycler.Pressure {
	return recycler.Pressure{Dir: dir, Avail: 750, Target: 1000, Total: 4096, Stranded: stranded}
}

func TestPressureIsNamedOnceAndAgainWhenItRecovers(t *testing.T) {
	seen := map[string]bool{}
	var out bytes.Buffer

	reportPressureChanges(&out, []recycler.Pressure{under("/bin", 0)}, seen)
	require.Contains(t, out.String(), "/bin", "the filesystem under its target was not named")
	require.True(t, seen["/bin"], "a reported filesystem was not remembered")

	// Already reported, and the daemon polls: saying so every tick buries it.
	out.Reset()
	reportPressureChanges(&out, []recycler.Pressure{under("/bin", 0)}, seen)
	require.Empty(t, out.String(), "the same pressure was reported twice")

	out.Reset()
	reportPressureChanges(&out, nil, seen)
	require.Contains(t, out.String(), "/bin is back above its target", "recovery was not reported")
	require.False(t, seen["/bin"], "a recovered filesystem is still remembered, so it cannot be reported again")
}

func TestPressureLineSaysWhetherAnythingWasStranded(t *testing.T) {
	line := pressureLine(under("/bin", 0))
	require.Contains(t, line, "nothing recycled is left to give back", "an exhausted bin was not named as such")

	line = pressureLine(under("/bin", 512))
	require.Contains(t, line, "would not go", "stranded bytes were not reported")
	require.NotContains(t, line, "nothing recycled is left", "a bin holding stranded bytes was called empty")
}

func TestPressureReportsEachFilesystemSeparately(t *testing.T) {
	seen := map[string]bool{}
	var out bytes.Buffer

	reportPressureChanges(&out, []recycler.Pressure{under("/bin", 0), under("/other", 0)}, seen)
	got := out.String()
	require.Contains(t, got, "/bin", "both filesystems were not named")
	require.Contains(t, got, "/other", "both filesystems were not named")

	// A single recovers, the other does not. Only the recovery is news.
	out.Reset()
	reportPressureChanges(&out, []recycler.Pressure{under("/bin", 0)}, seen)
	got = out.String()
	require.Contains(t, got, "/other is back above its target", "the recovered filesystem was not reported")
	require.NotContains(t, got, "cannot keep /bin", "a filesystem still under its target was reported again")
}
