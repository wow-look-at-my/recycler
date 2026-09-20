package widgets

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/wow-look-at-my/tml/widget"
)

var sparklineAttrs = []string{"values", "max", "color"}

// sparkBars are the partial-height blocks, low to high.
var sparkBars = []rune{'▁', '▂', '▃', '▄', '▅', '▆', '▇', '█'}

// sparkline plots a series in a single row of cells.
type sparkline struct {
	values []float64
	limit  float64
	color  string
}

func newSparkline(ctx widget.Context) (widget.Native, error) {
	raw := ctx.Attrs.List("values")
	line := &sparkline{color: ctx.Attrs.String("color", "")}
	for _, field := range raw {
		n, err := strconv.ParseFloat(strings.TrimSpace(field), 64)
		if err != nil {
			return nil, fmt.Errorf("<Sparkline> values: %q is not a number", field)
		}
		line.values = append(line.values, n)
	}
	limit, err := ctx.Attrs.Float("max", 0)
	if err != nil {
		return nil, err
	}
	line.limit = limit
	return line, nil
}

// scale is the value the tallest bar stands for: the declared maximum, or the largest value when none was declared.
func (s *sparkline) scale() float64 {
	if s.limit > 0 {
		return s.limit
	}
	scale := 0.0
	for _, v := range s.values {
		scale = max(scale, v)
	}
	if scale <= 0 {
		return 1
	}
	return scale
}

func (s *sparkline) Measure(maxW, _ int) (int, int) {
	width := len(s.values)
	if maxW > 0 {
		width = min(width, maxW)
	}
	return width, 1
}

func (s *sparkline) Render(w, _ int) string {
	values := s.values
	// Too many points for the space: keep the most recent, because a series scrolls off the left in every chart anybody
	if w > 0 && len(values) > w {
		values = values[len(values)-w:]
	}
	scale := s.scale()

	var b strings.Builder
	for _, v := range values {
		index := int(min(1, max(0, v/scale)) * float64(len(sparkBars)-1))
		b.WriteRune(sparkBars[index])
	}
	return paint(s.color).Render(b.String())
}
