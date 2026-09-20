package validator

import (
	"fmt"
	"strings"

	"github.com/wow-look-at-my/go-containers/set"
)

// Substitution groups. A global element names a head with substitutionGroup,
// and may then appear anywhere a reference to that head does. The instance is
// validated against the SUBSTITUTE's own declaration, which is the point: the
// member carries its own type.
//
// A member of a member substitutes too, so the members are closed transitively
// at resolution and each head ends up with the full list.

// resolveSubstitutionGroups links every global element that names a head to
// that head, rejecting a schema whose declarations cannot mean anything.
func resolveSubstitutionGroups(s *Schema) error {
	heads := map[*ElementDecl][]*ElementDecl{}
	for _, ed := range s.Elements {
		if ed.SubstitutionGroup == "" {
			continue
		}
		head := s.lookupElement(ed.SubstitutionGroup)
		if head == nil {
			return fmt.Errorf("element %q has substitutionGroup %q, which names no global element declaration", ed.Name, ed.SubstitutionGroup)
		}
		if head == ed {
			return fmt.Errorf("element %q substitutes for itself", ed.Name)
		}
		if err := checkSubstitutionType(ed, head, s); err != nil {
			return err
		}
		heads[head] = append(heads[head], ed)
	}
	if len(heads) == 0 {
		return nil
	}
	for head := range heads {
		if err := detectSubstitutionCycle(head, s, set.New[*ElementDecl]()); err != nil {
			return err
		}
	}
	for head, direct := range heads {
		if blocksSubstitution(head, s) {
			// The head refuses to be replaced, so it gets no members and an
			// instance using one is an unexpected element there.
			continue
		}
		head.substitutes = closeSubstitutes(direct, heads)
	}
	return nil
}

// closeSubstitutes returns the direct members plus the members of those
// members, which are equally valid substitutes for the head.
func closeSubstitutes(direct []*ElementDecl, heads map[*ElementDecl][]*ElementDecl) []*ElementDecl {
	out := make([]*ElementDecl, 0, len(direct))
	seen := set.New[*ElementDecl]()
	queue := append([]*ElementDecl(nil), direct...)
	for len(queue) > 0 {
		member := queue[0]
		queue = queue[1:]
		if !seen.Add(member) {
			continue
		}
		out = append(out, member)
		queue = append(queue, heads[member]...)
	}
	return out
}

// detectSubstitutionCycle walks a head's own substitutionGroup chain. A cycle
// describes a group that can never be resolved, and closing it would loop.
func detectSubstitutionCycle(ed *ElementDecl, s *Schema, path set.Set[*ElementDecl]) error {
	for cur := ed; cur != nil && cur.SubstitutionGroup != ""; {
		if !path.Add(cur) {
			return fmt.Errorf("substitutionGroup chain through element %q is circular", cur.Name)
		}
		cur = s.lookupElement(cur.SubstitutionGroup)
	}
	return nil
}

// blocksSubstitution reports whether a head refuses substitution, through its
// own block attribute or the schema's blockDefault.
func blocksSubstitution(head *ElementDecl, s *Schema) bool {
	block := head.Block
	if block == "" {
		block = s.BlockDefault
	}
	for _, token := range strings.Fields(block) {
		if token == "substitution" || token == "#all" {
			return true
		}
	}
	return false
}

// checkSubstitutionType enforces the one rule that makes a substitution
// meaningful: the member's type must be the head's or derive from it. The check
// stays quiet where it cannot see the whole chain -- an unresolved imported type
// is unknown, not wrong, and rejecting it would fail a schema that is fine.
func checkSubstitutionType(member, head *ElementDecl, s *Schema) error {
	memberType := declaredType(member, s)
	headType := declaredType(head, s)
	if memberType == nil || headType == nil {
		return nil
	}
	if bt, ok := headType.(*BuiltinType); ok && (bt.name == "anyType" || bt.name == "anySimpleType") {
		return nil
	}
	if typeDerivesFrom(memberType, headType) {
		return nil
	}
	return fmt.Errorf("element %q substitutes for %q but its type %q does not derive from %q",
		member.Name, head.Name, typeLabel(memberType), typeLabel(headType))
}

// declaredType resolves an element's type well enough to compare derivations.
// It returns nil when the type is named but absent, which is the "cannot see"
// case checkSubstitutionType lets through.
func declaredType(ed *ElementDecl, s *Schema) Type {
	if ed.Type != nil {
		return ed.Type
	}
	if ed.TypeName == "" {
		return nil
	}
	local := stripPrefix(ed.TypeName)
	if t, ok := s.Types[local]; ok {
		return t
	}
	return resolveBuiltinType(local)
}

// typeDerivesFrom reports whether member is head, or reaches it by walking the
// chain of bases. A chain that ends in an unresolved base answers false, and
// the caller decides what that means.
func typeDerivesFrom(member, head Type) bool {
	for depth := 0; member != nil && depth < 100; depth++ {
		if member == head {
			return true
		}
		switch t := member.(type) {
		case *ComplexType:
			if t.baseType == nil {
				if t.SimpleText != nil && t.SimpleText == head {
					return true
				}
				return false
			}
			member = t.baseType
		case *SimpleType:
			if t.BaseType == nil {
				return false
			}
			member = t.BaseType
		default:
			return false
		}
	}
	return false
}

func typeLabel(t Type) string {
	switch typ := t.(type) {
	case *BuiltinType:
		return typ.name
	case *ComplexType:
		if typ.Name != "" {
			return typ.Name
		}
		return "an anonymous complex type"
	case *SimpleType:
		return simpleTypeLabel(typ)
	}
	return "an unknown type"
}

// allSlotFor finds the element particle a child fills in an all group, and the
// declaration to validate it against: the particle's own, or the member
// substituting for it.
func (sv *schemaValidator) allSlotFor(child *Element, items []Particle, declMap map[string]*ElementDecl) (slot, decl *ElementDecl) {
	if ed, ok := declMap[child.Local]; ok {
		return ed, ed
	}
	for _, p := range items {
		ed, ok := p.(*ElementDecl)
		if !ok {
			continue
		}
		if m := sv.substituteFor(child, ed); m != nil {
			return ed, m
		}
	}
	return nil, nil
}

// substituteFor returns the declaration to validate child against where decl is
// expected: decl itself when the names match, or the substitution group member
// standing in for it. Substitution replaces a REFERENCE to a global element, so
// a local declaration matches by name only.
func (sv *schemaValidator) substituteFor(child *Element, decl *ElementDecl) *ElementDecl {
	if child.Local == decl.Name {
		return decl
	}
	if decl.Ref == "" {
		return nil
	}
	// The members are closed on the GLOBAL declaration, after the particle
	// copied its fields, so ask the global rather than this copy.
	head := sv.schema.Elements[qnameKey(decl.Namespace, decl.Name)]
	if head == nil {
		head = findByLocal(sv.schema.Elements, decl.Name)
	}
	if head == nil {
		return nil
	}
	for _, m := range head.substitutes {
		if m.Name == child.Local && m.Namespace == child.Namespace {
			return m
		}
	}
	return nil
}
