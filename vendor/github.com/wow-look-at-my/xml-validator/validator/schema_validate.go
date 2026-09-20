package validator

import (
	"fmt"
	"strings"

	"github.com/wow-look-at-my/go-containers/set"
)

type schemaValidator struct {
	schema *Schema
	errors []error
	// elemDecls and attrDecls record which declaration matched each instance
	// node. The identity pass needs the declared type of a field to compare
	// values in the right value space.
	elemDecls map[*Element]*ElementDecl
	attrDecls map[*Element]map[attrKey]*AttrDecl
}

func ValidateSchema(doc *Document, schema *Schema) error {
	sv := &schemaValidator{
		schema:    schema,
		elemDecls: map[*Element]*ElementDecl{},
		attrDecls: map[*Element]map[attrKey]*AttrDecl{},
	}
	sv.validateRoot(doc.Root)
	// Identity constraints run only over a document that already matches the
	// schema. On a document with structural errors the fields point at nodes
	// that never matched a declaration, so every constraint would report noise
	// on top of the real failure.
	if len(sv.errors) == 0 {
		sv.checkIdentity(doc.Root, nil)
	}
	if len(sv.errors) > 0 {
		return sv.errors[0]
	}
	return nil
}

func (sv *schemaValidator) addError(el *Element, format string, args ...any) {
	sv.addErrorAt(el.Line, el.Col, format, args...)
}

func (sv *schemaValidator) addErrorAt(line, col int, format string, args ...any) {
	msg := fmt.Sprintf(format, args...)
	sv.errors = append(sv.errors, &Error{Line: line, Col: col, Message: msg})
}

// validateRoot matches the document's root against a global declaration, by namespace AND local name, the same as every
// other element. A root in another namespace is a different element, and the local name it shares with a declared one is
// a coincidence. Falling back to that local name here accepted a document written against a different vocabulary, which
// is the one place a schema most has to say no.
func (sv *schemaValidator) validateRoot(el *Element) {
	rootName := el.Local
	decl, ok := sv.schema.Elements[qnameKey(el.Namespace, rootName)]
	if !ok {
		if other := findByLocal(sv.schema.Elements, rootName); other != nil {
			sv.addError(el, "root element %q is in namespace %q, but the schema declares it in %q",
				rootName, el.Namespace, other.Namespace)
			return
		}
		sv.addError(el, "element %q is not declared as a global element in the schema", rootName)
		return
	}
	if err := resolveElementType(decl, sv.schema, set.New[*ComplexType]()); err != nil {
		sv.addError(el, "schema: %v", err)
		return
	}
	if decl.Abstract {
		sv.addError(el, "element %q is abstract: only an element that substitutes for it may appear here", rootName)
		return
	}
	sv.validateElement(el, decl)
}

func (sv *schemaValidator) validateElement(el *Element, decl *ElementDecl) {
	sv.elemDecls[el] = decl
	if decl.Fixed != "" && el.TextContent() != decl.Fixed {
		sv.addError(el, "element %q has fixed value %q but got %q", el.Local, decl.Fixed, el.TextContent())
		return
	}

	// An xs:alternative can hand this instance a different type than the
	// declared one, decided by its own attributes.
	typ := chooseType(el, decl)
	if typ == nil {
		return
	}

	switch t := typ.(type) {
	case *BuiltinType, *SimpleType:
		sv.validateSimpleElement(el, t)
	case *ComplexType:
		sv.validateComplexElement(el, t)
	}
}

func (sv *schemaValidator) validateSimpleElement(el *Element, t Type) {
	children := el.ChildElements()
	if len(children) > 0 {
		sv.addError(el, "element %q has simple type %q but contains child elements", el.Local, simpleTypeLabel(t))
		return
	}
	if err := validateSimpleValue(el.TextContent(), t); err != nil {
		sv.addError(el, "element %q: %v", el.Local, err)
	}
}

