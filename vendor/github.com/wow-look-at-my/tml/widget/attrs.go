package widget

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/wow-look-at-my/tml/sema"
)

// Attrs are a single element's evaluated attributes. Every accessor takes the fallback to use when the attribute is absent
type Attrs struct {
	element string
	values  map[string]sema.Value
	order   []string
}

// NewAttrs builds an attribute set for an element. Order fixes the sequence Names reports, which keeps diagnostics
func NewAttrs(element string, values map[string]sema.Value, order []string) Attrs {
	return Attrs{element: element, values: values, order: order}
}

// Element is the element name these attributes came from.
func (a Attrs) Element() string { return a.element }

// Has reports whether the attribute was written at all.
func (a Attrs) Has(name string) bool {
	_, ok := a.values[name]
	return ok
}

// Names lists the attributes in document order.
func (a Attrs) Names() []string { return a.order }

// Value returns the raw typed value, for a widget that needs more than the accessors below.
func (a Attrs) Value(name string) (sema.Value, bool) {
	value, ok := a.values[name]
	return value, ok
}

// String reads a text attribute.
func (a Attrs) String(name, deflt string) string {
	value, ok := a.values[name]
	if !ok {
		return deflt
	}
	return value.String()
}

// Int reads a whole number.
func (a Attrs) Int(name string, deflt int) (int, error) {
	value, ok := a.values[name]
	if !ok {
		return deflt, nil
	}
	if value.Type().Kind == sema.KindInt && !value.Type().IsList {
		return value.Int(), nil
	}
	n, err := strconv.Atoi(strings.TrimSpace(value.String()))
	if err != nil {
		return 0, a.Errorf(name, "expected a whole number, got %q", value.String())
	}
	return n, nil
}

// Float reads a fractional number, which is what a ratio such as a progress value needs and the language's own int
func (a Attrs) Float(name string, deflt float64) (float64, error) {
	value, ok := a.values[name]
	if !ok {
		return deflt, nil
	}
	if value.Type().Kind == sema.KindInt && !value.Type().IsList {
		return float64(value.Int()), nil
	}
	f, err := strconv.ParseFloat(strings.TrimSpace(value.String()), 64)
	if err != nil {
		return 0, a.Errorf(name, "expected a number, got %q", value.String())
	}
	return f, nil
}

// Bool reads a flag.
func (a Attrs) Bool(name string, deflt bool) (bool, error) {
	value, ok := a.values[name]
	if !ok {
		return deflt, nil
	}
	if value.Type().Kind == sema.KindBool && !value.Type().IsList {
		return value.Bool(), nil
	}
	switch value.String() {
	case "true":
		return true, nil
	case "false":
		return false, nil
	default:
		return false, a.Errorf(name, "expected true or false, got %q", value.String())
	}
}

// Enum reads a word from a fixed set.
func (a Attrs) Enum(name, deflt string, allowed ...string) (string, error) {
	raw := a.String(name, deflt)
	for _, member := range allowed {
		if raw == member {
			return raw, nil
		}
	}
	return "", a.Errorf(name, "expected one of %s, got %q", strings.Join(allowed, ", "), raw)
}

// List reads a list attribute. A typed list keeps its elements; anything else splits on commas, which is the same
func (a Attrs) List(name string) []string {
	value, ok := a.values[name]
	if !ok {
		return nil
	}
	if value.Type().IsList {
		items := make([]string, 0, len(value.Items()))
		for _, item := range value.Items() {
			items = append(items, item.String())
		}
		return items
	}
	raw := value.String()
	if strings.TrimSpace(raw) == "" {
		return nil
	}
	parts := strings.Split(raw, ",")
	for i, part := range parts {
		parts[i] = strings.TrimSpace(part)
	}
	return parts
}

// Rune reads a single-character attribute, which is how a widget takes the glyph it draws with.
func (a Attrs) Rune(name string, deflt rune) (rune, error) {
	value, ok := a.values[name]
	if !ok {
		return deflt, nil
	}
	runes := []rune(value.String())
	if len(runes) != 1 {
		return 0, a.Errorf(name, "expected a single character, got %q", value.String())
	}
	return runes[0], nil
}

// Errorf reports a problem with a single attribute. It is exported because a widget built outside this package validates
func (a Attrs) Errorf(name, format string, args ...any) error {
	return fmt.Errorf("<%s> %s: %s", a.element, name, fmt.Sprintf(format, args...))
}
