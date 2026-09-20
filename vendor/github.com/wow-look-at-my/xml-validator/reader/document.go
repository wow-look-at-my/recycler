package reader

type Document struct {
	Root *Element
}

type Node interface{ node() }

type Element struct {
	Name      string
	Namespace string
	Local     string
	Prefix    string
	Attrs     []Attr
	Children  []Node
	// Namespaces is every prefix in scope here, inherited declarations
	// included, with the default namespace under the empty key. The xmlns
	// attributes themselves are not in Attrs -- they are declarations, not
	// data -- so this is the only way to resolve a QName that appears inside
	// an attribute VALUE, which is how a schema writes `ref="a:dup"`.
	Namespaces map[string]string
	Line       int
	Col        int
}

type Attr struct {
	Name      string
	Namespace string
	Local     string
	Prefix    string
	Value     string
	// Line and Col locate the attribute's name in the source. Without them a
	// consumer reporting a problem with an attribute's value can only point at
	// the element that owns it, which on a multi-attribute element is the wrong
	// place to look.
	Line int
	Col  int
}

type CharData struct {
	Content string
}

func (*Element) node()  {}
func (*CharData) node() {}

func (e *Element) ChildElements() []*Element {
	var elems []*Element
	for _, c := range e.Children {
		if el, ok := c.(*Element); ok {
			elems = append(elems, el)
		}
	}
	return elems
}

func (e *Element) TextContent() string {
	var b []byte
	for _, c := range e.Children {
		if t, ok := c.(*CharData); ok {
			b = append(b, t.Content...)
		}
	}
	return string(b)
}

func (e *Element) Attr(local string) (string, bool) {
	for _, a := range e.Attrs {
		if a.Local == local && a.Namespace == "" {
			return a.Value, true
		}
	}
	return "", false
}
