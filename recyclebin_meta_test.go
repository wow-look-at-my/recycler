package recycler

import (
	"encoding/binary"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"testing"
	"time"
	"unicode/utf16"
)

func TestBinMetadataRoundTrip(t *testing.T) {
	want := binMetadata{
		Size:         1234567,
		DeletedAt:    time.Date(2026, 7, 26, 11, 24, 9, 0, time.UTC),
		OriginalPath: `C:\Users\pazer\Documents\report .txt`,
	}
	got, err := parseBinMetadata(encodeBinMetadata(want))
	require.Nil(t, err)

	assert.Equal(t, want.Size, got.Size)

	assert.True(t, got.DeletedAt.Equal(want.DeletedAt))

	assert.Equal(t, want.OriginalPath, got.OriginalPath)

}

// TestBinMetadataVersion1 decodes a hand-built version 1 record, the layout
// Windows Vista through 8.1 write, with its fixed-width 260-character path.
func TestBinMetadataVersion1(t *testing.T) {
	const path = `D:\photos\holiday.jpg`
	data := make([]byte, binMetaHeaderSize+binMetaV1PathLen*2)
	binary.LittleEndian.PutUint64(data[0:8], 1)
	binary.LittleEndian.PutUint64(data[8:16], 4096)
	deletedAt := time.Date(2013, 3, 1, 8, 30, 0, 0, time.UTC)
	binary.LittleEndian.PutUint64(data[16:24], timeToFileTime(deletedAt))
	for i, c := range utf16.Encode([]rune(path)) {
		binary.LittleEndian.PutUint16(data[binMetaHeaderSize+i*2:], c)
	}

	got, err := parseBinMetadata(data)
	require.Nil(t, err)

	assert.Equal(t, path, got.OriginalPath)

	assert.Equal(t, int64(4096), got.Size)

	assert.True(t, got.DeletedAt.Equal(deletedAt))

}

func TestBinMetadataRejectsGarbage(t *testing.T) {
	cases := map[string][]byte{
		"empty":       {},
		"header only": make([]byte, binMetaHeaderSize),
		"truncated v1 path": func() []byte {
			b := make([]byte, binMetaHeaderSize+8)
			binary.LittleEndian.PutUint64(b[0:8], 1)
			return b
		}(),
		"v2 path longer than the file": func() []byte {
			b := make([]byte, binMetaHeaderSize+4+4)
			binary.LittleEndian.PutUint64(b[0:8], 2)
			binary.LittleEndian.PutUint32(b[binMetaHeaderSize:], 1000)
			return b
		}(),
	}
	for _, data := range cases {
		_, err := parseBinMetadata(data)
		assert.NotNil(t, err)

	}
}

func TestFileTimeConversion(t *testing.T) {
	// 1601-01-01T00:00:00Z is FILETIME 0 plus one second per 10,000,000 ticks.
	got := fileTimeToTime(fileTimeEpochOffset)
	assert.True(t, got.Equal(time.Unix(0, 0).UTC()))

	when := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	got := fileTimeToTime(timeToFileTime(when))
	assert.True(t, got.Equal(when))

	got := fileTimeToTime(0)
	assert.True(t, got.IsZero())

}
