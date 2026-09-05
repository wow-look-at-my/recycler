package daemon

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Ensure has to actually start something, so this hands it a program that exits
// at once. It is a script rather than the test binary on purpose: running the
// test binary with a "daemon" argument is the fork bomb the suffix check exists
// to refuse.
func TestEnsureStartsADaemon(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the stand-in daemon is a shell script")
	}
	t.Setenv("XDG_CACHE_HOME", t.TempDir())

	exe := filepath.Join(t.TempDir(), "stand-in-recycler")
	require.NoError(t, os.WriteFile(exe, []byte("#!/bin/sh\nexit 0\n"), 0o700))

	started, err := Ensure(exe)
	require.NoError(t, err)
	assert.True(t, started, "no daemon was started")
}

// One daemon per user is the whole point of the lock: a recycle every few
// seconds must not start a daemon every few seconds.
func TestEnsureStandsDownWhileADaemonHoldsTheLock(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	lock, err := LockPath()
	require.NoError(t, err)

	unlock, held, err := tryLock(lock)
	require.NoError(t, err)
	require.True(t, held)
	defer unlock()

	started, err := Ensure(filepath.Join(t.TempDir(), "stand-in-recycler"))
	require.NoError(t, err)
	assert.False(t, started, "a second daemon was started while one held the lock")
}

// The lock lives under the user's cache directory, and Ensure creates it rather
// than failing on a cache directory nobody has made yet.
func TestLockPathIsCreatedUnderTheCacheDirectory(t *testing.T) {
	cache := t.TempDir()
	t.Setenv("XDG_CACHE_HOME", cache)

	lock, err := LockPath()
	require.NoError(t, err)
	assert.Equal(t, filepath.Join(cache, "recycler", "daemon.lock"), lock)
	assert.DirExists(t, filepath.Dir(lock))
}
