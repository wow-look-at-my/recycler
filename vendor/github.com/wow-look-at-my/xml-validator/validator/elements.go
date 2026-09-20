package validator

import (
	"fmt"
	"strings"

	"github.com/wow-look-at-my/go-containers/set"
	"github.com/wow-look-at-my/xml-validator/reader"
)

type attribute struct {
	name string
	line int
	col  int
}

func (p *parser) parseElement() error {
	if p.eof() || p.peek() != '<' {
		return p.errorf("expected '<' to begin element")
	}
	p.advance()

	name, err := p.parseName()
	if err != nil {
		return p.errorf("expected element name")
	}
	if err := p.validateQName(name); err != nil {
		return err
	}

	attrs, nsDecls, err := p.parseAttributes()
	if err != nil {
		return err
	}

	p.pushNSScope(nsDecls)

	if err := p.validatePrefixDeclared(name); err != nil {
		p.popNSScope()
		return err
	}
	for _, a := range attrs {
		if err := p.validatePrefixDeclared(a.name); err != nil {
			p.popNSScope()
			return err
		}
	}
	if err := p.checkAttributeUniqueness(attrs); err != nil {
		p.popNSScope()
		return err
	}

	p.skipWhitespace()

	if p.lookingAt("/>") {
		p.expect("/>")
		p.popNSScope()
		return nil
	}

	if err := p.expect(">"); err != nil {
		p.popNSScope()
		return p.errorf("expected '>' or '/>' to close start tag")
	}

	if err := p.parseContent(); err != nil {
		p.popNSScope()
		return err
	}

	if err := p.expect("</"); err != nil {
		p.popNSScope()
		return p.errorf("expected end tag '</%s>'", name)
	}
	endName, err := p.parseName()
	if err != nil {
		p.popNSScope()
		return p.errorf("expected element name in end tag")
	}
	if endName != name {
		p.popNSScope()
		return p.errorf("mismatched end tag: expected '</%s>', got '</%s>'", name, endName)
	}
	p.skipWhitespace()
	if err := p.expect(">"); err != nil {
		p.popNSScope()
		return p.errorf("expected '>' to close end tag")
	}

	p.popNSScope()
	return nil
}

func (p *parser) parseAttributes() ([]attribute, map[string]string, error) {
	var attrs []attribute
	nsDecls := make(map[string]string)
	seen := set.New[string]()

	for {
		if !p.skipWhitespace() {
			break
		}
		if p.eof() || p.peek() == '>' || p.peek() == '/' {
			break
		}

		attrLine, attrCol := p.line, p.col
		aname, err := p.parseName()
		if err != nil {
			break
		}
		if err := p.validateQName(aname); err != nil {
			return nil, nil, err
		}
		if !seen.Add(aname) {
			return nil, nil, &Error{Line: attrLine, Col: attrCol,
				Message: fmt.Sprintf("duplicate attribute %q", aname)}
		}

		if err := p.parseEq(); err != nil {
			return nil, nil, err
		}
		val, err := p.parseAttValue()
		if err != nil {
			return nil, nil, err
		}

		if aname == "xmlns" {
			nsDecls[""] = val
		} else if strings.HasPrefix(aname, "xmlns:") {
			prefix := aname[6:]
			if prefix == "" {
				return nil, nil, &Error{Line: attrLine, Col: attrCol,
					Message: "empty namespace prefix in declaration"}
			}
			if prefix == "xmlns" {
				return nil, nil, &Error{Line: attrLine, Col: attrCol,
					Message: "the prefix 'xmlns' must not be declared"}
			}
			if prefix == "xml" && val != "http://www.w3.org/XML/1998/namespace" {
				return nil, nil, &Error{Line: attrLine, Col: attrCol,
					Message: "the prefix 'xml' must not be bound to any namespace other than http://www.w3.org/XML/1998/namespace"}
			}
			nsDecls[prefix] = val
		} else {
			attrs = append(attrs, attribute{name: aname, line: attrLine, col: attrCol})
		}
	}

	return attrs, nsDecls, nil
}

func (p *parser) parseAttValue() (string, error) {
	if p.eof() {
		return "", p.errorf("expected attribute value")
	}
	quote := p.peek()
	if quote != '"' && quote != '\'' {
		return "", p.errorf("expected '\"' or \"'\" to begin attribute value, got %q", string(quote))
	}
	p.advance()

	var val []rune
	for !p.eof() && p.peek() != quote {
		r := p.peek()
		if r == '<' {
			return "", p.errorf("'<' is not allowed in attribute values")
		}
		if r == '&' {
			resolved, err := p.parseReference()
			if err != nil {
				return "", err
			}
			val = append(val, resolved)
			continue
		}
		if IsRestrictedChar(r) {
			return "", p.errorf("restricted character U+%04X must not appear literally in attribute value (use a character reference)", r)
		}
		if !IsChar(r) {
			return "", p.errorf("invalid character U+%04X in attribute value", r)
		}
		val = append(val, p.advance())
	}
	if p.eof() {
		return "", p.errorf("unterminated attribute value")
	}
	p.advance()
	return string(val), nil
}

