package validator

import (
	"fmt"

	"github.com/wow-look-at-my/go-containers/set"
)

// resolving tracks the complex types already visited in one resolution pass.
// A recursive schema -- an element whose content refers back to itself, which
// is how a schema describes an arbitrarily nested tree -- would otherwise walk
// the same type forever. Resolution is idempotent per type, so visiting each
// pointer once is both a termination guarantee and the correct result.
type resolving = set.Set[*ComplexType]

func resolveSchemaRefs(s *Schema) error {
	seen := set.New[*ComplexType]()
	for _, ed := range s.Elements {
		if err := resolveElementType(ed, s, seen); err != nil {
			return err
		}
	}
	for _, t := range s.Types {
		switch typ := t.(type) {
		case *ComplexType:
			if err := resolveComplexTypeRefs(typ, s, seen); err != nil {
				return err
			}
		case *SimpleType:
			if err := resolveSimpleTypeRefs(typ, s, set.New[*SimpleType]()); err != nil {
				return err
			}
		}
	}
	if err := resolveSubstitutionGroups(s); err != nil {
		return err
	}
	return resolveIdentityRefs(s)
}

// compileElementConstraints compiles the identity-constraint XPaths on one
// element declaration and registers its keys on the schema. A ref particle
// shares these pointers with the declaration it names, so the work runs once.
func compileElementConstraints(ed *ElementDecl, s *Schema) error {
	if ed.compiled || len(ed.Constraints) == 0 {
		return nil
	}
	ed.compiled = true
	for _, c := range ed.Constraints {
		sel, err := compileIDPaths(c.selectorXPath, s, false)
		if err != nil {
			return fmt.Errorf("xs:%s %q selector: %w", c.Kind, c.Name, err)
		}
		c.selector = sel
		c.fields = nil
		for _, fx := range c.fieldXPaths {
			f, err := compileIDPaths(fx, s, true)
			if err != nil {
				return fmt.Errorf("xs:%s %q field: %w", c.Kind, c.Name, err)
			}
			c.fields = append(c.fields, f)
		}
		if err := registerIdentityConstraint(c, s); err != nil {
			return err
		}
	}
	return nil
}

func registerIdentityConstraint(c *IdentityConstraint, s *Schema) error {
	name := qnameKey(s.TargetNamespace, c.Name)
	if c.Kind == "keyref" {
		ns, local := s.resolveQName(c.Refer)
		c.referKey = qnameKey(ns, local)
		s.identityRefs = append(s.identityRefs, c)
		return nil
	}
	if s.identity == nil {
		s.identity = map[string]*IdentityConstraint{}
	}
	if prev, ok := s.identity[name]; ok && prev != c {
		return fmt.Errorf("identity constraint %q is declared twice; a constraint name is schema-wide", c.Name)
	}
	s.identity[name] = c
	return nil
}

// resolveIdentityRefs checks that every xs:keyref names a key that exists. A
// keyref pointing at nothing used to be a rule that could never fail.
func resolveIdentityRefs(s *Schema) error {
	for _, c := range s.identityRefs {
		if _, ok := s.identity[c.referKey]; ok {
			continue
		}
		if key, ok := lookupIdentityKey(s, stripPrefix(c.Refer)); ok {
			c.referKey = key
			continue
		}
		return fmt.Errorf("xs:keyref %q refers to %q, which names no xs:key or xs:unique in the schema", c.Name, c.Refer)
	}
	return nil
}

func resolveElementType(ed *ElementDecl, s *Schema, seen resolving) error {
	if err := compileElementConstraints(ed, s); err != nil {
		return err
	}
	if err := resolveAlternatives(ed, s); err != nil {
		return err
	}
	if ed.Type != nil {
		if ct, ok := ed.Type.(*ComplexType); ok {
			return resolveComplexTypeRefs(ct, s, seen)
		}
		if st, ok := ed.Type.(*SimpleType); ok {
			return resolveSimpleTypeRefs(st, s, set.New[*SimpleType]())
		}
		return nil
	}
	if ed.TypeName != "" {
		local := stripPrefix(ed.TypeName)
		if t, ok := s.Types[local]; ok {
			ed.Type = t
		} else if bt := resolveBuiltinType(local); bt != nil {
			ed.Type = bt
		}
	}
	return nil
}

