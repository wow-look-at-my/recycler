package daemon

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A walk costs a system call for each file in the folder. A folder under its
// cap that nothing touched has nothing new to say.
func TestAnUnchangedFolderIsNotWalkedAgain(t *testing.T) {
	isolateBin(t)
	dir := aFolder(t, map[string]time.Duration{"a/old.bin": 48 * time.Hour})
	s := Settings{Folders: []Folder{{Dir: dir, Max: "1GiB", Delete: true}}}
	w := newWatchedFolders()
	t.Cleanup(w.close)
	w.tick(Settings{}, time.Now())
	assert.Nil(t, w.watch, "a watch opened with no folder capped")

	now := time.Now()
	w.tick(s, now)
	require.NotNil(t, w.watch)
	require.Equal(t, 1, w.walks, "the first tick has to walk")
	for range 3 {
		now = now.Add(folderInterval)
		w.tick(s, now)
	}
	assert.Equal(t, 1, w.walks, "an unchanged folder under its cap was walked again")

	// A change deep in the folder, in a directory the walk entered.
	require.NoError(t, os.WriteFile(filepath.Join(dir, "a", "new.bin"), []byte("x"), 0o600))
	w.waitForChange(t, s, dir)
	now = now.Add(folderInterval)
	w.tick(s, now)
	assert.Equal(t, 2, w.walks, "a change the watch saw was never walked")

	// A directory made after the walk is watched by the walk that follows.
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "b"), 0o700))
	w.waitForChange(t, s, dir)
	now = now.Add(folderInterval)
	w.tick(s, now)
	require.Equal(t, 3, w.walks)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "b", "later.bin"), []byte("x"), 0o600))
	w.waitForChange(t, s, dir)
	now = now.Add(folderInterval)
	w.tick(s, now)
	assert.Equal(t, 4, w.walks, "a change in a directory made after the first walk was missed")
}

// waitForChange drains the watch until it marks dir. Some file systems deliver
// an event a few milliseconds after the change that caused it.
func (w *folderWatch) waitForChange(t *testing.T, s Settings, dir string) {
	t.Helper()
	for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); time.Sleep(5 * time.Millisecond) {
		w.drain(s.Folders)
		if w.dirty.Contains(dir) {
			return
		}
	}
	t.Fatalf("the watch never saw %s change", dir)
}

// A folder over its cap is walked again, because its files age past FolderGrace.
func TestAFolderOverItsCapIsWalkedAgain(t *testing.T) {
	isolateBin(t)
	dir := aFolder(t, map[string]time.Duration{"recent.bin": time.Minute})
	w := newWatchedFolders()
	t.Cleanup(w.close)
	w.over.Add(dir)
	s := Settings{Folders: []Folder{{Dir: dir, Max: "1GiB", Delete: true}}}

	now := time.Now()
	w.tick(s, now)
	w.over.Add(dir)
	w.tick(s, now.Add(folderInterval))
	assert.Equal(t, 2, w.walks)
}
