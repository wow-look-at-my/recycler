package validator

import (
	"fmt"

	"github.com/wow-look-at-my/go-containers/set"
)

// A content model is written once and used from several places: a named group
// is referenced by many types, and a derived type states only its own half of
// a declaration. Resolution flattens both into the model the validator walks,
// so matching never has to know how a declaration was assembled.
//
// Both are COPIED rather than shared. A reference states its own occurrence
// counts, and resolution fills in element refs in place, so two uses of one
// group would otherwise overwrite each other's counts.

// expandGroupRefs replaces every GroupRef in a content model with a copy of the
// group it names. stack carries the groups currently being expanded: a group
// that reaches itself would otherwise describe an infinitely deep document and
// loop here forever.
func expandGroupRefs(cm ContentModel, s *Schema, stack set.Set[string]) error {
	items := contentItems(cm)
	for i, item := range items {
		switch p := item.(type) {
		case *GroupRef:
			expanded, err := expandGroupRef(p, s, stack)
			if err != nil {
				return err
			}
			items[i] = expanded
		case *Sequence:
			if err := expandGroupRefs(p, s, stack); err != nil {
				return err
			}
		case *Choice:
			if err := expandGroupRefs(p, s, stack); err != nil {
				return err
			}
		case *All:
			if err := expandGroupRefs(p, s, stack); err != nil {
				return err
			}
		}
	}
	return nil
}

func expandGroupRef(gr *GroupRef, s *Schema, stack set.Set[string]) (Particle, error) {
	name := stripPrefix(gr.Ref)
	g, ok := s.Groups[name]
	if !ok {
		return nil, fmt.Errorf("group ref %q does not name a global group declaration", gr.Ref)
	}
	if stack.Contains(name) {
		return nil, fmt.Errorf("group %q refers to itself", name)
	}
	if g.Content == nil {
		return nil, fmt.Errorf("group %q declares no content model", name)
	}
	stack.Add(name)
	defer stack.Remove(name)

	clone := cloneContentModel(g.Content)
	setOccurs(clone, gr.MinOccurs, gr.MaxOccurs)
	if err := expandGroupRefs(clone, s, stack); err != nil {
		return nil, err
	}
	p, ok := clone.(Particle)
	if !ok {
		return nil, fmt.Errorf("group %q has an unusable content model", name)
	}
	return p, nil
}

// resolveDerivation folds a complexContent base into the type deriving from it.
// An extension's own content follows the base's, which is the order an instance
// document has to be in; a restriction states its content in full, so only the
// base's attributes carry over, and only where the restriction did not restate
// or prohibit them.
func resolveDerivation(ct *ComplexType, s *Schema, seen resolving) error {
	if ct.baseName == "" {
		return nil
	}
	name := stripPrefix(ct.baseName)
	baseName, derivation := ct.baseName, ct.derivation
	ct.baseName, ct.derivation = "", ""

	t, ok := s.Types[name]
	if !ok {
		// A builtin base carries no content model or attributes to inherit;
		// simpleContent handles the text-typed case separately.
		if resolveBuiltinType(name) != nil {
			return nil
		}
		return fmt.Errorf("complexContent %s base %q does not name a known type", derivation, baseName)
	}
	ct.baseType = t
	base, ok := t.(*ComplexType)
	if !ok {
		return nil
	}
	if err := resolveComplexTypeRefs(base, s, seen); err != nil {
		return err
	}

	if derivation == "extension" {
		merged, err := concatContent(cloneContentModel(base.Content), ct.Content)
		if err != nil {
			return fmt.Errorf("complexContent extension of %q: %w", name, err)
		}
		ct.Content = merged
		ct.Mixed = ct.Mixed || base.Mixed
		if ct.SimpleText == nil {
			ct.SimpleText = base.SimpleText
		}
	}
	ct.Attributes = inheritAttrs(base.Attributes, ct.Attributes)
	if ct.AnyAttribute == nil {
		ct.AnyAttribute = base.AnyAttribute
	}
	return nil
}

// inheritAttrs adds the base's attributes to the derived type's, keeping the
// derived declaration wherever both name the same attribute.
func inheritAttrs(base, own []*AttrDecl) []*AttrDecl {
	if len(base) == 0 {
		return own
	}
	declared := set.New[attrKey](len(own))
	for _, ad := range own {
		declared.Add(attrKey{ad.Namespace, ad.Name})
	}
	out := make([]*AttrDecl, 0, len(base)+len(own))
	for _, ad := range base {
		if !declared.Contains(attrKey{ad.Namespace, ad.Name}) {
			out = append(out, ad)
		}
	}
	return append(out, own...)
}

// concatContent puts the base's model before the extension's own.
func concatContent(base, own ContentModel) (ContentModel, error) {
	switch {
	case base == nil:
		return own, nil
	case own == nil:
		return base, nil
	}
	if isAll(base) || isAll(own) {
		return nil, fmt.Errorf("an xs:all content model cannot be combined with another, since order-free matching only has a meaning over a whole element")
	}
	basePart, baseOK := base.(Particle)
	ownPart, ownOK := own.(Particle)
	if !baseOK || !ownOK {
		return own, nil
	}
	return &Sequence{Items: []Particle{basePart, ownPart}, MinOccurs: 1, MaxOccurs: 1}, nil
}

func isAll(cm ContentModel) bool {
	_, ok := cm.(*All)
	return ok
}

func contentItems(cm ContentModel) []Particle {
	switch c := cm.(type) {
	case *Sequence:
		return c.Items
	case *Choice:
		return c.Items
	case *All:
		return c.Items
	}
	return nil
}

func setOccurs(cm ContentModel, minOccurs, maxOccurs int) {
	switch c := cm.(type) {
	case *Sequence:
		c.MinOccurs, c.MaxOccurs = minOccurs, maxOccurs
	case *Choice:
		c.MinOccurs, c.MaxOccurs = minOccurs, maxOccurs
	case *All:
		c.MinOccurs, c.MaxOccurs = minOccurs, maxOccurs
	}
}

func cloneContentModel(cm ContentModel) ContentModel {
	switch c := cm.(type) {
	case *Sequence:
		return &Sequence{Items: cloneParticles(c.Items), MinOccurs: c.MinOccurs, MaxOccurs: c.MaxOccurs}
	case *Choice:
		return &Choice{Items: cloneParticles(c.Items), MinOccurs: c.MinOccurs, MaxOccurs: c.MaxOccurs}
	case *All:
		return &All{Items: cloneParticles(c.Items), MinOccurs: c.MinOccurs, MaxOccurs: c.MaxOccurs}
	}
	return nil
}

func cloneParticles(items []Particle) []Particle {
	if items == nil {
		return nil
	}
	out := make([]Particle, 0, len(items))
	for _, item := range items {
		out = append(out, cloneParticle(item))
	}
	return out
}

func cloneParticle(p Particle) Particle {
	switch c := p.(type) {
	case *ElementDecl:
		ed := *c
		return &ed
	case *AnyParticle:
		ap := *c
		return &ap
	case *GroupRef:
		gr := *c
		return &gr
	case *Sequence, *Choice, *All:
		cloned, _ := cloneContentModel(c.(ContentModel)).(Particle)
		return cloned
	}
	return p
}
