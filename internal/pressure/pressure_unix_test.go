//go:build unix

package pressure

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/wow-look-at-my/recycler/internal/diskfree"
	"github.com/wow-look-at-my/recycler/internal/fsutil"
)

// sparseBeyondFree writes a file whose stated length is past everything its
// filesystem has free, while it occupies almost none of it. A compressed
// filesystem does the same to an ordinary build directory.
func sparseBeyondFree(t *testing.T, dir string) string {
	t.Helper()
	avail, _, err := diskfree.Free(dir)
	require.NoError(t, err)
	path := filepath.Join(dir, "sparse")
	f, err := os.Create(path)
	require.NoError(t, err)
	require.NoError(t, f.Truncate(int64(avail)+1<<30))
	require.NoError(t, f.Close())
	require.Greater(t, fsutil.TreeSize(path), int64(avail), "the fixture states no more than the free space")
	return path
}

// A rename into a bin on the same filesystem needs no free space, so no size
// turns it into a deletion.
func TestAMoveWithinOneFilesystemIsNeverDeletedForItsSize(t *testing.T) {
	dir := t.TempDir()
	path := sparseBeyondFree(t, dir)
	bin := filepath.Join(dir, "bin")
	require.NoError(t, os.Mkdir(bin, 0o700))

	d := Check(path, bin)
	assert.False(t, d.Permanent, "a same-filesystem move was turned into a deletion: %s", d.Reason)
}

// A copy needs room for the blocks a path occupies, not for the length its
// files state.
func TestACopyIsSizedByTheBlocksItOccupies(t *testing.T) {
	path := sparseBeyondFree(t, t.TempDir())
	avail, total, err := diskfree.Free(filepath.Dir(path))
	require.NoError(t, err)

	d := fits(decide(avail, total), avail, fsutil.DiskUsage(path))
	assert.False(t, d.Permanent, "a copy was sized by its stated length: %s", d.Reason)
}
