//go:build unix

package recycler

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPutBackRecordsSurviveTheTrash(t *testing.T) {
	dir := t.TempDir()

	require.NoError(t, setPutBack(dir, "notes.txt", putBackOf("/", "/Users/ada/Documents/notes.txt")))
	require.NoError(t, setPutBack(dir, "notes_1.txt", putBackOf("/", "/Users/ada/Desktop/notes.txt")))

	origins := readPutBacks(dir)
	assert.Equal(t, map[string]putBack{
		"notes.txt":   {Dir: "Users/ada/Documents", Name: "notes.txt"},
		"notes_1.txt": {Dir: "Users/ada/Desktop", Name: "notes.txt"},
	}, origins)
	assert.Equal(t, "/Users/ada/Desktop/notes.txt", origins["notes_1.txt"].path("/"),
		"a file renamed on its way into the trash lost its original name")

	require.NoError(t, clearPutBack(dir, "notes.txt"))
	assert.Equal(t, map[string]putBack{
		"notes_1.txt": {Dir: "Users/ada/Desktop", Name: "notes.txt"},
	}, readPutBacks(dir), "restoring one item disturbed another")
}

func TestPutBackRecordsAreWrittenWhereFinderLooks(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, setPutBack(dir, "notes.txt", putBack{Dir: "Users/ada/Documents", Name: "notes.txt"}))

	data, err := os.ReadFile(filepath.Join(dir, dsStoreName))
	require.NoError(t, err, "no .DS_Store was written, so Finder has nothing to put the item back with")
	records, err := parseDSStore(data)
	require.NoError(t, err)
	assert.Equal(t, []dsRecord{
		dsUstr("notes.txt", "ptbL", "Users/ada/Documents"),
		dsUstr("notes.txt", "ptbN", "notes.txt"),
	}, records)
}

func TestPutBackKeepsTheRestOfTheDSStore(t *testing.T) {
	dir := t.TempDir()
	display := []dsRecord{
		{Name: ".", Code: "vSrn", Type: "long", Data: []byte{0, 0, 0, 1}},
		{Name: "notes.txt", Code: "Iloc", Type: "blob", Data: []byte{0, 0, 0, 2, 1, 2}},
	}
	data, err := buildDSStore(display)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(dir, dsStoreName), data, 0o600))

	require.NoError(t, setPutBack(dir, "notes.txt", putBack{Dir: "Users/ada", Name: "notes.txt"}))
	require.NoError(t, clearPutBack(dir, "notes.txt"))

	data, err = os.ReadFile(filepath.Join(dir, dsStoreName))
	require.NoError(t, err)
	records, err := parseDSStore(data)
	require.NoError(t, err)
	assert.ElementsMatch(t, display, records, "the Finder settings in the trash's .DS_Store were lost")
	assert.Empty(t, readPutBacks(dir))
}

func TestPutBackReplacesAStaleRecord(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, setPutBack(dir, "notes.txt", putBack{Dir: "Users/ada/Documents", Name: "notes.txt"}))
	require.NoError(t, setPutBack(dir, "notes.txt", putBack{Dir: "Users/ada/Desktop", Name: "other.txt"}))

	assert.Equal(t, map[string]putBack{
		"notes.txt": {Dir: "Users/ada/Desktop", Name: "other.txt"},
	}, readPutBacks(dir), "a name reused in the trash kept the previous item's origin")
}

func TestPutBackOfPathsIsRelativeToTheVolume(t *testing.T) {
	for name, tc := range map[string]struct {
		root, original string
		want           putBack
	}{
		"home trash":   {"/", "/Users/ada/Documents/notes.txt", putBack{Dir: "Users/ada/Documents", Name: "notes.txt"}},
		"other volume": {"/Volumes/Backup", "/Volumes/Backup/photos/cat.jpg", putBack{Dir: "photos", Name: "cat.jpg"}},
		"volume root":  {"/Volumes/Backup", "/Volumes/Backup/cat.jpg", putBack{Dir: ".", Name: "cat.jpg"}},
		"outside the volume": {
			"/Volumes/Backup", "/Users/ada/notes.txt",
			putBack{Dir: "/Users/ada", Name: "notes.txt"},
		},
	} {
		got := putBackOf(tc.root, tc.original)
		assert.Equal(t, tc.want, got, "%s: wrong put back pair", name)
		assert.Equal(t, filepath.Clean(tc.original), got.path(tc.root), "%s: the original path did not come back", name)
	}
}