func (sv *schemaValidator) validateComplexElement(el *Element, ct *ComplexType) {
	sv.validateAttributes(el, ct.Attributes, ct.AnyAttribute)

	if ct.SimpleText != nil {
		sv.validateSimpleText(el, ct.SimpleText)
		return
	}

	children := el.ChildElements()

	if ct.Content == nil {
		if len(children) > 0 {
			sv.addError(el, "element %q expects no child elements but got %d", el.Local, len(children))
		}
		if !ct.Mixed {
			text := strings.TrimSpace(el.TextContent())
			if text != "" {
				sv.addError(el, "element %q expects empty content but has text", el.Local)
			}
		}
		return
	}

	if !ct.Mixed {
		text := strings.TrimSpace(el.TextContent())
		if text != "" && len(children) > 0 {
			// mixed text and elements without mixed="true"
		}
	}

	sv.validateContentModel(el, children, ct.Content)
}

func (sv *schemaValidator) validateSimpleText(el *Element, textType Type) {
	children := el.ChildElements()
	if len(children) > 0 {
		sv.addError(el, "element %q has simpleContent but contains child elements", el.Local)
		return
	}
	if err := validateSimpleValue(el.TextContent(), textType); err != nil {
		sv.addError(el, "element %q: %v", el.Local, err)
	}
}

func (sv *schemaValidator) validateAttributes(el *Element, decls []*AttrDecl, anyAttr *AnyAttrDecl) {
	declared := make(map[attrKey]*AttrDecl)
	for _, ad := range decls {
		if ad.Use == "prohibited" {
			continue
		}
		declared[attrKey{ns: ad.Namespace, local: ad.Name}] = ad
	}

	for _, attr := range el.Attrs {
		if attr.Prefix == "xmlns" || attr.Name == "xmlns" {
			continue
		}
		if strings.HasPrefix(attr.Name, "xml:") {
			continue
		}
		if attr.Namespace == xsiNS {
			continue
		}
		key := attrKey{ns: attr.Namespace, local: attr.Local}
		ad, ok := declared[key]
		if !ok {
			if anyAttr != nil && sv.wildcardMatchesNS(anyAttr.Namespace, attr.Namespace) {
				continue
			}
			sv.addErrorAt(attr.Line, attr.Col, "unexpected attribute %q on element %q", qualifiedName(attr.Namespace, attr.Local), el.Local)
			continue
		}
		if ad.Fixed != "" && attr.Value != ad.Fixed {
			sv.addErrorAt(attr.Line, attr.Col, "attribute %q on element %q must have fixed value %q", attr.Local, el.Local, ad.Fixed)
			continue
		}
		if sv.attrDecls[el] == nil {
			sv.attrDecls[el] = map[attrKey]*AttrDecl{}
		}
		sv.attrDecls[el][key] = ad
		sv.validateAttrValue(el, attr, ad)
		delete(declared, key)
	}

	for key, ad := range declared {
		if ad.Use == "required" {
			sv.addError(el, "required attribute %q is missing on element %q", qualifiedName(key.ns, key.local), el.Local)
		}
	}
}

// attrKey identifies an attribute declaration the way an instance document
// does: by namespace and local name together. Local declarations carry an empty
// namespace, so an unqualified attribute never matches a global one that a
// dialect declared under its own namespace.
type attrKey struct {
	ns    string
	local string
}

// qualifiedName renders an attribute for an error message, showing the
// namespace when there is one -- "top-k" alone does not say which vocabulary it
// came from when several are in play.
func qualifiedName(ns, local string) string {
	if ns == "" {
		return local
	}
	return "{" + ns + "}" + local
}

// validateAttrValue checks an attribute against its declared type through the
// same path element text takes, and reports a violation at the attribute's own
// position rather than the element's.
func (sv *schemaValidator) validateAttrValue(el *Element, attr Attr, ad *AttrDecl) {
	if ad.Type == nil {
		return
	}
	if err := validateSimpleValue(attr.Value, ad.Type); err != nil {
		sv.addErrorAt(attr.Line, attr.Col, "attribute %q on element %q: %v", attr.Local, el.Local, err)
	}
}

