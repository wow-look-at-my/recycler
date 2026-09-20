package validator

import (
	"fmt"
	"github.com/wow-look-at-my/xml-validator/reader"
	"strings"
)

type parser struct {
	input   []rune
	pos     int
	line    int
	col     int
	nsStack []map[string]string
}

func newParser(input []rune) *parser {
	return &parser{
		input: input,
		pos:   0,
		line:  1,
		col:   1,
	}
}

func (p *parser) peek() rune {
	if p.pos >= len(p.input) {
		return 0
	}
	return p.input[p.pos]
}

func (p *parser) peekAt(offset int) rune {
	i := p.pos + offset
	if i >= len(p.input) || i < 0 {
		return 0
	}
	return p.input[i]
}

func (p *parser) advance() rune {
	if p.pos >= len(p.input) {
		return 0
	}
	r := p.input[p.pos]
	p.pos++
	if r == '\n' {
		p.line++
		p.col = 1
	} else {
		p.col++
	}
	return r
}

func (p *parser) eof() bool {
	return p.pos >= len(p.input)
}

func (p *parser) errorf(format string, args ...any) *Error {
	return &Error{Line: p.line, Col: p.col, Message: fmt.Sprintf(format, args...)}
}

func (p *parser) expect(s string) error {
	for _, c := range s {
		if p.eof() {
			return p.errorf("unexpected end of input, expected %q", s)
		}
		if p.peek() != c {
			return p.errorf("expected %q, got %q", string(c), string(p.peek()))
		}
		p.advance()
	}
	return nil
}

func (p *parser) lookingAt(s string) bool {
	runes := []rune(s)
	for i, c := range runes {
		if p.pos+i >= len(p.input) || p.input[p.pos+i] != c {
			return false
		}
	}
	return true
}

func (p *parser) skipWhitespace() bool {
	found := false
	for !p.eof() && IsWhitespace(p.peek()) {
		p.advance()
		found = true
	}
	return found
}

func (p *parser) requireWhitespace() error {
	if !p.skipWhitespace() {
		return p.errorf("expected whitespace")
	}
	return nil
}

func (p *parser) parseDocument() error {
	if err := p.parseProlog(); err != nil {
		return err
	}
	if p.eof() {
		return p.errorf("expected root element")
	}
	if err := p.parseElement(); err != nil {
		return err
	}
	if err := p.parseMiscSeq(); err != nil {
		return err
	}
	if !p.eof() {
		return p.errorf("unexpected content after root element")
	}
	return nil
}

func (p *parser) parseProlog() error {
	if err := p.parseXMLDecl(); err != nil {
		return err
	}
	if err := p.parseMiscSeq(); err != nil {
		return err
	}
	if p.lookingAt("<!DOCTYPE") {
		return p.errorf("unsupported: DOCTYPE declarations are not supported")
	}
	return nil
}

func (p *parser) parseXMLDecl() error {
	if !p.lookingAt("<?xml") {
		return p.errorf("XML 1.1 documents must begin with an XML declaration (<?xml ...?>)")
	}
	if err := p.expect("<?xml"); err != nil {
		return err
	}
	if !p.eof() && IsNameChar(p.peek()) {
		return p.errorf("XML declaration must be exactly <?xml, not <?xml followed by name character %q", string(p.peek()))
	}
	if err := p.requireWhitespace(); err != nil {
		return err
	}

	if err := p.expect("version"); err != nil {
		return p.errorf("expected 'version' in XML declaration")
	}
	if err := p.parseEq(); err != nil {
		return err
	}
	version, err := p.parseQuotedValue()
	if err != nil {
		return err
	}
	if version != "1.1" {
		return p.errorf("unsupported XML version %q; only XML 1.1 is supported", version)
	}

	savedPos, savedLine, savedCol := p.pos, p.line, p.col
	hadSpace := p.skipWhitespace()
	if p.lookingAt("encoding") {
		if !hadSpace {
			return p.errorf("expected whitespace before 'encoding'")
		}
		if err := p.expect("encoding"); err != nil {
			return err
		}
		if err := p.parseEq(); err != nil {
			return err
		}
		encName, err := p.parseQuotedValue()
		if err != nil {
			return err
		}
		if err := p.validateEncName(encName); err != nil {
			return err
		}
		if err := p.validateEncodingMatch(encName); err != nil {
			return err
		}
		savedPos, savedLine, savedCol = p.pos, p.line, p.col
		hadSpace = p.skipWhitespace()
	}

	if p.lookingAt("standalone") {
		if !hadSpace {
			p.pos, p.line, p.col = savedPos, savedLine, savedCol
			return p.errorf("expected whitespace before 'standalone'")
		}
		if err := p.expect("standalone"); err != nil {
			return err
		}
		if err := p.parseEq(); err != nil {
			return err
		}
		sd, err := p.parseQuotedValue()
		if err != nil {
			return err
		}
		if sd != "yes" && sd != "no" {
			return p.errorf("standalone must be 'yes' or 'no', got %q", sd)
		}
	}

	p.skipWhitespace()
	if err := p.expect("?>"); err != nil {
		return p.errorf("expected '?>' to close XML declaration")
	}
	return nil
}

