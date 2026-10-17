package daemon

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Ensure has to start something, so this hands it a program that exits
// together.
func TestEnsureStartsADaemon(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the stand-in daemon is a shell script")
	}
	isolateDaemonState(t)

	exe := filepath.Join(t.TempDir(), "stand-in-recycler")
	require.NoError(t, os.WriteFile(exe, []byte("#!/bin/sh\nexit 0\n"), 0o700))

	started, err := Ensure(exe)
	require.NoError(t, err)
	assert.True(t, started, "no daemon was started")
}

// A daemon is the whole point of the lock: a recycle every few seconds must
// not start a daemon every.
func TestEnsureStandsDownWhileADaemonHoldsTheLock(t *testing.T) {
	isolateDaemonState(t)
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

// Racing callers must start a single daemon between them. The stand-in holds the daemon lock the
// way a real one does, so a loser has something to stand down to.
func TestConcurrentEnsureStartsOneDaemon(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the stand-in daemon is a shell script")
	}
	if _, err := exec.LookPath("flock"); err != nil {
		t.Skip("the stand-in daemon holds the lock with flock(1)")
	}
	isolateDaemonState(t)
	lock, err := LockPath()
	require.NoError(t, err)

	exe := filepath.Join(t.TempDir(), "stand-in-recycler")
	script := "#!/bin/sh\nexec flock " + lock + " sleep 5\n"
	require.NoError(t, os.WriteFile(exe, []byte(script), 0o700))

	const callers = 8
	started := make(chan bool, callers)
	var wg sync.WaitGroup
	for range callers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ok, err := Ensure(exe)
			assert.NoError(t, err)
			started <- ok
		}()
	}
	wg.Wait()
	close(started)

	count := 0
	for ok := range started {
		if ok {
			count++
		}
	}
	assert.Equal(t, 1, count, "expected exactly one caller to start a daemon")
}

// The lock lives in the state directory, which LockPath creates rather than
// failing on a missing one.
func TestLockPathIsCreatedInTheStateDirectory(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "not", "there", "yet")
	useStateDir(t, dir)

	lock, err := LockPath()
	require.NoError(t, err)
	assert.Equal(t, filepath.Join(dir, "daemon.lock"), lock)
	assert.DirExists(t, dir)
}

// One user gets one daemon. A caller that exports its own HOME, cache or data
// directory must reach the same lock as every other caller, or each such
// caller starts a daemon of its own that nothing ever stops.
func TestTheLockIgnoresTheCallersEnvironment(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows keeps the state where the shell's known folders say")
	}
	stateDirIn := func(home string) string {
		t.Helper()
		t.Setenv("HOME", home)
		t.Setenv("XDG_CACHE_HOME", filepath.Join(home, ".cache"))
		t.Setenv("XDG_DATA_HOME", filepath.Join(home, ".local", "share"))
		dir, err := StateDir()
		require.NoError(t, err)
		assert.NotContains(t, dir, home, "the state directory follows the environment")
		return dir
	}
	assert.Equal(t, stateDirIn(t.TempDir()), stateDirIn(t.TempDir()),
		"callers with different environments reach different locks")
}

// A failure here reaches the user as a single line under a recycle, so it has
// to name the call that produced it. A bare errno reads as "function not
// implemented" and says nothing about which operation is missing, which is what
// a cosmopolitan build on macOS reports.
func TestALockFailureNamesTheCallAndThePath(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("this opens a directory as a file")
	}
	dir := t.TempDir()

	_, _, err := tryLock(dir)
	require.Error(t, err, "locking a directory has to fail")
	assert.Contains(t, err.Error(), dir, "the message names the path")
	assert.Regexp(t, `opening the daemon lock|flock on`, err.Error(),
		"the message names the call: %v", err)
}
