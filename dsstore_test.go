package recycler

import (
	"encoding/binary"
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Fixtures written by an independent implementation of the format - the
// ds_store Python package behind dmgbuild - so that reading real files is
// tested against something other than this package's own writer.
const (
	// finderTrashStore holds what a trash directory's file holds: put back
	// records, plus the display settings Finder keeps next to them.
	finderTrashStore = "testdata/finder_trash.DS_Store"

	// finderTrashManyStore holds enough records to need a tree of several
	// nodes, so that reading internal nodes is covered too.
	finderTrashManyStore = "testdata/finder_trash_many.DS_Store"

	// editedElsewhereStore was written by this package and then had records
	// inserted into it by that other implementation, which is what happens
	// whenever Finder touches a trash directory this package has written to.
	editedElsewhereStore = "testdata/edited_elsewhere.DS_Store"
)

func TestReadsAFileWrittenByAnotherImplementation(t *testing.T) {
	data, err := os.ReadFile(finderTrashStore)
	require.NoError(t, err)

	records, err := parseDSStore(data)
	require.NoError(t, err)

	got := map[string]map[string]string{}
	for _, r := range records {
		if value, ok := r.ustr(); ok {
			if got[r.Name] == nil {
				got[r.Name] = map[string]string{}
			}
			got[r.Name][r.Code] = value
		}
	}
	assert.Equal(t, map[string]map[string]string{
		"notes.txt":    {"ptbL": "Users/ada/Documents", "ptbN": "notes.txt"},
		"report 2.pdf": {"ptbL": "Users/ada/Desktop", "ptbN": "report.pdf"},
		"café 📁":       {"ptbL": "Volumes/Backup/Photos", "ptbN": "café 📁"},
	}, got, "the put back records other implementations write were not read back")

	// Records this package has no use for must survive being read.
	assert.Contains(t, records, dsRecord{Name: ".", Code: "vSrn", Type: "long", Data: []byte{0, 0, 0, 1}})
	assert.Contains(t, records, dsRecord{
		Name: "notes.txt", Code: "Iloc", Type: "blob",
		Data: append([]byte{0, 0, 0, 16}, []byte{0, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15}...),
	})
}

func TestReadsAForeignTreeOfManyNodes(t *testing.T) {
	data, err := os.ReadFile(finderTrashManyStore)
	require.NoError(t, err)

	levels, count, nodes := treeStats(t, data)
	require.Greater(t, nodes, 1, "the fixture is a single node and proves nothing about internal nodes")
	require.GreaterOrEqual(t, levels, 1)

	records, err := parseDSStore(data)
	require.NoError(t, err)
	assert.Len(t, records, count, "the tree walk found a different number of records than the file claims")
	assert.Contains(t, records, dsUstr("item-17.txt", "ptbL", "Users/ada/Documents/a/reasonably/long/path/17"),
		"a record stored under an internal node was not found")
}

func TestRewritesAForeignFileWithoutLosingRecords(t *testing.T) {
	data, err := os.ReadFile(finderTrashStore)
	require.NoError(t, err)
	original, err := parseDSStore(data)
	require.NoError(t, err)

	rebuilt, err := buildDSStore(original)
	require.NoError(t, err)
	records, err := parseDSStore(rebuilt)
	require.NoError(t, err)
	assert.ElementsMatch(t, original, records)
}

func TestRoundTripsEveryValueType(t *testing.T) {
	records := []dsRecord{
		dsUstr("notes.txt", "ptbL", "Users/ada/Documents"),
		dsUstr("notes.txt", "ptbN", "notes.txt"),
		dsUstr("emoji 🗑", "ptbL", "Users/ada/Ünicode"),
		{Name: ".", Code: "vSrn", Type: "long", Data: []byte{0, 0, 0, 1}},
		{Name: ".", Code: "vstl", Type: "type", Data: []byte("icnv")},
		{Name: "a", Code: "dscl", Type: "bool", Data: []byte{1}},
		{Name: "a", Code: "Iloc", Type: "blob", Data: []byte{0, 0, 0, 2, 0xab, 0xcd}},
		{Name: "a", Code: "moDD", Type: "dutc", Data: []byte{1, 2, 3, 4, 5, 6, 7, 8}},
		{Name: "a", Code: "modD", Type: "comp", Data: []byte{8, 7, 6, 5, 4, 3, 2, 1}},
		{Name: "a", Code: "icvt", Type: "shor", Data: []byte{0, 0, 0, 12}},
	}

	data, err := buildDSStore(records)
	require.NoError(t, err)
	parsed, err := parseDSStore(data)
	require.NoError(t, err)
	assert.ElementsMatch(t, records, parsed)

	// Records come back in the order the Finder stores them: by name, case
	// insensitively, then by structure id.
	require.Len(t, parsed, len(records))
	assert.Equal(t, dsRecord{Name: ".", Code: "vSrn", Type: "long", Data: []byte{0, 0, 0, 1}}, parsed[0])
	assert.Equal(t, "ptbL", parsed[len(parsed)-2].Code)
	assert.Equal(t, "ptbN", parsed[len(parsed)-1].Code)
}

func TestUstrValues(t *testing.T) {
	value, ok := dsUstr("f", "ptbL", "Users/ada/Ünicode 🗄").ustr()
	assert.True(t, ok)
	assert.Equal(t, "Users/ada/Ünicode 🗄", value)

	_, ok = dsRecord{Name: "f", Code: "vSrn", Type: "long", Data: []byte{0, 0, 0, 1}}.ustr()
	assert.False(t, ok, "a long value was decoded as a string")

	_, ok = dsRecord{Name: "f", Code: "ptbL", Type: "ustr", Data: []byte{0, 0, 0, 4, 0, 'a'}}.ustr()
	assert.False(t, ok, "a truncated string was decoded anyway")
}

func TestBuildsATreeOfManyNodes(t *testing.T) {
	// Enough records, with long enough values, to need several 4KB nodes and
	// therefore a level of internal nodes above them.
	var records []dsRecord
	for i := 0; i < 400; i++ {
		name := fmt.Sprintf("item-%03d.txt", i)
		records = append(records,
			dsUstr(name, "ptbL", fmt.Sprintf("Users/ada/Documents/some/reasonably/long/path/%03d", i)),
			dsUstr(name, "ptbN", name))
	}

	data, err := buildDSStore(records)
	require.NoError(t, err)
	levels, count, nodes := treeStats(t, data)
	assert.Greater(t, nodes, 1, "everything fitted in a single node, so nothing was proved")
	assert.GreaterOrEqual(t, levels, 1, "a multi-node tree has at least one level of internal nodes")
	assert.Equal(t, len(records), count)

	parsed, err := parseDSStore(data)
	require.NoError(t, err)
	assert.ElementsMatch(t, records, parsed)
}

func TestRejectsRecordsItCannotStore(t *testing.T) {
	_, err := buildDSStore([]dsRecord{{Name: "f", Code: "ptb", Type: "ustr", Data: []byte{0, 0, 0, 0}}})
	assert.ErrorIs(t, err, errBadDSStore, "a structure id that is not four characters was accepted")

	_, err = buildDSStore([]dsRecord{{Name: "f", Code: "ptbL", Type: "zzzz", Data: nil}})
	assert.ErrorIs(t, err, errBadDSStore, "an unknown data type was accepted")

	_, err = buildDSStore([]dsRecord{{Name: "f", Code: "ptbL", Type: "long", Data: []byte{1, 2}}})
	assert.ErrorIs(t, err, errBadDSStore, "a value that does not match its type was accepted")

	huge := make([]rune, dsPageSize)
	for i := range huge {
		huge[i] = 'x'
	}
	_, err = buildDSStore([]dsRecord{dsUstr(string(huge), "ptbL", "Users/ada")})
	assert.ErrorIs(t, err, errBadDSStore, "a record too big for a node was accepted")
}

func TestRejectsMalformedFiles(t *testing.T) {
	valid, err := buildDSStore([]dsRecord{dsUstr("notes.txt", "ptbL", "Users/ada")})
	require.NoError(t, err)

	corrupt := func(fn func([]byte)) []byte {
		data := make([]byte, len(valid))
		copy(data, valid)
		fn(data)
		return data
	}

	for name, data := range map[string][]byte{
		"empty":            {},
		"too short":        valid[:20],
		"bad prefix":       corrupt(func(b []byte) { b[3] = 9 }),
		"bad magic":        corrupt(func(b []byte) { copy(b[4:8], "Bud2") }),
		"offsets disagree": corrupt(func(b []byte) { binary.BigEndian.PutUint32(b[16:20], 64) }),
		"bookkeeping past the end": corrupt(func(b []byte) {
			binary.BigEndian.PutUint32(b[8:12], uint32(len(b)))
			binary.BigEndian.PutUint32(b[16:20], uint32(len(b)))
		}),
	} {
		_, err := parseDSStore(data)
		assert.ErrorIs(t, err, errBadDSStore, "%s was parsed as a valid file", name)
	}
}

func TestSurvivesArbitraryDamage(t *testing.T) {
	// A .DS_Store is written by other programs and can be damaged by any of
	// them; parsing one must fail cleanly rather than panic or hang.
	valid, err := buildDSStore([]dsRecord{
		dsUstr("notes.txt", "ptbL", "Users/ada/Documents"),
		dsUstr("notes.txt", "ptbN", "notes.txt"),
		{Name: ".", Code: "vSrn", Type: "long", Data: []byte{0, 0, 0, 1}},
	})
	require.NoError(t, err)

	for n := 0; n <= len(valid); n += 7 {
		_, _ = parseDSStore(valid[:n]) // must not panic
	}

	random := rand.New(rand.NewSource(1))
	for i := 0; i < 500; i++ {
		data := make([]byte, len(valid))
		copy(data, valid)
		for n := 0; n < 4; n++ {
			data[random.Intn(len(data))] = byte(random.Intn(256))
		}
		_, _ = parseDSStore(data) // must not panic
	}
}

func TestValueLengths(t *testing.T) {
	for _, tc := range []struct {
		typ  string
		data []byte
		want int
	}{
		{"bool", []byte{1}, 1},
		{"long", []byte{0, 0, 0, 1}, 4},
		{"comp", []byte{0, 0, 0, 0, 0, 0, 0, 1}, 8},
		{"blob", []byte{0, 0, 0, 2, 7, 7}, 6},
		{"ustr", []byte{0, 0, 0, 1, 0, 'a'}, 6},
	} {
		got, err := dsValueLen(tc.typ, tc.data)
		require.NoError(t, err)
		assert.Equal(t, tc.want, got, "wrong length for a %s value", tc.typ)
	}

	for name, tc := range map[string]struct {
		typ  string
		data []byte
	}{
		"unknown type":     {"zzzz", []byte{0}},
		"truncated fixed":  {"long", []byte{0, 0}},
		"truncated header": {"ustr", []byte{0, 0}},
		"length past end":  {"blob", []byte{0, 0, 0, 9, 1}},
		"huge length":      {"ustr", []byte{0xff, 0xff, 0xff, 0xff, 1}},
	} {
		_, err := dsValueLen(tc.typ, tc.data)
		assert.ErrorIs(t, err, errBadDSStore, "%s was accepted", name)
	}
}

// treeStats reads the B-tree master block of a .DS_Store, so a test can tell
// what shape of tree was actually written.
func treeStats(t *testing.T, data []byte) (levels, records, nodes int) {
	t.Helper()
	book, err := dsSlice(data, binary.BigEndian.Uint32(data[8:12]), binary.BigEndian.Uint32(data[12:16]))
	require.NoError(t, err)
	addresses, directory, err := parseDSBookkeeping(book)
	require.NoError(t, err)
	master, ok := directory["DSDB"]
	require.True(t, ok, "the file has no B-tree")
	addr := addresses[master]
	head, err := dsSlice(data, addr&^0x1f, 1<<(addr&0x1f))
	require.NoError(t, err)

	assert.Equal(t, uint32(dsPageSize), binary.BigEndian.Uint32(head[16:20]), "the master block records an unexpected page size")
	return int(binary.BigEndian.Uint32(head[4:8])), int(binary.BigEndian.Uint32(head[8:12])), int(binary.BigEndian.Uint32(head[12:16]))
}

func TestEmptyStore(t *testing.T) {
	data, err := buildDSStore(nil)
	require.NoError(t, err)
	records, err := parseDSStore(data)
	require.NoError(t, err)
	assert.Empty(t, records)

	levels, count, nodes := treeStats(t, data)
	assert.Equal(t, 0, levels)
	assert.Equal(t, 0, count)
	assert.Equal(t, 1, nodes, "an empty store still has its root node")
}

func TestWritesToARealDirectory(t *testing.T) {
	// The written file must be exactly what lands on disk, byte for byte.
	dir := t.TempDir()
	path := filepath.Join(dir, dsStoreName)
	data, err := buildDSStore([]dsRecord{dsUstr("notes.txt", "ptbL", "Users/ada")})
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(path, data, 0o600))

	onDisk, err := os.ReadFile(path)
	require.NoError(t, err)
	records, err := parseDSStore(onDisk)
	require.NoError(t, err)
	require.Len(t, records, 1)
	value, ok := records[0].ustr()
	require.True(t, ok)
	assert.Equal(t, "Users/ada", value)
}
