package trash

import (
	"os"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestVolumeTrashesFindsATrashOnAnyMountPoint(t *testing.T) {
	root := t.TempDir()
	uid := 501

	// A volume mounted outside /Volumes, as the repos volume is.
	nested := filepath.Join(root, "Users", "ada", "repos")
	nestedTrash := filepath.Join(nested, ".Trashes", strconv.Itoa(uid))
	require.NoError(t, os.MkdirAll(nestedTrash, 0o700))

	// A volume that has never had anything trashed on it.
	empty := filepath.Join(root, "Volumes", "empty")
	require.NoError(t, os.MkdirAll(empty, 0o700))

	// A volume whose trash belongs to another user.
	other := filepath.Join(root, "Volumes", "other")
	require.NoError(t, os.MkdirAll(filepath.Join(other, ".Trashes", "502"), 0o700))

	got := volumeTrashes([]string{nested, empty, other, nested}, uid)
	require.Equal(t, []string{nestedTrash}, got)
}
