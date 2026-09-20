package validator

import (
	"fmt"
	"strconv"
	"strings"
)

// The test language of xs:alternative. XSD 1.1 defines a "required subset" of
// XPath 2.0 that a conforming processor must accept, and this is that grammar:
//
//	Test        ::= OrExpr
//	OrExpr      ::= AndExpr ( 'or' AndExpr )*
//	AndExpr     ::= BooleanExpr ( 'and' BooleanExpr )*
//	BooleanExpr ::= '(' OrExpr ')' | BooleanFunction | ValueExpr ( Comparator ValueExpr )?
//	BooleanFunction ::= QName '(' OrExpr ')'
//	Comparator  ::= '=' | '!=' | '<' | '<=' | '>' | '>='
//	ValueExpr   ::= CastExpr | ConstructorFunction
//	CastExpr    ::= SimpleValue ( 'cast' 'as' QName '?'? )?
//	SimpleValue ::= AttrName | Literal
//	AttrName    ::= '@' NameTest
//	ConstructorFunction ::= QName '(' SimpleValue ')'
//
// An expression outside it is a hard error at schema-parse time. Only fn:not is
// a required function; another name is rejected rather than guessed at.
//
// see docs/conditional-types.md

// atom is one evaluated value. An attribute is untyped, which is what decides
// how a comparison against it reads: numeric when the other side is numeric,
// text otherwise.
type atom struct {
	absent  bool
	text    string
	numeric bool
	num     float64
}

type testExpr interface {
	eval(el *Element) bool
}

type valueExpr interface {
	value(el *Element) (atom, error)
}

type orNode struct{ terms []testExpr }

func (n orNode) eval(el *Element) bool {
	for _, t := range n.terms {
		if t.eval(el) {
			return true
		}
	}
	return false
}

type andNode struct{ terms []testExpr }

func (n andNode) eval(el *Element) bool {
	for _, t := range n.terms {
		if !t.eval(el) {
			return false
		}
	}
	return true
}

type notNode struct{ inner testExpr }

func (n notNode) eval(el *Element) bool { return !n.inner.eval(el) }

// existsNode is a ValueExpr standing alone as a condition: its effective
// boolean value. An absent attribute is false, and so is an empty string.
type existsNode struct{ inner valueExpr }

func (n existsNode) eval(el *Element) bool {
	v, err := n.inner.value(el)
	if err != nil || v.absent {
		return false
	}
	if v.numeric {
		return v.num != 0
	}
	return v.text != ""
}

type compareNode struct {
	left, right valueExpr
	op          string
}

func (n compareNode) eval(el *Element) bool {
	l, err := n.left.value(el)
	if err != nil || l.absent {
		return false
	}
	r, err := n.right.value(el)
	if err != nil || r.absent {
		return false
	}
	// A numeric operand pulls an untyped one into a numeric comparison, which
	// is what XPath does with an untyped attribute against a number.
	if l.numeric || r.numeric {
		ln, lok := asNumber(l)
		rn, rok := asNumber(r)
		if !lok || !rok {
			return false
		}
		return compareOrder(ln > rn, ln < rn, n.op)
	}
	return compareOrder(l.text > r.text, l.text < r.text, n.op)
}

func compareOrder(greater, less bool, op string) bool {
	switch op {
	case "=":
		return !greater && !less
	case "!=":
		return greater || less
	case "<":
		return less
	case "<=":
		return !greater
	case ">":
		return greater
	case ">=":
		return !less
	}
	return false
}

func asNumber(a atom) (float64, bool) {
	if a.numeric {
		return a.num, true
	}
	n, err := strconv.ParseFloat(strings.TrimSpace(a.text), 64)
	return n, err == nil
}

// attrValue reads one attribute of the element under test.
type attrValue struct{ name nameTest }

func (v attrValue) value(el *Element) (atom, error) {
	text, ok := lookupAttrValue(el, v.name)
	if !ok {
		return atom{absent: true}, nil
	}
	return atom{text: text}, nil
}

type literalValue struct{ a atom }

func (v literalValue) value(*Element) (atom, error) { return v.a, nil }

// castValue is both "cast as T" and a constructor function T(...): each checks
// the value against T and fails the alternative when it does not fit.
type castValue struct {
	inner    valueExpr
	typeName string
	optional bool
}

func (v castValue) value(el *Element) (atom, error) {
	in, err := v.inner.value(el)
	if err != nil {
		return atom{}, err
	}
	if in.absent {
		if v.optional {
			return in, nil
		}
		return atom{}, fmt.Errorf("cast of an empty sequence to %s", v.typeName)
	}
	if err := validateBuiltinValue(v.typeName, in.text); err != nil {
		return atom{}, err
	}
	out := atom{text: in.text}
	if isNumericType(v.typeName) {
		n, err := strconv.ParseFloat(strings.TrimSpace(in.text), 64)
		if err != nil {
			return atom{}, err
		}
		out.numeric, out.num = true, n
	}
	return out, nil
}

