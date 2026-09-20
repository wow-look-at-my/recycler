package sema

import (
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/wow-look-at-my/go-containers/set"

	"github.com/wow-look-at-my/tml/syntax"
)

// Node is an element after expansion. Components, slots and control flow are gone; only native elements, panels and
type Node struct {
	Kind     syntax.NodeKind
	Name     string
	Attrs    map[string]Value
	Order    []string
	Children []*Node
	Text     string
	Pos      syntax.Pos
	// Slot is which of the parent's slots this child was written into, empty for the default slot. A component's slots are
	Slot string
	// Component marks a node that carries a component's name rather than an element's. Nothing downstream may resolve it
	Component bool
}

// Attr reads an evaluated attribute.
func (n *Node) Attr(name string) (Value, bool) {
	v, ok := n.Attrs[name]
	return v, ok
}

// ExpandOptions control instantiation.
type ExpandOptions struct {
	// Dark selects the dark half of every adaptive theme token.
	Dark bool
}

// evalScope is the set of names visible inside a single component instance. Components do not nest lexically: an instance
type evalScope struct {
	props  map[string]Value
	tokens map[string]Value
}

func (s *evalScope) Lookup(name string) (Value, bool) { v, ok := s.props[name]; return v, ok }
func (s *evalScope) Token(name string) (Value, bool)  { v, ok := s.tokens[name]; return v, ok }

func (s *evalScope) with(name string, value Value) *evalScope {
	props := make(map[string]Value, len(s.props)+1)
	for k, v := range s.props {
		props[k] = v
	}
	props[name] = value
	return &evalScope{props: props, tokens: s.tokens}
}

// slotArgs is the content a call site passed into a component, together with everything needed to expand it. Slot
type slotArgs struct {
	content map[string][]*tnode
	scope   *evalScope
	file    string
	outer   *slotArgs
}

const defaultSlot = ""

// Expand instantiates the entry component with the given arguments.
func (p *Program) Expand(args map[string]Value, opts ExpandOptions) (*Node, error) {
	if p.root == nil {
		return nil, fmt.Errorf("this file declares a theme, not a component; there is nothing to expand or render")
	}
	tokens, err := p.resolveTokens(opts)
	if err != nil {
		return nil, err
	}
	entry, ok := p.resolveComponent(p.rootFile, p.root.Name)
	if !ok {
		return nil, fmt.Errorf("entry component %q was not compiled", p.root.Name)
	}

	scope, err := p.bindProps(entry, args, tokens, entry.def.Pos)
	if err != nil {
		return nil, err
	}
	children, err := p.expandNodes(entry.body, scope, entry.file, nil, []string{entry.def.Name})
	if err != nil {
		return nil, err
	}
	return &Node{
		Kind:     syntax.ElementNode,
		Name:     entry.def.Name,
		Attrs:    map[string]Value{},
		Children: children,
		Pos:      entry.def.Pos,
		// The root wears the component's name, which is what makes a dumped tree readable. Marking it keeps that name from
		Component: true,
	}, nil
}

// Tokens returns the theme tokens as plain text for the given mode. The style package needs them before any component
func (p *Program) Tokens(opts ExpandOptions) map[string]string {
	resolved, _ := p.resolveTokens(opts)
	out := make(map[string]string, len(resolved))
	for name, value := range resolved {
		out[name] = value.String()
	}
	return out
}

// resolveTokens picks the light or dark half of every adaptive token.
func (p *Program) resolveTokens(opts ExpandOptions) (map[string]Value, error) {
	tokens := make(map[string]Value, len(p.tokens))
	for name, token := range p.tokens {
		switch {
		case token.Light != "" || token.Dark != "":
			if opts.Dark {
				tokens[name] = StringValue(token.Dark)
			} else {
				tokens[name] = StringValue(token.Light)
			}
		default:
			tokens[name] = StringValue(token.Value)
		}
	}
	return tokens, nil
}

// bindProps turns call-site arguments into an instance scope, applying defaults and enforcing required properties.
func (p *Program) bindProps(c *compiled, args map[string]Value, tokens map[string]Value, at syntax.Pos) (*evalScope, error) {
	scope := &evalScope{props: make(map[string]Value, len(c.props)), tokens: tokens}

	for name := range args {
		if _, declared := c.props[name]; !declared {
			return nil, &syntax.Error{Pos: at, Message: fmt.Sprintf(
				"component %q has no property %q%s", c.def.Name, name, didYouMean(name, c.order))}
		}
	}
	for _, name := range c.order {
		declared := c.props[name]
		if given, ok := args[name]; ok {
			value, err := coerce(declared.typ, given)
			if err != nil {
				return nil, &syntax.Error{Pos: at, Message: fmt.Sprintf("property %q: %v", name, err)}
			}
			scope.props[name] = value
			continue
		}
		if declared.required {
			return nil, &syntax.Error{Pos: at, Message: fmt.Sprintf(
				"component %q requires property %q", c.def.Name, name)}
		}
		if declared.deflt == nil {
			return nil, &syntax.Error{Pos: at, Message: fmt.Sprintf(
				"property %q has no value and no default", name)}
		}
		// A default sees the theme but not the instance's other properties, so defaults never depend on the order they are
		raw, err := declared.deflt.Eval(&evalScope{tokens: tokens})
		if err != nil {
			return nil, &syntax.Error{Pos: declared.pos, Message: fmt.Sprintf("property %q default: %v", name, err)}
		}
		value, err := coerce(declared.typ, raw)
		if err != nil {
			return nil, &syntax.Error{Pos: declared.pos, Message: fmt.Sprintf("property %q default: %v", name, err)}
		}
		scope.props[name] = value
	}
	return scope, nil
}

