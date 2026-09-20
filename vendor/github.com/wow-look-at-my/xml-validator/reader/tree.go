package reader

import (
	"fmt"
	"io"
	"strconv"
	"strings"
)

func ParseTree(r io.Reader) (*Document, error) {
	runes, err := Decode(r)
	if err != nil {
		return nil, err
	}
	tp := &treeParser{input: runes, line: 1, col: 1}
	return tp.parseDocument()
}

type treeParser struct {
	input []rune
	pos   int
	line  int
	col   int
}

func (tp *treeParser) peek() rune {
	if tp.pos >= len(tp.input) {
		return 0
	}
	return tp.input[tp.pos]
}

func (tp *treeParser) advance() rune {
	if tp.pos >= len(tp.input) {
		return 0
	}
	r := tp.input[tp.pos]
	tp.pos++
	if r == '\n' {
		tp.line++
		tp.col = 1
	} else {
		tp.col++
	}
	return r
}

func (tp *treeParser) eof() bool { return tp.pos >= len(tp.input) }

func (tp *treeParser) errorf(format string, args ...any) error {
	return &Error{Line: tp.line, Col: tp.col, Message: fmt.Sprintf(format, args...)}
}

func (tp *treeParser) expect(s string) error {
	for _, c := range s {
		if tp.eof() || tp.peek() != c {
			return tp.errorf("expected %q", s)
		}
		tp.advance()
	}
	return nil
}

func (tp *treeParser) lookingAt(s string) bool {
	for i, c := range []rune(s) {
		if tp.pos+i >= len(tp.input) || tp.input[tp.pos+i] != c {
			return false
		}
	}
	return true
}

func (tp *treeParser) skipWS() {
	for !tp.eof() && IsWhitespace(tp.peek()) {
		tp.advance()
	}
}

func (tp *treeParser) parseDocument() (*Document, error) {
	if tp.lookingAt("<?xml") {
		tp.skipPIOrDecl()
	}
	tp.skipMisc()
	if tp.eof() {
		return nil, tp.errorf("no root element")
	}
	root, err := tp.parseElement(map[string]string{})
	if err != nil {
		return nil, err
	}
	return &Document{Root: root}, nil
}

func (tp *treeParser) skipPIOrDecl() {
	tp.expect("<?")
	for !tp.eof() {
		if tp.lookingAt("?>") {
			tp.expect("?>")
			return
		}
		tp.advance()
	}
}

func (tp *treeParser) skipComment() {
	tp.expect("<!--")
	for !tp.eof() {
		if tp.lookingAt("-->") {
			tp.expect("-->")
			return
		}
		tp.advance()
	}
}

func (tp *treeParser) skipMisc() {
	for {
		tp.skipWS()
		if tp.lookingAt("<!--") {
			tp.skipComment()
		} else if tp.lookingAt("<?") {
			tp.skipPIOrDecl()
		} else if tp.lookingAt("<!DOCTYPE") {
			tp.skipDoctype()
		} else {
			return
		}
	}
}

func (tp *treeParser) skipDoctype() {
	depth := 0
	for !tp.eof() {
		r := tp.advance()
		if r == '<' {
			depth++
		} else if r == '>' {
			if depth <= 1 {
				return
			}
			depth--
		}
	}
}

func (tp *treeParser) parseName() string {
	return string(tp.nameRunes())
}

// nameRunes is parseName without the string: it slices the name out of the
// input, so a caller that only compares the name allocates nothing.
func (tp *treeParser) nameRunes() []rune {
	start := tp.pos
	if !tp.eof() && IsNameStartChar(tp.peek()) {
		tp.advance()
	}
	for !tp.eof() && IsNameChar(tp.peek()) {
		tp.advance()
	}
	return tp.input[start:tp.pos]
}

