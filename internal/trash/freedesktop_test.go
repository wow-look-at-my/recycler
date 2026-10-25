//go:build linux || freebsd || netbsd || openbsd || dragonfly || solaris

package trash

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/wow-look-at-my/recycler/internal/bin"
	"github.com/wow-look-at-my/recycler/internal/diskfree"
)

// testFdo is the FreeDesktop backend over the isolated test environment.
func testFdo() *fdoTrash {
	return &fdoTrash{home: filepath.Join(os.Getenv("XDG_DATA_HOME"), "Trash"), uid: os.Getuid()}
}

// homeTrash returns the trash directory the isolated test environment uses.
func homeTrash(t *testing.T) string {
	t.Helper()
	return testFdo().home
}

func fdoRecycle(paths ...string) error {
	_, err := testFdo().Recycle(paths)
	return err
}

func fdoList() ([]bin.Item, error) { return testFdo().List() }

func fdoRestore(id string) (string, error) { return testFdo().Restore(id, "") }

func fdoDispose(paths ...string) ([]bin.Disposal, error) { return testFdo().Recycle(paths) }

// TestTrashInfoIsSpecCompliant checks the metadata this package writes against the FreeDesktop trash.
func TestTrashInfoIsSpecCompliant(t *testing.T) {
	work := isolateTrash(t)
	path := writeFile(t, filepath.Join(work, "a file with spaces & symbols.txt"), "x")
	require.NoError(t, fdoRecycle(path))

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

	// The specification names the keys an entry must carry, not the only ones it may.
	for _, line := range lines[3:] {
		key, _, ok := strings.Cut(line, "=")
		assert.True(t, ok, "line %q is not a key=value pair", line)
		assert.NotEmpty(t, key, "line %q has an empty key", line)
	}
}

// TestEvictDestroysAnEntryAndItsMetadata covers the only operation here that destroys anything.
func TestEvictDestroysAnEntryAndItsMetadata(t *testing.T) {
	work := isolateTrash(t)
	require.NoError(t, fdoRecycle(writeFile(t, filepath.Join(work, "doomed.txt"), "bytes")))

	b := testFdo()
	items, err := b.List()
	require.NoError(t, err)
	require.Len(t, items, 1)

	require.NoError(t, b.Evict(items[0].ID))

	after, err := b.List()
	require.NoError(t, err)
	assert.Empty(t, after)
	assert.NoFileExists(t, items[0].ID)

	entries, err := os.ReadDir(filepath.Join(homeTrash(t), trashInfoDir))
	require.NoError(t, err)
	assert.Empty(t, entries, "the .trashinfo outlived the file it described")
}

// TestEvictRejectsAnIDOutsideTheBin holds eviction to the same validation restore gets.
func TestEvictRejectsAnIDOutsideTheBin(t *testing.T) {
	work := isolateTrash(t)
	outsider := writeFile(t, filepath.Join(work, "innocent.txt"), "do not touch")
	require.NoError(t, fdoRecycle(writeFile(t, filepath.Join(work, "decoy.txt"), "decoy")))

	b := testFdo()
	for _, id := range []string{"", "not-an-id", outsider, filepath.Join(work, "nope", "files", "x")} {
		assert.ErrorIs(t, b.Evict(id), bin.ErrNotFound, "evict(%q)", id)
	}
	assert.FileExists(t, outsider, "a hostile ID reached a file outside the bin")
}