// coerce fits a value to a declared type. A string is re-read as the target type, which is what makes a literal
func coerce(target Type, value Value) (Value, error) {
	if value.typ.Kind == target.Kind && value.typ.IsList == target.IsList &&
		slices.Equal(value.typ.Enum, target.Enum) {
		return value, nil
	}
	if value.typ.Kind == KindString && !value.typ.IsList {
		return ParseValue(target, value.str)
	}
	return Value{}, fmt.Errorf("expected %s, got %s", target, value.typ)
}

func (p *Program) expandNodes(nodes []*tnode, scope *evalScope, file string, slots *slotArgs, stack []string) ([]*Node, error) {
	var out []*Node
	for _, node := range nodes {
		expanded, err := p.expandNode(node, scope, file, slots, stack)
		if err != nil {
			return nil, err
		}
		out = append(out, expanded...)
	}
	return out, nil
}

// expandNode returns nothing or more nodes: a conditional can drop out, and a For or a Slot can produce several.
func (p *Program) expandNode(node *tnode, scope *evalScope, file string, slots *slotArgs, stack []string) ([]*Node, error) {
	if node.kind == syntax.TextNode {
		value, err := node.text.Eval(scope)
		if err != nil {
			return nil, &syntax.Error{Pos: node.pos, Message: err.Error()}
		}
		return []*Node{{Kind: syntax.TextNode, Text: value.String(), Pos: node.pos}}, nil
	}

	if node.cond != nil {
		value, err := node.cond.Eval(scope)
		if err != nil {
			return nil, &syntax.Error{Pos: node.pos, Message: fmt.Sprintf("if: %v", err)}
		}
		keep, err := value.Truthy()
		if err != nil {
			return nil, &syntax.Error{Pos: node.pos, Message: fmt.Sprintf("if: %v", err)}
		}
		if !keep {
			return nil, nil
		}
	}

	switch {
	case node.name == "For":
		return p.expandFor(node, scope, file, slots, stack)
	case node.name == "Slot":
		return p.expandSlot(node, scope, file, slots, stack)
	case strings.Contains(node.name, "."):
		// A property element is consumed by its parent as slot content and is never expanded in place.
		return nil, &syntax.Error{Pos: node.pos, Message: fmt.Sprintf(
			"<%s> is slot content and must be a direct child of a <%s> element",
			node.name, strings.SplitN(node.name, ".", 2)[0])}
	}

	if component, ok := p.resolveComponent(file, node.name); ok {
		return p.expandInstance(component, node, scope, file, slots, stack)
	}
	return p.expandNative(node, scope, file, slots, stack)
}

func (p *Program) expandNative(node *tnode, scope *evalScope, file string, slots *slotArgs, stack []string) ([]*Node, error) {
	out := &Node{Kind: syntax.ElementNode, Name: node.name, Attrs: map[string]Value{}, Pos: node.pos}
	for _, attr := range node.attrs {
		// The items-control attributes are directives: they say what this element CONTAINS, and a widget that received them
		if attr.name == attrItemsSource || attr.name == attrItemTemplate {
			continue
		}
		value, err := attr.expr.Eval(scope)
		if err != nil {
			return nil, &syntax.Error{Pos: attr.pos, Message: fmt.Sprintf("attribute %q: %v", attr.name, err)}
		}
		out.Attrs[attr.name] = value
		out.Order = append(out.Order, attr.name)
	}

	if isItemsControl(node) {
		items, err := p.expandItems(node, scope, file, stack)
		if err != nil {
			return nil, err
		}
		out.Children = append(out.Children, items...)
	}
	// A native element's slots stay slots: unlike a component, whose template decides where the content goes, a widget is
	content, err := collectSlotContent(node)
	if err != nil {
		return nil, err
	}
	for _, name := range slotOrder(node, content) {
		children, err := p.expandNodes(content[name], scope, file, slots, stack)
		if err != nil {
			return nil, err
		}
		for _, child := range children {
			child.Slot = name
			out.Children = append(out.Children, child)
		}
	}
	return []*Node{out}, nil
}

