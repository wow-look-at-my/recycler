package validator

import "strings"

// A schema names things across namespaces, and two namespaces may legitimately
// use the same local name -- one <params> per imported vocabulary is the
// ordinary case, not an exotic one. Global element and attribute declarations
// are therefore keyed by namespace AND local name, and a QName in a ref is
// resolved through the prefixes its own schema document declared.

// qnameKey is the map key for a global declaration.
func qnameKey(ns, local string) string { return ns + " " + local }

// resolveQName splits a QName and resolves its prefix through the schema's own
// declarations. An unprefixed name takes the default namespace when the schema
// declared one, and the schema's target namespace otherwise -- which is what a
// schema that omits xmlns= but names a targetNamespace means in practice.
func (s *Schema) resolveQName(name string) (ns, local string) {
	prefix := ""
	local = name
	if i := strings.IndexByte(name, ':'); i >= 0 {
		prefix, local = name[:i], name[i+1:]
	}
	if ns, ok := s.prefixes[prefix]; ok {
		return ns, local
	}
	if prefix == "" {
		return s.TargetNamespace, local
	}
	return "", local
}

// lookupElement finds a global element declaration by QName, falling back to a
// local-name match when nothing declared that namespace. The fallback is what
// keeps a schema that never mentions namespaces working exactly as before.
func (s *Schema) lookupElement(name string) *ElementDecl {
	ns, local := s.resolveQName(name)
	if ed, ok := s.Elements[qnameKey(ns, local)]; ok {
		return ed
	}
	return findByLocal(s.Elements, local)
}

// lookupAttribute finds a global attribute declaration by QName.
func (s *Schema) lookupAttribute(name string) *AttrDecl {
	ns, local := s.resolveQName(name)
	if ad, ok := s.Attributes[qnameKey(ns, local)]; ok {
		return ad
	}
	return findByLocal(s.Attributes, local)
}

// lookupIdentityKey finds the map key of the sole xs:key or xs:unique with the
// given local name. A schema that never mentions namespaces reaches its keys
// this way, the same fallback lookupElement makes.
func lookupIdentityKey(s *Schema, local string) (string, bool) {
	found := ""
	count := 0
	for key := range s.identity {
		if key[strings.IndexByte(key, ' ')+1:] == local {
			found = key
			count++
		}
	}
	return found, count == 1
}

// findByLocal returns the sole declaration with the given local name, or nil
// when there is none or more than one -- an ambiguous fallback would pick a
// vocabulary at random, and answering "I do not know which" is the honest
// result.
func findByLocal[T any](m map[string]T, local string) T {
	var found T
	count := 0
	for key, v := range m {
		if key[strings.IndexByte(key, ' ')+1:] == local {
			found = v
			count++
		}
	}
	if count == 1 {
		return found
	}
	var zero T
	return zero
}
