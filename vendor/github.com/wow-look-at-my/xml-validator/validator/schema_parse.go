package validator

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/wow-look-at-my/go-containers/set"
)

// ParseSchema parses an XSD schema tree with no support for resolving
// xs:import schemaLocation hints. Imports without a schemaLocation are
// accepted as namespace declarations; imports that name a schemaLocation
// produce an error. Callers that need to follow schemaLocation hints should
// use ParseSchemaWithResolver instead.
func ParseSchema(doc *Document) (*Schema, error) {
	return ParseSchemaWithResolver(doc, nil)
}

// ParseSchemaWithResolver parses an XSD schema tree. Each xs:import or
// xs:include directive with a non-empty schemaLocation is loaded via resolver,
// parsed, and merged into the returned schema. Components from
// imported/included schemas are looked up by local name (the same way the
// validator resolves types in the main schema), so per-namespace name
// collisions are not supported.
func ParseSchemaWithResolver(doc *Document, resolver SchemaResolver) (*Schema, error) {
	visited := set.New[importKey]()
	return parseSchemaDoc(doc, resolver, visited)
}

func parseSchemaDoc(doc *Document, resolver SchemaResolver, visited set.Set[importKey]) (*Schema, error) {
	root := doc.Root
	if root.Local != "schema" || root.Namespace != xsdNS {
		return nil, fmt.Errorf("expected xs:schema root element, got {%s}%s", root.Namespace, root.Local)
	}

	s := &Schema{
		Elements:   make(map[string]*ElementDecl),
		Types:      make(map[string]Type),
		Groups:     make(map[string]*Group),
		AttrGroups: make(map[string]*AttrGroup),
		Attributes: make(map[string]*AttrDecl),
	}

	s.prefixes = root.Namespaces
	s.TargetNamespace, _ = root.Attr("targetNamespace")
	s.ElementFormDefault, _ = root.Attr("elementFormDefault")
	if s.ElementFormDefault == "" {
		s.ElementFormDefault = "unqualified"
	}
	s.BlockDefault, _ = root.Attr("blockDefault")
	s.AttributeFormDefault, _ = root.Attr("attributeFormDefault")
	if s.AttributeFormDefault == "" {
		s.AttributeFormDefault = "unqualified"
	}

	for _, child := range root.ChildElements() {
		if child.Namespace != xsdNS {
			continue
		}
		switch child.Local {
		case "element":
			ed, err := parseElementDecl(child)
			if err != nil {
				return nil, fmt.Errorf("parsing element declaration: %w", err)
			}
			if ed.Name != "" {
				ed.Namespace = s.TargetNamespace
				s.Elements[qnameKey(s.TargetNamespace, ed.Name)] = ed
			}
		case "attribute":
			ad, err := parseAttrDecl(child)
			if err != nil {
				return nil, fmt.Errorf("parsing attribute declaration: %w", err)
			}
			if ad.Name != "" {
				ad.Namespace = s.TargetNamespace
				s.Attributes[qnameKey(s.TargetNamespace, ad.Name)] = ad
			}
		case "complexType":
			ct, err := parseComplexType(child)
			if err != nil {
				return nil, fmt.Errorf("parsing complex type: %w", err)
			}
			if ct.Name != "" {
				s.Types[ct.Name] = ct
			}
		case "simpleType":
			st, err := parseSimpleType(child)
			if err != nil {
				return nil, fmt.Errorf("parsing simple type: %w", err)
			}
			if st.Name != "" {
				s.Types[st.Name] = st
			}
		case "group":
			g, err := parseGroup(child)
			if err != nil {
				return nil, fmt.Errorf("parsing group: %w", err)
			}
			if g.Name != "" {
				s.Groups[g.Name] = g
			}
		case "attributeGroup":
			ag, err := parseAttrGroup(child)
			if err != nil {
				return nil, fmt.Errorf("parsing attribute group: %w", err)
			}
			if ag.Name != "" {
				s.AttrGroups[ag.Name] = ag
			}
		case "import":
			imp, err := parseImport(child, resolver, visited)
			if err != nil {
				return nil, err
			}
			s.Imports = append(s.Imports, imp.directive)
			if imp.imported != nil {
				if err := mergeImportedSchema(s, imp.imported); err != nil {
					return nil, err
				}
			}
		case "include":
			included, err := parseInclude(child, s.TargetNamespace, resolver, visited)
			if err != nil {
				return nil, err
			}
			if included != nil {
				if err := mergeImportedSchema(s, included); err != nil {
					return nil, fmt.Errorf("xs:include: %w", err)
				}
			}
		case "redefine", "override":
			return nil, fmt.Errorf("unsupported: xs:%s is not supported", child.Local)
		case "notation":
			return nil, fmt.Errorf("unsupported: xs:notation is not supported")
		case "annotation":
			// skip
		default:
			return nil, fmt.Errorf("unsupported schema element xs:%s", child.Local)
		}
	}

	if err := resolveSchemaRefs(s); err != nil {
		return nil, err
	}
	return s, nil
}

