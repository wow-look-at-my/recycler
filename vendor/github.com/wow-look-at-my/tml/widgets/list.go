package widgets

import (
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/wow-look-at-my/tml/widget"
)

var listAttrs = []string{"items", "selected", "cursor", "disabled"}

// list is a column of items with a single item marked. Which item is selected is a property, not state: the host owns
type list struct {
	items    []string
	selected int
	cursor   rune
	disabled bool
	state    widget.State
	measure  widget.Measurer
}

func newList(ctx widget.Context) (widget.Native, error) {
	l := &list{items: ctx.Attrs.List("items"), measure: ctx.Measure}
	var err error
	if l.selected, err = ctx.Attrs.Int("selected", -1); err != nil {
		return nil, err
	}
	if l.cursor, err = ctx.Attrs.Rune("cursor", '>'); err != nil {
		return nil, err
	}
	if l.disabled, err = ctx.Attrs.Bool("disabled", false); err != nil {
		return nil, err
	}
	return l, nil
}

func (l *list) AcceptsFocus() bool { return !l.disabled && len(l.items) > 0 }

func (l *list) SetState(state widget.State) { l.state = state }

// gutter is the cursor column plus its trailing space, kept even on unselected rows so the text does not shift
const gutter = 2

func (l *list) Measure(maxW, _ int) (int, int) {
	width := 0
	for _, item := range l.items {
		width = max(width, l.measure.Width(item)+gutter)
	}
	if maxW > 0 {
		width = min(width, maxW)
	}
	return width, len(l.items)
}

func (l *list) Render(w, _ int) string {
	rows := make([]string, 0, len(l.items))
	for i, item := range l.items {
		mark := strings.Repeat(" ", gutter)
		style := lipgloss.NewStyle()
		if i == l.selected {
			mark = string(l.cursor) + " "
			style = style.Bold(true)
			if l.state.Focused && !l.disabled {
				style = style.Reverse(true)
			}
		}
		// A row too wide for the space is cut, never wrapped: a wrapped row would take a pair of lines, push every row below it
		row := ansi.Truncate(mark+item, max(0, w), "…")
		if gap := w - l.measure.Width(row); gap > 0 {
			row += strings.Repeat(" ", gap)
		}
		rows = append(rows, style.Render(row))
	}
	if l.disabled {
		return lipgloss.NewStyle().Faint(true).Render(strings.Join(rows, "\n"))
	}
	return strings.Join(rows, "\n")
}