func (sv *schemaValidator) validateContentModel(el *Element, children []*Element, cm ContentModel) {
	switch c := cm.(type) {
	case *Sequence:
		sv.validateSequence(el, children, c)
	case *Choice:
		sv.validateChoice(el, children, c)
	case *All:
		sv.validateAll(el, children, c)
	}
}

func (sv *schemaValidator) validateSequence(el *Element, children []*Element, seq *Sequence) {
	pos := 0
	for _, item := range seq.Items {
		consumed, err := sv.matchParticle(el, children[pos:], item)
		if err != nil {
			sv.errors = append(sv.errors, err)
			return
		}
		pos += consumed
	}
	if pos < len(children) {
		sv.addError(children[pos], "unexpected element %q in element %q", children[pos].Local, el.Local)
	}
}

func (sv *schemaValidator) validateChoice(el *Element, children []*Element, ch *Choice) {
	if len(children) == 0 {
		if ch.MinOccurs > 0 {
			var names []string
			for _, item := range ch.Items {
				if ed, ok := item.(*ElementDecl); ok {
					names = append(names, ed.Name)
				}
			}
			sv.addError(el, "element %q requires one of: %s", el.Local, strings.Join(names, ", "))
		}
		return
	}

	pos := 0
	iterations := 0
	for pos < len(children) {
		matched := false
		for _, item := range ch.Items {
			consumed, err := sv.matchParticle(el, children[pos:], item)
			if err == nil && consumed > 0 {
				pos += consumed
				matched = true
				iterations++
				break
			}
		}
		if !matched {
			if iterations < ch.MinOccurs {
				var names []string
				for _, item := range ch.Items {
					if ed, ok := item.(*ElementDecl); ok {
						names = append(names, ed.Name)
					}
				}
				sv.addError(children[pos], "element %q is not allowed here; expected one of: %s", children[pos].Local, strings.Join(names, ", "))
			} else if ch.MaxOccurs >= 0 && iterations >= ch.MaxOccurs {
				sv.addError(children[pos], "unexpected element %q in element %q (choice exceeded maxOccurs)", children[pos].Local, el.Local)
			} else {
				sv.addError(children[pos], "element %q is not allowed here", children[pos].Local)
			}
			return
		}
		if ch.MaxOccurs >= 0 && iterations > ch.MaxOccurs {
			sv.addError(children[pos-1], "choice in element %q exceeded maxOccurs %d", el.Local, ch.MaxOccurs)
			return
		}
	}
}