func parseElementDecl(el *Element) (*ElementDecl, error) {
	ed := &ElementDecl{
		MinOccurs: 1,
		MaxOccurs: 1,
	}
	ed.Name, _ = el.Attr("name")
	ed.TypeName, _ = el.Attr("type")
	ed.Ref, _ = el.Attr("ref")
	ed.Default, _ = el.Attr("default")
	ed.Fixed, _ = el.Attr("fixed")

	ed.SubstitutionGroup, _ = el.Attr("substitutionGroup")
	ed.Block, _ = el.Attr("block")
	if v, ok := el.Attr("abstract"); ok && v == "true" {
		ed.Abstract = true
	}

	if v, ok := el.Attr("nillable"); ok && v == "true" {
		ed.Nillable = true
	}
	if v, ok := el.Attr("minOccurs"); ok {
		n, err := strconv.Atoi(v)
		if err != nil {
			return nil, fmt.Errorf("invalid minOccurs %q", v)
		}
		ed.MinOccurs = n
	}
	if v, ok := el.Attr("maxOccurs"); ok {
		if v == "unbounded" {
			ed.MaxOccurs = -1
		} else {
			n, err := strconv.Atoi(v)
			if err != nil {
				return nil, fmt.Errorf("invalid maxOccurs %q", v)
			}
			ed.MaxOccurs = n
		}
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
			ed.Type = ct
		case "simpleType":
			st, err := parseSimpleType(child)
			if err != nil {
				return nil, err
			}
			ed.Type = st
		case "annotation":
			// skip
		case "alternative":
			alt, err := parseTypeAlternative(child)
			if err != nil {
				return nil, fmt.Errorf("element %q: %w", ed.Name, err)
			}
			ed.Alternatives = append(ed.Alternatives, alt)
		case "key", "keyref", "unique":
			ic, err := parseIdentityConstraint(child)
			if err != nil {
				return nil, fmt.Errorf("element %q: %w", ed.Name, err)
			}
			ed.Constraints = append(ed.Constraints, ic)
		default:
			return nil, fmt.Errorf("unsupported schema element xs:%s inside xs:element %q", child.Local, ed.Name)
		}
	}

	return ed, nil
}

func parseComplexType(el *Element) (*ComplexType, error) {
	ct := &ComplexType{}
	ct.Name, _ = el.Attr("name")
	if v, ok := el.Attr("mixed"); ok && v == "true" {
		ct.Mixed = true
	}

	for _, child := range el.ChildElements() {
		if child.Namespace != xsdNS {
			continue
		}
		switch child.Local {
		case "sequence":
			seq, err := parseSequence(child)
			if err != nil {
				return nil, err
			}
			ct.Content = seq
		case "choice":
			ch, err := parseChoice(child)
			if err != nil {
				return nil, err
			}
			ct.Content = ch
		case "all":
			a, err := parseAll(child)
			if err != nil {
				return nil, err
			}
			ct.Content = a
		case "group":
			gr, err := parseGroupRef(child)
			if err != nil {
				return nil, err
			}
			ct.Content = &Sequence{Items: []Particle{gr}, MinOccurs: 1, MaxOccurs: 1}
		case "attribute":
			ad, err := parseAttrDecl(child)
			if err != nil {
				return nil, err
			}
			ct.Attributes = append(ct.Attributes, ad)
		case "attributeGroup":
			if ref, ok := child.Attr("ref"); ok {
				ct.attrGroupRefs = append(ct.attrGroupRefs, stripPrefix(ref))
			}
		case "simpleContent":
			if err := parseSimpleContent(child, ct); err != nil {
				return nil, err
			}
		case "complexContent":
			if err := parseComplexContent(child, ct); err != nil {
				return nil, err
			}
		case "anyAttribute":
			aa, err := parseAnyAttrDecl(child)
			if err != nil {
				return nil, err
			}
			ct.AnyAttribute = aa
		case "annotation":
			// skip
		}
	}

	return ct, nil
}