func (p *parser) validateEncName(name string) error {
	if len(name) == 0 {
		return p.errorf("empty encoding name")
	}
	r := rune(name[0])
	if !((r >= 'A' && r <= 'Z') || (r >= 'a' && r <= 'z')) {
		return p.errorf("encoding name must start with a letter, got %q", string(r))
	}
	for i := 1; i < len(name); i++ {
		r = rune(name[i])
		if !((r >= 'A' && r <= 'Z') || (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '.' || r == '_' || r == '-') {
			return p.errorf("invalid character %q in encoding name", string(r))
		}
	}
	return nil
}

func (p *parser) validateEncodingMatch(declared string) error {
	if reader.CanonicalEncoding(declared) == "" {
		return p.errorf("unsupported encoding %q (UTF-8 and ISO-8859-1 are supported)", declared)
	}
	return nil
}

func (p *parser) parseEq() error {
	p.skipWhitespace()
	if err := p.expect("="); err != nil {
		return p.errorf("expected '='")
	}
	p.skipWhitespace()
	return nil
}

func (p *parser) parseQuotedValue() (string, error) {
	if p.eof() {
		return "", p.errorf("expected quoted value")
	}
	quote := p.peek()
	if quote != '"' && quote != '\'' {
		return "", p.errorf("expected '\"' or \"'\", got %q", string(quote))
	}
	p.advance()
	var val []rune
	for !p.eof() && p.peek() != quote {
		val = append(val, p.advance())
	}
	if p.eof() {
		return "", p.errorf("unterminated quoted value")
	}
	p.advance()
	return string(val), nil
}

func (p *parser) parseMiscSeq() error {
	for {
		if p.skipWhitespace() {
			continue
		}
		if p.lookingAt("<!--") {
			if err := p.parseComment(); err != nil {
				return err
			}
			continue
		}
		if p.lookingAt("<?") {
			if err := p.parsePI(); err != nil {
				return err
			}
			continue
		}
		break
	}
	return nil
}

func (p *parser) parseComment() error {
	if err := p.expect("<!--"); err != nil {
		return err
	}
	for {
		if p.eof() {
			return p.errorf("unterminated comment")
		}
		if p.lookingAt("-->") {
			p.expect("-->")
			return nil
		}
		if p.peek() == '-' && p.peekAt(1) == '-' {
			return p.errorf("'--' is not allowed inside a comment")
		}
		r := p.advance()
		if !IsChar(r) {
			return p.errorf("invalid character U+%04X in comment", r)
		}
	}
}

func (p *parser) parsePI() error {
	if err := p.expect("<?"); err != nil {
		return err
	}
	name, err := p.parseName()
	if err != nil {
		return p.errorf("expected processing instruction target name")
	}
	if strings.EqualFold(name, "xml") {
		return p.errorf("processing instruction target must not be 'xml' (reserved)")
	}
	if p.lookingAt("?>") {
		p.expect("?>")
		return nil
	}
	if err := p.requireWhitespace(); err != nil {
		return p.errorf("expected whitespace after PI target")
	}
	for {
		if p.eof() {
			return p.errorf("unterminated processing instruction")
		}
		if p.lookingAt("?>") {
			p.expect("?>")
			return nil
		}
		r := p.advance()
		if !IsChar(r) {
			return p.errorf("invalid character U+%04X in processing instruction", r)
		}
	}
}

func (p *parser) parseName() (string, error) {
	if p.eof() {
		return "", p.errorf("expected name, got end of input")
	}
	r := p.peek()
	if !IsNameStartChar(r) {
		return "", p.errorf("invalid name start character %q (U+%04X)", string(r), r)
	}
	// Slice the name out of the input instead of growing a rune slice and
	// converting that: the caller wants one string, so one allocation.
	start := p.pos
	p.advance()
	for !p.eof() && IsNameChar(p.peek()) {
		p.advance()
	}
	return string(p.input[start:p.pos]), nil
}
