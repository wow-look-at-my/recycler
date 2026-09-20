package widgets

import (
	"strings"

	"github.com/wow-look-at-my/tml/widget"
)

var ruleAttrs = []string{"orientation", "char", "title", "color"}

// rule is a divider: a line across the space it is given, optionally broken by a title.
type rule struct {
	vertical bool
	char     rune
	title    string
	color    string
	measure  widget.Measurer
}

func newRule(ctx widget.Context) (widget.Native, error) {
	orientation, err := ctx.Attrs.Enum("orientation", "horizontal", "horizontal", "vertical")
	if err != nil {
		return nil, err
	}
	r := &rule{
		vertical: orientation == "vertical",
		title:    ctx.Attrs.String("title", ""),
		measure:  ctx.Measure,
	}
	deflt := '─'
	if r.vertical {
		deflt = '│'
	}
	if r.char, err = ctx.Attrs.Rune("char", deflt); err != nil {
		return nil, err
	}
	r.color = ctx.Attrs.String("color", "")
	return r, nil
}

func (r *rule) Measure(maxW, maxH int) (int, int) {
	if r.vertical {
		return 1, max(1, maxH)
	}
	return max(r.measure.Width(r.title), maxW), 1
}

func (r *rule) Render(w, h int) string {
	style := paint(r.color)
	if r.vertical {
		lines := make([]string, max(1, h))
		for i := range lines {
			lines[i] = style.Render(string(r.char))
		}
		return strings.Join(lines, "\n")
	}
	if r.title == "" {
		return style.Render(repeat(r.char, w))
	}
	// The title sits a single cell in from the left with a space either side, which is what keeps it from touching the line it
	label := " " + r.title + " "
	lead := min(1, max(0, w-r.measure.Width(label)))
	trail := max(0, w-lead-r.measure.Width(label))
	return style.Render(repeat(r.char, lead)) + label + style.Render(repeat(r.char, trail))
}