func (sv *schemaValidator) validateAll(el *Element, children []*Element, all *All) {
	seen := make(map[string]int)
	declMap := make(map[string]*ElementDecl)
	var anyParticles []*AnyParticle
	for _, item := range all.Items {
		if ed, ok := item.(*ElementDecl); ok {
			declMap[ed.Name] = ed
		}
		if ap, ok := item.(*AnyParticle); ok {
			anyParticles = append(anyParticles, ap)
		}
	}

	anyCount := make(map[*AnyParticle]int)

	for _, child := range children {
		slot, decl := sv.allSlotFor(child, all.Items, declMap)
		if slot == nil {
			matched := false
			for _, ap := range anyParticles {
				if sv.anyMatchesElement(ap, child) {
					anyCount[ap]++
					maxOccurs := ap.MaxOccurs
					if maxOccurs < 0 {
						maxOccurs = len(children)
					}
					if anyCount[ap] > maxOccurs {
						sv.addError(child, "xs:any wildcard in element %q exceeded maxOccurs %d", el.Local, maxOccurs)
					}
					sv.processWildcardElement(ap, child)
					matched = true
					break
				}
			}
			if !matched {
				sv.addError(child, "unexpected element %q in all group of %q", child.Local, el.Local)
			}
			continue
		}
		if decl.Abstract {
			sv.addError(child, "element %q is abstract: only an element that substitutes for it may appear here", child.Local)
			continue
		}
		// A substitute fills the slot of the element it stands in for, so the
		// occurrence counts are the particle's, not the member's.
		seen[slot.Name]++
		maxOccurs := slot.MaxOccurs
		if maxOccurs < 0 {
			maxOccurs = len(children)
		}
		if seen[slot.Name] > maxOccurs {
			sv.addError(child, "element %q appears too many times (max %d)", child.Local, maxOccurs)
			continue
		}
		sv.validateElement(child, decl)
	}

	for _, item := range all.Items {
		if ed, ok := item.(*ElementDecl); ok {
			if seen[ed.Name] < ed.MinOccurs {
				sv.addError(el, "element %q requires at least %d occurrence(s) of %q, got %d",
					el.Local, ed.MinOccurs, ed.Name, seen[ed.Name])
			}
		}
	}
	for _, ap := range anyParticles {
		if anyCount[ap] < ap.MinOccurs {
			sv.addError(el, "xs:any wildcard in element %q requires at least %d element(s), got %d",
				el.Local, ap.MinOccurs, anyCount[ap])
		}
	}
}

func (sv *schemaValidator) matchParticle(parent *Element, children []*Element, p Particle) (int, error) {
	switch item := p.(type) {
	case *ElementDecl:
		return sv.matchElement(parent, children, item)
	case *Sequence:
		return sv.matchSequence(parent, children, item)
	case *Choice:
		return sv.matchChoice(parent, children, item)
	case *AnyParticle:
		return sv.matchAny(children, item)
	}
	return 0, nil
}

func (sv *schemaValidator) matchElement(parent *Element, children []*Element, decl *ElementDecl) (int, error) {
	count := 0
	maxOccurs := decl.MaxOccurs
	if maxOccurs < 0 {
		maxOccurs = len(children) + 1
	}

	for count < len(children) && count < maxOccurs {
		child := children[count]
		match := sv.substituteFor(child, decl)
		if match == nil {
			break
		}
		if match.Abstract {
			sv.addError(child, "element %q is abstract: only an element that substitutes for it may appear here", child.Local)
			return count + 1, nil
		}
		sv.validateElement(child, match)
		count++
	}

	if count < decl.MinOccurs {
		return count, &Error{
			Line:    parent.Line,
			Col:     parent.Col,
			Message: fmt.Sprintf("element %q requires at least %d occurrence(s) of %q, got %d", parent.Local, decl.MinOccurs, decl.Name, count),
		}
	}
	return count, nil
}

func (sv *schemaValidator) matchSequence(parent *Element, children []*Element, seq *Sequence) (int, error) {
	total := 0
	iterations := 0
	maxOccurs := seq.MaxOccurs
	if maxOccurs < 0 {
		maxOccurs = len(children) + 1
	}

	for iterations < maxOccurs {
		pos := 0
		remaining := children[total:]
		matched := true
		for _, item := range seq.Items {
			consumed, err := sv.matchParticle(parent, remaining[pos:], item)
			if err != nil {
				if iterations < seq.MinOccurs {
					return total, err
				}
				matched = false
				break
			}
			pos += consumed
		}
		if !matched || pos == 0 {
			break
		}
		total += pos
		iterations++
	}

	if iterations < seq.MinOccurs {
		return total, &Error{
			Line:    parent.Line,
			Col:     parent.Col,
			Message: fmt.Sprintf("sequence in element %q requires at least %d iteration(s), got %d", parent.Local, seq.MinOccurs, iterations),
		}
	}
	return total, nil
}

