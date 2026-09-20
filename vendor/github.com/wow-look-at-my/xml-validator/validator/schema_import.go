package validator

import (
	"bytes"
	"fmt"

	"github.com/wow-look-at-my/go-containers/set"
)

// SchemaResolver loads the bytes of a referenced schema. It is invoked once
// per xs:import directive that has a schemaLocation. The namespace argument
// is the target namespace of the imported schema (may be empty), and
// schemaLocation is the URI hint from the directive. Returning a nil byte
// slice with a nil error is treated as "skip this import"; returning an
// empty-but-non-nil slice is treated as malformed schema content and will
// surface as a parse error.
type SchemaResolver func(namespace, schemaLocation string) ([]byte, error)

type importKey struct {
	Namespace string
	Location  string
}

type importResult struct {
	directive *Import
	imported  *Schema
}

func parseInclude(el *Element, parentNS string, resolver SchemaResolver, visited set.Set[importKey]) (*Schema, error) {
	loc, _ := el.Attr("schemaLocation")
	if loc == "" {
		return nil, fmt.Errorf("xs:include requires a schemaLocation attribute")
	}
	if resolver == nil {
		return nil, fmt.Errorf("xs:include schemaLocation %q requires a schema resolver", loc)
	}
	key := importKey{Location: loc}
	if visited.Contains(key) {
		return nil, nil
	}
	visited.Add(key)

	data, err := resolver(parentNS, loc)
	if err != nil {
		return nil, fmt.Errorf("resolving xs:include %q: %w", loc, err)
	}
	if data == nil {
		return nil, nil
	}
	doc, err := ParseTree(bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("parsing included schema %q: %w", loc, err)
	}
	included, err := parseSchemaDoc(doc, resolver, visited)
	if err != nil {
		return nil, fmt.Errorf("parsing included schema %q: %w", loc, err)
	}
	if included.TargetNamespace != "" && included.TargetNamespace != parentNS {
		return nil, fmt.Errorf("xs:include %q: included schema targetNamespace %q does not match including schema %q",
			loc, included.TargetNamespace, parentNS)
	}
	// Chameleon include: an included schema with no targetNamespace adopts the
	// including schema's namespace. Re-stamp global element declarations so
	// wildcard lookups can find them under the correct namespace.
	if included.TargetNamespace == "" && parentNS != "" {
		elements := make(map[string]*ElementDecl, len(included.Elements))
		for _, ed := range included.Elements {
			ed.Namespace = parentNS
			elements[qnameKey(parentNS, ed.Name)] = ed
		}
		included.Elements = elements
		attrs := make(map[string]*AttrDecl, len(included.Attributes))
		for _, ad := range included.Attributes {
			ad.Namespace = parentNS
			attrs[qnameKey(parentNS, ad.Name)] = ad
		}
		included.Attributes = attrs
		included.TargetNamespace = parentNS
	}
	return included, nil
}

func parseImport(el *Element, resolver SchemaResolver, visited set.Set[importKey]) (*importResult, error) {
	ns, _ := el.Attr("namespace")
	loc, _ := el.Attr("schemaLocation")
	directive := &Import{Namespace: ns, SchemaLocation: loc}

	if loc == "" {
		return &importResult{directive: directive}, nil
	}
	if resolver == nil {
		return nil, fmt.Errorf("xs:import schemaLocation %q requires a schema resolver", loc)
	}
	key := importKey{Namespace: ns, Location: loc}
	if visited.Contains(key) {
		return &importResult{directive: directive}, nil
	}
	visited.Add(key)

	data, err := resolver(ns, loc)
	if err != nil {
		return nil, fmt.Errorf("resolving xs:import %q: %w", loc, err)
	}
	if data == nil {
		return &importResult{directive: directive}, nil
	}
	doc, err := ParseTree(bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("parsing imported schema %q: %w", loc, err)
	}
	imported, err := parseSchemaDoc(doc, resolver, visited)
	if err != nil {
		return nil, fmt.Errorf("parsing imported schema %q: %w", loc, err)
	}
	return &importResult{directive: directive, imported: imported}, nil
}

// mergeImportedSchema folds src into dst. Because the validator resolves
// components by local name only, any name that is already defined in dst
// (whether by the main schema or by an earlier import) is a hard error --
// silently dropping the second definition would let users validate against
// an incomplete schema set without any signal.
func mergeImportedSchema(dst, src *Schema) error {
	for name, ed := range src.Elements {
		if _, exists := dst.Elements[name]; exists {
			return fmt.Errorf("xs:import: element %q is defined more than once in the same namespace", name)
		}
		dst.Elements[name] = ed
	}
	for name, t := range src.Types {
		if _, exists := dst.Types[name]; exists {
			return fmt.Errorf("xs:import: type %q is defined more than once across schemas", name)
		}
		dst.Types[name] = t
	}
	for name, g := range src.Groups {
		if _, exists := dst.Groups[name]; exists {
			return fmt.Errorf("xs:import: group %q is defined more than once across schemas", name)
		}
		dst.Groups[name] = g
	}
	for name, ag := range src.AttrGroups {
		if _, exists := dst.AttrGroups[name]; exists {
			return fmt.Errorf("xs:import: attributeGroup %q is defined more than once across schemas", name)
		}
		dst.AttrGroups[name] = ag
	}
	for name, ad := range src.Attributes {
		if _, exists := dst.Attributes[name]; exists {
			return fmt.Errorf("xs:import: attribute %q is defined more than once in the same namespace", name)
		}
		dst.Attributes[name] = ad
	}
	dst.Imports = append(dst.Imports, src.Imports...)
	return nil
}
