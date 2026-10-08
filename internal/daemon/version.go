package daemon

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

// Version is the build this daemon calls itself. The CLI sets it to the same string it prints for --version.
var Version string

// An Identity names the build behind a daemon: what it calls itself, the
// executable it runs from, and when that executable was written.
type Identity struct {
	Version string    `json:"version"`
	Exe     string    `json:"exe"`
	ModTime time.Time `json:"modTime"`
}

// String describes a build closely enough to name it in a message someone reads.
func (id Identity) String() string {
	name := id.Version
	if !versioned(name) {
		name = "an unversioned build"
	}
	return fmt.Sprintf("%s at %s, written %s", name, id.Exe, id.ModTime.Format(time.RFC3339))
}

// selfIdentity describes the build in exe.
func selfIdentity(exe string) Identity {
	id := Identity{Version: Version, Exe: exe}
	if st, err := os.Stat(exe); err == nil {
		id.ModTime = st.ModTime().UTC()
	}
	return id
}

// sameBuild reports whether records name the same build.
func sameBuild(a, b Identity) bool {
	return a.Version == b.Version && a.Exe == b.Exe && a.ModTime.Equal(b.ModTime)
}

// Newer reports whether a is a newer build than b.
func Newer(a, b Identity) bool {
	if versioned(a.Version) && versioned(b.Version) {
		return compareVersions(a.Version, b.Version) > 0
	}
	return a.ModTime.After(b.ModTime)
}

// versioned reports whether s names a release. The placeholders an untagged
// build carries are not numbers anything can be ordered against.
func versioned(s string) bool {
	switch strings.TrimSpace(s) {
	case "", "dev", "devel", "(devel)", "unknown", "none":
		return false
	}
	return true
}

func compareVersions(a, b string) int {
	aCore, aPre := splitPrerelease(a)
	bCore, bPre := splitPrerelease(b)
	aFields, bFields := strings.Split(aCore, "."), strings.Split(bCore, ".")
	for i := 0; i < len(aFields) || i < len(bFields); i++ {
		if c := compareFields(field(aFields, i), field(bFields, i)); c != 0 {
			return c
		}
	}
	return comparePrerelease(aPre, bPre)
}

// splitPrerelease separates the numeric part of a version from the prerelease or
// build suffix after it, and drops the leading v a tag usually carries.
func splitPrerelease(s string) (core, pre string) {
	s = strings.TrimSpace(s)
	s = strings.TrimPrefix(strings.TrimPrefix(s, "v"), "V")
	if i := strings.IndexAny(s, "-+"); i >= 0 {
		return s[:i], s[i+1:]
	}
	return s, ""
}

func field(fields []string, i int) string {
	if i < len(fields) {
		return fields[i]
	}
	return "0"
}

// compareFields orders one dotted field.
func compareFields(a, b string) int {
	an, aErr := strconv.ParseUint(a, 10, 64)
	bn, bErr := strconv.ParseUint(b, 10, 64)
	if aErr == nil && bErr == nil {
		switch {
		case an < bn:
			return -1
		case an > bn:
			return 1
		}
		return 0
	}
	return strings.Compare(a, b)
}

// comparePrerelease orders the suffix. A release outranks the prereleases that
// led to it, which is why an absent suffix wins.
func comparePrerelease(a, b string) int {
	switch {
	case a == b:
		return 0
	case a == "":
		return 1
	case b == "":
		return -1
	}
	return strings.Compare(a, b)
}