func resolveComplexTypeRefs(ct *ComplexType, s *Schema, seen resolving) error {
	if seen.Contains(ct) {
		return nil
	}
	seen.Add(ct)
	if err := resolveDerivation(ct, s, seen); err != nil {
		return err
	}
	if ct.Content != nil {
		if err := expandGroupRefs(ct.Content, s, set.New[string]()); err != nil {
			return err
		}
	}
	for _, ref := range ct.attrGroupRefs {
		if ag, ok := s.AttrGroups[ref]; ok {
			ct.Attributes = append(ct.Attributes, ag.Attributes...)
			if ag.AnyAttribute != nil && ct.AnyAttribute == nil {
				ct.AnyAttribute = ag.AnyAttribute
			}
		}
	}
	ct.attrGroupRefs = nil
	if ct.Content != nil {
		if err := resolveContentModel(ct.Content, s, seen); err != nil {
			return err
		}
	}
	for _, ad := range ct.Attributes {
		if err := resolveAttrRef(ad, s); err != nil {
			return err
		}
		if err := resolveAttrType(ad, s); err != nil {
			return err
		}
	}
	if ct.SimpleText != nil {
		if st, ok := ct.SimpleText.(*SimpleType); ok {
			if err := resolveSimpleTypeRefs(st, s, set.New[*SimpleType]()); err != nil {
				return err
			}
		}
	}
	return nil
}

func resolveContentModel(cm ContentModel, s *Schema, seen resolving) error {
	var items []Particle
	switch c := cm.(type) {
	case *Sequence:
		items = c.Items
	case *Choice:
		items = c.Items
	case *All:
		items = c.Items
	}
	for _, item := range items {
		var err error
		switch p := item.(type) {
		case *ElementDecl:
			if p.Ref != "" {
				ref := s.lookupElement(p.Ref)
				if ref == nil {
					return fmt.Errorf("element ref %q does not name a global element declaration", p.Ref)
				}
				p.Name = ref.Name
				p.Namespace = ref.Namespace
				p.TypeName = ref.TypeName
				p.Type = ref.Type
				p.Constraints = ref.Constraints
				p.Abstract = ref.Abstract
				p.Alternatives = ref.Alternatives
			}
			err = resolveElementType(p, s, seen)
		case *Sequence:
			err = resolveContentModel(p, s, seen)
		case *Choice:
			err = resolveContentModel(p, s, seen)
		case *All:
			err = resolveContentModel(p, s, seen)
		}
		if err != nil {
			return err
		}
	}
	return nil
}

// resolveAttrRef fills a use="..." reference from the global attribute it names.
// The reference contributes only whether the attribute is required: the name,
// namespace, and type are the global declaration's, which is what makes a
// qualified attribute in an instance document resolvable at all.
func resolveAttrRef(ad *AttrDecl, s *Schema) error {
	if ad.Ref == "" {
		return nil
	}
	g := s.lookupAttribute(ad.Ref)
	if g == nil {
		return fmt.Errorf("attribute ref %q does not name a global attribute declaration", ad.Ref)
	}
	ad.Name = g.Name
	ad.Namespace = g.Namespace
	if ad.TypeName == "" {
		ad.TypeName = g.TypeName
	}
	if ad.Type == nil {
		ad.Type = g.Type
	}
	if ad.Fixed == "" {
		ad.Fixed = g.Fixed
	}
	return nil
}

func resolveAttrType(ad *AttrDecl, s *Schema) error {
	if ad.Type == nil && ad.TypeName != "" {
		local := stripPrefix(ad.TypeName)
		if t, ok := s.Types[local]; ok {
			ad.Type = t
		} else if bt := resolveBuiltinType(local); bt != nil {
			ad.Type = bt
		}
	}
	if st, ok := ad.Type.(*SimpleType); ok {
		return resolveSimpleTypeRefs(st, s, set.New[*SimpleType]())
	}
	return nil
}

// resolveSimpleTypeRefs links a simple type to its base type, its list item
// type, and its union members. Validation walks those links to apply the facets
// a type inherits as well as the ones it states, so a derivation cycle would
// recurse forever and fails here instead.
func resolveSimpleTypeRefs(st *SimpleType, s *Schema, path set.Set[*SimpleType]) error {
	if st == nil {
		return nil
	}
	if path.Contains(st) {
		return fmt.Errorf("simple type %q derives from itself", simpleTypeLabel(st))
	}
	path.Add(st)
	defer path.Remove(st)

	resolveSimpleTypeBase(st, s)
	if base, ok := st.BaseType.(*SimpleType); ok {
		if err := resolveSimpleTypeRefs(base, s, path); err != nil {
			return err
		}
	}
	if err := resolveSimpleTypeRefs(st.List, s, path); err != nil {
		return err
	}
	for _, m := range st.Union {
		if err := resolveSimpleTypeRefs(m, s, path); err != nil {
			return err
		}
	}
	return nil
}

func resolveSimpleTypeBase(st *SimpleType, s *Schema) {
	if st.Base != "" {
		local := stripPrefix(st.Base)
		if bt := resolveBuiltinType(local); bt != nil {
			st.BaseType = bt
		} else if t, ok := s.Types[local]; ok {
			st.BaseType = t
		}
	}
}
