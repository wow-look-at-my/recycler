package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// This drives a BUILT binary, named by RECYCLER_TEST_BINARY. The APE picks
// its bin and its lock at run time, so only a Mac answers for a Mac.

// shippedBinary answers the binary to drive, or skips.
func shippedBinary(t *testing.T) string {
	t.Helper()
	path := os.Getenv("RECYCLER_TEST_BINARY")
	if path == "" {
		t.Skip("RECYCLER_TEST_BINARY names no binary")
	}
	abs, err := filepath.Abs(path)
	require.NoError(t, err)
	require.FileExists(t, abs)
	return abs
}

// runShipped runs the binary against a home of its own, so the host's real
// recycle bin is never touched.
func runShipped(t *testing.T, bin, home string, args ...string) (string, error) {
	t.Helper()
	cmd := exec.Command(bin, args...)
	cmd.Env = append(os.Environ(),
		"HOME="+home,
		"XDG_DATA_HOME="+filepath.Join(home, ".local", "share"),
		"XDG_CACHE_HOME="+filepath.Join(home, ".cache"),
	)
	out, err := cmd.CombinedOutput()
	t.Logf("%s %s\n%s", filepath.Base(bin), strings.Join(args, " "), out)
	return string(out), err
}

// The whole reported failure, end to end: recycling must succeed, and it must
// say nothing about a daemon it could not start.
func TestTheShippedBinaryRecyclesAndRestores(t *testing.T) {
	bin := shippedBinary(t)
	home := t.TempDir()
	path := filepath.Join(home, "notes.txt")
	require.NoError(t, os.WriteFile(path, []byte("keep"), 0o644))

	out, err := runShipped(t, bin, home, "trash", path)
	require.NoError(t, err, "recycling failed: %s", out)
	assert.NotContains(t, out, "could not start the disk-pressure daemon",
		"the daemon did not start, which is the failure this proves gone")
	assert.NoFileExists(t, path, "the file never left its place")

	out, err = runShipped(t, bin, home, "restore", "notes.txt")
	require.NoError(t, err, "restoring failed: %s", out)
	assert.FileExists(t, path, "nothing recorded where it came from")
}

// A bin picked off the build tags is the wrong one on a Mac: Finder cannot see it.
func TestTheShippedBinaryUsesThisHostsBin(t *testing.T) {
	bin := shippedBinary(t)
	home := t.TempDir()
	path := filepath.Join(home, "notes.txt")
	require.NoError(t, os.WriteFile(path, []byte("keep"), 0o644))

	out, err := runShipped(t, bin, home, "trash", path)
	require.NoError(t, err, "recycling failed: %s", out)

	mac := filepath.Join(home, ".Trash", "notes.txt")
	fdo := filepath.Join(home, ".local", "share", "Trash", "files", "notes.txt")
	wanted, other := mac, fdo
	if !onMacOS() {
		wanted, other = fdo, mac
	}
	assert.FileExists(t, wanted, "this host's bin is empty")
	assert.NoFileExists(t, other, "the other platform's bin was filled")
}

// onMacOS reports whether this host is a Mac. runtime.GOOS reads "cosmo" in
// the binary under test, so the directory is what answers here too.
func onMacOS() bool {
	st, err := os.Stat("/System/Library/CoreServices")
	return err == nil && st.IsDir()
}
