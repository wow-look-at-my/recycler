//go:build unix

package recycler

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDeviceOf(t *testing.T) {
	dir := t.TempDir()
	dev, err := deviceOf(dir)
	require.NoError(t, err)

	// Everything inside one directory is on one filesystem.
	sub := filepath.Join(dir, "sub")
	require.NoError(t, os.Mkdir(sub, 0o755))
	subDev, err := deviceOf(sub)
	require.NoError(t, err)
	assert.Equal(t, dev, subDev)

	_, err = deviceOf(filepath.Join(dir, "missing"))
	assert.Error(t, err)
}

func TestTopDirOf(t *testing.T) {
	dir := t.TempDir()
	dev, err := deviceOf(dir)
	require.NoError(t, err)

	// Walking up from a directory on a filesystem reaches that filesystem's
	// mount point, which is always an ancestor of where it started.
	top := topDirOf(dir, dev)
	assert.True(t, dir == top || len(top) < len(dir), "top directory %q is not an ancestor of %q", top, dir)
	topDev, err := deviceOf(top)
	require.NoError(t, err)
	assert.Equal(t, dev, topDev)

	// A device number that matches nothing stops the walk immediately.
	assert.Equal(t, dir, topDirOf(dir, ^uint64(0)))
}

func TestIsStickyDir(t *testing.T) {
	dir := t.TempDir()

	plain := filepath.Join(dir, "plain")
	require.NoError(t, os.Mkdir(plain, 0o755))
	assert.False(t, isStickyDir(plain))

	sticky := filepath.Join(dir, "sticky")
	require.NoError(t, os.Mkdir(sticky, 0o755))
	require.NoError(t, os.Chmod(sticky, 0o777|os.ModeSticky))
	assert.True(t, isStickyDir(sticky))

	file := filepath.Join(dir, "file")
	require.NoError(t, os.WriteFile(file, nil, 0o600))
	assert.False(t, isStickyDir(file), "a file is not a sticky directory")

	link := filepath.Join(dir, "link")
	require.NoError(t, os.Symlink(sticky, link))
	assert.False(t, isStickyDir(link), "a symlink to a sticky directory does not count")

	assert.False(t, isStickyDir(filepath.Join(dir, "missing")))
}
