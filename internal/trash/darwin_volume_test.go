//go:build darwin

package trash

import (
	"os"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/wow-look-at-my/recycler/internal/putback"
)

func TestAnItemTrashedOnAnotherVolumeIsListedAndRestorable(t *testing.T) {
	work := isolateTrash(t)

	volume := filepath.Join(work, "volume")
	original := filepath.Join(volume, "docs", "note.txt")
	dir := filepath.Join(volume, ".Trashes", strconv.Itoa(os.Getuid()))
	require.NoError(t, os.MkdirAll(dir, 0o700))

	trashed := writeFile(t, filepath.Join(dir, "note.txt"), "kept")
	require.NoError(t, putback.Set(dir, "note.txt", putback.Of(volume, original)))

	b, err := Backend()
	require.NoError(t, err)
	b.(*macTrash).mounts = func() []string { return []string{volume} }

	items, err := b.List()
	require.NoError(t, err)
	require.Len(t, items, 1)
	require.Equal(t, trashed, items[0].ID)
	require.Equal(t, original, items[0].OriginalPath)

	dest, err := b.Restore(items[0].ID, "")
	require.NoError(t, err)
	require.Equal(t, original, dest)
	got, err := os.ReadFile(original)
	require.NoError(t, err)
	require.Equal(t, "kept", string(got))
}
