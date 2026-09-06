package winbin

// Codec for the "$I" metadata files Windows writes next to every recycled
// file in <drive>:\$Recycle.Bin\<user SID>\. Each recycled item has:
// "$R<id><ext>" holds the data, "$I<id><ext>" the metadata below.
//
// The layout is fixed little-endian:
//
//	offset  size  meaning
// The record opens with the format version, then the size of the recycled item
// in bytes, then the deletion time as a Windows FILETIME, and ends with the
// original path in Unicode. The older version, which Vista onward wrote, gives
// the path a fixed-width buffer. The newer version, which Windows a decade
// later writes, gives it a character count and then that many characters. Each
// includes the terminating NUL. binMetaVersion below names each, and the
// constants beside it carry every width.
//
// This file deliberately has no build constraint: keeping the codec portable
// keeps it testable on any platform.

import (
	"encoding/binary"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf16"
)

const (
	binMetaHeaderSize = 24
	binMetaV1PathLen  = 260 // characters, including the terminating NUL
)

// errBadMetadata is returned for a $I file that cannot be understood.
var errBadMetadata = errors.New("recycler: malformed recycle bin metadata")

// Metadata is the content of a $I file.
type Metadata struct {
	Size         int64
	DeletedAt    time.Time
	OriginalPath string
}

// Parse decodes a $I file.
func Parse(data []byte) (Metadata, error) {
	if len(data) < binMetaHeaderSize {
		return Metadata{}, fmt.Errorf("%w: %d bytes is too short", errBadMetadata, len(data))
	}
	version := binary.LittleEndian.Uint64(data[0:8])
	meta := Metadata{
		Size:      int64(binary.LittleEndian.Uint64(data[8:16])),
		DeletedAt: fileTimeToTime(binary.LittleEndian.Uint64(data[16:24])),
	}

	var chars []uint16
	switch version {
	case 1:
		if len(data) < binMetaHeaderSize+binMetaV1PathLen*2 {
			return Metadata{}, fmt.Errorf("%w: truncated version 1 path", errBadMetadata)
		}
		chars = decodeUTF16LE(data[binMetaHeaderSize : binMetaHeaderSize+binMetaV1PathLen*2])
	case 2:
		if len(data) < binMetaHeaderSize+4 {
			return Metadata{}, fmt.Errorf("%w: truncated version 2 header", errBadMetadata)
		}
		count := int(binary.LittleEndian.Uint32(data[binMetaHeaderSize : binMetaHeaderSize+4]))
		start := binMetaHeaderSize + 4
		if count < 0 || start+count*2 > len(data) {
			return Metadata{}, fmt.Errorf("%w: path length %d exceeds the file", errBadMetadata, count)
		}
		chars = decodeUTF16LE(data[start : start+count*2])
	default:
		return Metadata{}, fmt.Errorf("%w: unknown version %d", errBadMetadata, version)
	}

	meta.OriginalPath = strings.TrimRight(string(utf16.Decode(chars)), "\x00")
	return meta, nil
}

// Encode produces a newer-version $I file.
func Encode(meta Metadata) []byte {
	chars := utf16.Encode([]rune(meta.OriginalPath))
	chars = append(chars, 0)

	out := make([]byte, binMetaHeaderSize+4+len(chars)*2)
	binary.LittleEndian.PutUint64(out[0:8], 2)
	binary.LittleEndian.PutUint64(out[8:16], uint64(meta.Size))
	binary.LittleEndian.PutUint64(out[16:24], timeToFileTime(meta.DeletedAt))
	binary.LittleEndian.PutUint32(out[24:28], uint32(len(chars)))
	for i, c := range chars {
		binary.LittleEndian.PutUint16(out[28+i*2:30+i*2], c)
	}
	return out
}

func decodeUTF16LE(b []byte) []uint16 {
	chars := make([]uint16, 0, len(b)/2)
	for i := 0; i+1 < len(b); i += 2 {
		c := binary.LittleEndian.Uint16(b[i : i+2])
		if c == 0 {
			break
		}
		chars = append(chars, c)
	}
	return chars
}

// fileTimeEpochOffset is how many FILETIME.
const fileTimeEpochOffset = 116444736000000000

func fileTimeToTime(ft uint64) time.Time {
	if ft == 0 {
		return time.Time{}
	}
	return time.Unix(0, (int64(ft)-fileTimeEpochOffset)*100).UTC()
}

func timeToFileTime(t time.Time) uint64 {
	if t.IsZero() {
		return 0
	}
	return uint64(t.UTC().UnixNano()/100 + fileTimeEpochOffset)
}
