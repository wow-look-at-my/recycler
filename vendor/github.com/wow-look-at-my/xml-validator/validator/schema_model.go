package validator

const xsdNS = "http://www.w3.org/2001/XMLSchema"
const xsiNS = "http://www.w3.org/2001/XMLSchema-instance"

type Schema struct {
	TargetNamespace      string
	ElementFormDefault   string
	AttributeFormDefault string
	Elements             map[string]*ElementDecl
	Types                map[string]Type
	Groups               map[string]*Group
	AttrGroups           map[string]*AttrGroup
	// Attributes are the global xs:attribute declarations. A qualified
	// attribute in an instance document is validated against the declaration
	// here whose namespace matches its own; without one there is nothing to
	// validate against, so the attribute is an error.
	//
	// Elements and Attributes are keyed by qnameKey(namespace, local): two
	// imported vocabularies may each declare a <params>, and keying by local
	// name alone would call that a collision.
	Attributes map[string]*AttrDecl
	Imports    []*Import
	// identity holds every xs:key and xs:unique by resolved name, and
	// identityRefs the xs:keyref declarations that must find one there. A
	// constraint name is schema-wide in XSD, so both are kept on the schema
	// rather than on the element that declares them.
	identity     map[string]*IdentityConstraint
	identityRefs []*IdentityConstraint
	// BlockDefault is the schema's blockDefault attribute: the refusal every
	// element inherits when it states no block of its own.
	BlockDefault string
	// prefixes are the namespace declarations this schema document made, so a
	// QName in a ref resolves to the namespace its author meant.
	prefixes map[string]string
}

// Import is an xs:import directive recorded on the schema. The Namespace is
// the imported target namespace (may be empty) and SchemaLocation is the URI
// hint provided to locate the imported schema (may be empty).
type Import struct {
	Namespace      string
	SchemaLocation string
}

type ElementDecl struct {
	Name      string
	Namespace string // target namespace of the schema declaring this global element
	TypeName  string
	Type      Type
	MinOccurs int
	MaxOccurs int // -1 = unbounded
	Default   string
	Fixed     string
	Nillable  bool
	Ref       string
	// SubstitutionGroup names the global element this one may stand in for.
	// Abstract marks an element that only its substitutes may replace, and
	// Block ("substitution", "#all", ...) is the head's refusal to be replaced
	// at all; an empty Block takes the schema's blockDefault.
	SubstitutionGroup string
	Abstract          bool
	Block             string
	// substitutes are the declarations that may appear where a reference to
	// this element does, transitively. A member of a member substitutes too.
	substitutes []*ElementDecl
	// Alternatives are the xs:alternative type choices, in schema order. The
	// first whose test holds gives the instance its type.
	Alternatives []*TypeAlternative
	// Constraints are the xs:key, xs:keyref, and xs:unique declarations on this
	// element. They are evaluated over the element's own subtree.
	Constraints []*IdentityConstraint
	// compiled records that the constraint XPaths were compiled and registered
	// on the schema. A ref particle shares its target's constraint pointers, so
	// registration must not run twice for one declaration.
	compiled bool
}

// IdentityConstraint is one xs:key, xs:keyref, or xs:unique. Selector and
// Fields hold the compiled XPaths; a keyref also names the key it points at.
type IdentityConstraint struct {
	Kind  string // "key", "keyref", or "unique"
	Name  string
	Refer string

	selectorXPath string
	fieldXPaths   []string
	selector      []idPath
	fields        [][]idPath
	referKey      string
}

type Type interface {
	typeName() string
}

type ComplexType struct {
	Name         string
	Mixed        bool
	Content      ContentModel
	Attributes   []*AttrDecl
	AnyAttribute *AnyAttrDecl
	SimpleText   Type // non-nil for simpleContent
	// baseName and derivation record an xs:complexContent derivation until the
	// base type is resolvable. A derived type OWNS what it inherits: dropping
	// the base's content model and attributes would validate documents against
	// half a declaration and call them conforming.
	baseName      string
	derivation    string // "extension" or "restriction"
	attrGroupRefs []string
	// baseType is the type this one derived from, kept after resolution folded
	// the base in. A substitution group member must derive from its head's
	// type, and that walk needs the chain.
	baseType Type
}

func (t *ComplexType) typeName() string { return t.Name }

type SimpleType struct {
	Name     string
	Base     string
	BaseType Type
	Facets   []Facet
	List     *SimpleType
	Union    []*SimpleType
}

func (t *SimpleType) typeName() string { return t.Name }

type ContentModel interface{ contentModel() }

type Sequence struct {
	Items     []Particle
	MinOccurs int
	MaxOccurs int
}

func (*Sequence) contentModel() {}

type Choice struct {
	Items     []Particle
	MinOccurs int
	MaxOccurs int
}

func (*Choice) contentModel() {}

type All struct {
	Items     []Particle
	MinOccurs int
	MaxOccurs int
}

func (*All) contentModel() {}

type Particle interface{ particle() }

func (*ElementDecl) particle() {}
func (*Sequence) particle()    {}
func (*Choice) particle()      {}
func (*All) particle()         {}
func (*AnyParticle) particle() {}
func (*GroupRef) particle()    {}

// GroupRef is an xs:group ref particle. It stands in for the named group's
// content model until resolution replaces it with a copy of that model,
// carrying the occurrence counts stated at the reference.
type GroupRef struct {
	Ref       string
	MinOccurs int
	MaxOccurs int
}

// AnyParticle is an xs:any wildcard. processContents is always "strict";
// the schema parser rejects any other value because this validator does not
// support a no-validation mode.
type AnyParticle struct {
	Namespace string
	MinOccurs int
	MaxOccurs int
}

// AnyAttrDecl is an xs:anyAttribute wildcard. processContents is always
// "strict" -- see [AnyParticle] for the rationale.
type AnyAttrDecl struct {
	Namespace string
}

type AttrDecl struct {
	Name string
	// Namespace is the target namespace of the schema declaring this global
	// attribute. It is empty on a local declaration, which is unqualified
	// unless the schema says otherwise.
	Namespace string
	TypeName  string
	Type      Type
	Use       string // required, optional, prohibited
	Default   string
	Fixed     string
	Ref       string
}

type Facet struct {
	Kind  string
	Value string
}

type Group struct {
	Name    string
	Content ContentModel
}

type AttrGroup struct {
	Name         string
	Attributes   []*AttrDecl
	AnyAttribute *AnyAttrDecl
}
