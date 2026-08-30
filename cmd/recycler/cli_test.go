package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"

	"github.com/spf13/pflag"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/wow-look-at-my/recycler"
)

// isolateTrash points the recycle bin at a temporary directory so the tests
// never touch the developer's real one, and returns a scratch directory to
// recycle files from.
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

// run executes the command line and returns everything it printed. The command
// tree is a package-level singleton, so every flag is put back the way it was
// afterwards.
func run(t *testing.T, stdin string, args ...string) (string, error) {
	t.Helper()
	t.Cleanup(resetCommands)

	out := &bytes.Buffer{}
	rootCmd.SetOut(out)
	rootCmd.SetErr(out)
	rootCmd.SetIn(strings.NewReader(stdin))
	rootCmd.SetArgs(args)
	err := rootCmd.Execute()
	return out.String(), err
}

func resetCommands() {
	rootCmd.SetArgs(nil)
	for _, cmd := range rootCmd.Commands() {
		cmd.Flags().VisitAll(func(f *pflag.Flag) {
			_ = f.Value.Set(f.DefValue)
			f.Changed = false
		})
	}
}

func TestTrashListRestore(t *testing.T) {
	work := isolateTrash(t)
	path := writeFile(t, filepath.Join(work, "notes.txt"), "keep me")

	out, err := run(t, "", "trash", path)
	require.NoError(t, err)
	assert.Contains(t, out, "recycled "+path)
	assert.NoFileExists(t, path)

	out, err = run(t, "", "list")
	require.NoError(t, err)
	assert.Contains(t, out, path)
	assert.Contains(t, out, "7 B")

	// The original path is enough to name the item.
	out, err = run(t, "", "restore", path)
	require.NoError(t, err)
	assert.Contains(t, out, "restored "+path)
	assert.FileExists(t, path)
}

func TestTrashQuiet(t *testing.T) {
	work := isolateTrash(t)
	path := writeFile(t, filepath.Join(work, "quiet.txt"), "x")

	out, err := run(t, "", "trash", "--quiet", path)
	require.NoError(t, err)
	assert.Empty(t, out)
}

func TestTrashReportsMissingPaths(t *testing.T) {
	work := isolateTrash(t)
	_, err := run(t, "", "trash", filepath.Join(work, "absent.txt"))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "absent.txt")
}

func TestListJSON(t *testing.T) {
	work := isolateTrash(t)
	path := writeFile(t, filepath.Join(work, "data.bin"), "0123456789")
	_, err := run(t, "", "trash", path)
	require.NoError(t, err)

	out, err := run(t, "", "list", "--json")
	require.NoError(t, err)

	var items []recycler.Item
	require.NoError(t, json.Unmarshal([]byte(out), &items))
	require.Len(t, items, 1)
	assert.Equal(t, path, items[0].OriginalPath)
	assert.Equal(t, int64(10), items[0].Size)
}

func TestListEmpty(t *testing.T) {
	isolateTrash(t)

	out, err := run(t, "", "list")
	require.NoError(t, err)
	assert.Contains(t, out, "empty")

	out, err = run(t, "", "list", "--json")
	require.NoError(t, err)
	assert.Equal(t, "[]", strings.TrimSpace(out))
}

func TestRestoreTo(t *testing.T) {
	work := isolateTrash(t)
	path := writeFile(t, filepath.Join(work, "somewhere.txt"), "hello")
	_, err := run(t, "", "trash", path)
	require.NoError(t, err)

	dest := filepath.Join(work, "new", "place.txt")
	out, err := run(t, "", "restore", "--to", dest, "somewhere.txt")
	require.NoError(t, err)
	assert.Contains(t, out, "restored "+dest)

	content, err := os.ReadFile(dest)
	require.NoError(t, err)
	assert.Equal(t, "hello", string(content))
}

func TestRestoreToRefusesSeveralItems(t *testing.T) {
	work := isolateTrash(t)
	first := writeFile(t, filepath.Join(work, "a.txt"), "a")
	second := writeFile(t, filepath.Join(work, "b.txt"), "b")
	_, err := run(t, "", "trash", first, second)
	require.NoError(t, err)

	_, err = run(t, "", "restore", "--to", filepath.Join(work, "out"), "a.txt", "b.txt")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "exactly one")
}

func TestAmbiguousReferenceIsRejected(t *testing.T) {
	work := isolateTrash(t)
	first := writeFile(t, filepath.Join(work, "one", "same.txt"), "first")
	second := writeFile(t, filepath.Join(work, "two", "same.txt"), "second")
	_, err := run(t, "", "trash", first, second)
	require.NoError(t, err)

	_, err = run(t, "", "restore", "same.txt")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "matches 2 items")
	assert.NoFileExists(t, first, "nothing should have been restored")
	assert.NoFileExists(t, second, "nothing should have been restored")
}

func TestUnknownReferenceIsRejected(t *testing.T) {
	isolateTrash(t)
	_, err := run(t, "", "restore", "nothing-like-this")
	require.ErrorIs(t, err, recycler.ErrNotFound)
}

// TestNoDestructiveCommands locks down what this CLI must never grow back: a
// way to destroy a recycled file. An item leaves the bin by being restored, and
// by nothing else.
func TestNoDestructiveCommands(t *testing.T) {
	for _, name := range []string{"purge", "empty", "remove", "destroy", "shred", "wipe"} {
		for _, cmd := range rootCmd.Commands() {
			assert.NotEqual(t, name, cmd.Name(), "the CLI grew a %q command", name)
			assert.NotContains(t, cmd.Aliases, name, "%q is an alias of %q", name, cmd.Name())
		}
	}

	// The names a user reaches for to delete something must keep meaning
	// "recycle it", so typing one of them can never destroy the file.
	for _, cmd := range rootCmd.Commands() {
		for _, alias := range []string{"rm", "delete"} {
			if slices.Contains(cmd.Aliases, alias) {
				assert.Equal(t, "trash", cmd.Name(), "%q no longer recycles", alias)
			}
		}
	}

	work := isolateTrash(t)
	path := writeFile(t, filepath.Join(work, "safe.txt"), "safe")
	_, err := run(t, "", "trash", path)
	require.NoError(t, err)

	_, err = run(t, "y\n", "purge", "--yes", "safe.txt")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unknown command")

	items, err := recycler.List()
	require.NoError(t, err)
	assert.Len(t, items, 1, "the item must still be in the recycle bin")
}

func TestHumanSize(t *testing.T) {
	cases := map[int64]string{
		-1:            "?",
		0:             "0 B",
		999:           "999 B",
		1024:          "1.0 KiB",
		1536:          "1.5 KiB",
		1024 * 1024:   "1.0 MiB",
		3 << 30:       "3.0 GiB",
		5 << 40:       "5.0 TiB",
		1 << 50:       "1.0 PiB",
		1024*1024 - 1: "1024.0 KiB",
	}
	for size, want := range cases {
		assert.Equal(t, want, humanSize(size), "humanSize(%d)", size)
	}
}

func TestPathsEqual(t *testing.T) {
	assert.True(t, pathsEqual("/a/b", "/a/b"))
	assert.True(t, pathsEqual("/a/./b", "/a/b"))
	assert.False(t, pathsEqual("", "/a/b"))
	assert.False(t, pathsEqual("/a/b", ""))
	assert.Equal(t, caseInsensitiveFilesystem, pathsEqual("/a/B", "/a/b"))
}
