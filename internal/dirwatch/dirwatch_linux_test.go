package dirwatch

import (
	"os"
	"path/filepath"
	"slices"
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
	readFor(t, w, Event{Dir: dir, Name: "new"})

	require.NoError(t, os.Remove(filepath.Join(dir, "new")))
	readFor(t, w, Event{Dir: dir, Name: "new"})
}

// readFor reads until want arrives. Some file systems deliver an event a few
// milliseconds after the change that caused it.
func readFor(t *testing.T, w *Watcher, want Event) []Event {
	t.Helper()
	var seen []Event
	for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); time.Sleep(5 * time.Millisecond) {
		b, err := w.Read()
		require.NoError(t, err)
		seen = append(seen, b.Events...)
		if slices.Contains(seen, want) {
			return seen
		}
	}
	t.Fatalf("no %+v in %+v", want, seen)
	return nil
}

func TestARemovedDirectoryReportsItselfAndDropsItsWatch(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "sub")
	require.NoError(t, os.Mkdir(dir, 0o700))
	w := watching(t, dir)
	require.True(t, w.Watching(dir))

	require.NoError(t, os.Remove(dir))
	readFor(t, w, Event{Dir: dir})
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
	quiet, loud := t.TempDir(), t.TempDir()
	w := watching(t, quiet)
	require.NoError(t, w.Add(loud))
	w.Remove(quiet)
	assert.False(t, w.Watching(quiet))

	require.NoError(t, os.WriteFile(filepath.Join(quiet, "new"), nil, 0o600))
	// An event in the other directory arrives after any the first one sent.
	require.NoError(t, os.WriteFile(filepath.Join(loud, "marker"), nil, 0o600))
	for _, ev := range readFor(t, w, Event{Dir: loud, Name: "marker"}) {
		assert.NotEqual(t, quiet, ev.Dir)
	}
}
