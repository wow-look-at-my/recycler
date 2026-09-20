package widgets

import (
	"fmt"

	"github.com/wow-look-at-my/tml/widget"
)

var progressAttrs = []string{"value", "max", "filled", "empty", "color", "trackColor", "percent"}

// progressBar fills in proportion to value.
type progressBar struct {
	value, limit  float64
	filled, empty rune
	color, track  string
	percent       bool
	measure       widget.Measurer
}

func newProgressBar(ctx widget.Context) (widget.Native, error) {
	bar := &progressBar{measure: ctx.Measure}
	var err error
	if bar.value, err = ctx.Attrs.Float("value", 0); err != nil {
		return nil, err
	}
	if bar.limit, err = ctx.Attrs.Float("max", 1); err != nil {
		return nil, err
	}
	if bar.limit <= 0 {
		return nil, fmt.Errorf("<ProgressBar> max must be greater than zero, got %v", bar.limit)
	}
	if bar.filled, err = ctx.Attrs.Rune("filled", '█'); err != nil {
		return nil, err
	}
	if bar.empty, err = ctx.Attrs.Rune("empty", '░'); err != nil {
		return nil, err
	}
	if bar.percent, err = ctx.Attrs.Bool("percent", false); err != nil {
		return nil, err
	}
	bar.color = ctx.Attrs.String("color", "")
	bar.track = ctx.Attrs.String("trackColor", "")
	return bar, nil
}

// ratio is the fill fraction, clamped: a value outside the range is the host's arithmetic being off, and a bar longer
func (b *progressBar) ratio() float64 {
	return min(1, max(0, b.value/b.limit))
}

func (b *progressBar) label() string {
	if !b.percent {
		return ""
	}
	return fmt.Sprintf(" %3d%%", int(b.ratio()*100+0.5))
}

func (b *progressBar) Measure(maxW, _ int) (int, int) {
	// A bar has no natural length: it is as long as it is allowed to be, with enough room for the label when a label exists.
	width := maxW
	if width <= 0 {
		width = 20 + b.measure.Width(b.label())
	}
	return width, 1
}

func (b *progressBar) Render(w, _ int) string {
	label := b.label()
	track := max(0, w-b.measure.Width(label))
	full := int(float64(track)*b.ratio() + 0.5)
	return paint(b.color).Render(repeat(b.filled, full)) +
		paint(b.track).Render(repeat(b.empty, track-full)) + label
}
