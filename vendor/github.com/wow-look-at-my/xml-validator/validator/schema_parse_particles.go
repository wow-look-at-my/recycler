package validator

import "fmt"

// A particle is one entry in a content model: an element declaration, a nested
// model, a group reference, or a wildcard. Every content model parses through
// parseParticles, so a new particle kind is added in one place.

func parseSequence(el *Element) (*Sequence, error) {
	seq := &Sequence{MinOccurs: 1, MaxOccurs: 1}
	parseOccurs(el, &seq.MinOccurs, &seq.MaxOccurs)
	items, err := parseParticles(el)
	if err != nil {
		return nil, err
	}
	seq.Items = items
	return seq, nil
}

func parseChoice(el *Element) (*Choice, error) {
	ch := &Choice{MinOccurs: 1, MaxOccurs: 1}
	parseOccurs(el, &ch.MinOccurs, &ch.MaxOccurs)
	items, err := parseParticles(el)
	if err != nil {
		return nil, err
	}
	ch.Items = items
	return ch, nil
}

func parseAll(el *Element) (*All, error) {
	a := &All{MinOccurs: 1, MaxOccurs: 1}
	parseOccurs(el, &a.MinOccurs, &a.MaxOccurs)
	items, err := parseParticles(el)
	if err != nil {
		return nil, err
	}
	// Order-free matching is defined over individual children, so an all-group
	// holds element declarations and wildcards. A nested compositor -- a group
	// reference expands to one -- would be matched by no branch at all, which
	// reads as "this content is allowed".
	for _, item := range items {
		switch item.(type) {
		case *ElementDecl, *AnyParticle:
		default:
			return nil, fmt.Errorf("xs:all may only contain element declarations and xs:any wildcards")
		}
	}
	a.Items = items
	return a, nil
}

func parseGroupRef(el *Element) (*GroupRef, error) {
	ref, ok := el.Attr("ref")
	if !ok {
		return nil, fmt.Errorf("xs:group inside a content model requires a ref attribute")
	}
	gr := &GroupRef{Ref: ref, MinOccurs: 1, MaxOccurs: 1}
	parseOccurs(el, &gr.MinOccurs, &gr.MaxOccurs)
	return gr, nil
}

func parseParticles(el *Element) ([]Particle, error) {
	var items []Particle
	for _, child := range el.ChildElements() {
		if child.Namespace != xsdNS {
			continue
		}
		switch child.Local {
		case "element":
			ed, err := parseElementDecl(child)
			if err != nil {
				return nil, err
			}
			items = append(items, ed)
		case "sequence":
			s, err := parseSequence(child)
			if err != nil {
				return nil, err
			}
			items = append(items, s)
		case "choice":
			c, err := parseChoice(child)
			if err != nil {
				return nil, err
			}
			items = append(items, c)
		case "all":
			// An all-group matches its members in any order, which only has a
			// meaning when it covers a whole element. Nested, it would have to
			// share the child list with its siblings positionally, so XSD
			// forbids it -- and matching quietly ignored it, which is worse.
			return nil, fmt.Errorf("xs:all must be the entire content model of a complex type, not a particle inside xs:%s", el.Local)
		case "group":
			gr, err := parseGroupRef(child)
			if err != nil {
				return nil, err
			}
			items = append(items, gr)
		case "any":
			ap := &AnyParticle{MinOccurs: 1, MaxOccurs: 1}
			parseOccurs(child, &ap.MinOccurs, &ap.MaxOccurs)
			ap.Namespace, _ = child.Attr("namespace")
			pc, _ := child.Attr("processContents")
			if pc == "" {
				pc = "strict"
			}
			if err := validateProcessContents(pc); err != nil {
				return nil, fmt.Errorf("xs:any: %w", err)
			}
			items = append(items, ap)
		case "annotation":
			// skip
		}
	}
	return items, nil
}
