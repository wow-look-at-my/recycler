//go:build unix

package daemon

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// withoutFlock makes every lock attempt report that this system has no flock,
// which is what a macOS user reported and what no host running these tests
// does.
func withoutFlock(t *testing.T, errno syscall.Errno) {
	t.Helper()
	was := flockFn
	flockFn = func(int, int) error { return errno }
	t.Cleanup(func() { flockFn = was })
}

// The reported failure, end to end: a recycle asks for the lock, flock says it
// is not implemented, and the caller gets a lock anyway rather than the error.
func TestALockIsStillTakenWhereFlockIsNotImplemented(t *testing.T) {
	withoutFlock(t, syscall.ENOSYS)
	path := filepath.Join(t.TempDir(), "daemon.lock")

	unlock, ok, err := tryLock(path)
	require.NoError(t, err, "the report this exists for: %v", err)
	require.True(t, ok, "no lock was taken")
	assert.FileExists(t, path+".pid", "the fallback records its holder")

	_, held, err := tryLock(path)
	require.NoError(t, err)
	assert.False(t, held, "the lock excludes a second caller here too")

	unlock()
	_, ok, err = tryLock(path)
	require.NoError(t, err)
	assert.True(t, ok, "the lock frees")
}

// Ensure is what a recycle calls, and it is the place the error was printed.
// With no flock it has to start a daemon rather than report a single missing.
func TestEnsureStartsADaemonWhereFlockIsNotImplemented(t *testing.T) {
	withoutFlock(t, syscall.ENOSYS)
	t.Setenv("XDG_CACHE_HOME", t.TempDir())

	exe := filepath.Join(t.TempDir(), "stand-in")
	require.NoError(t, os.WriteFile(exe, []byte("#!/bin/sh\nexit 0\n"), 0o755))

	started, err := Ensure(exe)
	require.NoError(t, err, "this is the message the user reads: %v", err)
	assert.True(t, started, "no daemon was started")
}

// A lock somebody holds must never reach the fallback: daemons sweeping the
// same items is what the lock exists to prevent.
func TestAHeldLockDoesNotReachTheFallback(t *testing.T) {
	withoutFlock(t, syscall.EWOULDBLOCK)
	path := filepath.Join(t.TempDir(), "daemon.lock")

	_, ok, err := tryLock(path)
	require.NoError(t, err)
	assert.False(t, ok, "a held lock read as free")
	assert.NoFileExists(t, path+".pid", "the fallback ran for a held lock")
}

// An errno that names neither an absent call nor a held lock is reported, and
// names the call. Swallowing it hides a real failure behind a weaker lock.
func TestAnUnexpectedFlockErrorIsReported(t *testing.T) {
	withoutFlock(t, syscall.EACCES)
	path := filepath.Join(t.TempDir(), "daemon.lock")

	_, _, err := tryLock(path)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "flock on", "the message names the call")
	assert.NoFileExists(t, path+".pid", "the fallback ran for a real failure")
}
