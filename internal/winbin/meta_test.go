package winbin

import (
	"encoding/binary"
	"testing"
	"time"
	"unicode/utf16"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBinMetadataRoundTrip(t *testing.T) {
	want := Metadata{
		Size:         1234567,
		DeletedAt:    time.Date(2026, 7, 26, 11, 24, 9, 0, time.UTC),
		OriginalPath: `C:\Users\pazer\Documents\report .txt`,
	}
	got, err := Parse(Encode(want))
	require.NoError(t, err)
	assert.Equal(t, want.Size, got.Size)
	assert.True(t, got.DeletedAt.Equal(want.DeletedAt), "DeletedAt = %s, want %s", got.DeletedAt, want.DeletedAt)
	assert.Equal(t, want.OriginalPath, got.OriginalPath)
}

// TestBinMetadataVersion1 decodes a hand-built version 1 record, the layout
// Windows Vista through 8.1 write, with its fixed-width 260-character path.
func TestBinMetadataVersion1(t *testing.T) {
	const path = `D:\photos\holiday.jpg`
	deletedAt := time.Date(2013, 3, 1, 8, 30, 0, 0, time.UTC)

	data := make([]byte, binMetaHeaderSize+binMetaV1PathLen*2)
	binary.LittleEndian.PutUint64(data[0:8], 1)
	binary.LittleEndian.PutUint64(data[8:16], 4096)
	binary.LittleEndian.PutUint64(data[16:24], timeToFileTime(deletedAt))
	for i, c := range utf16.Encode([]rune(path)) {
		binary.LittleEndian.PutUint16(data[binMetaHeaderSize+i*2:], c)
	}

	got, err := Parse(data)
	require.NoError(t, err)
	assert.Equal(t, path, got.OriginalPath)
	assert.Equal(t, int64(4096), got.Size)
	assert.True(t, got.DeletedAt.Equal(deletedAt), "DeletedAt = %s, want %s", got.DeletedAt, deletedAt)
}

func TestBinMetadataRejectsGarbage(t *testing.T) {
	truncatedV1 := make([]byte, binMetaHeaderSize+8)
	binary.LittleEndian.PutUint64(truncatedV1[0:8], 1)

	overlongV2 := make([]byte, binMetaHeaderSize+8)
	binary.LittleEndian.PutUint64(overlongV2[0:8], 2)
	binary.LittleEndian.PutUint32(overlongV2[binMetaHeaderSize:], 1000)

	unknownVersion := make([]byte, binMetaHeaderSize+8)
	binary.LittleEndian.PutUint64(unknownVersion[0:8], 99)

	cases := map[string][]byte{
		"empty":                        {},
		"header only":                  make([]byte, binMetaHeaderSize),
		"truncated version 1 path":     truncatedV1,
		"version 2 path past the end":  overlongV2,
		"unknown version":              unknownVersion,
		"shorter than a single header": make([]byte, 3),
	}
	for name, data := range cases {
		_, err := Parse(data)
		assert.ErrorIs(t, err, errBadMetadata, "%s should be rejected", name)
	}
}

func TestFileTimeConversion(t *testing.T) {
	// The FILETIME epoch offset is exactly the Unix epoch.
	assert.True(t, fileTimeToTime(fileTimeEpochOffset).Equal(time.Unix(0, 0).UTC()))

	when := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	assert.True(t, fileTimeToTime(timeToFileTime(when)).Equal(when))
	assert.True(t, fileTimeToTime(0).IsZero(), "a zero FILETIME should give the zero time")
}