// TestSizeIsRecordedWhenRecycled checks the number the daemon evicts on.
func TestSizeIsRecordedWhenRecycled(t *testing.T) {
	work := isolateTrash(t)
	path := writeFile(t, filepath.Join(work, "sized.txt"), strings.Repeat("x", 4096))
	require.NoError(t, fdoRecycle(path))

	items, err := fdoList()
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

// A recycle reports what it did with each path. With room to defer into, the
// report says deferred: the entry is in the bin and nothing was destroyed.
// Only pressure sets Permanent. No test can fill a real filesystem to ask for
// it, so the arm covered here is the thing a caller meets.
func TestARecycleWithRoomReportsADeferral(t *testing.T) {
	work := isolateTrash(t)
	path := writeFile(t, filepath.Join(work, "kept.txt"), "kept")

	disposals, err := fdoDispose(path)
	require.NoError(t, err)
	require.Len(t, disposals, 1)
	assert.Equal(t, path, disposals[0].Path)
	assert.False(t, disposals[0].Permanent, "reason: %s", disposals[0].Reason)

	items, err := fdoList()
	require.NoError(t, err)
	assert.Len(t, items, 1, "the file is in the bin rather than destroyed")
}

// A bin on the same filesystem takes an entry by rename, which needs no free
// space. A file whose stated length is past everything free is recycled, not
// deleted: a compressed filesystem states lengths like that for ordinary
// build output.
func TestAFileStatingMoreThanTheFreeSpaceIsRecycled(t *testing.T) {
	work := isolateTrash(t)
	avail, _, err := diskfree.Free(work)
	require.NoError(t, err)
	path := filepath.Join(work, "sparse")
	f, err := os.Create(path)
	require.NoError(t, err)
	require.NoError(t, f.Truncate(int64(avail)+1<<30))
	require.NoError(t, f.Close())

	disposals, err := fdoDispose(path)
	require.NoError(t, err)
	require.Len(t, disposals, 1)
	assert.False(t, disposals[0].Permanent, "deleted outright: %s", disposals[0].Reason)

	items, err := fdoList()
	require.NoError(t, err)
	require.Len(t, items, 1, "the file is not in the bin")
	assert.Equal(t, path, items[0].OriginalPath)
}

// TestAForeignEntryIsSizedOnceAndRecorded covers an entry another trash implementation wrote, which
// carries no size.
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

	items, err := fdoList()
	require.NoError(t, err)
	require.Len(t, items, 1)
	assert.Equal(t, int64(512), items[0].Size)

	raw, err := os.ReadFile(infoPath)
	require.NoError(t, err)
	assert.Contains(t, string(raw), "Size=512", "the size was not written back: %q", raw)
}

// A measurement that cannot be written back is a walk the next poll repeats. An entry whose
// record is read-only is left unsized instead.
func TestAnUnwritableForeignEntryIsNotWalked(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root writes a read-only file")
	}
	isolateTrash(t)
	trash := homeTrash(t)
	require.NoError(t, ensureTrashDir(trash))

	name := "readonly.txt"
	require.NoError(t, os.WriteFile(filepath.Join(trash, trashFilesDir, name),
		[]byte(strings.Repeat("z", 512)), 0o600))
	infoPath := filepath.Join(trash, trashInfoDir, name+trashInfoExt)
	require.NoError(t, os.WriteFile(infoPath, []byte(
		"[Trash Info]\nPath=/home/ada/readonly.txt\nDeletionDate=2026-01-02T03:04:05\n"), 0o400))

	items, err := fdoList()
	require.NoError(t, err)
	require.Len(t, items, 1)
	assert.Equal(t, int64(bin.SizeUnknown), items[0].Size,
		"an unrecordable size must stay unknown, which is what keeps the item un-evictable")

	raw, err := os.ReadFile(infoPath)
	require.NoError(t, err)
	assert.NotContains(t, string(raw), "Size=", "nothing may be written to a read-only record")
}

