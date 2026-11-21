package dirwatch

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func watching(t *testing.T, dir string) *Watcher {
	t.Helper()
	w, err := New()
	require.NoError(t, err)
	t.Cleanup(func() { w.Close() })
	require.NoError(t, w.Add(dir))
	return w
}

func TestAWatchNamesTheEntryThatChanged(t *testing.T) {
	dir := t.TempDir()
	w := watching(t, dir)

	b, err := w.Read()
	require.NoError(t, err)
	assert.Empty(t, b.Events, "nothing changed yet")

	require.NoError(t, os.WriteFile(filepath.Join(dir, "new"), []byte("x"), 0o600))
	for i := range 50 {
		b, err = w.Read()
		t.Logf("DEBUG read %d: %+v err=%v wds=%v", i, b, err, w.wds)
		if len(b.Events) > 0 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	require.NoError(t, err)
	assert.Contains(t, b.Events, Event{Dir: dir, Name: "new"})

	require.NoError(t, os.Remove(filepath.Join(dir, "new")))
	b, err = w.Read()
	require.NoError(t, err)
	assert.Contains(t, b.Events, Event{Dir: dir, Name: "new"})
}

func TestARemovedDirectoryReportsItselfAndDropsItsWatch(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "sub")
	require.NoError(t, os.Mkdir(dir, 0o700))
	w := watching(t, dir)
	require.True(t, w.Watching(dir))

	require.NoError(t, os.Remove(dir))
	b, err := w.Read()
	require.NoError(t, err)
	assert.Contains(t, b.Events, Event{Dir: dir})
	assert.False(t, w.Watching(dir))
	assert.Zero(t, w.Len())
}

func TestAMissingDirectoryCannotBeWatched(t *testing.T) {
	w, err := New()
	require.NoError(t, err)
	defer w.Close()
	assert.ErrorIs(t, w.Add(filepath.Join(t.TempDir(), "absent")), os.ErrNotExist)
}

func TestRemoveStopsTheEvents(t *testing.T) {
	dir := t.TempDir()
	w := watching(t, dir)
	w.Remove(dir)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "new"), nil, 0o600))
	b, err := w.Read()
	require.NoError(t, err)
	for _, ev := range b.Events {
		assert.NotEqual(t, "new", ev.Name)
	}
}