func (p *parser) parseContent() error {
	if err := p.parseCharData(); err != nil {
		return err
	}
	for {
		if p.eof() {
			return p.errorf("unexpected end of input inside element content")
		}
		if p.lookingAt("</") {
			return nil
		}
		if p.lookingAt("<!--") {
			if err := p.parseComment(); err != nil {
				return err
			}
		} else if p.lookingAt("<?") {
			if err := p.parsePI(); err != nil {
				return err
			}
		} else if p.lookingAt("<![CDATA[") {
			if err := p.parseCDSect(); err != nil {
				return err
			}
		} else if p.lookingAt("<!") {
			return p.errorf("unsupported: markup declarations are not supported in content")
		} else if p.peek() == '<' {
			if err := p.parseElement(); err != nil {
				return err
			}
		} else if p.peek() == '&' {
			r, err := p.parseReference()
			if err != nil {
				return err
			}
			if !IsCharRefValue(r) {
				return p.errorf("character reference resolves to invalid character U+%04X", r)
			}
		} else {
			return p.errorf("unexpected character %q in content", string(p.peek()))
		}
		if err := p.parseCharData(); err != nil {
			return err
		}
	}
}

func (p *parser) parseCharData() error {
	for !p.eof() {
		r := p.peek()
		if r == '<' || r == '&' {
			return nil
		}
		if r == ']' && p.peekAt(1) == ']' && p.peekAt(2) == '>' {
			return p.errorf("']]>' is not allowed in character data")
		}
		if IsRestrictedChar(r) {
			return p.errorf("restricted character U+%04X must not appear literally in character data (use a character reference)", r)
		}
		if !IsChar(r) {
			return p.errorf("invalid character U+%04X in character data", r)
		}
		p.advance()
	}
	return nil
}

func (p *parser) parseCDSect() error {
	if err := p.expect("<![CDATA["); err != nil {
		return err
	}
	for {
		if p.eof() {
			return p.errorf("unterminated CDATA section")
		}
		if p.lookingAt("]]>") {
			p.expect("]]>")
			return nil
		}
		r := p.advance()
		if !IsChar(r) {
			return p.errorf("invalid character U+%04X in CDATA section", r)
		}
	}
}

func (p *parser) parseReference() (rune, error) {
	if err := p.expect("&"); err != nil {
		return 0, err
	}
	if p.eof() {
		return 0, p.errorf("unexpected end of input in reference")
	}
	if p.peek() == '#' {
		return p.parseCharRef()
	}
	return p.parseEntityRef()
}

func (p *parser) parseCharRef() (rune, error) {
	p.advance() // consume '#'

	hex := false
	if !p.eof() && p.peek() == 'x' {
		hex = true
		p.advance()
	}

	// The digits go in a stack buffer, not a slice that grows: a document
	// that escapes anything pays this path on every reference, and the
	// allocation showed up as 2.6 per reference in the benchmarks.
	//
	// The buffer is small because a character is at most 8 hex digits, and
	// leading zeros are skipped first so `&#0000000;` still parses -- a
	// document may write as many of them as it likes.
	var buf [8]byte
	n := 0
	start := p.pos
	tooLong := false
	leading := true
	for !p.eof() && p.peek() != ';' {
		r := p.peek()
		switch {
		case r >= '0' && r <= '9':
		case hex && ((r >= 'a' && r <= 'f') || (r >= 'A' && r <= 'F')):
		case hex:
			return 0, p.errorf("invalid hex digit %q in character reference", string(r))
		default:
			return 0, p.errorf("invalid digit %q in character reference", string(r))
		}
		if leading && r == '0' {
			p.advance()
			continue
		}
		leading = false
		if n == len(buf) {
			tooLong = true
		} else {
			buf[n] = byte(r)
			n++
		}
		p.advance()
	}
	if p.eof() {
		return 0, p.errorf("unterminated character reference")
	}
	// The digit text names a problem and is needed nowhere else, so each
	// error path slices it out itself. A closure capturing the parser would
	// be neater and allocates on every reference, error or not.
	end := p.pos
	empty := end == start
	p.advance() // consume ';'

	if empty {
		return 0, p.errorf("empty character reference")
	}
	if tooLong {
		return 0, p.errorf("invalid character reference value %q", string(p.input[start:end]))
	}

	// Folded here rather than handed to strconv: the digits were already
	// scanned and checked above, and the call cost more than the arithmetic
	// on a path that runs once per escaped character. Zero digits is how
	// `&#0;` is written, and folds to zero on its own.
	var val int64
	for _, c := range buf[:n] {
		var d int64
		switch {
		case c >= '0' && c <= '9':
			d = int64(c - '0')
		case c >= 'a' && c <= 'f':
			d = int64(c-'a') + 10
		default:
			d = int64(c-'A') + 10
		}
		if hex {
			val = val*16 + d
		} else {
			val = val*10 + d
		}
	}

	r := rune(val)
	if !IsCharRefValue(r) {
		return 0, p.errorf("character reference &#%s; resolves to invalid XML 1.1 character U+%04X", string(p.input[start:end]), r)
	}
	return r, nil
}

// The five predefined entities are matched against the input where they sit.
// Building the name as a string first cost two allocations on every `&amp;`
// in the document, which is the whole cost of escaping text that is mostly
// ampersands.
func (p *parser) parseEntityRef() (rune, error) {
	if p.eof() || !IsNameStartChar(p.peek()) {
		return 0, p.errorf("expected entity name")
	}
	start := p.pos
	p.advance()
	for !p.eof() && IsNameChar(p.peek()) {
		p.advance()
	}
	name := p.input[start:p.pos]

	if p.eof() || p.peek() != ';' {
		return 0, p.errorf("expected ';' after entity name %q", string(name))
	}
	p.advance()

	if r, ok := reader.PredefinedEntity(name); ok {
		return r, nil
	}
	return 0, p.errorf("unsupported: general entity reference &%s; (only &amp; &lt; &gt; &apos; &quot; are supported without a DTD)", string(name))
}
