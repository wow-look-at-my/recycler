//go:build unix

package putback

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/wow-look-at-my/recycler/internal/dsstore"
)

// Fixtures written by an independent implementation of the format, kept beside
// the package that parses it. Reading them here is what proves a real Finder
// store's put back records come back.
const (
	finderTrashStore     = "../dsstore/testdata/finder_trash.DS_Store"
	editedElsewhereStore = "../dsstore/testdata/edited_elsewhere.DS_Store"
)

func TestPutBackRecordsSurviveTheTrash(t *testing.T) {
	dir := t.TempDir()

	require.NoError(t, Set(dir, "notes.txt", Of("/", "/Users/ada/Documents/notes.txt")))
	require.NoError(t, Set(dir, "notes_1.txt", Of("/", "/Users/ada/Desktop/notes.txt")))

	origins := Read(dir)
	assert.Equal(t, map[string]Origin{
		"notes.txt":   {Dir: "Users/ada/Documents", Name: "notes.txt"},
		"notes_1.txt": {Dir: "Users/ada/Desktop", Name: "notes.txt"},
	}, origins)
	assert.Equal(t, "/Users/ada/Desktop/notes.txt", origins["notes_1.txt"].Path("/"),
		"a file renamed on its way into the trash lost its original name")

	require.NoError(t, Clear(dir, "notes.txt"))
	assert.Equal(t, map[string]Origin{
		"notes_1.txt": {Dir: "Users/ada/Desktop", Name: "notes.txt"},
	}, Read(dir), "restoring one item disturbed another")
}

func TestPutBackRecordsAreWrittenWhereFinderLooks(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, Set(dir, "notes.txt", Origin{Dir: "Users/ada/Documents", Name: "notes.txt"}))

	data, err := os.ReadFile(filepath.Join(dir, dsstore.FileName))
	require.NoError(t, err, "no .DS_Store was written, so Finder has nothing to put the item back with")
	records, err := dsstore.Parse(data)
	require.NoError(t, err)
	assert.Equal(t, []dsstore.Record{
		dsstore.Ustr("notes.txt", "ptbL", "Users/ada/Documents"),
		dsstore.Ustr("notes.txt", "ptbN", "notes.txt"),
	}, records)
}

func TestPutBackKeepsTheRestOfTheDSStore(t *testing.T) {
	dir := t.TempDir()
	display := []dsstore.Record{
		{Name: ".", Code: "vSrn", Type: "long", Data: []byte{0, 0, 0, 1}},
		{Name: "notes.txt", Code: "Iloc", Type: "blob", Data: []byte{0, 0, 0, 2, 1, 2}},
	}
	data, err := dsstore.Build(display)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(dir, dsstore.FileName), data, 0o600))

	require.NoError(t, Set(dir, "notes.txt", Origin{Dir: "Users/ada", Name: "notes.txt"}))
	require.NoError(t, Clear(dir, "notes.txt"))

	data, err = os.ReadFile(filepath.Join(dir, dsstore.FileName))
	require.NoError(t, err)
	records, err := dsstore.Parse(data)
	require.NoError(t, err)
	assert.ElementsMatch(t, display, records, "the Finder settings in the trash's .DS_Store were lost")
	assert.Empty(t, Read(dir))
}

func TestPutBackReplacesAStaleRecord(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, Set(dir, "notes.txt", Origin{Dir: "Users/ada/Documents", Name: "notes.txt"}))
	require.NoError(t, Set(dir, "notes.txt", Origin{Dir: "Users/ada/Desktop", Name: "other.txt"}))

	assert.Equal(t, map[string]Origin{
		"notes.txt": {Dir: "Users/ada/Desktop", Name: "other.txt"},
	}, Read(dir), "a name reused in the trash kept the previous item's origin")
}

func TestNothingRecycledTakesTheDSStoreName(t *testing.T) {
	dir := t.TempDir()
	// A file really can be called .DS_Store, and recycling one must not put it
	// where the put back records live - writing them would destroy it.
	name := TrashName(dsstore.FileName, dir)
	assert.NotEqual(t, dsstore.FileName, name)

	require.NoError(t, os.WriteFile(filepath.Join(dir, name), []byte("the user's file"), 0o600))
	require.NoError(t, Set(dir, name, Of("/", "/Users/ada/.DS_Store")))

	kept, err := os.ReadFile(filepath.Join(dir, name))
	require.NoError(t, err)
	assert.Equal(t, "the user's file", string(kept), "the recycled file was overwritten by the put back records")
	assert.Equal(t, Origin{Dir: "Users/ada", Name: dsstore.FileName}, Read(dir)[name],
		"the item's real name was not recorded")
}

