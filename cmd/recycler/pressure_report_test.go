package main

import (
	"bytes"
	"strings"
	"testing"

	"github.com/wow-look-at-my/recycler"
)

func under(dir string, stranded uint64) recycler.Pressure {
	return recycler.Pressure{Dir: dir, Avail: 750, Target: 1000, Total: 4096, Stranded: stranded}
}

func TestPressureIsNamedOnceAndAgainWhenItRecovers(t *testing.T) {
	seen := map[string]bool{}
	var out bytes.Buffer

	reportPressureChanges(&out, []recycler.Pressure{under("/bin", 0)}, seen)
	first := out.String()
	if !strings.Contains(first, "/bin") {
		t.Fatalf("the filesystem under its target was not named: %q", first)
	}
	if !seen["/bin"] {
		t.Fatal("a reported filesystem was not remembered")
	}

	// Still under its target, and already reported: the daemon polls, so saying
	// so every tick would bury the transition nobody wants to miss.
	out.Reset()
	reportPressureChanges(&out, []recycler.Pressure{under("/bin", 0)}, seen)
	if out.Len() != 0 {
		t.Fatalf("the same pressure was reported twice: %q", out.String())
	}

	out.Reset()
	reportPressureChanges(&out, nil, seen)
	if got := out.String(); !strings.Contains(got, "/bin is back above its target") {
		t.Fatalf("recovery was not reported: %q", got)
	}
	if seen["/bin"] {
		t.Fatal("a recovered filesystem is still remembered, so it cannot be reported again")
	}
}

func TestPressureLineSaysWhetherAnythingWasStranded(t *testing.T) {
	line := pressureLine(under("/bin", 0))
	if !strings.Contains(line, "nothing recycled is left to give back") {
		t.Fatalf("an exhausted bin was not named as such: %q", line)
	}

	line = pressureLine(under("/bin", 512))
	if !strings.Contains(line, "would not go") {
		t.Fatalf("stranded bytes were not reported: %q", line)
	}
	if strings.Contains(line, "nothing recycled is left") {
		t.Fatalf("a bin holding stranded bytes was called empty: %q", line)
	}
}

func TestPressureReportsEachFilesystemSeparately(t *testing.T) {
	seen := map[string]bool{}
	var out bytes.Buffer

	reportPressureChanges(&out, []recycler.Pressure{under("/bin", 0), under("/other", 0)}, seen)
	got := out.String()
	if !strings.Contains(got, "/bin") || !strings.Contains(got, "/other") {
		t.Fatalf("both filesystems were not named: %q", got)
	}

	// A single recovers, the other does not. Only the recovery is news.
	out.Reset()
	reportPressureChanges(&out, []recycler.Pressure{under("/bin", 0)}, seen)
	got = out.String()
	if !strings.Contains(got, "/other is back above its target") {
		t.Fatalf("the recovered filesystem was not reported: %q", got)
	}
	if strings.Contains(got, "cannot keep /bin") {
		t.Fatalf("a filesystem still under its target was reported again: %q", got)
	}
}
