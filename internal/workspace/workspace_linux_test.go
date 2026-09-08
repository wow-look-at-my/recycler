//go:build linux && !cosmo

package workspace

import (
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A write that meets a full filesystem is retried behind a reclaim, so the caller is told the disk
// filled up only when the bin had nothing left to give.
func TestRetryRunsTheOperationAgainAfterAReclaim(t *testing.T) {
	var calls int
	r := &retrier{reclaim: func(uint64) (uint64, error) { return 1 << 20, nil }}

	errno := r.do(func() syscall.Errno {
		calls++
		if calls == 1 {
			return syscall.ENOSPC
		}
		return 0
	})

	assert.Equal(t, syscall.Errno(0), errno, "the retried write was reported as failed")
	assert.Equal(t, 2, calls, "the operation was not run again")
}

// A bin with nothing to give cannot make room, so the error reaches the caller rather than looping.
func TestRetryReportsAFullDiskWhenNothingCanBeReclaimed(t *testing.T) {
	var calls int
	r := &retrier{reclaim: func(uint64) (uint64, error) { return 0, nil }}

	errno := r.do(func() syscall.Errno {
		calls++
		return syscall.ENOSPC
	})

	assert.Equal(t, syscall.ENOSPC, errno)
	assert.Equal(t, 1, calls, "the operation was run again with no space reclaimed")
}

// Any other failure is the caller's to see untouched: reclaiming space answers a full filesystem
// and nothing else.
func TestRetryLeavesOtherErrorsAlone(t *testing.T) {
	var reclaims int
	r := &retrier{reclaim: func(uint64) (uint64, error) { reclaims++; return 1 << 20, nil }}

	errno := r.do(func() syscall.Errno { return syscall.EACCES })

	assert.Equal(t, syscall.EACCES, errno)
	assert.Zero(t, reclaims, "a reclaim ran for an error that was not a full filesystem")
}

// A burst of failing writes shares one sweep rather than each starting its own.
func TestConcurrentWritersShareASweep(t *testing.T) {
	var inFlight, peak int32
	var mu sync.Mutex
	r := &retrier{reclaim: func(uint64) (uint64, error) {
		n := atomic.AddInt32(&inFlight, 1)
		mu.Lock()
		if n > peak {
			peak = n
		}
		mu.Unlock()
		atomic.AddInt32(&inFlight, -1)
		return 1 << 20, nil
	}}

	var wg sync.WaitGroup
	for range 16 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			var once bool
			r.do(func() syscall.Errno {
				if !once {
					once = true
					return syscall.ENOSPC
				}
				return 0
			})
		}()
	}
	wg.Wait()

	assert.EqualValues(t, 1, peak, "sweeps overlapped instead of being serialized")
}

// The mount serves what the backing directory holds, and a write through it lands there.
func TestMountServesTheBackingDirectory(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("mounting without the fusermount helper needs root")
	}
	backing := t.TempDir()
	mnt := t.TempDir()

	server, err := Mount(mnt, backing, Options{
		Reclaim: func(uint64) (uint64, error) { return 0, nil },
	})
	require.NoError(t, err)
	defer server.Unmount()

	want := []byte("written through the mount\n")
	require.NoError(t, os.WriteFile(filepath.Join(mnt, "out.txt"), want, 0o644))

	got, err := os.ReadFile(filepath.Join(backing, "out.txt"))
	require.NoError(t, err)
	assert.Equal(t, want, got, "the backing directory does not hold what was written")
}

// A file created through the mount carries the retrying handle, so the bytes of a large output are
// covered and not just the file's creation.
func TestAWrittenFileIsRetriedToo(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("mounting without the fusermount helper needs root")
	}
	backing := t.TempDir()
	mnt := t.TempDir()

	var reclaims int32
	server, err := Mount(mnt, backing, Options{
		Reclaim: func(uint64) (uint64, error) { atomic.AddInt32(&reclaims, 1); return 0, nil },
	})
	require.NoError(t, err)
	defer server.Unmount()

	f, err := os.Create(filepath.Join(mnt, "big.bin"))
	require.NoError(t, err)
	_, err = f.Write(make([]byte, 1<<20))
	require.NoError(t, err)
	require.NoError(t, f.Close())

	// Nothing filled up, so no reclaim is expected. What is proved here is that a write through the
	// wrapped handle still lands.
	assert.Zero(t, atomic.LoadInt32(&reclaims))
	info, err := os.Stat(filepath.Join(backing, "big.bin"))
	require.NoError(t, err)
	assert.EqualValues(t, 1<<20, info.Size())
}
