package daemon

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

// A version is a number, not a string: 1.10 is what comes after 1.9.
func TestVersionsCompareAsNumbers(t *testing.T) {
	cases := []struct {
		a, b string
		want int
	}{
		{"1.10.0", "1.9.0", 1},
		{"1.9.0", "1.10.0", -1},
		{"2.0.0", "1.999.999", 1},
		{"1.2", "1.2.0", 0},
		{"v1.2.3", "1.2.3", 0},
		{"1.2.3", "1.2.3-rc1", 1},
		{"1.2.3-rc1", "1.2.3-rc2", -1},
		{"1.2.3+build9", "1.2.3+build8", 1},
		// A field that is not a number is all that is left to compare as text.
		{"1.2.x", "1.2.3", 1},
	}
	for _, c := range cases {
		assert.Equal(t, c.want, compareVersions(c.a, c.b), "%s against %s", c.a, c.b)
	}
}

// The placeholders an untagged build carries name no release.
func TestPlaceholderVersionsAreNotVersions(t *testing.T) {
	for _, s := range []string{"", " ", "dev", "devel", "(devel)", "unknown", "none"} {
		assert.False(t, versioned(s), "%q was taken for a version", s)
	}
	assert.True(t, versioned("0.0.1"))
}

// Two versions decide between themselves. Without one on both sides, the
// executable's modification time is the only fact left.
func TestNewerFallsBackToTheExecutablesModTime(t *testing.T) {
	now := time.Now().UTC()
	old := Identity{Exe: "/old", ModTime: now.Add(-time.Hour)}
	fresh := Identity{Exe: "/new", ModTime: now}

	assert.True(t, Newer(fresh, old))
	assert.False(t, Newer(old, fresh))
	assert.False(t, Newer(old, old), "a build is not newer than itself")

	// A version on one side alone orders nothing, so the times still decide.
	versionedOld := Identity{Version: "9.9.9", Exe: "/old", ModTime: now.Add(-time.Hour)}
	assert.False(t, Newer(versionedOld, fresh))
	assert.True(t, Newer(fresh, versionedOld))

	// With one on both sides, the times stop mattering.
	assert.True(t, Newer(
		Identity{Version: "2.0.0", ModTime: now.Add(-time.Hour)},
		Identity{Version: "1.0.0", ModTime: now},
	))
}

// The description is what a person reads to tell two daemons apart.
func TestAnIdentityDescribesItself(t *testing.T) {
	at := time.Date(2026, 9, 13, 10, 30, 0, 0, time.UTC)
	assert.Contains(t, Identity{Version: "1.2.3", Exe: "/usr/bin/recycler", ModTime: at}.String(), "1.2.3")
	assert.Contains(t, Identity{Exe: "/usr/bin/recycler", ModTime: at}.String(), "unversioned")
}
