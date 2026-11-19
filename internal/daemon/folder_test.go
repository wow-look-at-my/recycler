package daemon

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/wow-look-at-my/recycler/internal/trash"
)

func aFolder(t *testing.T, ages map[string]time.Duration) string {
	t.Helper()
	dir := t.TempDir()
	for name, age := range ages {
		path := filepath.Join(dir, name)
		require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o700))
		require.NoError(t, os.WriteFile(path, make([]byte, 64<<10), 0o600))
		when := time.Now().Add(-age)
		require.NoError(t, os.Chtimes(path, when, when))
	}
	return dir
}

// isolateBin moves this user's recycle bin into a temporary directory.
func isolateBin(t *testing.T) string {
	t.Helper()
	t.Serial("moves HOME and replaces trash.Mounts")
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_DATA_HOME", filepath.Join(home, "share"))
	mounts := trash.Mounts
	trash.Mounts = func() []string { return nil }
	t.Cleanup(func() { trash.Mounts = mounts })
	return home
}

func folderTotal(t *testing.T, dir string) uint64 {
	t.Helper()
	_, total, err := walkFolder(dir, nil, time.Now())
	require.NoError(t, err)
	return total
}

// Nothing is capped until a person names a folder.
func TestNoFolderIsCappedByDefault(t *testing.T) {
	assert.Empty(t, Settings{}.Folders)
	assert.Empty(t, newFolderWatch().tick(Settings{}, time.Now()))

	path := filepath.Join(t.TempDir(), "settings.json")
	s, err := readSettings(path)
	require.NoError(t, err)
	assert.Empty(t, s.Folders, "no settings file caps no folder")
}

func TestAFolderLosesItsLeastRecentlyUsedFilesFirst(t *testing.T) {
	isolateBin(t)
	dir := aFolder(t, map[string]time.Duration{"a": 3 * time.Hour, "sub/b": 2 * time.Hour, "c": 10 * time.Minute})
	total := folderTotal(t, dir)

	evicted, left, err := sweepFolderTo(Folder{Dir: dir, Delete: true}, total-1, time.Now())
	require.NoError(t, err)
	require.Len(t, evicted, 1)
	assert.Equal(t, filepath.Join(dir, "a"), evicted[0].Item.OriginalPath)
	assert.True(t, evicted[0].LastUsed)
	assert.Contains(t, evicted[0].Reason, "least recently used in "+dir)
	assert.NoFileExists(t, filepath.Join(dir, "a"))
	assert.FileExists(t, filepath.Join(dir, "sub", "b"))
	assert.Less(t, left, total)
}

// A file used within the grace period stays, even when that leaves the folder over its cap.
func TestAFolderKeepsWhatWasUsedRecently(t *testing.T) {
	isolateBin(t)
	dir := aFolder(t, map[string]time.Duration{"a": 3 * time.Hour, "b": 2 * time.Hour, "c": 10 * time.Minute})

	evicted, left, err := sweepFolderTo(Folder{Dir: dir, Delete: true}, 1, time.Now())
	require.NoError(t, err)
	assert.Len(t, evicted, 2)
	assert.FileExists(t, filepath.Join(dir, "c"))
	assert.Greater(t, left, uint64(1))
}

// Without delete a folder's files go to the recycle bin, where they can be restored.
func TestAFolderRecyclesByDefault(t *testing.T) {
	isolateBin(t)
	dir := aFolder(t, map[string]time.Duration{"old": 3 * time.Hour, "new": 2 * time.Hour})
	total := folderTotal(t, dir)

	evicted, _, err := sweepFolderTo(Folder{Dir: dir}, total-1, time.Now())
	require.NoError(t, err)
	require.Len(t, evicted, 1)
	require.NoError(t, evicted[0].Error)
	assert.Contains(t, evicted[0].Reason, "recycled")
	assert.NoFileExists(t, filepath.Join(dir, "old"))

	b, err := trash.Backend()
	require.NoError(t, err)
	items, err := b.List()
	require.NoError(t, err)
	require.Len(t, items, 1)
	assert.Equal(t, filepath.Join(dir, "old"), items[0].OriginalPath)
}

