package reader

// PredefinedEntity resolves the five entity references XML defines without a
// DTD. The name is passed as the runes it occupies in the input rather than as
// a string, because building the string first allocated on every `&amp;` in
// the document -- see docs/encodings.md.
func PredefinedEntity(name []rune) (rune, bool) {
	switch {
	case runesAre(name, "amp"):
		return '&', true
	case runesAre(name, "lt"):
		return '<', true
	case runesAre(name, "gt"):
		return '>', true
	case runesAre(name, "apos"):
		return '\'', true
	case runesAre(name, "quot"):
		return '"', true
	default:
		return 0, false
	}
}

// runesAre reports whether runes spells the given ASCII word. Comparing
// against a string directly would convert one of them first, which is the
// allocation this avoids.
func runesAre(runes []rune, word string) bool {
	if len(runes) != len(word) {
		return false
	}
	for i, r := range runes {
		if r != rune(word[i]) {
			return false
		}
	}
	return true
}
