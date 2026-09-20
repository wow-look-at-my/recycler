package layout

import (
	"charm.land/lipgloss/v2"

	"github.com/wow-look-at-my/tml/sema"
)

// arrange assigns a final rect to a box and places its children inside it.
func (e *Engine) arrange(box *Box, rect Rect) {
	box.Rect = rect

	outerW, outerH := box.outer()
	box.Content = Size{W: max(0, rect.W-outerW), H: max(0, rect.H-outerH)}

	if composer := box.composer(); composer != nil && len(box.Children) > 0 {
		e.arrangeComposer(box, composer)
		return
	}
	if box.Native != nil {
		return
	}
	switch box.Name {
	case "Text", "Spacer", "#text":
		return
	case "Stack":
		e.arrangeStack(box)
	case "Grid":
		e.arrangeGrid(box)
	case "Canvas":
		e.arrangeCanvas(box)
	default:
		// A decorator gives each child the whole content box, clamped to what the child asked for unless it is star-sized
		for _, child := range box.Children {
			w, h := child.desired.W, child.desired.H
			if child.width.Kind == sema.LengthStar {
				w = box.Content.W
			}
			if child.height.Kind == sema.LengthStar {
				h = box.Content.H
			}
			e.arrange(child, Rect{W: min(w, box.Content.W), H: min(h, box.Content.H)})
		}
	}
}

func (e *Engine) arrangeStack(box *Box) {
	if len(box.Children) == 0 {
		return
	}
	vertical := box.vertical()
	gap := box.gap()

	available := box.Content.W
	if vertical {
		available = box.Content.H
	}
	available = max(0, available-gap*(len(box.Children)-1))

	// Fixed and auto children keep what they asked for; the remainder is shared out by star weight.
	used, weight := 0, 0
	for _, child := range box.Children {
		length, size := child.width, child.desired.W
		if vertical {
			length, size = child.height, child.desired.H
		}
		if length.Kind == sema.LengthStar {
			weight += length.Weight
			continue
		}
		used += size
	}
	leftover := max(0, available-used)

	offset := 0
	remaining := leftover
	remainingWeight := weight

	for i, child := range box.Children {
		mainLength, crossLength := child.width, child.height
		main := child.desired.W
		crossAvailable := box.Content.H
		cross := child.desired.H
		if vertical {
			mainLength, crossLength = child.height, child.width
			main = child.desired.H
			crossAvailable = box.Content.W
			cross = child.desired.W
		}

		if mainLength.Kind == sema.LengthStar {
			// The last star child takes the rounding remainder so the row or column always fills exactly.
			if remainingWeight == mainLength.Weight {
				main = remaining
			} else {
				main = leftover * mainLength.Weight / weight
			}
			remaining -= main
			remainingWeight -= mainLength.Weight
		}
		if crossLength.Kind == sema.LengthStar {
			cross = crossAvailable
		}
		cross = min(cross, crossAvailable)

		// The renderer joins these parts with the same alignment, so the rects have to carry the same offset or the geometry
		align := box.Style.Align
		if !vertical {
			align = box.Style.VAlign
		}
		crossOffset := alignOffset(align, cross, crossAvailable)

		if vertical {
			e.arrange(child, Rect{X: crossOffset, Y: offset, W: cross, H: main})
		} else {
			e.arrange(child, Rect{X: offset, Y: crossOffset, W: main, H: cross})
		}

		offset += main
		if i < len(box.Children)-1 {
			offset += gap
		}
	}
}

// alignOffset is where a child of the given size starts along the cross axis. lipgloss positions are a fraction of the free space
func alignOffset(pos lipgloss.Position, size, available int) int {
	slack := max(0, available-size)
	return min(slack, max(0, int(float64(slack)*float64(pos)+0.5)))
}
