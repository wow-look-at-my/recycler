package widgets

import (
	"strings"

	"charm.land/lipgloss/v2"

	"github.com/wow-look-at-my/tml/widget"
)

var buttonAttrs = []string{"label", "disabled", "variant"}

// buttonSlots is the region a button's content goes in. A button is a frame around something, and that something is
var buttonSlots = []string{"Content"}

// variants are the looks a button comes in, as a foreground colour for its border and label. Anything more specific is
var variants = map[string]string{
	"default": "",
	"primary": "12",
	"danger":  "9",
}

var variantNames = []string{"danger", "default", "primary"}

// button is a control the keyboard and the pointer can reach. It is a composer so its content can be anything:
type button struct {
	label    string
	disabled bool
	accent   string
	state    widget.State
	measure  widget.Measurer
}

func newButton(ctx widget.Context) (widget.Native, error) {
	variant, err := ctx.Attrs.Enum("variant", "default", variantNames...)
	if err != nil {
		return nil, err
	}
	disabled, err := ctx.Attrs.Bool("disabled", false)
	if err != nil {
		return nil, err
	}
	return &button{
		label:    ctx.Attrs.String("label", ""),
		disabled: disabled,
		accent:   variants[variant],
		measure:  ctx.Measure,
	}, nil
}

// AcceptsFocus is false while disabled, which is what keeps tab from stopping on a control that would do nothing.
func (b *button) AcceptsFocus() bool { return !b.disabled }

func (b *button) SetState(state widget.State) { b.state = state }

// Inset is the border plus a space either side of the content.
func (b *button) Inset() (int, int) { return 4, 2 }

func (b *button) Slots() []string { return buttonSlots }

func (b *button) Measure(maxW, _ int) (int, int) {
	insetW, insetH := b.Inset()
	width := b.measure.Width(b.label) + insetW
	if maxW > 0 {
		width = min(width, maxW)
	}
	return width, 1 + insetH
}

func (b *button) Render(w, h int) string { return b.Compose(nil, w, h) }

func (b *button) Compose(slots widget.Slots, w, h int) string {
	content := slots.Get("Content")
	if len(content) == 0 {
		content = slots.Default()
	}
	// The label is the content when nothing was written inside, which is what makes the common case a single attribute rather
	body := strings.Join(content, "\n")
	if len(content) == 0 {
		body = b.label
	}

	// Width and Height are the whole block, border included: the inset was already spent when layout sized the content.
	style := lipgloss.NewStyle().
		Border(b.border()).
		Padding(0, 1).
		Align(lipgloss.Center).
		Width(max(0, w)).
		Height(max(0, h))

	switch {
	case b.disabled:
		style = style.Faint(true).BorderForeground(lipgloss.Color("8"))
	case b.state.Pressed:
		style = style.Reverse(true)
	case b.state.Focused:
		style = style.Bold(true).BorderForeground(lipgloss.Color(b.focusColor()))
	case b.state.Hovered:
		style = style.Underline(true)
	case b.accent != "":
		style = style.Foreground(lipgloss.Color(b.accent)).BorderForeground(lipgloss.Color(b.accent))
	}
	return style.Render(body)
}

// border is doubled while focused, so the focused control is obvious without relying on colour alone.
func (b *button) border() lipgloss.Border {
	if b.state.Focused && !b.disabled {
		return lipgloss.DoubleBorder()
	}
	return lipgloss.RoundedBorder()
}

func (b *button) focusColor() string {
	if b.accent != "" {
		return b.accent
	}
	return "13"
}