// compileTest parses a test into a tree. Every rejection names what it saw, so
// a schema author learns which part of the expression is outside the subset.
func compileTest(expr string, s *Schema) (testExpr, error) {
	p := &testParser{schema: s, whole: expr}
	if err := p.tokenize(expr); err != nil {
		return nil, err
	}
	if len(p.toks) == 0 {
		return nil, fmt.Errorf("test is empty")
	}
	node, err := p.parseOr()
	if err != nil {
		return nil, err
	}
	if p.pos != len(p.toks) {
		return nil, fmt.Errorf("unsupported test %q: unexpected %q", expr, p.toks[p.pos].text)
	}
	return node, nil
}

type token struct {
	kind string // "(" ")" "op" "name" "attr" "str" "num"
	text string
}

type testParser struct {
	schema *Schema
	whole  string
	toks   []token
	pos    int
}

func (p *testParser) tokenize(expr string) error {
	for i := 0; i < len(expr); {
		c := expr[i]
		switch {
		case c == ' ' || c == '\t' || c == '\n' || c == '\r':
			i++
		case c == '(' || c == ')':
			p.toks = append(p.toks, token{kind: string(c), text: string(c)})
			i++
		case c == '\'' || c == '"':
			end := strings.IndexByte(expr[i+1:], c)
			if end < 0 {
				return fmt.Errorf("unsupported test %q: a quoted literal is never closed", p.whole)
			}
			p.toks = append(p.toks, token{kind: "str", text: expr[i+1 : i+1+end]})
			i += end + 2
		case strings.HasPrefix(expr[i:], "!="), strings.HasPrefix(expr[i:], "<="), strings.HasPrefix(expr[i:], ">="):
			p.toks = append(p.toks, token{kind: "op", text: expr[i : i+2]})
			i += 2
		case c == '=' || c == '<' || c == '>':
			p.toks = append(p.toks, token{kind: "op", text: string(c)})
			i++
		case c == '?':
			p.toks = append(p.toks, token{kind: "op", text: "?"})
			i++
		case c == '@':
			j := i + 1
			for j < len(expr) && isNameByte(expr[j]) {
				j++
			}
			if j == i+1 {
				return fmt.Errorf("unsupported test %q: \"@\" with no attribute name", p.whole)
			}
			p.toks = append(p.toks, token{kind: "attr", text: expr[i+1 : j]})
			i = j
		case c >= '0' && c <= '9', c == '-' || c == '+':
			j := i + 1
			for j < len(expr) && (expr[j] == '.' || (expr[j] >= '0' && expr[j] <= '9')) {
				j++
			}
			p.toks = append(p.toks, token{kind: "num", text: expr[i:j]})
			i = j
		case isNameByte(c):
			j := i
			for j < len(expr) && isNameByte(expr[j]) {
				j++
			}
			p.toks = append(p.toks, token{kind: "name", text: expr[i:j]})
			i = j
		default:
			return fmt.Errorf("unsupported test %q: %q is not part of the test language", p.whole, string(c))
		}
	}
	return nil
}