func (sv *schemaValidator) matchChoice(parent *Element, children []*Element, ch *Choice) (int, error) {
	total := 0
	iterations := 0
	maxOccurs := ch.MaxOccurs
	if maxOccurs < 0 {
		maxOccurs = len(children) + 1
	}

	for iterations < maxOccurs && total < len(children) {
		best := 0
		for _, item := range ch.Items {
			consumed, err := sv.matchParticle(parent, children[total:], item)
			if err == nil && consumed > best {
				best = consumed
			}
		}
		if best == 0 {
			break
		}
		total += best
		iterations++
	}

	if iterations < ch.MinOccurs {
		return total, &Error{
			Line:    parent.Line,
			Col:     parent.Col,
			Message: fmt.Sprintf("choice in element %q requires at least %d match(es), got %d", parent.Local, ch.MinOccurs, iterations),
		}
	}
	return total, nil
}

func (sv *schemaValidator) matchAny(children []*Element, ap *AnyParticle) (int, error) {
	count := 0
	maxOccurs := ap.MaxOccurs
	if maxOccurs < 0 {
		maxOccurs = len(children) + 1
	}
	for count < len(children) && count < maxOccurs {
		if !sv.anyMatchesElement(ap, children[count]) {
			break
		}
		sv.processWildcardElement(ap, children[count])
		count++
	}
	if count < ap.MinOccurs {
		return count, fmt.Errorf("xs:any requires at least %d element(s), got %d", ap.MinOccurs, count)
	}
	return count, nil
}

func (sv *schemaValidator) anyMatchesElement(ap *AnyParticle, el *Element) bool {
	return sv.wildcardMatchesNS(ap.Namespace, el.Namespace)
}

// processWildcardElement validates an element matched by an xs:any wildcard
// against its global declaration. The matching element MUST have a global
// declaration the validator can find -- the schema parser rejects every
// processContents value other than "strict", so there is no "silently
// accept" branch.
func (sv *schemaValidator) processWildcardElement(_ *AnyParticle, el *Element) {
	decl := sv.lookupGlobalElement(el.Local, el.Namespace)
	if decl == nil {
		sv.addError(el, "xs:any wildcard: no declaration found for element {%s}%s", el.Namespace, el.Local)
		return
	}
	sv.validateElement(el, decl)
}

// lookupGlobalElement returns the global element declaration matching the
// given local name and namespace, or nil if none is found. The schema's
// Elements map is keyed by local name only (xs:import / xs:include flatten
// names across namespaces); we use the Namespace field recorded at parse time
// to disambiguate.
func (sv *schemaValidator) lookupGlobalElement(local, ns string) *ElementDecl {
	if decl, ok := sv.schema.Elements[qnameKey(ns, local)]; ok {
		return decl
	}
	return nil
}

func (sv *schemaValidator) wildcardMatchesNS(constraint, ns string) bool {
	if constraint == "" || constraint == "##any" {
		return true
	}
	if constraint == "##local" {
		return ns == ""
	}
	if constraint == "##other" {
		return ns != "" && ns != sv.schema.TargetNamespace
	}
	if constraint == "##targetNamespace" {
		return ns == sv.schema.TargetNamespace
	}
	for _, allowed := range strings.Fields(constraint) {
		if allowed == "##targetNamespace" {
			if ns == sv.schema.TargetNamespace {
				return true
			}
		} else if allowed == "##local" {
			if ns == "" {
				return true
			}
		} else if allowed == ns {
			return true
		}
	}
	return false
}

func resolveSimpleTypeBaseName(st *SimpleType) string {
	if st.BaseType != nil {
		if bt, ok := st.BaseType.(*BuiltinType); ok {
			return bt.name
		}
		if inner, ok := st.BaseType.(*SimpleType); ok {
			return resolveSimpleTypeBaseName(inner)
		}
	}
	if st.Base != "" {
		local := stripPrefix(st.Base)
		if resolveBuiltinType(local) != nil {
			return local
		}
	}
	return "string"
}
