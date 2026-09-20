package reader

import (
	"fmt"
	"io"
)

// Decode reads a document and returns its characters, choosing the decoder
// from the encoding declaration and normalizing line endings. It is what a
// parser starts from.
func Decode(r io.Reader) ([]rune, error) {
	raw, err := io.ReadAll(r)
	if err != nil {
		return nil, fmt.Errorf("reading input: %w", err)
	}
	if len(raw) == 0 {
		return nil, fmt.Errorf("empty input")
	}

	if err := rejectUnsupportedEncoding(raw); err != nil {
		return nil, err
	}

	var runes []rune
	if sniffEncoding(raw) == encodingByte {
		runes = decodeByteMode(raw)
	} else {
		runes, err = decodeUTF8(raw)
		if err != nil {
			return nil, err
		}
	}

	return normalizeLineEndings(runes), nil
}

// rejectUnsupportedEncoding rejects any input that begins with a byte-order
// mark or that matches the UTF-16 leading-NUL heuristic from XML 1.1 appendix
// F. Neither mode this validator reads takes a BOM: it is meaningless for
// UTF-8 (per the utf8everywhere recommendation) and some downstream tools read
// it as a literal U+FEFF, and byte mode has no character above U+00FF to spell
// one with. A document declaring byte mode still starts with `<?xml` in ASCII,
// so this check runs before the declaration is read and applies to both.
func rejectUnsupportedEncoding(data []byte) error {
	if len(data) >= 3 && data[0] == 0xEF && data[1] == 0xBB && data[2] == 0xBF {
		return fmt.Errorf("unsupported encoding: input begins with a UTF-8 BOM (raw UTF-8 only)")
	}
	if len(data) >= 2 {
		if data[0] == 0xFE && data[1] == 0xFF {
			return fmt.Errorf("unsupported encoding: UTF-16 BE (only UTF-8 and ISO-8859-1 are supported)")
		}
		if data[0] == 0xFF && data[1] == 0xFE {
			return fmt.Errorf("unsupported encoding: UTF-16 LE (only UTF-8 and ISO-8859-1 are supported)")
		}
		if data[0] == 0x00 && data[1] == 0x3C {
			return fmt.Errorf("unsupported encoding: input looks like UTF-16 BE (only UTF-8 and ISO-8859-1 are supported)")
		}
		if data[0] == 0x3C && data[1] == 0x00 {
			return fmt.Errorf("unsupported encoding: input looks like UTF-16 LE (only UTF-8 and ISO-8859-1 are supported)")
		}
	}
	return nil
}

// normalizeLineEndings applies XML 1.1 line ending normalization:
//
//	#xD #xA  -> #xA
//	#xD #x85 -> #xA
//	#x85     -> #xA
//	#x2028   -> #xA
//	#xD      -> #xA (when not followed by #xA or #x85)
//
// It rewrites input in place. Every rule either replaces one rune with one
// rune or two with one, so the write index never overtakes the read index,
// and the alternative is a second copy of the whole document per parse.
func normalizeLineEndings(input []rune) []rune {
	w := 0
	for i := 0; i < len(input); i++ {
		r := input[i]
		switch {
		case r == 0xD:
			input[w] = 0xA
			if i+1 < len(input) && (input[i+1] == 0xA || input[i+1] == 0x85) {
				i++
			}
		case r == 0x85, r == 0x2028:
			input[w] = 0xA
		default:
			input[w] = r
		}
		w++
	}
	return input[:w]
}