func TestPutBackOfPathsIsRelativeToTheVolume(t *testing.T) {
	for name, tc := range map[string]struct {
		root, original string
		want           Origin
	}{
		"home trash":   {"/", "/Users/ada/Documents/notes.txt", Origin{Dir: "Users/ada/Documents", Name: "notes.txt"}},
		"other volume": {"/Volumes/Backup", "/Volumes/Backup/photos/cat.jpg", Origin{Dir: "photos", Name: "cat.jpg"}},
		"volume root":  {"/Volumes/Backup", "/Volumes/Backup/cat.jpg", Origin{Dir: ".", Name: "cat.jpg"}},
		"outside the volume": {
			"/Volumes/Backup", "/Users/ada/notes.txt",
			Origin{Dir: "/Users/ada", Name: "notes.txt"},
		},
	} {
		got := Of(tc.root, tc.original)
		assert.Equal(t, tc.want, got, "%s: wrong put back pair", name)
		assert.Equal(t, filepath.Clean(tc.original), got.Path(tc.root), "%s: the original path did not come back", name)
	}
}

func TestPutBackWithoutALocationIsNotAnOrigin(t *testing.T) {
	origins := putBacksFrom([]dsstore.Record{
		dsstore.Ustr("orphan.txt", "ptbN", "orphan.txt"), // a name, but nowhere to put it
		dsstore.Ustr("located.txt", "ptbL", "Users/ada"), // a location, but no name
		{Name: "wrong-type.txt", Code: "ptbL", Type: "long", Data: []byte{0, 0, 0, 1}},
	})
	assert.Equal(t, map[string]Origin{
		"located.txt": {Dir: "Users/ada", Name: "located.txt"},
	}, origins, "a record pair that cannot name a destination was treated as one")
	assert.Empty(t, Origin{Name: "notes.txt"}.Path("/"), "a location-less record produced a path anyway")
}

func TestUnusableStoresAreNotFatal(t *testing.T) {
	dir := t.TempDir()
	assert.Empty(t, Read(dir), "a trash directory with no .DS_Store failed to list")

	require.NoError(t, os.WriteFile(filepath.Join(dir, dsstore.FileName), []byte("not a .DS_Store at all"), 0o600))
	assert.Empty(t, Read(dir), "a damaged .DS_Store failed to list")

	// Recording an origin must work even when what was there is unreadable: the
	// put back records matter, the icon positions do not.
	require.NoError(t, Set(dir, "notes.txt", Origin{Dir: "Users/ada", Name: "notes.txt"}))
	assert.Equal(t, map[string]Origin{
		"notes.txt": {Dir: "Users/ada", Name: "notes.txt"},
	}, Read(dir))
}

func TestReadsPutBacksFromAForeignStore(t *testing.T) {
	dir := t.TempDir()
	data, err := os.ReadFile(finderTrashStore)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(dir, dsstore.FileName), data, 0o600))

	origins := Read(dir)
	assert.Equal(t, Origin{Dir: "Users/ada/Desktop", Name: "report.pdf"}, origins["report 2.pdf"])
	assert.Equal(t, "/Users/ada/Desktop/report.pdf", origins["report 2.pdf"].Path("/"))
	assert.Equal(t, "/Volumes/Backup/Photos/café 📁", origins["café 📁"].Path("/"))
}

func TestReadsPutBacksAnotherImplementationWrote(t *testing.T) {
	// Finder rewrites the trash's .DS_Store whenever it feels like it, so a
	// file this package wrote comes back reorganised by something else, with
	// records this package never wrote in it.
	dir := t.TempDir()
	data, err := os.ReadFile(editedElsewhereStore)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(dir, dsstore.FileName), data, 0o600))

	assert.Equal(t, map[string]Origin{
		"notes.txt": {Dir: "Users/ada/Documents", Name: "notes.txt"},
		"café 📁":    {Dir: "Volumes/Backup/Photos", Name: "café 📁"},
		"added.txt": {Dir: "Users/ada/Added", Name: "added.txt"},
	}, Read(dir), "records inserted by another implementation were not read back")

	// And this package must be able to keep editing it afterwards.
	require.NoError(t, Clear(dir, "added.txt"))
	assert.NotContains(t, Read(dir), "added.txt")
	assert.Contains(t, Read(dir), "notes.txt")
}

func TestUpdatingAStoreThatCannotBeOpened(t *testing.T) {
	assert.Error(t, Set(filepath.Join(t.TempDir(), "missing"), "notes.txt", Origin{Dir: "Users/ada", Name: "notes.txt"}),
		"writing into a directory that does not exist reported success")
}
