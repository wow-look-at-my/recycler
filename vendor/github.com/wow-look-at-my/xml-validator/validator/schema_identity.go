package validator

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/wow-look-at-my/go-containers/set"
)

// Identity constraints -- xs:key, xs:keyref, xs:unique -- over the XPath subset
// XSD defines for a selector and a field. Anything outside that subset is a
// hard error at schema-parse time, so a constraint either runs or says it
// cannot.
//
// Two rules differ from XPath 1.0 read literally, both to keep a constraint
// from being silently vacuous:
//   - An unprefixed name matches its local name in ANY namespace. XPath reads
//     it as the no-namespace name, which selects nothing under a schema with a
//     target namespace.
//   - Values compare in the field's value space: two numerals that denote the
//     same number are one key, and "true" and "1" are one boolean.

// nameTest matches one element or attribute name in a compiled path. The three
// forms XSD allows are "*" (anyName), "prefix:*" (anyLocal within one
// namespace), and a QName.
type nameTest struct {
	anyName  bool
	anyLocal bool
	anyNS    bool
	ns       string
	local    string
}

func (n nameTest) matches(ns, local string) bool {
	if n.anyName {
		return true
	}
	if !n.anyLocal && n.local != local {
		return false
	}
	return n.anyNS || n.ns == ns
}

// idPath is one alternative of a selector or field XPath: an optional ".//"
// prefix, a run of child steps, and -- for a field -- a trailing attribute.
type idPath struct {
	descendant bool
	steps      []nameTest
	attr       *nameTest
}

