package layout

import (
	"github.com/wow-look-at-my/tml/sema"
	"github.com/wow-look-at-my/tml/widget"
)

// composer is the widget behind this box if it wraps children, and nil otherwise. A composer with nothing inside it is
func (b *Box) composer() widget.Composer {
	if b.Native == nil {
		return nil
	}
	composer, ok := b.Native.(widget.Composer)
	if !ok {
		return nil
	}
	return composer
}

// Composer exposes the wrapping widget to the renderer, which hands it the children it drew.
func (b *Box) Composer() widget.Composer { return b.composer() }

// Slot is which of the parent widget's slots this box was written into.
func (b *Box) Slot() string { return b.slot }

// arrangement is how this box's widget wants its children handled. The nothing value is the default: shrink to the
func arrangement(box *Box) widget.Layout {
	arranger, ok := box.Native.(widget.Arranger)
	if !ok {
		return widget.Layout{}
	}
	return arranger.Arrange()
}

// childrenHeight is how tall the children stack up, which is the content a scrolling region is a window onto.
func childrenHeight(box *Box) int {
	total := 0
	for _, child := range box.Children {
		total += child.desired.H
	}
	return total
}

func childrenWidth(box *Box) int {
	widest := 0
	for _, child := range box.Children {
		widest = max(widest, child.desired.W)
	}
	return widest
}

// measureComposer sizes a wrapping widget: its children measured against what is left after its own inset, stacked
func (e *Engine) measureComposer(box *Box, inner Constraints) Size {
	insetW, insetH := box.composer().Inset()
	want := arrangement(box)

	child := Constraints{MaxW: max(0, inner.MaxW-insetW), MaxH: max(0, inner.MaxH-insetH)}
	if want.FreeW {
		child.MaxW = 0
	}
	if want.FreeH {
		child.MaxH = 0
	}

	var content Size
	for _, kid := range box.Children {
		size := e.measure(kid, child)
		content.W = max(content.W, size.W)
		content.H += size.H
	}

	// A free axis measured the children at their full extent, which is not what the widget occupies: it takes what it is
	if want.FreeW && inner.MaxW > 0 {
		content.W = min(content.W, max(0, inner.MaxW-insetW))
	}
	if want.FreeH && inner.MaxH > 0 {
		content.H = min(content.H, max(0, inner.MaxH-insetH))
	}
	if want.FillW && inner.MaxW > 0 {
		content.W = max(0, inner.MaxW-insetW)
	}
	if want.FillH && inner.MaxH > 0 {
		content.H = max(0, inner.MaxH-insetH)
	}
	return Size{W: content.W + insetW, H: content.H + insetH}
}

func (e *Engine) arrangeComposer(box *Box, composer widget.Composer) {
	insetW, insetH := composer.Inset()
	width := max(0, box.Content.W-insetW)
	height := max(0, box.Content.H-insetH)
	want := arrangement(box)

	// A scrolling region cannot scroll past its own content: the widget draws the last screenful rather than blank space,
	var scroll Scroll
	if want.FreeH {
		total := childrenHeight(box)
		if want.ContentH > 0 {
			total = want.ContentH
		}
		scroll.MaxY = max(0, total-height)
		want.OffsetY = min(want.OffsetY, scroll.MaxY)
	}
	if want.FreeW {
		total := childrenWidth(box)
		if want.ContentW > 0 {
			total = want.ContentW
		}
		scroll.MaxX = max(0, total-width)
		want.OffsetX = min(want.OffsetX, scroll.MaxX)
	}
	want.OffsetX, want.OffsetY = max(0, want.OffsetX), max(0, want.OffsetY)
	scroll.X, scroll.Y = want.OffsetX, want.OffsetY
	box.scroll = scroll

	// Children that are a window start at the top of it: the host sliced at the offset, so shifting by it again would
	shiftX, shiftY := want.OffsetX, want.OffsetY
	if want.ContentH > 0 {
		shiftY = 0
	}
	if want.ContentW > 0 {
		shiftX = 0
	}

	y := -shiftY
	for _, child := range box.Children {
		w, h := child.desired.W, child.desired.H
		if child.width.Kind == sema.LengthStar {
			w = width
		}
		if child.height.Kind == sema.LengthStar {
			h = height
		}
		// A child may only overflow on an axis the widget declared free. That is what a scrolling region is for; anywhere
		if !want.FreeW {
			w = min(w, width)
		}
		if !want.FreeH {
			h = min(h, height)
		}
		e.arrange(child, Rect{X: -shiftX, Y: y, W: w, H: h})
		y += h
	}
}
