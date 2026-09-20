package sema

import (
	"fmt"
	"sort"
	"strings"

	"github.com/wow-look-at-my/tml/syntax"
)

// Builtins are the element names the language defines itself. A host adds its own native elements on top; anything not
var Builtins = []string{
	"Stack", "Grid", "Canvas", "Box", "Text", "Spacer",
}

// Directives are element names with meaning in the language rather than in the rendered output.
var Directives = []string{"Slot", "For"}

// Program is a checked unit, ready to instantiate.
type Program struct {
	unit       *syntax.Unit
	root       *syntax.Component
	rootFile   string
	natives    map[string]bool
	slots      map[string][]string
	components map[string]*compiled
	tokens     map[string]*syntax.Token
}

// compiled is a component with its declarations parsed and its template expressions pre-parsed, so expansion never
type compiled struct {
	def   *syntax.Component
	file  string
	props map[string]*prop
	order []string
	body  []*tnode
}

type prop struct {
	name     string
	typ      Type
	required bool
	deflt    *Expr
	pos      syntax.Pos
}

// tnode is a template node with its attribute expressions parsed.
type tnode struct {
	kind     syntax.NodeKind
	name     string
	attrs    []tattr
	cond     *Expr
	condPos  syntax.Pos
	children []*tnode
	text     *Expr
	pos      syntax.Pos
}

// tattr carries its own position so an attribute-scoped diagnostic points at the attribute rather than at the element,
type tattr struct {
	name string
	expr *Expr
	pos  syntax.Pos
}

// Options control analysis.
type Options struct {
	// Natives are widget element names, on top of the builtins.
	Natives []string
	// Slots are the slot names each widget accepts, keyed by element name. A widget that takes no slot content has no
	Slots map[string][]string
}

// Analyze checks a loaded unit and prepares it for expansion. Everything that can be decided without values is decided
func Analyze(unit *syntax.Unit, opts Options) (*Program, error) {
	if unit.Root == nil {
		return nil, fmt.Errorf("nothing was loaded")
	}

	program := &Program{
		unit:       unit,
		natives:    make(map[string]bool),
		slots:      opts.Slots,
		components: make(map[string]*compiled),
		tokens:     make(map[string]*syntax.Token),
	}
	// A theme has no properties and nothing to instantiate, so it is checked for its own sake -- duplicate tokens, an
	if unit.Root.Component != nil {
		program.root = unit.Root.Component
		program.rootFile = unit.Root.Path
	}
	for _, name := range Builtins {
		program.natives[name] = true
	}
	for _, name := range opts.Natives {
		program.natives[name] = true
	}
	for _, theme := range unit.Themes {
		for _, token := range theme.Tokens {
			program.tokens[token.Name] = token
		}
	}

	for path, file := range unit.Files {
		if file.Component == nil {
			continue
		}
		if err := program.compileTree(path, file.Component); err != nil {
			return nil, err
		}
	}
	for _, component := range program.components {
		if err := program.checkComponent(component); err != nil {
			return nil, err
		}
	}
	return program, program.checkCycles()
}

func (p *Program) compileTree(path string, def *syntax.Component) error {
	if err := p.compileOne(path, def); err != nil {
		return err
	}
	for _, data := range def.DataTemplates {
		if err := p.compileTree(path, data); err != nil {
			return err
		}
	}
	return nil
}

func (p *Program) compileOne(path string, def *syntax.Component) error {
	c := &compiled{def: def, file: path, props: make(map[string]*prop)}

	for _, declared := range def.Properties {
		if _, dup := c.props[declared.Name]; dup {
			return &syntax.Error{Pos: declared.Pos, Message: fmt.Sprintf("duplicate property %q", declared.Name)}
		}
		typ, err := ParseType(declared.Type)
		if err != nil {
			return &syntax.Error{Pos: declared.Pos, Message: fmt.Sprintf("property %q: %v", declared.Name, err)}
		}
		compiledProp := &prop{name: declared.Name, typ: typ, required: declared.Required, pos: declared.Pos}
		if declared.HasDefault {
			expr, err := ParseExpr(declared.Default)
			if err != nil {
				return &syntax.Error{Pos: declared.Pos, Message: fmt.Sprintf("property %q default: %v", declared.Name, err)}
			}
			// A literal default must be valid for its type right now; a default that reads a token cannot be checked until the theme
			if expr.IsLiteral() {
				if _, err := ParseValue(typ, declared.Default); err != nil {
					return &syntax.Error{Pos: declared.Pos, Message: fmt.Sprintf("property %q default: %v", declared.Name, err)}
				}
			}
			compiledProp.deflt = expr
		}
		c.props[declared.Name] = compiledProp
		c.order = append(c.order, declared.Name)
	}

	body, err := p.compileNodes(def.Template.Children)
	if err != nil {
		return err
	}
	c.body = body
	p.components[key(path, def.Name)] = c
	return nil
}

