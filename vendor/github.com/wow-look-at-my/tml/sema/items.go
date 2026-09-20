package sema

import (
	"fmt"
	"strconv"

	"github.com/wow-look-at-my/tml/syntax"
)

// Both attributes that make an element an items control. They are DIRECTIVES, not widget properties: they never
const (
	attrItemsSource  = "itemsSource"
	attrItemTemplate = "itemTemplate"
)

// isItemsControl reports whether this element repeats a template over a list.
func isItemsControl(node *tnode) bool {
	for _, attr := range node.attrs {
		if attr.name == attrItemsSource || attr.name == attrItemTemplate {
			return true
		}
	}
	return false
}

// expandItems builds a single instance of a data template per item. This is the seam that lets a repeated thing be a
func (p *Program) expandItems(node *tnode, scope *evalScope, file string, stack []string) ([]*Node, error) {
	var source *Expr
	var template string
	for _, attr := range node.attrs {
		switch attr.name {
		case attrItemsSource:
			source = attr.expr
		case attrItemTemplate:
			if !attr.expr.IsLiteral() {
				return nil, &syntax.Error{Pos: attr.pos, Message: fmt.Sprintf(
					"%s names a template and cannot be an expression", attrItemTemplate)}
			}
			template = attr.expr.Source
		}
	}
	if source == nil {
		return nil, &syntax.Error{Pos: node.pos, Message: fmt.Sprintf(
			"<%s> has %s but no %s: there is nothing to repeat", node.name, attrItemTemplate, attrItemsSource)}
	}
	if template == "" {
		return nil, &syntax.Error{Pos: node.pos, Message: fmt.Sprintf(
			"<%s> has %s but no %s: there is nothing to draw an item with", node.name, attrItemsSource, attrItemTemplate)}
	}

	compiledTemplate, ok := p.resolveComponent(file, template)
	if !ok {
		return nil, &syntax.Error{Pos: node.pos, Message: fmt.Sprintf(
			"unknown %s %q", attrItemTemplate, template)}
	}
	if !compiledTemplate.def.IsData {
		return nil, &syntax.Error{Pos: node.pos, Message: fmt.Sprintf(
			"%q is a <Component>, not a <DataTemplate>; only a data template takes its values from an item", template)}
	}

	items, err := source.Eval(scope)
	if err != nil {
		return nil, &syntax.Error{Pos: node.pos, Message: fmt.Sprintf("%s: %v", attrItemsSource, err)}
	}
	if !items.Type().IsList {
		return nil, &syntax.Error{Pos: node.pos, Message: fmt.Sprintf(
			"%s needs a list, got %s", attrItemsSource, items.Type())}
	}

	var out []*Node
	for i, item := range items.Items() {
		args, err := itemArgs(item)
		if err != nil {
			return nil, &syntax.Error{Pos: node.pos, Message: fmt.Sprintf(
				"%s item %d: %v", attrItemsSource, i, err)}
		}
		// The position is offered only to a template that asked for it. Handing it to every template would make an ordinary
		if _, declared := compiledTemplate.props["index"]; declared {
			position, err := ParseValue(Type{Kind: KindInt}, strconv.Itoa(i))
			if err != nil {
				return nil, err
			}
			args["index"] = position
		}
		inner, err := p.bindProps(compiledTemplate, args, scope.tokens, node.pos)
		if err != nil {
			return nil, err
		}
		expanded, err := p.expandNodes(compiledTemplate.body, inner, compiledTemplate.file,
			&slotArgs{scope: scope, file: file}, append(stack, compiledTemplate.def.Name))
		if err != nil {
			return nil, err
		}
		out = append(out, expanded...)
	}
	return out, nil
}

// itemArgs is a single item's fields, as the values its template's properties take. A record supplies them by name. A plain
func itemArgs(item Value) (map[string]Value, error) {
	if item.Type().Kind != KindRecord {
		return map[string]Value{"value": item}, nil
	}
	args := make(map[string]Value, len(item.fields))
	for _, name := range item.FieldNames() {
		field, err := item.Field(name)
		if err != nil {
			return nil, err
		}
		args[name] = field
	}
	return args, nil
}
