package syntax

// File is a single parsed .tml source file. A file holds exactly a single definition: either a component or a theme.
type File struct {
	Path      string
	Component *Component
	Theme     *Theme
}

// Component is a reusable element definition. DataTemplates are the file's <DataTemplate> declarations: the same shape
type Component struct {
	Name          string
	Imports       []*Import
	Properties    []*Property
	Template      *Node
	DataTemplates []*Component
	// IsData marks a <DataTemplate>: a component instantiated as soon as per item by an items control, with the item supplying
	IsData bool
	Pos    Pos
}

// Import brings the definition in another file into scope under its own name.
type Import struct {
	Src string
	Pos Pos
}

// Property declares a single typed input to a component. Type is kept as source text here; sema parses it, so an unknown
type Property struct {
	Name       string
	Type       string
	Default    string
	HasDefault bool
	Required   bool
	Pos        Pos
}

// Theme is a set of tokens and named styles.
type Theme struct {
	Name   string
	Tokens []*Token
	Styles []*Style
	Pos    Pos
}

// Token is a named theme value. A token either has a single Value or a light/dark pair, never both.
type Token struct {
	Name  string
	Value string
	Light string
	Dark  string
	Pos   Pos
}

// Style is a named bundle of style attributes. Extends names another style whose unset attributes are inherited.
type Style struct {
	Name    string
	Extends string
	Attrs   []Attr
	Pos     Pos
}

// NodeKind distinguishes both things that can appear in a template body.
type NodeKind int

const (
	// ElementNode is a markup element: a panel, a native element, a component instance, or a language directive such as
	ElementNode NodeKind = iota
	// TextNode is character data, which may contain {expr} interpolations.
	TextNode
)

// Node is a single entry in a template body. Name holds the element's local name, including any dot: "Stack", "Card", and
type Node struct {
	Kind     NodeKind
	Name     string
	Attrs    []Attr
	Children []*Node
	Text     string
	Pos      Pos
}

// Attr is a single attribute on an element. Name keeps any dot, so an attached property such as "Grid.row" arrives intact.
type Attr struct {
	Name  string
	Value string
	Pos   Pos
}

// Attr returns the value of the named attribute and whether it was present.
func (n *Node) Attr(name string) (string, bool) {
	for _, a := range n.Attrs {
		if a.Name == name {
			return a.Value, true
		}
	}
	return "", false
}

// Elements returns only the element children, skipping text.
func (n *Node) Elements() []*Node {
	var out []*Node
	for _, c := range n.Children {
		if c.Kind == ElementNode {
			out = append(out, c)
		}
	}
	return out
}
