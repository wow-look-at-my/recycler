// Package style resolves TML style attributes into a lipgloss.Style. The cascade lives here rather than in lipgloss.
package style

import (
	"fmt"
	"maps"
	"sort"
	"strings"

	"charm.land/lipgloss/v2"

	"github.com/wow-look-at-my/tml/sema"
	"github.com/wow-look-at-my/tml/syntax"
)

// Resolved is a single element's finished styling. Margin is kept out of the lipgloss.Style on purpose: lipgloss treats the
type Resolved struct {
	Style  lipgloss.Style
	Margin sema.Thickness
	Align  lipgloss.Position
	VAlign lipgloss.Position
}

// Frame is the space padding and borders take from the content box.
func (r Resolved) Frame() (horizontal, vertical int) {
	return r.Style.GetHorizontalFrameSize(), r.Style.GetVerticalFrameSize()
}

// ContentOffset is the distance from a box's outer top-left corner to the leading cell of its content. A child's rect is
func (r Resolved) ContentOffset() (x, y int) {
	x = r.Margin.Left + r.Style.GetPaddingLeft() + r.Style.GetBorderLeftSize()
	y = r.Margin.Top + r.Style.GetPaddingTop() + r.Style.GetBorderTopSize()
	return x, y
}

// Sheet holds the named styles declared across every theme in scope, with each extends chain already flattened.
type Sheet struct {
	styles map[string]map[string]string
}

// NewSheet flattens the named styles from the given themes. Token references in style attributes are resolved here, so
func NewSheet(themes []*syntax.Theme, tokens map[string]string) (*Sheet, error) {
	declared := map[string]*syntax.Style{}
	for _, theme := range themes {
		for _, style := range theme.Styles {
			if existing, dup := declared[style.Name]; dup {
				return nil, &syntax.Error{Pos: style.Pos, Message: fmt.Sprintf(
					"style %q is already declared at %s", style.Name, existing.Pos)}
			}
			declared[style.Name] = style
		}
	}

	sheet := &Sheet{styles: make(map[string]map[string]string, len(declared))}
	for name := range declared {
		if _, err := sheet.flatten(declared, name, tokens, nil); err != nil {
			return nil, err
		}
	}
	return sheet, nil
}

func (s *Sheet) flatten(declared map[string]*syntax.Style, name string, tokens map[string]string, stack []string) (map[string]string, error) {
	if done, ok := s.styles[name]; ok {
		return done, nil
	}
	style, ok := declared[name]
	if !ok {
		return nil, fmt.Errorf("unknown style %q", name)
	}
	for _, seen := range stack {
		if seen == name {
			return nil, &syntax.Error{Pos: style.Pos, Message: fmt.Sprintf(
				"style %q extends itself: %s", name, strings.Join(append(stack, name), " -> "))}
		}
	}

	attrs := map[string]string{}
	if style.Extends != "" {
		parent, err := s.flatten(declared, style.Extends, tokens, append(stack, name))
		if err != nil {
			if _, isSyntax := err.(*syntax.Error); isSyntax {
				return nil, err
			}
			return nil, &syntax.Error{Pos: style.Pos, Message: fmt.Sprintf("style %q: %v", name, err)}
		}
		maps.Copy(attrs, parent)
	}
	for _, attr := range style.Attrs {
		value, err := resolveTokens(attr.Value, tokens)
		if err != nil {
			return nil, &syntax.Error{Pos: style.Pos, Message: fmt.Sprintf("style %q attribute %q: %v", name, attr.Name, err)}
		}
		attrs[attr.Name] = value
	}

	s.styles[name] = attrs
	return attrs, nil
}

// resolveTokens evaluates the theme references a style attribute may contain. Styles live in a theme, so a token is
func resolveTokens(raw string, tokens map[string]string) (string, error) {
	expr, err := sema.ParseExpr(raw)
	if err != nil {
		return "", err
	}
	if expr.IsLiteral() {
		return raw, nil
	}
	var b strings.Builder
	for _, segment := range expr.Segments {
		if segment.Ref == nil {
			b.WriteString(segment.Literal)
			continue
		}
		if !segment.Ref.Theme {
			return "", fmt.Errorf("a style can only reference theme tokens, got %s", segment.Ref)
		}
		value, ok := tokens[segment.Ref.Name]
		if !ok {
			return "", fmt.Errorf("unknown theme token %q", segment.Ref.Name)
		}
		b.WriteString(value)
	}
	return b.String(), nil
}

