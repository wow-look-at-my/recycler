//go:build linux || freebsd || netbsd || openbsd || dragonfly || solaris

package recycler

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// homeTrash returns the trash directory the isolated test environment uses.
func homeTrash(t *testing.T) string {
	t.Helper()
	b, err := platformBackend()
	require.NoError(t, err)
	return b.(*fdoTrash).home
}

// TestTrashInfoIsSpecCompliant checks the metadata this package writes against
// the FreeDesktop trash specification, so other trash tools can read it.
func TestTrashInfoIsSpecCompliant(t *testing.T) {
	work := isolateTrash(t)
	path := writeFile(t, filepath.Join(work, "a file with spaces & symbols.txt"), "x")
	require.NoError(t, Recycle(path))

	infoDir := filepath.Join(homeTrash(t), trashInfoDir)
	entries, err := os.ReadDir(infoDir)
	require.NoError(t, err)
	require.Len(t, entries, 1)
	assert.True(t, strings.HasSuffix(entries[0].Name(), trashInfoExt),
		"metadata file %q does not end in %s", entries[0].Name(), trashInfoExt)

	raw, err := os.ReadFile(filepath.Join(infoDir, entries[0].Name()))
	require.NoError(t, err)

	lines := strings.Split(strings.TrimRight(string(raw), "\n"), "\n")
	require.Len(t, lines, 3, "unexpected metadata file: %q", raw)
	assert.Equal(t, "[Trash Info]", lines[0])
	assert.Equal(t, "Path="+escapePath(path), lines[1])
	assert.Contains(t, lines[1], "%20", "the path was not percent-encoded")

	date, ok := strings.CutPrefix(lines[2], "DeletionDate=")
	require.True(t, ok, "third line = %q, want a DeletionDate", lines[2])
	_, err = time.ParseInLocation(deletionDateLayout, date, time.Local)
	assert.NoError(t, err, "DeletionDate %q is not in the format the specification requires", date)
}

// TestReadsForeignTrashEntries checks that entries written by another trash
// implementation - a file manager, say - are listed and restored correctly.
func TestReadsForeignTrashEntries(t *testing.T) {
	work := isolateTrash(t)
	trash := homeTrash(t)
	require.NoError(t, ensureTrashDir(trash))

	original := filepath.Join(work, "from another tool.txt")
	writeFile(t, filepath.Join(trash, trashFilesDir, "from another tool.txt"), "foreign")
	info := "[Trash Info]\nPath=" + escapePath(original) + "\nDeletionDate=2026-07-26T11:24:09\n"
	infoPath := writeFile(t, filepath.Join(trash, trashInfoDir, "from another tool.txt"+trashInfoExt), info)

	items := mustList(t)
	require.Len(t, items, 1)
	assert.Equal(t, original, items[0].OriginalPath)
	assert.True(t, items[0].DeletedAt.Equal(time.Date(2026, 7, 26, 11, 24, 9, 0, time.Local)),
		"DeletedAt = %s", items[0].DeletedAt)

	restored, err := Restore(items[0].ID)
	require.NoError(t, err)
	assert.Equal(t, original, restored)

	content, err := os.ReadFile(original)
	require.NoError(t, err)
	assert.Equal(t, "foreign", string(content))
	assert.NoFileExists(t, infoPath, "the .trashinfo file was left behind after restoring")
}

func TestCreateInfoFileClaimsFreeNames(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, ensureTrashDir(dir))

	first, f, err := createInfoFile(dir, "report.txt")
	require.NoError(t, err)
	require.NoError(t, f.Close())
	assert.Equal(t, "report.txt", first)

	// The name is taken now, even though no data file exists yet.
	second, f, err := createInfoFile(dir, "report.txt")
	require.NoError(t, err)
	require.NoError(t, f.Close())
	assert.Equal(t, "report_1.txt", second)

	// A data file with no metadata also makes a name unavailable.
	writeFile(t, filepath.Join(dir, trashFilesDir, "report_2.txt"), "orphan")
	third, f, err := createInfoFile(dir, "report.txt")
	require.NoError(t, err)
	require.NoError(t, f.Close())
	assert.Equal(t, "report_3.txt", third)

	odd, f, err := createInfoFile(dir, "..")
	require.NoError(t, err)
	require.NoError(t, f.Close())
	assert.Equal(t, "recycled", odd, "a name that is not a file name should be replaced")
}

func TestReadInfoFile(t *testing.T) {
	dir := t.TempDir()

	full := writeFile(t, filepath.Join(dir, "full"+trashInfoExt),
		"[Trash Info]\nPath=/home/user/a%20file.txt\nDeletionDate=2026-07-26T11:24:09\n")
	info, err := readInfoFile(full)
	require.NoError(t, err)
	assert.Equal(t, "/home/user/a file.txt", info.origPath)
	assert.True(t, info.deletedAt.Equal(time.Date(2026, 7, 26, 11, 24, 9, 0, time.Local)))

	// A date with a zone, as some implementations write, is understood too.
	zoned := writeFile(t, filepath.Join(dir, "zoned"+trashInfoExt),
		"[Trash Info]\nPath=/tmp/x\nDeletionDate=2026-07-26T11:24:09Z\n")
	info, err = readInfoFile(zoned)
	require.NoError(t, err)
	assert.True(t, info.deletedAt.Equal(time.Date(2026, 7, 26, 11, 24, 9, 0, time.UTC)))

	// Junk lines are ignored, and an unparseable date leaves the zero time
	// behind for the caller to replace.
	odd := writeFile(t, filepath.Join(dir, "odd"+trashInfoExt),
		"[Trash Info]\nnonsense\nPath=%zz\nDeletionDate=not a date\n")
	info, err = readInfoFile(odd)
	require.NoError(t, err)
	assert.Equal(t, "%zz", info.origPath, "an undecodable path is kept as it was written")
	assert.True(t, info.deletedAt.IsZero())

	_, err = readInfoFile(filepath.Join(dir, "missing"+trashInfoExt))
	assert.Error(t, err)
}

func TestEscapePath(t *testing.T) {
	assert.Equal(t, "/home/user/a%20file.txt", escapePath("/home/user/a file.txt"))
	assert.Equal(t, "/home/user/plain.txt", escapePath("/home/user/plain.txt"))
	assert.Equal(t, "relative/path%20here", escapePath("relative/path here"))
}

func TestUnescapeMountPoint(t *testing.T) {
	cases := map[string]string{
		`/mnt/plain`:            `/mnt/plain`,
		`/mnt/with\040space`:    `/mnt/with space`,
		`/mnt/tab\011here`:      "/mnt/tab\there",
		`/mnt/back\134slash`:    `/mnt/back\slash`,
		`/mnt/not\9an\escape`:   `/mnt/not\9an\escape`,
		`/media/user/My\040USB`: `/media/user/My USB`,
	}
	for in, want := range cases {
		assert.Equal(t, want, unescapeMountPoint(in), "unescapeMountPoint(%q)", in)
	}
}

func TestTopDirOfTrash(t *testing.T) {
	cases := map[string]string{
		"/media/usb/.Trash-1000":  "/media/usb",
		"/media/usb/.Trash/1000":  "/media/usb",
		"/home/user/.local/share": "",
	}
	for in, want := range cases {
		assert.Equal(t, want, topDirOfTrash(in), "topDirOfTrash(%q)", in)
	}
}