func TestPutBackWithoutALocationIsNotAnOrigin(t *testing.T) {
	origins := putBacksFrom([]dsRecord{
		dsUstr("orphan.txt", "ptbN", "orphan.txt"), // a name, but nowhere to put it
		dsUstr("located.txt", "ptbL", "Users/ada"), // a location, but no name
		{Name: "wrong-type.txt", Code: "ptbL", Type: "long", Data: []byte{0, 0, 0, 1}},
	})
	assert.Equal(t, map[string]putBack{
		"located.txt": {Dir: "Users/ada", Name: "located.txt"},
	}, origins, "a record pair that cannot name a destination was treated as one")
	assert.Empty(t, putBack{Name: "notes.txt"}.path("/"), "a location-less record produced a path anyway")
}

func TestUnusableStoresAreNotFatal(t *testing.T) {
	dir := t.TempDir()
	assert.Empty(t, readPutBacks(dir), "a trash directory with no .DS_Store failed to list")

	require.NoError(t, os.WriteFile(filepath.Join(dir, dsStoreName), []byte("not a .DS_Store at all"), 0o600))
	assert.Empty(t, readPutBacks(dir), "a damaged .DS_Store failed to list")

	// Recording an origin must work even when what was there is unreadable: the
	// put back records matter, the icon positions do not.
	require.NoError(t, setPutBack(dir, "notes.txt", putBack{Dir: "Users/ada", Name: "notes.txt"}))
	assert.Equal(t, map[string]putBack{
		"notes.txt": {Dir: "Users/ada", Name: "notes.txt"},
	}, readPutBacks(dir))
}

func TestReadsPutBacksFromAForeignStore(t *testing.T) {
	dir := t.TempDir()
	data, err := os.ReadFile(finderTrashStore)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(dir, dsStoreName), data, 0o600))

	origins := readPutBacks(dir)
	assert.Equal(t, putBack{Dir: "Users/ada/Desktop", Name: "report.pdf"}, origins["report 2.pdf"])
	assert.Equal(t, "/Users/ada/Desktop/report.pdf", origins["report 2.pdf"].path("/"))
	assert.Equal(t, "/Volumes/Backup/Photos/café 📁", origins["café 📁"].path("/"))
}

func TestReadsPutBacksAnotherImplementationWrote(t *testing.T) {
	// Finder rewrites the trash's .DS_Store whenever it feels like it, so a
	// file this package wrote comes back reorganised by something else, with
	// records this package never wrote in it.
	dir := t.TempDir()
	data, err := os.ReadFile(editedElsewhereStore)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(dir, dsStoreName), data, 0o600))

	assert.Equal(t, map[string]putBack{
		"notes.txt": {Dir: "Users/ada/Documents", Name: "notes.txt"},
		"café 📁":    {Dir: "Volumes/Backup/Photos", Name: "café 📁"},
		"added.txt": {Dir: "Users/ada/Added", Name: "added.txt"},
	}, readPutBacks(dir), "records inserted by another implementation were not read back")

	// And this package must be able to keep editing it afterwards.
	require.NoError(t, clearPutBack(dir, "added.txt"))
	assert.NotContains(t, readPutBacks(dir), "added.txt")
	assert.Contains(t, readPutBacks(dir), "notes.txt")
}

func TestUpdatingAStoreThatCannotBeOpened(t *testing.T) {
	assert.Error(t, setPutBack(filepath.Join(t.TempDir(), "missing"), "notes.txt", putBack{Dir: "Users/ada", Name: "notes.txt"}),
		"writing into a directory that does not exist reported success")
}
