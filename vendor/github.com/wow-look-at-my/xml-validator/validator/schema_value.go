package validator

import (
	"fmt"
	"strconv"
	"strings"
)

// validateSimpleValue checks one lexical value against a simple type. Element
// text and attribute values both come through here, so a facet constrains an
// attribute exactly as it constrains element text.
func validateSimpleValue(value string, t Type) error {
	switch st := t.(type) {
	case *BuiltinType:
		return st.validate(value)
	case *SimpleType:
		return validateAgainstSimpleType(value, st)
	}
	return nil
}

// validateAgainstSimpleType applies the base type first, then this type's own
// facets. A restriction of a restriction recurses, so a derived type enforces
// what it inherits as well as what it states.
func validateAgainstSimpleType(value string, st *SimpleType) error {
	if len(st.Union) > 0 {
		return validateUnionValue(value, st)
	}

	base, baseIsSimple := st.BaseType.(*SimpleType)
	switch {
	case baseIsSimple:
		if err := validateAgainstSimpleType(value, base); err != nil {
			return err
		}
	case st.List == nil:
		if err := validateBuiltinValue(resolveSimpleTypeBaseName(st), value); err != nil {
			return err
		}
	}

	if st.List != nil {
		for _, item := range strings.Fields(value) {
			if err := validateAgainstSimpleType(item, st.List); err != nil {
				return fmt.Errorf("list item: %w", err)
			}
		}
	}

	if err := validateEnumerationFacets(value, st.Facets); err != nil {
		return err
	}
	return validateValueFacets(value, st)
}

// validateValueFacets applies the facets stated on one type. On a list the
// length facets count ITEMS, which is what XSD defines for them there; every
// other facet reads the whole literal.
func validateValueFacets(value string, st *SimpleType) error {
	isList := listItemType(st) != nil
	baseName := resolveSimpleTypeBaseName(st)
	for _, f := range st.Facets {
		if isList {
			switch f.Kind {
			case "length", "minLength", "maxLength":
				if err := validateListLengthFacet(len(strings.Fields(value)), f); err != nil {
					return err
				}
				continue
			}
		}
		if err := validateFacet(value, baseName, f); err != nil {
			return err
		}
	}
	return nil
}

// listItemType returns the item type of a list, following a restriction back to
// the list it derives from. Resolution rejects a derivation cycle, so this walk
// terminates.
func listItemType(st *SimpleType) *SimpleType {
	for cur := st; cur != nil; {
		if cur.List != nil {
			return cur.List
		}
		next, ok := cur.BaseType.(*SimpleType)
		if !ok {
			return nil
		}
		cur = next
	}
	return nil
}

func validateListLengthFacet(count int, f Facet) error {
	want, err := strconv.Atoi(f.Value)
	if err != nil {
		return fmt.Errorf("invalid %s facet %q", f.Kind, f.Value)
	}
	switch f.Kind {
	case "length":
		if count != want {
			return fmt.Errorf("list has %d item(s), which does not equal the required length %d", count, want)
		}
	case "minLength":
		if count < want {
			return fmt.Errorf("list has %d item(s), which is less than minLength %d", count, want)
		}
	case "maxLength":
		if count > want {
			return fmt.Errorf("list has %d item(s), which exceeds maxLength %d", count, want)
		}
	}
	return nil
}

// validateUnionValue accepts the value if one member type accepts it. Each
// member carries its own facets, so a union of two enumerations allows the
// values of both and nothing else.
func validateUnionValue(value string, st *SimpleType) error {
	matched := false
	for _, m := range st.Union {
		if validateAgainstSimpleType(value, m) == nil {
			matched = true
			break
		}
	}
	if !matched {
		return fmt.Errorf("value %q does not match any member type of union", value)
	}
	if err := validateEnumerationFacets(value, st.Facets); err != nil {
		return err
	}
	return validateValueFacets(value, st)
}

// simpleTypeLabel names a type in an error message. An anonymous type has no
// name of its own, so its base is the closest thing a reader can act on.
func simpleTypeLabel(t Type) string {
	switch st := t.(type) {
	case *BuiltinType:
		return st.name
	case *SimpleType:
		if st.Name != "" {
			return st.Name
		}
		return resolveSimpleTypeBaseName(st)
	}
	return "unknown"
}