// A folder under its cap is left alone.
func TestAFolderUnderItsCapIsLeftAlone(t *testing.T) {
	isolateBin(t)
	dir := aFolder(t, map[string]time.Duration{"a": 3 * time.Hour})
	evicted, _, err := sweepFolderTo(Folder{Dir: dir, Delete: true}, 1<<30, time.Now())
	require.NoError(t, err)
	assert.Empty(t, evicted)
	assert.FileExists(t, filepath.Join(dir, "a"))
}

// A folder reached through a link names its files the way the folder was named.
func TestAFolderBehindALinkKeepsItsName(t *testing.T) {
	isolateBin(t)
	real := aFolder(t, map[string]time.Duration{"a": 3 * time.Hour, "b": 2 * time.Hour})
	link := filepath.Join(t.TempDir(), "tmp")
	require.NoError(t, os.Symlink(real, link))
	total := folderTotal(t, link)

	evicted, _, err := sweepFolderTo(Folder{Dir: link, Delete: true}, total-1, time.Now())
	require.NoError(t, err)
	require.Len(t, evicted, 1)
	assert.Equal(t, filepath.Join(link, "a"), evicted[0].Item.OriginalPath)
	assert.NoFileExists(t, filepath.Join(real, "a"))
}

// The walk never reaches through a symbolic link.
func TestAFolderSweepDoesNotFollowLinks(t *testing.T) {
	isolateBin(t)
	outside := aFolder(t, map[string]time.Duration{"precious": 5 * time.Hour})
	dir := aFolder(t, map[string]time.Duration{"a": 3 * time.Hour})
	require.NoError(t, os.Symlink(outside, filepath.Join(dir, "link")))

	_, _, err := sweepFolderTo(Folder{Dir: dir, Delete: true}, 1, time.Now())
	require.NoError(t, err)
	assert.FileExists(t, filepath.Join(outside, "precious"))
}

func TestParseFolder(t *testing.T) {
	dir := t.TempDir()
	f, err := ParseFolder(dir + ":10GB:delete")
	require.NoError(t, err)
	assert.Equal(t, Folder{Dir: dir, Max: "10GB", Delete: true}, f)

	f, err = ParseFolder(dir + ":2GiB")
	require.NoError(t, err)
	assert.Equal(t, Folder{Dir: dir, Max: "2GiB"}, f)

	f, err = ParseFolder(dir + ":2GiB:recycle")
	require.NoError(t, err)
	assert.False(t, f.Delete)

	for _, bad := range []string{
		dir,
		dir + ":100MB",         // under the floor
		dir + ":10%",           // not a size
		"relative:10GB",        // not absolute
		"/:10GB:delete",        // the whole filesystem
		dir + ":10GB:sometime", // an unknown mode reads as a size
	} {
		_, err := ParseFolder(bad)
		assert.Error(t, err, bad)
	}
}

func TestAFolderCannotHoldTheHomeDirectory(t *testing.T) {
	home, err := accountHome()
	require.NoError(t, err)
	assert.Error(t, Folder{Dir: home, Max: "10GB"}.Validate())
	assert.Error(t, Folder{Dir: filepath.Dir(home), Max: "10GB"}.Validate())
	assert.NoError(t, Folder{Dir: filepath.Join(home, "Downloads"), Max: "10GB"}.Validate())
}

// A folder named on the command line takes the place of the one in the file.
func TestAFolderFlagReplacesTheSameFolderInTheFile(t *testing.T) {
	t.Serial("replaces Flags")
	Flags = Settings{Folders: []Folder{{Dir: "/tmp", Max: "5GB", Delete: true}}}
	t.Cleanup(func() { Flags = Settings{} })

	s := Settings{Folders: []Folder{{Dir: "/tmp", Max: "20GB"}, {Dir: "/var/tmp", Max: "2GB"}}}.withFlags()
	assert.ElementsMatch(t, []Folder{{Dir: "/var/tmp", Max: "2GB"}, {Dir: "/tmp", Max: "5GB", Delete: true}}, s.Folders)
}
