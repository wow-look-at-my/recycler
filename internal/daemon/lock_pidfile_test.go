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

// A system that cannot flock says so with any of these.
func TestOnlyAnUnsupportedFlockFallsThrough(t *testing.T) {
	assert.True(t, lockUnsupported(syscall.ENOSYS))
	assert.True(t, lockUnsupported(syscall.EOPNOTSUPP))
	assert.False(t, lockUnsupported(syscall.EWOULDBLOCK), "a held lock is not an absent one")
	assert.False(t, lockUnsupported(syscall.EACCES))
	assert.False(t, lockUnsupported(syscall.EINVAL), "a bad argument is this package's defect")
}

// The fallback is a lock: the next caller is told somebody holds it, and gets
// it a single time the holder lets go.
func TestThePidLockExcludesASecondCaller(t *testing.T) {
	path := filepath.Join(t.TempDir(), "daemon.lock.pid")

	unlock, ok, err := tryPidLock(path)
	require.NoError(t, err)
	require.True(t, ok, "the first caller takes the lock")
	assert.FileExists(t, path)

	_, held, err := tryPidLock(path)
	require.NoError(t, err)
	assert.False(t, held, "a second caller must not take a held lock")

	unlock()
	assert.NoFileExists(t, path, "letting go removes the file")

	_, ok, err = tryPidLock(path)
	require.NoError(t, err)
	assert.True(t, ok, "the lock is free again")
}

// A daemon killed outright leaves its file behind. A pid nothing answers to is
// its remains, so the lock is claimed rather than held forever.
func TestAStaleLockIsClaimed(t *testing.T) {
	path := filepath.Join(t.TempDir(), "daemon.lock.pid")
	// A pid past the kernel's maximum is never allocated to a live process.
	require.NoError(t, os.WriteFile(path, []byte("4194304\n"), 0o600))

	unlock, ok, err := tryPidLock(path)
	require.NoError(t, err)
	require.True(t, ok, "a dead holder does not keep the lock")
	unlock()
}

// An unreadable record reads as a live holder. Refusing to start costs a poll,
// and starting another daemon costs sweeps racing over the same items.
func TestAnUnparseableRecordCountsAsHeld(t *testing.T) {
	path := filepath.Join(t.TempDir(), "daemon.lock.pid")
	require.NoError(t, os.WriteFile(path, []byte("not a pid\n"), 0o600))

	_, ok, err := tryPidLock(path)
	require.NoError(t, err)
	assert.False(t, ok, "a record this cannot read is treated as somebody's")
}
