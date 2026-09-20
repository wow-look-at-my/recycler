package layout

import (
	"charm.land/lipgloss/v2"

	"github.com/wow-look-at-my/tml/sema"
)

// measure computes each box's desired outer size within the given constraints.
func (e *Engine) measure(box *Box, c Constraints) Size {
	outerW, outerH := box.outer()
	inner := Constraints{MaxW: max(0, c.MaxW-outerW), MaxH: max(0, c.MaxH-outerH)}

	// An explicit size is a constraint on the children too, not just on the box itself. Measuring them against the
	if box.width.Kind == sema.LengthCells {
		inner.MaxW = min(inner.MaxW, max(0, box.width.Cells-outerW))
	}
	if box.height.Kind == sema.LengthCells {
		inner.MaxH = min(inner.MaxH, max(0, box.height.Cells-outerH))
	}

	var content Size
	switch {
	case len(box.Children) > 0 && box.composer() != nil:
		content = e.measureComposer(box, inner)
	case box.Native != nil:
		// A widget measures itself. TML supplies the space and takes the answer, so a bubbles component keeps deciding its
		w, h := box.Native.Measure(inner.MaxW, inner.MaxH)
		content = Size{W: w, H: h}
	default:
		content = e.measureContent(box, inner)
	}

	// An explicit size overrides what the content asked for. A star size cannot be known until the parent shares out its
	switch box.width.Kind {
	case sema.LengthCells:
		content.W = max(0, box.width.Cells-outerW)
	case sema.LengthStar:
		content.W = 0
	}
	switch box.height.Kind {
	case sema.LengthCells:
		content.H = max(0, box.height.Cells-outerH)
	case sema.LengthStar:
		content.H = 0
	}

	box.desired = Size{W: content.W + outerW, H: content.H + outerH}
	return box.desired
}

func (e *Engine) measureContent(box *Box, inner Constraints) Size {
	var content Size
	switch box.Name {
	case "#text":
		content = Size{W: e.opts.Measure.Width(box.Text), H: lipgloss.Height(box.Text)}
	case "Text":
		content = e.measureText(box, inner)
	case "Spacer":
		content = Size{}
	case "Stack":
		content = e.measureStack(box, inner)
	case "Grid":
		content = e.measureGrid(box, inner)
	case "Canvas":
		content = e.measureFill(box, inner)
	default:
		content = e.measureChildren(box, inner)
	}
	return content
}

// measureText reports the height the text needs after wrapping, because lipgloss wraps rather than truncates and the
func (e *Engine) measureText(box *Box, inner Constraints) Size {
	if box.Text == "" {
		return Size{}
	}
	natural := e.opts.Measure.Width(box.Text)
	if inner.MaxW <= 0 || natural <= inner.MaxW {
		return Size{W: natural, H: lipgloss.Height(box.Text)}
	}
	// A clipped Text is as tall as it has lines, however narrow it gets, so a long line cannot push a card's border down.
	if box.Overflow == OverflowClip || box.Overflow == OverflowEllipsis {
		return Size{W: inner.MaxW, H: lipgloss.Height(box.Text)}
	}
	// lipgloss.Wrap is the call Style.Render makes to wrap, so this counts the lines the paint will produce. Rendering a
	wrapped := lipgloss.Wrap(box.Text, inner.MaxW, "")
	return Size{W: inner.MaxW, H: lipgloss.Height(wrapped)}
}

func (e *Engine) measureChildren(box *Box, inner Constraints) Size {
	var content Size
	for _, child := range box.Children {
		size := e.measure(child, inner)
		content.W = max(content.W, size.W)
		content.H = max(content.H, size.H)
	}
	return fillForStars(box, content, inner)
}

// fillForStars propagates a star size up the tree. A star child fills the space its parent gives it, so a parent that
func fillForStars(box *Box, content Size, inner Constraints) Size {
	for _, child := range box.Children {
		if child.width.Kind == sema.LengthStar {
			content.W = max(content.W, inner.MaxW)
		}
		if child.height.Kind == sema.LengthStar {
			content.H = max(content.H, inner.MaxH)
		}
	}
	return content
}

func (e *Engine) measureStack(box *Box, inner Constraints) Size {
	vertical := box.vertical()
	gap := box.gap()

	var content Size
	for i, child := range box.Children {
		size := e.measure(child, inner)
		if vertical {
			content.H += size.H
			content.W = max(content.W, size.W)
			if i > 0 {
				content.H += gap
			}
		} else {
			content.W += size.W
			content.H = max(content.H, size.H)
			if i > 0 {
				content.W += gap
			}
		}
	}
	return fillForStars(box, content, inner)
}