// slotOrder lists a native element's filled slots in document order, so the children come out in the sequence they
func slotOrder(node *tnode, content map[string][]*tnode) []string {
	order := make([]string, 0, len(content))
	seen := set.New[string]()
	for _, child := range node.children {
		name := defaultSlot
		if owner, slot, isProperty := strings.Cut(child.name, "."); isProperty && owner == node.name {
			name = slot
		}
		if _, filled := content[name]; filled && seen.Add(name) {
			order = append(order, name)
		}
	}
	return order
}

func (p *Program) expandInstance(c *compiled, node *tnode, scope *evalScope, file string, slots *slotArgs, stack []string) ([]*Node, error) {
	if slices.Contains(stack, c.def.Name) {
		return nil, &syntax.Error{Pos: node.pos, Message: fmt.Sprintf(
			"component %q instantiates itself: %s", c.def.Name, strings.Join(append(stack, c.def.Name), " -> "))}
	}

	args := make(map[string]Value, len(node.attrs))
	for _, attr := range node.attrs {
		value, err := attr.expr.Eval(scope)
		if err != nil {
			return nil, &syntax.Error{Pos: attr.pos, Message: fmt.Sprintf("attribute %q: %v", attr.name, err)}
		}
		args[attr.name] = value
	}

	content, err := collectSlotContent(node)
	if err != nil {
		return nil, err
	}
	inner, err := p.bindProps(c, args, scope.tokens, node.pos)
	if err != nil {
		return nil, err
	}

	passed := &slotArgs{content: content, scope: scope, file: file, outer: slots}
	return p.expandNodes(c.body, inner, c.file, passed, append(stack, c.def.Name))
}

// collectSlotContent splits a call site's children into named slots and the default slot. `<Card.actions>` fills the
func collectSlotContent(node *tnode) (map[string][]*tnode, error) {
	content := map[string][]*tnode{}
	for _, child := range node.children {
		owner, slot, isProperty := strings.Cut(child.name, ".")
		if child.kind != syntax.ElementNode || !isProperty {
			content[defaultSlot] = append(content[defaultSlot], child)
			continue
		}
		if owner != node.name {
			return nil, &syntax.Error{Pos: child.pos, Message: fmt.Sprintf(
				"<%s> names a slot on %q but is inside <%s>", child.name, owner, node.name)}
		}
		if slot == "" {
			return nil, &syntax.Error{Pos: child.pos, Message: fmt.Sprintf("<%s> names no slot", child.name)}
		}
		if _, dup := content[slot]; dup {
			return nil, &syntax.Error{Pos: child.pos, Message: fmt.Sprintf("slot %q is filled twice", slot)}
		}
		content[slot] = child.children
	}
	return content, nil
}

// expandSlot inserts the caller's content, or the slot's own fallback children when the caller supplied none.
func (p *Program) expandSlot(node *tnode, scope *evalScope, file string, slots *slotArgs, stack []string) ([]*Node, error) {
	name := defaultSlot
	if declared, ok := attrOf(node, "name"); ok {
		name = declared
	}

	if slots != nil {
		if content, ok := slots.content[name]; ok && len(content) > 0 {
			// Content belongs to the call site: it sees the caller's names, the caller's file for resolving components, and the
			return p.expandNodes(content, slots.scope, slots.file, slots.outer, stack)
		}
	}
	if required, ok := attrOf(node, "required"); ok && required == "true" {
		return nil, &syntax.Error{Pos: node.pos, Message: fmt.Sprintf("slot %q is required but was not filled", name)}
	}
	return p.expandNodes(node.children, scope, file, slots, stack)
}

func (p *Program) expandFor(node *tnode, scope *evalScope, file string, slots *slotArgs, stack []string) ([]*Node, error) {
	as, ok := attrOf(node, "as")
	if !ok {
		return nil, &syntax.Error{Pos: node.pos, Message: "<For> requires an as attribute naming the loop variable"}
	}
	var each *Expr
	for _, attr := range node.attrs {
		if attr.name == "each" {
			each = attr.expr
		}
	}
	if each == nil {
		return nil, &syntax.Error{Pos: node.pos, Message: "<For> requires an each attribute"}
	}

	value, err := each.Eval(scope)
	if err != nil {
		return nil, &syntax.Error{Pos: node.pos, Message: fmt.Sprintf("each: %v", err)}
	}
	if !value.Type().IsList {
		return nil, &syntax.Error{Pos: node.pos, Message: fmt.Sprintf(
			"<For each> needs a list, got %s", value.Type())}
	}

	indexName, hasIndex := attrOf(node, "index")

	var out []*Node
	for i, item := range value.Items() {
		iteration := scope.with(as, item)
		if hasIndex {
			indexValue, err := ParseValue(Type{Kind: KindInt}, strconv.Itoa(i))
			if err != nil {
				return nil, err
			}
			iteration = iteration.with(indexName, indexValue)
		}
		expanded, err := p.expandNodes(node.children, iteration, file, slots, stack)
		if err != nil {
			return nil, err
		}
		out = append(out, expanded...)
	}
	return out, nil
}
