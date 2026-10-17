package daemon

import (
	"bufio"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// standInLockEnv makes the test binary a stand-in daemon that holds the lock it names until it is killed.
const standInLockEnv = "RECYCLER_TEST_STAND_IN_LOCK"

func TestMain(m *testing.M) {
	if lock := os.Getenv(standInLockEnv); lock != "" {
		if _, ok, err := tryLock(lock); err != nil || !ok {
			os.Exit(3)
		}
		os.Stdout.WriteString("holding\n")
		select {}
	}
	os.Exit(m.Run())
}

// startStandIn runs a process with args as its command line, holding lock,
// and returns once it holds it.
func startStandIn(t *testing.T, lock string, args ...string) *exec.Cmd {
	t.Helper()
	if _, err := os.Stat("/proc/self/fd"); err != nil {
		t.Skip("a portable build runs this file on a kernel with no /proc, where nothing is reaped")
	}
	require.NoError(t, os.MkdirAll(filepath.Dir(lock), 0o700))
	cmd := exec.Command(os.Args[0], args...)
	cmd.Env = append(os.Environ(), standInLockEnv+"="+lock)
	out, err := cmd.StdoutPipe()
	require.NoError(t, err)
	require.NoError(t, cmd.Start())
	t.Cleanup(func() {
		cmd.Process.Kill()
		cmd.Wait()
	})
	line, err := bufio.NewReader(out).ReadString('\n')
	require.NoError(t, err, "the stand-in never took its lock")
	require.Equal(t, "holding\n", line)
	return cmd
}

// lockIn returns a daemon lock path under its own state directory.
func lockIn(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "recycler", "daemon.lock")
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o700))
	require.NoError(t, os.WriteFile(path, nil, 0o600))
	return path
}

// exited reports whether cmd ends within a few seconds.
func exited(cmd *exec.Cmd) bool {
	done := make(chan struct{})
	go func() {
		cmd.Process.Wait()
		close(done)
	}()
	select {
	case <-done:
		return true
	case <-time.After(5 * time.Second):
		return false
	}
}

// A daemon whose lock sits in another directory is a second daemon for the
// same user, and it never stops by itself.
func TestADaemonUnderAnotherLockIsStopped(t *testing.T) {
	mine := lockIn(t)
	stray := startStandIn(t, lockIn(t), "daemon", "--standby")

	reaped, err := reapAmong(mine, []int{stray.Process.Pid})
	require.NoError(t, err)
	assert.Equal(t, []int{stray.Process.Pid}, reaped)
	assert.True(t, exited(stray), "the second daemon is still running")
}

// A process that has this lock open is waiting for it, which is how a newer
// build takes over. Stopping it would end the handover.
func TestADaemonWaitingOnTheSameLockIsLeftAlone(t *testing.T) {
	mine := lockIn(t)
	waiting := startStandIn(t, mine, "daemon")

	reaped, err := reapAmong(mine, []int{waiting.Process.Pid})
	require.NoError(t, err)
	assert.Empty(t, reaped)
}

// Only the sweep loop counts. A recycle or a daemon up holds a lock for an
// instant, and stopping it would interrupt the user.
func TestOnlyTheSweepLoopIsStopped(t *testing.T) {
	mine := lockIn(t)
	for _, args := range [][]string{{"trash", "notes.txt"}, {"daemon", "up"}, {"daemon", "--once"}} {
		other := startStandIn(t, lockIn(t), args...)
		reaped, err := reapAmong(mine, []int{other.Process.Pid})
		require.NoError(t, err)
		assert.Empty(t, reaped, "%v was stopped", args)
	}
}
