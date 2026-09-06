//go:build unix

package fsutil

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/sys/unix"
)

// A copy has to refuse what it cannot reproduce.
func TestCopyTreeRefusesWhatItCannotReproduce(t *testing.T) {
	dir := t.TempDir()
	fifo := filepath.Join(dir, "pipe")
	require.NoError(t, unix.Mkfifo(fifo, 0o600))

	err := CopyTree(fifo, filepath.Join(dir, "copy"))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "pipe")
	assert.NoFileExists(t, filepath.Join(dir, "copy"))
}

func TestDeviceOf(t *testing.T) {
	dir := t.TempDir()
	dev, err := DeviceOf(dir)
	require.NoError(t, err)

	// Everything inside a directory shares its filesystem.
	sub := filepath.Join(dir, "sub")
	require.NoError(t, os.Mkdir(sub, 0o755))
	subDev, err := DeviceOf(sub)
	require.NoError(t, err)
	assert.Equal(t, dev, subDev)

	_, err = DeviceOf(filepath.Join(dir, "missing"))
	assert.Error(t, err)
}

func TestTopDirOf(t *testing.T) {
	dir := t.TempDir()
	dev, err := DeviceOf(dir)
	require.NoError(t, err)

	// Walking up from a directory on a filesystem reaches that filesystem's mount point, which is always.
	top := TopDirOf(dir, dev)
	assert.True(t, dir == top || len(top) < len(dir), "top directory %q is not an ancestor of %q", top, dir)
	topDev, err := DeviceOf(top)
	require.NoError(t, err)
	assert.Equal(t, dev, topDev)

	// A device number that matches nothing.
	assert.Equal(t, dir, TopDirOf(dir, ^uint64(0)))
}

func TestIsStickyDir(t *testing.T) {
	dir := t.TempDir()

	plain := filepath.Join(dir, "plain")
	require.NoError(t, os.Mkdir(plain, 0o755))
	assert.False(t, IsStickyDir(plain))

	sticky := filepath.Join(dir, "sticky")
	require.NoError(t, os.Mkdir(sticky, 0o755))
	require.NoError(t, os.Chmod(sticky, 0o777|os.ModeSticky))
	assert.True(t, IsStickyDir(sticky))

	file := filepath.Join(dir, "file")
	require.NoError(t, os.WriteFile(file, nil, 0o600))
	assert.False(t, IsStickyDir(file), "a file is not a sticky directory")

	link := filepath.Join(dir, "link")
	require.NoError(t, os.Symlink(sticky, link))
	assert.False(t, IsStickyDir(link), "a symlink to a sticky directory does not count")

	assert.False(t, IsStickyDir(filepath.Join(dir, "missing")))
}
