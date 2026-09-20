package validator

import (
	"fmt"

	"github.com/wow-look-at-my/go-containers/set"
)

// Conditional type assignment: xs:alternative gives an element a type chosen
// per instance. The first alternative whose test holds decides the type, and an
// alternative with no test always holds, which is how a default is written.
//
// The test language is the "required subset" of XPath 2.0 that XSD 1.1 defines
// for this attribute; `schema_alternative_expr.go` parses and evaluates it.
// Anything outside that subset is a hard error at schema-parse time, because a
// test this engine cannot evaluate would pick the wrong type in silence.
//
// see docs/conditional-types.md

// TypeAlternative is one xs:alternative on an element declaration.
type TypeAlternative struct {
	TypeName string
	Type     Type
	// test is nil for an alternative with no test attribute: the default.
	test     testExpr
	testText string
}

func lookupAttrValue(el *Element, want nameTest) (string, bool) {
	for _, attr := range el.Attrs {
		if attr.Prefix == "xmlns" || attr.Name == "xmlns" {
			continue
		}
		if want.matches(attr.Namespace, attr.Local) {
			return attr.Value, true
		}
	}
	return "", false
}

// parseTypeAlternative reads one xs:alternative. The test is kept as written
// and compiled during resolution, where the schema's prefixes are available.
func parseTypeAlternative(el *Element) (*TypeAlternative, error) {
	alt := &TypeAlternative{}
	alt.TypeName, _ = el.Attr("type")
	if test, ok := el.Attr("test"); ok {
		alt.testText = test
	}
	for _, child := range el.ChildElements() {
		if child.Namespace != xsdNS {
			continue
		}
		switch child.Local {
		case "complexType":
			ct, err := parseComplexType(child)
			if err != nil {
				return nil, err
			}
			alt.Type = ct
		case "simpleType":
			st, err := parseSimpleType(child)
			if err != nil {
				return nil, err
			}
			alt.Type = st
		case "annotation":
			// skip
		default:
			return nil, fmt.Errorf("unsupported schema element xs:%s inside xs:alternative", child.Local)
		}
	}
	if alt.Type == nil && alt.TypeName == "" {
		return nil, fmt.Errorf("xs:alternative requires a type attribute or an inline type")
	}
	return alt, nil
}

// resolveAlternatives compiles each test and resolves each named type. An
// alternative naming a type that does not exist would otherwise assign nothing
// and validate the element against no declaration at all.
func resolveAlternatives(ed *ElementDecl, s *Schema) error {
	for _, alt := range ed.Alternatives {
		if alt.Type == nil {
			local := stripPrefix(alt.TypeName)
			if t, ok := s.Types[local]; ok {
				alt.Type = t
			} else if bt := resolveBuiltinType(local); bt != nil {
				alt.Type = bt
			} else {
				return fmt.Errorf("element %q: xs:alternative type %q does not name a known type", ed.Name, alt.TypeName)
			}
		}
		if ct, ok := alt.Type.(*ComplexType); ok {
			if err := resolveComplexTypeRefs(ct, s, set.New[*ComplexType]()); err != nil {
				return err
			}
		}
		if st, ok := alt.Type.(*SimpleType); ok {
			if err := resolveSimpleTypeRefs(st, s, set.New[*SimpleType]()); err != nil {
				return err
			}
		}
		if alt.testText == "" {
			continue
		}
		test, err := compileTest(alt.testText, s)
		if err != nil {
			return fmt.Errorf("element %q: xs:alternative %w", ed.Name, err)
		}
		alt.test = test
	}
	return nil
}

// chooseType picks the type an instance element takes. The alternatives are
// tried in order; the declared type is the answer when none holds, which is
// what XSD says an element with no matching alternative falls back to.
func chooseType(el *Element, decl *ElementDecl) Type {
	for _, alt := range decl.Alternatives {
		if alt.test == nil || alt.test.eval(el) {
			return alt.Type
		}
	}
	return decl.Type
}