func isNameByte(c byte) bool {
	return c == ':' || c == '_' || c == '-' || c == '.' ||
		(c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9')
}

func (p *testParser) peek() (token, bool) {
	if p.pos < len(p.toks) {
		return p.toks[p.pos], true
	}
	return token{}, false
}

func (p *testParser) parseOr() (testExpr, error) {
	first, err := p.parseAnd()
	if err != nil {
		return nil, err
	}
	terms := []testExpr{first}
	for {
		t, ok := p.peek()
		if !ok || t.kind != "name" || t.text != "or" {
			break
		}
		p.pos++
		next, err := p.parseAnd()
		if err != nil {
			return nil, err
		}
		terms = append(terms, next)
	}
	if len(terms) == 1 {
		return terms[0], nil
	}
	return orNode{terms: terms}, nil
}

func (p *testParser) parseAnd() (testExpr, error) {
	first, err := p.parseBoolean()
	if err != nil {
		return nil, err
	}
	terms := []testExpr{first}
	for {
		t, ok := p.peek()
		if !ok || t.kind != "name" || t.text != "and" {
			break
		}
		p.pos++
		next, err := p.parseBoolean()
		if err != nil {
			return nil, err
		}
		terms = append(terms, next)
	}
	if len(terms) == 1 {
		return terms[0], nil
	}
	return andNode{terms: terms}, nil
}

func (p *testParser) parseBoolean() (testExpr, error) {
	t, ok := p.peek()
	if !ok {
		return nil, fmt.Errorf("unsupported test %q: it ends where a condition was expected", p.whole)
	}

	if t.kind == "(" {
		p.pos++
		inner, err := p.parseOr()
		if err != nil {
			return nil, err
		}
		if err := p.expect(")"); err != nil {
			return nil, err
		}
		return inner, nil
	}

	// A QName followed by "(" is a boolean function. Only fn:not is required of
	// a conforming processor, and a constructor function is a VALUE, so it is
	// handled with the value expressions below.
	if t.kind == "name" && p.isFunctionCall() && !isBuiltinTypeName(t.text) {
		if stripPrefix(t.text) != "not" {
			return nil, fmt.Errorf("unsupported test %q: function %q is not supported; only not() is", p.whole, t.text)
		}
		p.pos += 2
		inner, err := p.parseOr()
		if err != nil {
			return nil, err
		}
		if err := p.expect(")"); err != nil {
			return nil, err
		}
		return notNode{inner: inner}, nil
	}

	left, err := p.parseValue()
	if err != nil {
		return nil, err
	}
	if op, ok := p.peek(); ok && op.kind == "op" && op.text != "?" {
		p.pos++
		right, err := p.parseValue()
		if err != nil {
			return nil, err
		}
		return compareNode{left: left, right: right, op: op.text}, nil
	}
	return existsNode{inner: left}, nil
}

func (p *testParser) isFunctionCall() bool {
	return p.pos+1 < len(p.toks) && p.toks[p.pos+1].kind == "("
}

func (p *testParser) expect(kind string) error {
	t, ok := p.peek()
	if !ok || t.kind != kind {
		return fmt.Errorf("unsupported test %q: expected %q", p.whole, kind)
	}
	p.pos++
	return nil
}

func (p *testParser) parseValue() (valueExpr, error) {
	t, ok := p.peek()
	if !ok {
		return nil, fmt.Errorf("unsupported test %q: it ends where a value was expected", p.whole)
	}

	var base valueExpr
	switch {
	case t.kind == "attr":
		nt, err := compileNameTest(t.text, p.whole, p.schema)
		if err != nil {
			return nil, err
		}
		p.pos++
		base = attrValue{name: nt}
	case t.kind == "str":
		p.pos++
		base = literalValue{a: atom{text: t.text}}
	case t.kind == "num":
		n, err := strconv.ParseFloat(t.text, 64)
		if err != nil {
			return nil, fmt.Errorf("unsupported test %q: %q is not a number", p.whole, t.text)
		}
		p.pos++
		base = literalValue{a: atom{text: t.text, numeric: true, num: n}}
	case t.kind == "name" && p.isFunctionCall():
		// A constructor function: xs:int(@a).
		typeName, err := p.builtinTypeName(t.text)
		if err != nil {
			return nil, err
		}
		p.pos += 2
		inner, err := p.parseValue()
		if err != nil {
			return nil, err
		}
		if err := p.expect(")"); err != nil {
			return nil, err
		}
		base = castValue{inner: inner, typeName: typeName}
	default:
		return nil, fmt.Errorf("unsupported test %q: %q is not an attribute, a literal, or a constructor function", p.whole, t.text)
	}

	return p.parseCastSuffix(base)
}

// parseCastSuffix reads a trailing "cast as QName" with its optional "?".
func (p *testParser) parseCastSuffix(base valueExpr) (valueExpr, error) {
	t, ok := p.peek()
	if !ok || t.kind != "name" || t.text != "cast" {
		return base, nil
	}
	p.pos++
	as, ok := p.peek()
	if !ok || as.kind != "name" || as.text != "as" {
		return nil, fmt.Errorf("unsupported test %q: \"cast\" must be followed by \"as\"", p.whole)
	}
	p.pos++
	name, ok := p.peek()
	if !ok || name.kind != "name" {
		return nil, fmt.Errorf("unsupported test %q: \"cast as\" must name a type", p.whole)
	}
	typeName, err := p.builtinTypeName(name.text)
	if err != nil {
		return nil, err
	}
	p.pos++
	optional := false
	if q, ok := p.peek(); ok && q.kind == "op" && q.text == "?" {
		optional = true
		p.pos++
	}
	return castValue{inner: base, typeName: typeName, optional: optional}, nil
}

// builtinTypeName resolves a QName to a built-in datatype. A cast to a
// user-defined type is not required of a conforming processor and is rejected
// rather than silently treated as a string.
func (p *testParser) builtinTypeName(qname string) (string, error) {
	local := stripPrefix(qname)
	if resolveBuiltinType(local) == nil {
		return "", fmt.Errorf("unsupported test %q: %q does not name a built-in datatype", p.whole, qname)
	}
	return local, nil
}

func isBuiltinTypeName(qname string) bool {
	return resolveBuiltinType(stripPrefix(qname)) != nil
}
