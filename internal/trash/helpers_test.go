package trash

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/wow-look-at-my/recycler/internal/bin"
)

// recycle, list and restore are what the recycler package's own functions do,
// so a backend test reads the way a caller's code does.
func recycle(paths ...string) error {
	b, err := Backend()
	if err != nil {
		return err
	}
	return b.Recycle(paths)
}

func list() ([]bin.Item, error) {
	b, err := Backend()
	if err != nil {
		return nil, err
	}
	return b.List()
}

func restore(id string) (string, error) {
	b, err := Backend()
	if err != nil {
		return "", err
	}
	return b.Restore(id, "")
}

// isolateTrash points the recycle bin at a temporary directory so tests never
// touch the developer's real one, and returns a scratch directory to recycle
// files from. Both live on the same filesystem, which is what makes recycling a
// rename.
func isolateTrash(t *testing.T) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("the Windows recycle bin is a system location and cannot be redirected for tests")
	}
	root := t.TempDir()
	home := filepath.Join(root, "home")
	require.NoError(t, os.MkdirAll(home, 0o700))
	t.Setenv("HOME", home)
	t.Setenv("XDG_DATA_HOME", filepath.Join(home, ".local", "share"))

	work := filepath.Join(root, "work")
	require.NoError(t, os.MkdirAll(work, 0o700))
	return work
}

func writeFile(t *testing.T, path, content string) string {
	t.Helper()
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
	require.NoError(t, os.WriteFile(path, []byte(content), 0o644))
	return path
}

func mustList(t *testing.T) []bin.Item {
	t.Helper()
	items, err := list()
	require.NoError(t, err)
	return items
}