func compileIDPaths(xpath string, s *Schema, allowAttr bool) ([]idPath, error) {
	var out []idPath
	for _, alt := range strings.Split(xpath, "|") {
		p, err := compileIDPath(strings.TrimSpace(alt), xpath, s, allowAttr)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, nil
}

func compileIDPath(expr, whole string, s *Schema, allowAttr bool) (idPath, error) {
	var p idPath
	if expr == "" {
		return p, fmt.Errorf("xpath %q has an empty alternative", whole)
	}
	switch {
	case strings.HasPrefix(expr, ".//"):
		p.descendant = true
		expr = expr[3:]
	case strings.HasPrefix(expr, "./"):
		expr = expr[2:]
	case expr == ".":
		return p, nil
	}
	if expr == "" {
		return p, fmt.Errorf("xpath %q ends after its axis", whole)
	}

	parts := strings.Split(expr, "/")
	for i, part := range parts {
		last := i == len(parts)-1
		// XSD spells the attribute axis "@name" or "attribute::name", and lets
		// a child step carry an explicit "child::". Both are in the grammar the
		// schema-for-schemas enforces, so both resolve to the same step here.
		attrStep := strings.HasPrefix(part, "@")
		if rest, ok := strings.CutPrefix(part, "attribute::"); ok {
			attrStep, part = true, "@"+rest
		}
		if rest, ok := strings.CutPrefix(part, "child::"); ok {
			part = rest
		}
		switch {
		case part == "":
			return p, fmt.Errorf("unsupported xpath %q: an empty step (\"//\") is not supported", whole)
		case part == ".":
			// A self step selects what it started from, so it drops out.
			continue
		case attrStep:
			if !allowAttr {
				return p, fmt.Errorf("unsupported xpath %q: an attribute step is allowed in xs:field, not in xs:selector", whole)
			}
			if !last {
				return p, fmt.Errorf("unsupported xpath %q: an attribute step must come last", whole)
			}
			nt, err := compileNameTest(part[1:], whole, s)
			if err != nil {
				return p, err
			}
			p.attr = &nt
		default:
			nt, err := compileNameTest(part, whole, s)
			if err != nil {
				return p, err
			}
			p.steps = append(p.steps, nt)
		}
	}
	return p, nil
}

func compileNameTest(name, whole string, s *Schema) (nameTest, error) {
	if name == "*" {
		return nameTest{anyName: true}, nil
	}
	prefix, local := "", name
	if i := strings.IndexByte(name, ':'); i >= 0 {
		prefix, local = name[:i], name[i+1:]
	}
	if prefix != "" && validateNCName(prefix) != nil {
		return nameTest{}, unsupportedXPathStep(name, whole)
	}
	// "prefix:*" is every name in one namespace.
	anyLocal := local == "*"
	if !anyLocal && validateNCName(local) != nil {
		return nameTest{}, unsupportedXPathStep(name, whole)
	}
	if prefix == "" {
		if anyLocal {
			return nameTest{anyName: true}, nil
		}
		return nameTest{anyNS: true, local: local}, nil
	}
	ns, ok := s.prefixes[prefix]
	if !ok {
		return nameTest{}, fmt.Errorf("xpath %q uses prefix %q, which the schema does not declare", whole, prefix)
	}
	return nameTest{ns: ns, local: local, anyLocal: anyLocal}, nil
}

func unsupportedXPathStep(step, whole string) error {
	return fmt.Errorf("unsupported xpath %q: step %q is not a name, \"*\", \"prefix:*\", or \"@name\" -- a predicate, a function, and an axis other than child:: or attribute:: are outside the subset XSD allows here", whole, step)
}

// selectIDNodes returns the nodes the paths select from base, in document
// order, each node once.
func selectIDNodes(base *Element, paths []idPath) []*Element {
	var out []*Element
	seen := set.New[*Element]()
	for _, p := range paths {
		for _, n := range p.selectFrom(base) {
			if seen.Add(n) {
				out = append(out, n)
			}
		}
	}
	return out
}

func (p idPath) selectFrom(base *Element) []*Element {
	current := []*Element{base}
	if p.descendant {
		current = appendDescendants(current, base)
	}
	for _, step := range p.steps {
		var next []*Element
		for _, node := range current {
			for _, child := range node.ChildElements() {
				if step.matches(child.Namespace, child.Local) {
					next = append(next, child)
				}
			}
		}
		current = next
	}
	return current
}

func appendDescendants(out []*Element, el *Element) []*Element {
	for _, child := range el.ChildElements() {
		out = append(out, child)
		out = appendDescendants(out, child)
	}
	return out
}

// identityScope holds the key tables one element's constraints produced, keyed
// by constraint name. A keyref resolves against the nearest enclosing scope
// that evaluated the key it names.
type identityScope map[string]map[string]*Element

func (sv *schemaValidator) checkIdentity(el *Element, scopes []identityScope) {
	decl := sv.elemDecls[el]
	if decl != nil && len(decl.Constraints) > 0 {
		scope := identityScope{}
		for _, c := range decl.Constraints {
			if c.Kind != "keyref" {
				scope[qnameKey(sv.schema.TargetNamespace, c.Name)] = sv.buildKeyTable(el, c)
			}
		}
		// The full slice expression forces a copy, so one child's scope never
		// lands in a sibling's stack.
		scopes = append(scopes[:len(scopes):len(scopes)], scope)
		for _, c := range decl.Constraints {
			if c.Kind == "keyref" {
				sv.checkKeyref(el, c, scopes)
			}
		}
	}
	for _, child := range el.ChildElements() {
		sv.checkIdentity(child, scopes)
	}
}

func (sv *schemaValidator) buildKeyTable(el *Element, c *IdentityConstraint) map[string]*Element {
	table := map[string]*Element{}
	for _, target := range selectIDNodes(el, c.selector) {
		key, ok := sv.fieldTuple(target, c)
		if !ok {
			continue
		}
		if prev, dup := table[key]; dup {
			sv.addError(target, "xs:%s %q: element %q repeats a value first used at line %d, column %d",
				c.Kind, c.Name, target.Local, prev.Line, prev.Col)
			continue
		}
		table[key] = target
	}
	return table
}

func (sv *schemaValidator) checkKeyref(el *Element, c *IdentityConstraint, scopes []identityScope) {
	targets := selectIDNodes(el, c.selector)
	if len(targets) == 0 {
		return
	}
	table := keyTableFor(c.referKey, scopes)
	if table == nil {
		sv.addError(el, "xs:keyref %q: %q is declared on no enclosing element, so nothing evaluated it here", c.Name, c.Refer)
		return
	}
	for _, target := range targets {
		key, ok := sv.fieldTuple(target, c)
		if !ok {
			continue
		}
		if _, found := table[key]; !found {
			sv.addError(target, "xs:keyref %q: element %q refers to a value that %q does not declare", c.Name, target.Local, c.Refer)
		}
	}
}

func keyTableFor(name string, scopes []identityScope) map[string]*Element {
	for i := len(scopes) - 1; i >= 0; i-- {
		if table, ok := scopes[i][name]; ok {
			return table
		}
	}
	return nil
}

// fieldTuple builds one target's key. It reports false when a field selects
// nothing: xs:key requires every field, while xs:unique and xs:keyref simply do
// not count that node.
func (sv *schemaValidator) fieldTuple(target *Element, c *IdentityConstraint) (string, bool) {
	parts := make([]string, 0, len(c.fields))
	for i, f := range c.fields {
		value, found, err := sv.fieldValue(target, f)
		if err != nil {
			sv.addError(target, "xs:%s %q field %q: %v", c.Kind, c.Name, c.fieldXPaths[i], err)
			return "", false
		}
		if !found {
			if c.Kind == "key" {
				sv.addError(target, "xs:key %q: element %q has no value for field %q", c.Name, target.Local, c.fieldXPaths[i])
			}
			return "", false
		}
		parts = append(parts, value)
	}
	return strings.Join(parts, "\x00"), true
}

func (sv *schemaValidator) fieldValue(target *Element, paths []idPath) (string, bool, error) {
	var keys []string
	for _, p := range paths {
		for _, node := range p.selectFrom(target) {
			if p.attr == nil {
				keys = append(keys, identityValueKey(node.TextContent(), sv.elemFieldType(node)))
				continue
			}
			for _, attr := range node.Attrs {
				if attr.Prefix == "xmlns" || attr.Name == "xmlns" {
					continue
				}
				if p.attr.matches(attr.Namespace, attr.Local) {
					keys = append(keys, identityValueKey(attr.Value, sv.attrFieldType(node, attr)))
				}
			}
		}
	}
	switch {
	case len(keys) > 1:
		return "", false, fmt.Errorf("selects %d nodes; a field must select at most one", len(keys))
	case len(keys) == 0:
		return "", false, nil
	}
	return keys[0], true, nil
}

func (sv *schemaValidator) elemFieldType(el *Element) Type {
	if decl, ok := sv.elemDecls[el]; ok {
		return decl.Type
	}
	return nil
}

func (sv *schemaValidator) attrFieldType(el *Element, attr Attr) Type {
	if decls, ok := sv.attrDecls[el]; ok {
		if ad, ok := decls[attrKey{ns: attr.Namespace, local: attr.Local}]; ok {
			return ad.Type
		}
	}
	return nil
}

// identityValueKey renders a field value for comparison. The field's declared
// type decides the value space: 1 and 1.0 are one integer key, and only an
// xs:string keeps whitespace that would otherwise collapse.
func identityValueKey(value string, t Type) string {
	base := "string"
	switch typ := t.(type) {
	case *BuiltinType:
		base = typ.name
	case *SimpleType:
		base = resolveSimpleTypeBaseName(typ)
	}
	v := value
	if base != "string" {
		v = strings.Join(strings.Fields(v), " ")
	}
	switch {
	case isNumericType(base):
		if f, err := strconv.ParseFloat(v, 64); err == nil {
			return "n:" + strconv.FormatFloat(f, 'g', -1, 64)
		}
	case base == "boolean":
		switch v {
		case "1":
			v = "true"
		case "0":
			v = "false"
		}
	}
	return "s:" + v
}

// parseIdentityConstraint reads an xs:key, xs:keyref, or xs:unique. The XPaths
// are kept as written and compiled during resolution, where the schema's own
// prefix declarations are available.
func parseIdentityConstraint(el *Element) (*IdentityConstraint, error) {
	ic := &IdentityConstraint{Kind: el.Local}
	ic.Name, _ = el.Attr("name")
	if ic.Name == "" {
		return nil, fmt.Errorf("xs:%s requires a name attribute", el.Local)
	}
	if ic.Kind == "keyref" {
		ic.Refer, _ = el.Attr("refer")
		if ic.Refer == "" {
			return nil, fmt.Errorf("xs:keyref %q requires a refer attribute", ic.Name)
		}
	}

	for _, child := range el.ChildElements() {
		if child.Namespace != xsdNS {
			continue
		}
		switch child.Local {
		case "selector":
			xpath, ok := child.Attr("xpath")
			if !ok {
				return nil, fmt.Errorf("xs:selector in %q requires an xpath attribute", ic.Name)
			}
			ic.selectorXPath = xpath
		case "field":
			xpath, ok := child.Attr("xpath")
			if !ok {
				return nil, fmt.Errorf("xs:field in %q requires an xpath attribute", ic.Name)
			}
			ic.fieldXPaths = append(ic.fieldXPaths, xpath)
		case "annotation":
			// skip
		default:
			return nil, fmt.Errorf("unsupported schema element xs:%s inside xs:%s %q", child.Local, ic.Kind, ic.Name)
		}
	}

	if ic.selectorXPath == "" {
		return nil, fmt.Errorf("xs:%s %q requires an xs:selector", ic.Kind, ic.Name)
	}
	if len(ic.fieldXPaths) == 0 {
		return nil, fmt.Errorf("xs:%s %q requires at least one xs:field", ic.Kind, ic.Name)
	}
	return ic, nil
}

// parseInlineSimpleType returns the xs:simpleType written inside an xs:list or