// Resolve builds the finished style for an element: the named style when the element names a style, with inline attributes layered on
func (s *Sheet) Resolve(named string, inline map[string]string) (Resolved, error) {
	attrs := map[string]string{}
	if named != "" {
		base, ok := s.styles[named]
		if !ok {
			return Resolved{}, fmt.Errorf("unknown style %q", named)
		}
		maps.Copy(attrs, base)
	}
	maps.Copy(attrs, inline)
	return build(attrs)
}

// build turns a flattened attribute map into a lipgloss style.
func build(attrs map[string]string) (Resolved, error) {
	resolved := Resolved{Style: lipgloss.NewStyle(), Align: lipgloss.Left, VAlign: lipgloss.Top}

	for name, raw := range attrs {
		var err error
		switch name {
		case "fg":
			resolved.Style = resolved.Style.Foreground(lipgloss.Color(raw))
		case "bg":
			resolved.Style = resolved.Style.Background(lipgloss.Color(raw))
		case "bold", "italic", "underline", "strikethrough", "faint", "reverse":
			err = applyTextFlag(&resolved, name, raw)
		case "padding":
			var thick sema.Thickness
			if thick, err = sema.ParseThickness(raw); err == nil {
				resolved.Style = resolved.Style.Padding(thick.Top, thick.Right, thick.Bottom, thick.Left)
			}
		case "margin":
			resolved.Margin, err = sema.ParseThickness(raw)
		case "border":
			err = applyBorder(&resolved, raw)
		case "borderColor":
			resolved.Style = resolved.Style.BorderForeground(lipgloss.Color(raw))
		case "align":
			resolved.Align, err = parseHorizontal(raw)
			if err == nil {
				resolved.Style = resolved.Style.AlignHorizontal(resolved.Align)
			}
		case "valign":
			resolved.VAlign, err = parseVertical(raw)
			if err == nil {
				resolved.Style = resolved.Style.AlignVertical(resolved.VAlign)
			}
		default:
			err = fmt.Errorf("unknown style attribute %q", name)
		}
		if err != nil {
			return Resolved{}, err
		}
	}
	return resolved, nil
}

func applyTextFlag(resolved *Resolved, name, raw string) error {
	on, err := parseBool(raw)
	if err != nil {
		return fmt.Errorf("%s: %w", name, err)
	}
	switch name {
	case "bold":
		resolved.Style = resolved.Style.Bold(on)
	case "italic":
		resolved.Style = resolved.Style.Italic(on)
	case "underline":
		resolved.Style = resolved.Style.Underline(on)
	case "strikethrough":
		resolved.Style = resolved.Style.Strikethrough(on)
	case "faint":
		resolved.Style = resolved.Style.Faint(on)
	case "reverse":
		resolved.Style = resolved.Style.Reverse(on)
	}
	return nil
}

// borders are named rather than described, so a template never spells out box drawing characters.
var borders = map[string]func() lipgloss.Border{
	"normal":  lipgloss.NormalBorder,
	"rounded": lipgloss.RoundedBorder,
	"thick":   lipgloss.ThickBorder,
	"double":  lipgloss.DoubleBorder,
	"hidden":  lipgloss.HiddenBorder,
	"block":   lipgloss.BlockBorder,
	"ascii":   lipgloss.ASCIIBorder,
}

func applyBorder(resolved *Resolved, raw string) error {
	if raw == "none" {
		return nil
	}
	border, ok := borders[raw]
	if !ok {
		return fmt.Errorf("unknown border %q; want none, %s", raw, strings.Join(borderNames(), ", "))
	}
	resolved.Style = resolved.Style.Border(border())
	return nil
}

// borderNames is sorted so the diagnostic is stable rather than map-ordered.
func borderNames() []string {
	names := make([]string, 0, len(borders))
	for name := range borders {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func parseHorizontal(raw string) (lipgloss.Position, error) {
	switch raw {
	case "left":
		return lipgloss.Left, nil
	case "center":
		return lipgloss.Center, nil
	case "right":
		return lipgloss.Right, nil
	default:
		return 0, fmt.Errorf("align must be left, center or right, got %q", raw)
	}
}

func parseVertical(raw string) (lipgloss.Position, error) {
	switch raw {
	case "top":
		return lipgloss.Top, nil
	case "middle":
		return lipgloss.Center, nil
	case "bottom":
		return lipgloss.Bottom, nil
	default:
		return 0, fmt.Errorf("valign must be top, middle or bottom, got %q", raw)
	}
}

func parseBool(raw string) (bool, error) {
	switch raw {
	case "true":
		return true, nil
	case "false":
		return false, nil
	default:
		return false, fmt.Errorf("expected true or false, got %q", raw)
	}
}
