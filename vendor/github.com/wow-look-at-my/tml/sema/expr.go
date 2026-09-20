package sema

import (
	"fmt"
	"strings"
)

// Expr is an attribute value or a run of text, split into literal and interpolated parts. The grammar is deliberately
type Expr struct {
	Segments []Segment
	Source   string
}

// Segment is either literal text or a single interpolated reference.
type Segment struct {
	Literal string
	Ref     *Ref
}

// Ref is a single `{...}` interpolation.
type Ref struct {
	// Name is the property, loop variable or token being read.
	Name string
	// Theme marks a `{theme.x}` reference, which reads a token rather than a property.
	Theme bool
	// Not marks `{not x}`.
	Not bool
}

func (r *Ref) String() string {
	var b strings.Builder
	b.WriteByte('{')
	if r.Not {
		b.WriteString("not ")
	}
	if r.Theme {
		b.WriteString("theme.")
	}
	b.WriteString(r.Name)
	b.WriteByte('}')
	return b.String()
}

// IsLiteral reports whether the expression interpolates nothing, in which case its value is the source text.
func (e *Expr) IsLiteral() bool {
	for _, s := range e.Segments {
		if s.Ref != nil {
			return false
		}
	}
	return true
}

// SoleRef returns the reference when the expression is exactly a single interpolation and nothing else. Such an expression
func (e *Expr) SoleRef() (*Ref, bool) {
	if len(e.Segments) == 1 && e.Segments[0].Ref != nil {
		return e.Segments[0].Ref, true
	}
	return nil, false
}

// Refs lists every reference in the expression, so analysis can check each name against what is in scope before
func (e *Expr) Refs() []*Ref {
	var refs []*Ref
	for _, s := range e.Segments {
		if s.Ref != nil {
			refs = append(refs, s.Ref)
		}
	}
	return refs
}

// ParseExpr splits raw into literal and interpolated segments. `{{` and `}}` are literal braces, which is the only
func ParseExpr(raw string) (*Expr, error) {
	expr := &Expr{Source: raw}
	var literal strings.Builder

	flush := func() {
		if literal.Len() > 0 {
			expr.Segments = append(expr.Segments, Segment{Literal: literal.String()})
			literal.Reset()
		}
	}

	for i := 0; i < len(raw); {
		switch {
		case strings.HasPrefix(raw[i:], "{{"):
			literal.WriteByte('{')
			i += 2
		case strings.HasPrefix(raw[i:], "}}"):
			literal.WriteByte('}')
			i += 2
		case raw[i] == '{':
			end := strings.IndexByte(raw[i:], '}')
			if end < 0 {
				return nil, fmt.Errorf("unclosed { in %q; write {{ for a literal brace", raw)
			}
			ref, err := parseRef(raw[i+1 : i+end])
			if err != nil {
				return nil, err
			}
			flush()
			expr.Segments = append(expr.Segments, Segment{Ref: ref})
			i += end + 1
		case raw[i] == '}':
			return nil, fmt.Errorf("unmatched } in %q; write }} for a literal brace", raw)
		default:
			literal.WriteByte(raw[i])
			i++
		}
	}
	flush()
	return expr, nil
}

func parseRef(body string) (*Ref, error) {
	body = strings.TrimSpace(body)
	if body == "" {
		return nil, fmt.Errorf("empty interpolation {}")
	}

	ref := &Ref{}
	if body == "not" {
		return nil, fmt.Errorf("interpolation names nothing after not")
	}
	if rest, ok := strings.CutPrefix(body, "not "); ok {
		ref.Not = true
		body = strings.TrimSpace(rest)
	}
	if rest, ok := strings.CutPrefix(body, "theme."); ok {
		ref.Theme = true
		body = strings.TrimSpace(rest)
	}
	if body == "" {
		return nil, fmt.Errorf("interpolation names nothing")
	}
	if !isIdent(body) {
		if strings.Contains(body, ".") {
			return nil, fmt.Errorf("unknown reference %q; only theme.<token> uses a dot", body)
		}
		return nil, fmt.Errorf("cannot parse interpolation %q; expressions are a name, optionally prefixed with not", body)
	}
	ref.Name = body
	return ref, nil
}

// isIdent reports whether s is a bare name. Anything else -- an operator, a call, a path -- is outside the grammar and
func isIdent(s string) bool {
	if s == "" {
		return false
	}
	for i, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r == '_':
		case r >= '0' && r <= '9' && i > 0:
		default:
			return false
		}
	}
	return true
}

// Scope resolves names during evaluation.
type Scope interface {
	// Lookup resolves a property or loop variable.
	Lookup(name string) (Value, bool)
	// Token resolves a theme token.
	Token(name string) (Value, bool)
}

// Eval resolves the expression against a scope. A lone reference keeps its value's type. Anything else concatenates
func (e *Expr) Eval(scope Scope) (Value, error) {
	if ref, ok := e.SoleRef(); ok {
		return evalRef(ref, scope)
	}

	var b strings.Builder
	for _, segment := range e.Segments {
		if segment.Ref == nil {
			b.WriteString(segment.Literal)
			continue
		}
		value, err := evalRef(segment.Ref, scope)
		if err != nil {
			return Value{}, err
		}
		b.WriteString(value.String())
	}
	return StringValue(b.String()), nil
}

func evalRef(ref *Ref, scope Scope) (Value, error) {
	var (
		value Value
		ok    bool
	)
	if ref.Theme {
		value, ok = scope.Token(ref.Name)
	} else {
		value, ok = scope.Lookup(ref.Name)
	}
	if !ok {
		return Value{}, fmt.Errorf("unknown reference %s", ref)
	}
	if !ref.Not {
		return value, nil
	}
	truth, err := value.Truthy()
	if err != nil {
		return Value{}, fmt.Errorf("cannot negate %s: %w", ref, err)
	}
	return BoolValue(!truth), nil
}
