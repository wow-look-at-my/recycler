package validator

import (
	"fmt"
	"strings"
)

// Parsing for xs:simpleType and its facets: the restriction/list/union forms
// an element or attribute declares its value space with.

func parseSimpleType(el *Element) (*SimpleType, error) {
	st := &SimpleType{}
	st.Name, _ = el.Attr("name")

	for _, child := range el.ChildElements() {
		if child.Namespace != xsdNS {
			continue
		}
		switch child.Local {
		case "restriction":
			st.Base, _ = child.Attr("base")
			for _, facetEl := range child.ChildElements() {
				if facetEl.Namespace != xsdNS {
					continue
				}
				if isFacetElement(facetEl.Local) {
					val, _ := facetEl.Attr("value")
					st.Facets = append(st.Facets, Facet{Kind: facetEl.Local, Value: val})
				}
			}
		case "list":
			itemType, _ := child.Attr("itemType")
			inline, err := parseInlineSimpleType(child)
			if err != nil {
				return nil, err
			}
			switch {
			case inline != nil:
				st.List = inline
			case itemType != "":
				st.List = &SimpleType{Base: itemType}
			default:
				return nil, fmt.Errorf("xs:list requires an itemType attribute or an inline xs:simpleType")
			}
		case "union":
			memberTypes, _ := child.Attr("memberTypes")
			for _, mt := range strings.Fields(memberTypes) {
				st.Union = append(st.Union, &SimpleType{Base: mt})
			}
			for _, member := range child.ChildElements() {
				if member.Namespace != xsdNS || member.Local != "simpleType" {
					continue
				}
				m, err := parseSimpleType(member)
				if err != nil {
					return nil, err
				}
				st.Union = append(st.Union, m)
			}
			if len(st.Union) == 0 {
				return nil, fmt.Errorf("xs:union requires a memberTypes attribute or inline xs:simpleType members")
			}
		case "annotation":
			// skip
		}
	}

	return st, nil
}

// parseInlineSimpleType returns the xs:simpleType written inside an xs:list or
// an xs:union member, or nil when the type is named by an attribute instead.
func parseInlineSimpleType(el *Element) (*SimpleType, error) {
	for _, child := range el.ChildElements() {
		if child.Namespace == xsdNS && child.Local == "simpleType" {
			return parseSimpleType(child)
		}
	}
	return nil, nil
}

func isFacetElement(local string) bool {
	switch local {
	case "enumeration", "pattern", "minLength", "maxLength", "length",
		"minInclusive", "maxInclusive", "minExclusive", "maxExclusive",
		"totalDigits", "fractionDigits", "whiteSpace":
		return true
	}
	return false
}