func (p *Program) compileNodes(nodes []*syntax.Node) ([]*tnode, error) {
	out := make([]*tnode, 0, len(nodes))
	for _, node := range nodes {
		compiled, err := p.compileNode(node)
		if err != nil {
			return nil, err
		}
		out = append(out, compiled)
	}
	return out, nil
}

func (p *Program) compileNode(node *syntax.Node) (*tnode, error) {
	if node.Kind == syntax.TextNode {
		expr, err := ParseExpr(node.Text)
		if err != nil {
			return nil, &syntax.Error{Pos: node.Pos, Message: err.Error()}
		}
		return &tnode{kind: syntax.TextNode, text: expr, pos: node.Pos}, nil
	}

	compiled := &tnode{kind: syntax.ElementNode, name: node.Name, pos: node.Pos}
	for _, attr := range node.Attrs {
		expr, err := ParseExpr(attr.Value)
		if err != nil {
			return nil, &syntax.Error{Pos: attr.Pos, Message: fmt.Sprintf("attribute %q: %v", attr.Name, err)}
		}
		if attr.Name == "if" {
			compiled.cond = expr
			compiled.condPos = attr.Pos
			continue
		}
		compiled.attrs = append(compiled.attrs, tattr{name: attr.Name, expr: expr, pos: attr.Pos})
	}

	children, err := p.compileNodes(node.Children)
	if err != nil {
		return nil, err
	}
	compiled.children = children
	return compiled, nil
}

// checkComponent validates a single component's template against its own declarations.
func (p *Program) checkComponent(c *compiled) error {
	names := map[string]bool{}
	for name := range c.props {
		names[name] = true
	}
	return p.checkNodes(c, c.body, names)
}

func (p *Program) checkNodes(c *compiled, nodes []*tnode, names map[string]bool) error {
	for _, node := range nodes {
		if err := p.checkNode(c, node, names); err != nil {
			return err
		}
	}
	return nil
}

func (p *Program) checkNode(c *compiled, node *tnode, names map[string]bool) error {
	if node.kind == syntax.TextNode {
		return p.checkRefs(c, node.text, node.pos, names)
	}
	if node.cond != nil {
		if err := p.checkRefs(c, node.cond, node.condPos, names); err != nil {
			return err
		}
	}
	for _, attr := range node.attrs {
		if err := p.checkRefs(c, attr.expr, attr.pos, names); err != nil {
			return err
		}
	}

	// A `For` binds new names for the duration of its children.
	scoped := names
	if node.name == "For" {
		bound, err := forBindings(node, names)
		if err != nil {
			return err
		}
		scoped = bound
	}

	if err := p.checkElementName(c, node); err != nil {
		return err
	}
	return p.checkNodes(c, node.children, scoped)
}

// forBindings returns the names visible inside a For, which are the enclosing names plus the loop variable and
func forBindings(node *tnode, names map[string]bool) (map[string]bool, error) {
	as, ok := attrOf(node, "as")
	if !ok {
		return nil, &syntax.Error{Pos: node.pos, Message: "<For> requires an as attribute naming the loop variable"}
	}
	// `each` is normally an expression, so its presence is checked directly rather than through attrOf, which only reads
	if !hasAttr(node, "each") {
		return nil, &syntax.Error{Pos: node.pos, Message: "<For> requires an each attribute"}
	}
	bound := make(map[string]bool, len(names)+2)
	for name := range names {
		bound[name] = true
	}
	bound[as] = true
	if index, ok := attrOf(node, "index"); ok {
		bound[index] = true
	}
	return bound, nil
}

func hasAttr(node *tnode, name string) bool {
	for _, attr := range node.attrs {
		if attr.name == name {
			return true
		}
	}
	return false
}

// attrOf reads a literal attribute value at analysis time. Attributes that name a binding, such as `as`, cannot
func attrOf(node *tnode, name string) (string, bool) {
	for _, attr := range node.attrs {
		if attr.name == name && attr.expr.IsLiteral() {
			return attr.expr.Source, true
		}
	}
	return "", false
}

