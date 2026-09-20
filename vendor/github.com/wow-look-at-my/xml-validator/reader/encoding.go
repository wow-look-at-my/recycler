package reader

import (
	"fmt"
	"github.com/wow-look-at-my/go-containers/set"
	"strings"
	"unicode/utf8"
)

// The two input modes. UTF-8 is the default: a document with no encoding
// declaration is UTF-8, and so is one that declares it.
//
// Byte mode is the 8-bit alternative. Every byte is one character with the
// same value, U+0000 through U+00FF, so a document is exactly as long as the
// text it carries. A character above U+00FF has no byte, so it needs a
// character reference -- `&#9731;` is a snowman in either mode.
// see docs/encodings.md
const (
	encodingUTF8 = "UTF-8"
	encodingByte = "ISO-8859-1"
)

// byteEncodingNames are the spellings that select byte mode. IANA registers
// the first as the name and the rest as aliases of one 8-bit coded character
// set, and a document may write any of them.
var byteEncodingNames = set.Of(
	"ISO-8859-1",
	"ISO8859-1",
	"ISO_8859-1",
	"LATIN1",
	"LATIN-1",
	"L1",
	"IBM819",
	"CP819",
	"CSISOLATIN1",
)

// canonicalEncoding maps a declared name to the mode it selects. The empty
// string means the name is neither, which the declaration parser reports.
// CanonicalEncoding maps a declared encoding name to the mode it selects. The
// empty string means the name selects neither, which the declaration parser
// reports.
func CanonicalEncoding(declared string) string {
	upper := strings.ToUpper(declared)
	switch {
	case upper == "UTF-8" || upper == "UTF8":
		return encodingUTF8
	case byteEncodingNames.Contains(upper):
		return encodingByte
	default:
		return ""
	}
}

// sniffEncoding reads the encoding declaration out of the raw bytes, before
// anything decodes them. It has to work on bytes because the answer decides
// how to read the rest, and it can: the declaration is ASCII in both modes,
// which the XML 1.1 spec guarantees by requiring every encoding it admits to
// agree with ASCII on the characters a declaration uses.
//
// It reports UTF-8 for a document that declares nothing. A declaration it
// cannot make sense of also reads as UTF-8, so the declaration parser is the
// one that reports the syntax error, at the right position.
func sniffEncoding(raw []byte) string {
	const window = 256
	head := raw
	if len(head) > window {
		head = head[:window]
	}
	if !strings.HasPrefix(string(head), "<?xml") {
		return encodingUTF8
	}
	decl := string(head)
	if end := strings.Index(decl, "?>"); end >= 0 {
		decl = decl[:end]
	}
	at := strings.Index(decl, "encoding")
	if at < 0 {
		return encodingUTF8
	}
	rest := strings.TrimLeft(decl[at+len("encoding"):], " \t\r\n")
	if !strings.HasPrefix(rest, "=") {
		return encodingUTF8
	}
	rest = strings.TrimLeft(rest[1:], " \t\r\n")
	if rest == "" {
		return encodingUTF8
	}
	quote := rest[0]
	if quote != '"' && quote != '\'' {
		return encodingUTF8
	}
	closing := strings.IndexByte(rest[1:], quote)
	if closing < 0 {
		return encodingUTF8
	}
	if name := CanonicalEncoding(rest[1 : 1+closing]); name != "" {
		return name
	}
	return encodingUTF8
}

func decodeUTF8(data []byte) ([]rune, error) {
	runes := make([]rune, 0, len(data))
	for len(data) > 0 {
		r, size := utf8.DecodeRune(data)
		if r == utf8.RuneError && size <= 1 {
			return nil, fmt.Errorf("invalid UTF-8 byte sequence")
		}
		runes = append(runes, r)
		data = data[size:]
	}
	return runes, nil
}

// decodeByteMode reads byte mode: byte b is the character U+00XX with the same
// value. Every byte decodes, so this cannot fail. What the byte means is
// still checked downstream -- a literal NUL and a literal restricted
// character are as invalid here as they are in UTF-8.
func decodeByteMode(data []byte) []rune {
	runes := make([]rune, len(data))
	for i, b := range data {
		runes[i] = rune(b)
	}
	return runes
}
