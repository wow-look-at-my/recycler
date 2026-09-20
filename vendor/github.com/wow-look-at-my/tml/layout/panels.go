package layout

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/wow-look-at-my/tml/sema"
	"github.com/wow-look-at-my/tml/syntax"
	"github.com/wow-look-at-my/tml/widget"
)

// anchors are the corners a canvas child can be positioned from. The anchor picks which edges the coordinates are
var anchors = []string{"topLeft", "topRight", "bottomLeft", "bottomRight", "center"}

// canvasPlacement is a single child's position on a canvas.
type canvasPlacement struct {
	x, y   int
	anchor string
}

// initCanvas reads every child's placement. A canvas child needs no attributes at all: without them it sits where its
func initCanvas(box *Box) error {
	for _, child := range box.Children {
		place := canvasPlacement{anchor: defaultAnchor(child)}
		for name, raw := range child.attrs {
			owner, property, dotted := strings.Cut(name, ".")
			if !dotted {
				continue
			}
			if owner != "Canvas" {
				return &syntax.Error{Pos: child.Pos, Message: fmt.Sprintf(
					"attached property %q only applies to a child of <%s>, but this is inside <Canvas>", name, owner)}
			}
			var err error
			switch property {
			case "x":
				place.x, err = strconv.Atoi(raw)
			case "y":
				place.y, err = strconv.Atoi(raw)
			case "anchor":
				if !contains(anchors, raw) {
					err = fmt.Errorf("expected one of %s", strings.Join(anchors, ", "))
				}
				place.anchor = raw
			default:
				err = fmt.Errorf("a canvas child takes Canvas.x, Canvas.y and Canvas.anchor")
			}
			if err != nil {
				return &syntax.Error{Pos: child.Pos, Message: fmt.Sprintf("%s: %v", name, err)}
			}
		}
		child.canvas = place
	}
	return nil
}

// defaultAnchor asks a widget where it belongs when nobody said. A dialog answers center; everything else starts in
func defaultAnchor(box *Box) string {
	anchored, ok := box.Native.(widget.Anchored)
	if !ok {
		return "topLeft"
	}
	if anchor := anchored.DefaultAnchor(); contains(anchors, anchor) {
		return anchor
	}
	return "topLeft"
}

// measureFill is what a positioning surface asks for: everything on offer. A canvas has no content of its own to
func (e *Engine) measureFill(box *Box, inner Constraints) Size {
	for _, child := range box.Children {
		e.measure(child, inner)
	}
	return Size{W: inner.MaxW, H: inner.MaxH}
}

func (e *Engine) arrangeCanvas(box *Box) {
	for _, child := range box.Children {
		w, h := child.desired.W, child.desired.H
		if child.width.Kind == sema.LengthStar {
			w = box.Content.W
		}
		if child.height.Kind == sema.LengthStar {
			h = box.Content.H
		}
		w = min(w, box.Content.W)
		h = min(h, box.Content.H)

		place := child.canvas
		x, y := place.x, place.y
		switch place.anchor {
		case "topRight":
			x = box.Content.W - w - place.x
		case "bottomLeft":
			y = box.Content.H - h - place.y
		case "bottomRight":
			x = box.Content.W - w - place.x
			y = box.Content.H - h - place.y
		case "center":
			x = (box.Content.W-w)/2 + place.x
			y = (box.Content.H-h)/2 + place.y
		}
		e.arrange(child, Rect{X: x, Y: y, W: w, H: h})
	}
}

func contains(haystack []string, needle string) bool {
	for _, s := range haystack {
		if s == needle {
			return true
		}
	}
	return false
}