func (p *Program) checkRefs(c *compiled, expr *Expr, pos syntax.Pos, names map[string]bool) error {
	for _, ref := range expr.Refs() {
		if ref.Theme {
			if _, ok := p.tokens[ref.Name]; !ok {
				return &syntax.Error{Pos: pos, Message: fmt.Sprintf(
					"unknown theme token %q%s", ref.Name, didYouMean(ref.Name, tokenNames(p.tokens)))}
			}
			continue
		}
		if !names[ref.Name] {
			return &syntax.Error{Pos: pos, Message: fmt.Sprintf(
				"unknown reference %q in component %q%s", ref.Name, c.def.Name, didYouMean(ref.Name, sortedNames(names)))}
		}
	}
	return nil
}

// checkElementName resolves an element to a directive, a native element, a component in scope, or a property element
func (p *Program) checkElementName(c *compiled, node *tnode) error {
	name := node.name
	if contains(Directives, name) || p.natives[name] {
		return nil
	}
	if owner, slot, ok := strings.Cut(name, "."); ok {
		// A property element is checked against its parent when the parent is walked; here only the owner has to be
		if _, found := p.unit.Lookup(c.file, owner); found {
			return nil
		}
		if p.natives[owner] {
			return p.checkSlotName(node, owner, slot)
		}
		return &syntax.Error{Pos: node.pos, Message: fmt.Sprintf(
			"<%s> names a slot on %q, which is not a component or widget in scope", name, owner)}
	}
	if _, found := p.unit.Lookup(c.file, name); found {
		return nil
	}
	return &syntax.Error{Pos: node.pos, Message: fmt.Sprintf(
		"unknown element <%s>%s", name, didYouMean(name, p.knownElements(c.file)))}
}

// checkSlotName rejects a slot a widget does not have. A component's slots are declared in its own template and
func (p *Program) checkSlotName(node *tnode, owner, slot string) error {
	accepted := p.slots[owner]
	if len(accepted) == 0 {
		return &syntax.Error{Pos: node.pos, Message: fmt.Sprintf(
			"<%s> takes no slot content", owner)}
	}
	if contains(accepted, slot) {
		return nil
	}
	return &syntax.Error{Pos: node.pos, Message: fmt.Sprintf(
		"<%s> has no slot %q; it has %s", owner, slot, strings.Join(accepted, ", "))}
}

func (p *Program) knownElements(file string) []string {
	known := append([]string{}, Builtins...)
	known = append(known, Directives...)
	for name := range p.natives {
		if !contains(known, name) {
			known = append(known, name)
		}
	}
	known = append(known, p.unit.InScope(file)...)
	sort.Strings(known)
	return known
}

// checkCycles rejects a component that can reach itself, which would expand forever.
func (p *Program) checkCycles() error {
	var walk func(c *compiled, stack []string) error
	walk = func(c *compiled, stack []string) error {
		name := key(c.file, c.def.Name)
		if contains(stack, name) {
			return &syntax.Error{Pos: c.def.Pos, Message: fmt.Sprintf(
				"component %q instantiates itself: %s", c.def.Name, strings.Join(append(stack, name), " -> "))}
		}
		stack = append(stack, name)

		var visit func(nodes []*tnode) error
		visit = func(nodes []*tnode) error {
			for _, node := range nodes {
				if node.kind == syntax.ElementNode {
					if child, ok := p.resolveComponent(c.file, node.name); ok {
						if err := walk(child, stack); err != nil {
							return err
						}
					}
				}
				if err := visit(node.children); err != nil {
					return err
				}
			}
			return nil
		}
		return visit(c.body)
	}

	for _, c := range p.components {
		if err := walk(c, nil); err != nil {
			return err
		}
	}
	return nil
}

func (p *Program) resolveComponent(fromFile, name string) (*compiled, bool) {
	def, ok := p.unit.Lookup(fromFile, name)
	if !ok {
		return nil, false
	}
	for _, c := range p.components {
		if c.def == def {
			return c, true
		}
	}
	return nil, false
}

func key(file, name string) string { return file + "#" + name }

func contains(haystack []string, needle string) bool {
	for _, s := range haystack {
		if s == needle {
			return true
		}
	}
	return false
}

func sortedNames(set map[string]bool) []string {
	names := make([]string, 0, len(set))
	for name := range set {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func tokenNames(set map[string]*syntax.Token) []string {
	names := make([]string, 0, len(set))
	for name := range set {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// didYouMean appends the available names to a diagnostic. Listing what exists turns "unknown reference" into something
func didYouMean(_ string, available []string) string {
	if len(available) == 0 {
		return ""
	}
	return "; in scope: " + strings.Join(available, ", ")
}
