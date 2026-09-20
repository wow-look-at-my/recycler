package widgets

import (
	"github.com/wow-look-at-my/tml/widget"
)

var badgeAttrs = []string{"label"}

// badge is a short label with breathing room, for a status chip or a count. Its colours come from the element's own
type badge struct {
	label   string
	measure widget.Measurer
}

func newBadge(ctx widget.Context) (widget.Native, error) {
	return &badge{label: " " + ctx.Attrs.String("label", "") + " ", measure: ctx.Measure}, nil
}

func (b *badge) Measure(_, _ int) (int, int) { return b.measure.Width(b.label), 1 }

func (b *badge) Render(_, _ int) string { return b.label }