func (tp *treeParser) parseElement(parentNS map[string]string) (*Element, error) {
	line, col := tp.line, tp.col
	if err := tp.expect("<"); err != nil {
		return nil, err
	}
	name := tp.parseName()
	if name == "" {
		return nil, tp.errorf("expected element name")
	}

	nsScope := make(map[string]string)
	for k, v := range parentNS {
		nsScope[k] = v
	}

	var rawAttrs []struct {
		name, value string
		line, col   int
	}
	for {
		before := tp.pos
		tp.skipWS()
		if tp.eof() || tp.peek() == '>' || tp.peek() == '/' {
			break
		}
		aline, acol := tp.line, tp.col
		aname := tp.parseName()
		if aname == "" {
			tp.pos = before
			break
		}
		tp.skipWS()
		tp.expect("=")
		tp.skipWS()
		val, err := tp.parseAttrValue()
		if err != nil {
			return nil, err
		}

		if aname == "xmlns" {
			nsScope[""] = val
		} else if strings.HasPrefix(aname, "xmlns:") {
			nsScope[aname[6:]] = val
		}
		rawAttrs = append(rawAttrs, struct {
			name, value string
			line, col   int
		}{aname, val, aline, acol})
	}

	var attrs []Attr
	for _, ra := range rawAttrs {
		if ra.name == "xmlns" || strings.HasPrefix(ra.name, "xmlns:") {
			continue
		}
		a := Attr{Name: ra.name, Value: ra.value, Line: ra.line, Col: ra.col}
		if idx := strings.Index(ra.name, ":"); idx >= 0 {
			a.Prefix = ra.name[:idx]
			a.Local = ra.name[idx+1:]
			a.Namespace = nsScope[a.Prefix]
		} else {
			a.Local = ra.name
		}
		attrs = append(attrs, a)
	}

	elem := &Element{
		Name:       name,
		Attrs:      attrs,
		Namespaces: nsScope,
		Line:       line,
		Col:        col,
	}
	if idx := strings.Index(name, ":"); idx >= 0 {
		elem.Prefix = name[:idx]
		elem.Local = name[idx+1:]
		elem.Namespace = nsScope[elem.Prefix]
	} else {
		elem.Local = name
		elem.Namespace = nsScope[""]
	}

	if tp.lookingAt("/>") {
		tp.expect("/>")
		return elem, nil
	}
	if err := tp.expect(">"); err != nil {
		return nil, err
	}

	children, err := tp.parseContent(nsScope)
	if err != nil {
		return nil, err
	}
	elem.Children = children

	// A truncated document ends here, with content already parsed. Reporting it
	// as a document would hand a caller a partial answer that reads as a whole
	// one -- a stream cut mid-element is exactly the case that has to be told
	// apart from a stream that finished.
	if err := tp.expect("</"); err != nil {
		return nil, tp.errorf("element %q is never closed", name)
	}
	if closing := tp.parseName(); closing != name {
		return nil, tp.errorf("element %q is closed by </%s>", name, closing)
	}
	tp.skipWS()
	if err := tp.expect(">"); err != nil {
		return nil, err
	}
	return elem, nil
}

func (tp *treeParser) parseContent(nsScope map[string]string) ([]Node, error) {
	var nodes []Node
	var text []rune

	flushText := func() {
		if len(text) > 0 {
			nodes = append(nodes, &CharData{Content: string(text)})
			text = nil
		}
	}

	for !tp.eof() {
		if tp.lookingAt("</") {
			flushText()
			return nodes, nil
		}
		if tp.lookingAt("<!--") {
			flushText()
			tp.skipComment()
			continue
		}
		if tp.lookingAt("<?") {
			flushText()
			tp.skipPIOrDecl()
			continue
		}
		if tp.lookingAt("<![CDATA[") {
			if err := tp.expect("<![CDATA["); err != nil {
				return nil, err
			}
			closed := false
			for !tp.eof() {
				if tp.lookingAt("]]>") {
					if err := tp.expect("]]>"); err != nil {
						return nil, err
					}
					closed = true
					break
				}
				text = append(text, tp.advance())
			}
			if !closed {
				return nil, tp.errorf("CDATA section is never closed")
			}
			continue
		}
		if tp.peek() == '<' {
			flushText()
			child, err := tp.parseElement(nsScope)
			if err != nil {
				return nil, err
			}
			nodes = append(nodes, child)
			continue
		}
		if tp.peek() == '&' {
			r, err := tp.parseRef()
			if err != nil {
				return nil, err
			}
			text = append(text, r)
			continue
		}
		text = append(text, tp.advance())
	}
	flushText()
	return nodes, nil
}

func (tp *treeParser) parseAttrValue() (string, error) {
	quote := tp.peek()
	if quote != '"' && quote != '\'' {
		return "", tp.errorf("expected quote")
	}
	tp.advance()
	var val []rune
	for !tp.eof() && tp.peek() != quote {
		if tp.peek() == '&' {
			r, err := tp.parseRef()
			if err != nil {
				return "", err
			}
			val = append(val, r)
		} else {
			val = append(val, tp.advance())
		}
	}
	if !tp.eof() {
		tp.advance()
	}
	return string(val), nil
}

func (tp *treeParser) parseRef() (rune, error) {
	tp.advance() // '&'
	if tp.peek() == '#' {
		tp.advance()
		hex := false
		if tp.peek() == 'x' {
			hex = true
			tp.advance()
		}
		// A stack buffer, and leading zeros skipped so an arbitrarily padded
		// reference still parses. Growing a slice here allocated on every
		// reference in the document. See parseCharRef in elements.go.
		var buf [8]byte
		n := 0
		leading := true
		tooLong := false
		for !tp.eof() && tp.peek() != ';' {
			r := tp.advance()
			if leading && r == '0' {
				continue
			}
			leading = false
			if n == len(buf) {
				tooLong = true
				continue
			}
			buf[n] = byte(r)
			n++
		}
		if !tp.eof() {
			tp.advance()
		}
		if tooLong {
			return 0, tp.errorf("invalid character reference")
		}
		if n == 0 {
			return 0, nil // every digit was a leading zero
		}
		base := 10
		if hex {
			base = 16
		}
		val, err := strconv.ParseInt(string(buf[:n]), base, 32)
		if err != nil {
			return 0, tp.errorf("invalid character reference")
		}
		return rune(val), nil
	}
	// Matched where it sits: the name is only a string on the error path.
	name := tp.nameRunes()
	if !tp.eof() && tp.peek() == ';' {
		tp.advance()
	}
	if r, ok := PredefinedEntity(name); ok {
		return r, nil
	}
	return 0, tp.errorf("unknown entity &%s;", string(name))
}
