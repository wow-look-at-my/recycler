package recycler

import (
	"encoding/binary"
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
	if err != nil {
		t.Fatalf("parseBinMetadata: %v", err)
	}
	if got.Size != want.Size {
		t.Errorf("Size = %d, want %d", got.Size, want.Size)
	}
	if !got.DeletedAt.Equal(want.DeletedAt) {
		t.Errorf("DeletedAt = %s, want %s", got.DeletedAt, want.DeletedAt)
	}
	if got.OriginalPath != want.OriginalPath {
		t.Errorf("OriginalPath = %q, want %q", got.OriginalPath, want.OriginalPath)
	}
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
	if err != nil {
		t.Fatalf("parseBinMetadata: %v", err)
	}
	if got.OriginalPath != path {
		t.Errorf("OriginalPath = %q, want %q", got.OriginalPath, path)
	}
	if got.Size != 4096 {
		t.Errorf("Size = %d, want 4096", got.Size)
	}
	if !got.DeletedAt.Equal(deletedAt) {
		t.Errorf("DeletedAt = %s, want %s", got.DeletedAt, deletedAt)
	}
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
	for name, data := range cases {
		if _, err := parseBinMetadata(data); err == nil {
			t.Errorf("%s: expected an error, got none", name)
		}
	}
}

func TestFileTimeConversion(t *testing.T) {
	// 1601-01-01T00:00:00Z is FILETIME 0 plus one second per 10,000,000 ticks.
	if got := fileTimeToTime(fileTimeEpochOffset); !got.Equal(time.Unix(0, 0).UTC()) {
		t.Errorf("the FILETIME epoch offset maps to %s, want the Unix epoch", got)
	}
	when := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	if got := fileTimeToTime(timeToFileTime(when)); !got.Equal(when) {
		t.Errorf("round trip gave %s, want %s", got, when)
	}
	if got := fileTimeToTime(0); !got.IsZero() {
		t.Errorf("a zero FILETIME gave %s, want the zero time", got)
	}
}