func parseSimpleContent(el *Element, ct *ComplexType) error {
	for _, child := range el.ChildElements() {
		if child.Namespace != xsdNS {
			continue
		}
		switch child.Local {
		case "extension":
			base, _ := child.Attr("base")
			ct.SimpleText = resolveTypeByName(base)
			for _, attr := range child.ChildElements() {
				if attr.Namespace != xsdNS {
					continue
				}
				if attr.Local == "attribute" {
					ad, err := parseAttrDecl(attr)
					if err != nil {
						return err
					}
					ct.Attributes = append(ct.Attributes, ad)
				} else if attr.Local == "anyAttribute" {
					aa, err := parseAnyAttrDecl(attr)
					if err != nil {
						return err
					}
					ct.AnyAttribute = aa
				} else if attr.Local == "attributeGroup" {
					if ref, ok := attr.Attr("ref"); ok {
						ct.attrGroupRefs = append(ct.attrGroupRefs, stripPrefix(ref))
					}
				}
			}
		case "restriction":
			base, _ := child.Attr("base")
			st := &SimpleType{Base: base}
			for _, facetEl := range child.ChildElements() {
				if facetEl.Namespace != xsdNS {
					continue
				}
				if isFacetElement(facetEl.Local) {
					val, _ := facetEl.Attr("value")
					st.Facets = append(st.Facets, Facet{Kind: facetEl.Local, Value: val})
				} else if facetEl.Local == "attribute" {
					ad, err := parseAttrDecl(facetEl)
					if err != nil {
						return err
					}
					ct.Attributes = append(ct.Attributes, ad)
				} else if facetEl.Local == "anyAttribute" {
					aa, err := parseAnyAttrDecl(facetEl)
					if err != nil {
						return err
					}
					ct.AnyAttribute = aa
				} else if facetEl.Local == "attributeGroup" {
					if ref, ok := facetEl.Attr("ref"); ok {
						ct.attrGroupRefs = append(ct.attrGroupRefs, stripPrefix(ref))
					}
				}
			}
			ct.SimpleText = st
		}
	}
	return nil
}

func parseComplexContent(el *Element, ct *ComplexType) error {
	if v, ok := el.Attr("mixed"); ok && v == "true" {
		ct.Mixed = true
	}
	for _, child := range el.ChildElements() {
		if child.Namespace != xsdNS {
			continue
		}
		switch child.Local {
		case "extension", "restriction":
			ct.baseName, _ = child.Attr("base")
			ct.derivation = child.Local
			for _, inner := range child.ChildElements() {
				if inner.Namespace != xsdNS {
					continue
				}
				switch inner.Local {
				case "sequence":
					seq, err := parseSequence(inner)
					if err != nil {
						return err
					}
					ct.Content = seq
				case "choice":
					ch, err := parseChoice(inner)
					if err != nil {
						return err
					}
					ct.Content = ch
				case "all":
					a, err := parseAll(inner)
					if err != nil {
						return err
					}
					ct.Content = a
				case "group":
					gr, err := parseGroupRef(inner)
					if err != nil {
						return err
					}
					ct.Content = &Sequence{Items: []Particle{gr}, MinOccurs: 1, MaxOccurs: 1}
				case "attribute":
					ad, err := parseAttrDecl(inner)
					if err != nil {
						return err
					}
					ct.Attributes = append(ct.Attributes, ad)
				case "anyAttribute":
					aa, err := parseAnyAttrDecl(inner)
					if err != nil {
						return err
					}
					ct.AnyAttribute = aa
				case "attributeGroup":
					if ref, ok := inner.Attr("ref"); ok {
						ct.attrGroupRefs = append(ct.attrGroupRefs, stripPrefix(ref))
					}
				}
			}
		}
	}
	return nil
}

