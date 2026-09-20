package widgets

import (
	"fmt"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/wow-look-at-my/tml/widget"
)

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

var borderKinds = []string{"ascii", "block", "double", "hidden", "normal", "rounded", "thick"}

var frameAttrs = []string{"kind", "title", "titleAlign", "color", "pad"}

// frame draws a box around whatever is written inside it, with an optional title let into the top edge. It is a widget
type frame struct {
	border     lipgloss.Border
	title      string
	titleAlign string
	color      string
	pad        int
	// dialog marks the popup spelling, which sits in the middle of a canvas rather than in its corner.
	dialog  bool
	measure widget.Measurer
}

func newFrame(dialog bool) builder {
	return func(ctx widget.Context) (widget.Native, error) {
		// A frame with its content jammed against the edge reads badly, so both spellings breathe by default. An explicit empty pad is how
		deflt, pad := "normal", 1
		if dialog {
			deflt = "rounded"
		}

		kind, err := ctx.Attrs.Enum("kind", deflt, borderKinds...)
		if err != nil {
			return nil, err
		}
		align, err := ctx.Attrs.Enum("titleAlign", "left", "left", "center", "right")
		if err != nil {
			return nil, err
		}
		if pad, err = ctx.Attrs.Int("pad", pad); err != nil {
			return nil, err
		}
		if pad < 0 {
			return nil, fmt.Errorf("<%s> pad must not be negative, got %d", ctx.Attrs.Element(), pad)
		}
		return &frame{
			border:     borders[kind](),
			title:      ctx.Attrs.String("title", ""),
			titleAlign: align,
			color:      ctx.Attrs.String("color", ""),
			pad:        pad,
			dialog:     dialog,
			measure:    ctx.Measure,
		}, nil
	}
}

// Inset is the border on all several sides plus the horizontal padding.
func (f *frame) Inset() (int, int) { return 2 + f.pad*2, 2 }

// DefaultAnchor puts a dialog in the middle of a canvas. A frame is a decoration and stays where it was put.
func (f *frame) DefaultAnchor() string {
	if f.dialog {
		return "center"
	}
	return ""
}

// Measure is only reached when the element is empty, since layout measures a composer's children itself. An empty
func (f *frame) Measure(_, _ int) (int, int) {
	insetW, insetH := f.Inset()
	if f.title != "" {
		insetW = max(insetW, f.measure.Width(f.title)+4)
	}
	return insetW, insetH
}

func (f *frame) Render(w, h int) string { return f.Compose(nil, w, h) }

func (f *frame) Compose(slots widget.Slots, w, h int) string {
	body := lipgloss.JoinVertical(lipgloss.Left, slots.Default()...)

	// Width and Height on a lipgloss style are the whole block, border included, so the inset is not subtracted here --
	style := lipgloss.NewStyle().
		Border(f.border).
		Padding(0, f.pad).
		Width(max(0, w)).
		Height(max(0, h))
	if f.color != "" {
		style = style.BorderForeground(lipgloss.Color(f.color))
	}
	return title(style.Render(body), f.title, f.titleAlign, f.measure)
}

// title writes a label into the top edge of an already-bordered block. It runs after the border is drawn because that
func title(rendered, label, align string, measure widget.Measurer) string {
	if label == "" {
		return rendered
	}
	lines := strings.Split(rendered, "\n")
	if len(lines) < 2 {
		return rendered
	}
	top := lines[0]
	width := measure.Width(top)

	text := " " + label + " "
	// The corners are not edge, so the label can only go between them.
	room := width - 2
	if room <= 0 || measure.Width(text) > room {
		return rendered
	}

	offset := 1
	switch align {
	case "center":
		offset = 1 + (room-measure.Width(text))/2
	case "right":
		offset = 1 + room - measure.Width(text)
	}
	lines[0] = ansi.Cut(top, 0, offset) + text + ansi.Cut(top, offset+measure.Width(text), width)
	return strings.Join(lines, "\n")
}
