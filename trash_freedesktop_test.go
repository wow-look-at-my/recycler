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
	require.GreaterOrEqual(t, len(lines), 3, "unexpected metadata file: %q", raw)
	assert.Equal(t, "[Trash Info]", lines[0])
	assert.Equal(t, "Path="+escapePath(path), lines[1])
	assert.Contains(t, lines[1], "%20", "the path was not percent-encoded")

	date, ok := strings.CutPrefix(lines[2], "DeletionDate=")
	require.True(t, ok, "third line = %q, want a DeletionDate", lines[2])
	_, err = time.ParseInLocation(deletionDateLayout, date, time.Local)
	assert.NoError(t, err, "DeletionDate %q is not in the format the specification requires", date)

	// The specification names the keys an entry must carry, not the only ones
	// it may: a reader takes the keys it knows and ignores the rest, which is
	// what lets this record a Size next to them. Every extra line still has to
	// be a key, so nothing here can produce a file another tool trips over.
	for _, line := range lines[3:] {
		key, _, ok := strings.Cut(line, "=")
		assert.True(t, ok, "line %q is not a key=value pair", line)
		assert.NotEmpty(t, key, "line %q has an empty key", line)
	}
}

// TestEvictDestroysAnEntryAndItsMetadata covers the only operation here that
// destroys anything. Both halves of the entry have to go: a leftover
// .trashinfo is an entry whose file is missing.
func TestEvictDestroysAnEntryAndItsMetadata(t *testing.T) {
	work := isolateTrash(t)
	require.NoError(t, Recycle(writeFile(t, filepath.Join(work, "doomed.txt"), "bytes")))

	b, err := platformBackend()
	require.NoError(t, err)
	items, err := b.list()
	require.NoError(t, err)
	require.Len(t, items, 1)

	require.NoError(t, b.evict(items[0].ID))

	after, err := b.list()
	require.NoError(t, err)
	assert.Empty(t, after)
	assert.NoFileExists(t, items[0].ID)

	entries, err := os.ReadDir(filepath.Join(homeTrash(t), trashInfoDir))
	require.NoError(t, err)
	assert.Empty(t, entries, "the .trashinfo outlived the file it described")
}

// TestEvictRejectsAnIDOutsideTheBin holds eviction to the same validation
// restore gets. This is the one path that deletes, so an ID that names
// something outside the bin must reach nothing at all.
func TestEvictRejectsAnIDOutsideTheBin(t *testing.T) {
	work := isolateTrash(t)
	outsider := writeFile(t, filepath.Join(work, "innocent.txt"), "do not touch")
	require.NoError(t, Recycle(writeFile(t, filepath.Join(work, "decoy.txt"), "decoy")))

	b, err := platformBackend()
	require.NoError(t, err)
	for _, id := range []string{"", "not-an-id", outsider, filepath.Join(work, "nope", "files", "x")} {
		assert.ErrorIs(t, b.evict(id), ErrNotFound, "evict(%q)", id)
	}
	assert.FileExists(t, outsider, "a hostile ID reached a file outside the bin")
}

// TestSizeIsRecordedWhenRecycled checks the number the daemon evicts by. It has
// to be written when the item is recycled: after that the original is gone, and
// walking the copy in the bin is what recording it exists to avoid.
func TestSizeIsRecordedWhenRecycled(t *testing.T) {
	work := isolateTrash(t)
	path := writeFile(t, filepath.Join(work, "sized.txt"), strings.Repeat("x", 4096))
	require.NoError(t, Recycle(path))

	items, err := List()
	require.NoError(t, err)
	require.Len(t, items, 1)
	assert.Equal(t, int64(4096), items[0].Size)

	infoDir := filepath.Join(homeTrash(t), trashInfoDir)
	entries, err := os.ReadDir(infoDir)
	require.NoError(t, err)
	require.Len(t, entries, 1)
	raw, err := os.ReadFile(filepath.Join(infoDir, entries[0].Name()))
	require.NoError(t, err)
	assert.Contains(t, string(raw), "Size=4096")
}

// TestAForeignEntryIsSizedOnceAndRecorded covers an entry another trash
// implementation wrote, which carries no size. It is measured on first sight
// and the number written back, so the walk happens once rather than on every
// poll the daemon makes.
func TestAForeignEntryIsSizedOnceAndRecorded(t *testing.T) {
	isolateTrash(t)
	trash := homeTrash(t)
	require.NoError(t, ensureTrashDir(trash))

	name := "foreign.txt"
	require.NoError(t, os.WriteFile(filepath.Join(trash, trashFilesDir, name),
		[]byte(strings.Repeat("y", 512)), 0o600))
	infoPath := filepath.Join(trash, trashInfoDir, name+trashInfoExt)
	require.NoError(t, os.WriteFile(infoPath, []byte(
		"[Trash Info]\nPath=/home/ada/foreign.txt\nDeletionDate=2026-01-02T03:04:05\n"), 0o600))

	items, err := List()
	require.NoError(t, err)
	require.Len(t, items, 1)
	assert.Equal(t, int64(512), items[0].Size)

	raw, err := os.ReadFile(infoPath)
	require.NoError(t, err)
	assert.Contains(t, string(raw), "Size=512", "the size was not written back: %q", raw)
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