func parseAttrDecl(el *Element) (*AttrDecl, error) {
	ad := &AttrDecl{Use: "optional"}
	ad.Name, _ = el.Attr("name")
	ad.TypeName, _ = el.Attr("type")
	ad.Ref, _ = el.Attr("ref")
	ad.Default, _ = el.Attr("default")
	ad.Fixed, _ = el.Attr("fixed")
	if v, ok := el.Attr("use"); ok {
		ad.Use = v
	}

	for _, child := range el.ChildElements() {
		if child.Namespace == xsdNS && child.Local == "simpleType" {
			st, err := parseSimpleType(child)
			if err != nil {
				return nil, err
			}
			ad.Type = st
		}
	}

	return ad, nil
}

func parseGroup(el *Element) (*Group, error) {
	g := &Group{}
	g.Name, _ = el.Attr("name")
	for _, child := range el.ChildElements() {
		if child.Namespace != xsdNS {
			continue
		}
		switch child.Local {
		case "sequence":
			seq, err := parseSequence(child)
			if err != nil {
				return nil, err
			}
			g.Content = seq
		case "choice":
			ch, err := parseChoice(child)
			if err != nil {
				return nil, err
			}
			g.Content = ch
		case "all":
			a, err := parseAll(child)
			if err != nil {
				return nil, err
			}
			g.Content = a
		}
	}
	return g, nil
}

func parseAttrGroup(el *Element) (*AttrGroup, error) {
	ag := &AttrGroup{}
	ag.Name, _ = el.Attr("name")
	for _, child := range el.ChildElements() {
		if child.Namespace != xsdNS {
			continue
		}
		switch child.Local {
		case "attribute":
			ad, err := parseAttrDecl(child)
			if err != nil {
				return nil, err
			}
			ag.Attributes = append(ag.Attributes, ad)
		case "anyAttribute":
			aa, err := parseAnyAttrDecl(child)
			if err != nil {
				return nil, err
			}
			ag.AnyAttribute = aa
		}
	}
	return ag, nil
}

func parseAnyAttrDecl(el *Element) (*AnyAttrDecl, error) {
	aa := &AnyAttrDecl{}
	aa.Namespace, _ = el.Attr("namespace")
	pc, _ := el.Attr("processContents")
	if pc == "" {
		pc = "strict"
	}
	if err := validateProcessContents(pc); err != nil {
		return nil, fmt.Errorf("xs:anyAttribute: %w", err)
	}
	return aa, nil
}

// validateProcessContents rejects every wildcard processContents value other
// than "strict". This validator does not provide a no-validation mode: "skip"
// disables validation outright, and "lax" disables it for any element whose
// declaration cannot be located -- both contradict the project's reason for
// existing. Use "strict" (the default) or do not run the validator.
func validateProcessContents(pc string) error {
	if pc == "strict" {
		return nil
	}
	switch pc {
	case "skip", "lax":
		return fmt.Errorf(`processContents=%q is not supported: this validator does not provide a partial- or no-validation mode (only "strict" is allowed)`, pc)
	default:
		return fmt.Errorf("invalid processContents value %q (only \"strict\" is allowed)", pc)
	}
}

func parseOccurs(el *Element, min, max *int) {
	if v, ok := el.Attr("minOccurs"); ok {
		n, err := strconv.Atoi(v)
		if err == nil {
			*min = n
		}
	}
	if v, ok := el.Attr("maxOccurs"); ok {
		if v == "unbounded" {
			*max = -1
		} else {
			n, err := strconv.Atoi(v)
			if err == nil {
				*max = n
			}
		}
	}
}

func resolveTypeByName(name string) Type {
	local := stripPrefix(name)
	if bt := resolveBuiltinType(local); bt != nil {
		return bt
	}
	return &SimpleType{Base: local}
}

func stripPrefix(name string) string {
	if idx := strings.Index(name, ":"); idx >= 0 {
		return name[idx+1:]
	}
	return name
}