// TestReadsForeignTrashEntries checks that another implementation's entries read back.
func TestReadsForeignTrashEntries(t *testing.T) {
	work := isolateTrash(t)
	trash := homeTrash(t)
	require.NoError(t, ensureTrashDir(trash))

	original := filepath.Join(work, "from another tool.txt")
	writeFile(t, filepath.Join(trash, trashFilesDir, "from another tool.txt"), "foreign")
	info := "[Trash Info]\nPath=" + escapePath(original) + "\nDeletionDate=2026-07-26T11:24:09\n"
	infoPath := writeFile(t, filepath.Join(trash, trashInfoDir, "from another tool.txt"+trashInfoExt), info)

	items, err := fdoList()
	require.NoError(t, err)
	require.Len(t, items, 1)
	assert.Equal(t, original, items[0].OriginalPath)
	assert.True(t, items[0].DeletedAt.Equal(time.Date(2026, 7, 26, 11, 24, 9, 0, time.Local)),
		"DeletedAt = %s", items[0].DeletedAt)

	restored, err := fdoRestore(items[0].ID)
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

	// Junk lines are ignored, and an unparseable date leaves an empty time
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

func TestParseMountOutputReadsMacMountLines(t *testing.T) {
	out := "/dev/disk3s1s1 on / (apfs, sealed, local, read-only, journaled)\n" +
		"devfs on /dev (devfs, local, nobrowse)\n" +
		"map auto_home on /System/Volumes/Data/home (autofs, automounted, nobrowse)\n" +
		"/dev/disk5s1 on /Users/me/repos (apfs, local, journaled)\n" +
		"/dev/disk6s1 on /Volumes/My Disk (msdos, local, nodev)\n" +
		"not a mount line\n"
	assert.Equal(t, []string{"/", "/dev", "/Users/me/repos", "/Volumes/My Disk"}, parseMountOutput(out))
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

// Data in files/ with no .trashinfo holds disk space all the same. The daemon
// sees only what List returns, so such an entry must be listed with its size.
func TestAnUnrecordedEntryIsListedSizedAndRecorded(t *testing.T) {
	isolateTrash(t)
	trash := homeTrash(t)
	require.NoError(t, ensureTrashDir(trash))

	orphan := filepath.Join(trash, trashFilesDir, "base-b")
	writeFile(t, filepath.Join(orphan, "logs", "run.log"), strings.Repeat("a", 300))
	writeFile(t, filepath.Join(orphan, "edits-r1", "out"), strings.Repeat("b", 212))

	items, err := fdoList()
	require.NoError(t, err)
	require.Len(t, items, 1, "the unrecorded entry is not listed, so nothing can ever evict it")
	assert.Equal(t, orphan, items[0].ID)
	assert.Equal(t, "base-b", items[0].Name)
	assert.Empty(t, items[0].OriginalPath)
	assert.Equal(t, int64(512), items[0].Size)
	assert.True(t, items[0].IsDir)

	infoPath := filepath.Join(trash, trashInfoDir, "base-b"+trashInfoExt)
	raw, err := os.ReadFile(infoPath)
	require.NoError(t, err, "the entry got no record, so every poll walks it again")
	assert.Contains(t, string(raw), "Size=512")

	again, err := fdoList()
	require.NoError(t, err)
	require.Len(t, again, 1, "the record and the adoption must not list the entry twice")
	assert.Equal(t, "base-b", again[0].Name)
	assert.Empty(t, again[0].OriginalPath)
	assert.Equal(t, int64(512), again[0].Size)

	_, err = fdoRestore(again[0].ID)
	require.ErrorIs(t, err, bin.ErrUnknownOrigin)

	require.NoError(t, testFdo().Evict(again[0].ID))
	assert.NoDirExists(t, orphan)
	assert.NoFileExists(t, infoPath)
}

// An empty Path in a per-filesystem bin must not resolve to the top directory.
func TestAnUnrecordedEntryInAVolumeBinHasNoOrigin(t *testing.T) {
	work := isolateTrash(t)
	volume := filepath.Join(work, "volume")
	dir := filepath.Join(volume, ".Trash-"+strconv.Itoa(os.Getuid()))
	require.NoError(t, ensureTrashDir(dir))
	Mounts = func() []string { return []string{volume} }
	writeFile(t, filepath.Join(dir, trashFilesDir, "stray"), "xyz")

	for range 2 {
		items, err := fdoList()
		require.NoError(t, err)
		require.Len(t, items, 1)
		assert.Empty(t, items[0].OriginalPath, "an unknown origin read as the volume root")
		assert.Equal(t, "stray", items[0].Name)
		assert.Equal(t, int64(3), items[0].Size)
	}
}
