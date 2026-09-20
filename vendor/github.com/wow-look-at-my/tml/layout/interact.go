package layout

import (
	"fmt"

	"github.com/wow-look-at-my/tml/syntax"
	"github.com/wow-look-at-my/tml/widget"
)

// Target is a single interactive element in a rendered frame: what to call it, what it does, whether the keyboard can land
type Target struct {
	ID     string
	Action string
	Focus  bool
	Rect   Rect
	// Scroll is where a scrolling region ended up. It is the nothing value for anything that does not scroll.
	Scroll Scroll
}

// Scroll is how far a scrolling region's content is shifted, and how far it could be. The maximum depends on how the
type Scroll struct{ X, Y, MaxX, MaxY int }

// Interaction is the focus and pointer state a frame renders against. Layout asks it how each focusable element should
type Interaction interface {
	// States reports how each element should draw, in the order given. It is asked before anything is measured, so the
	States(targets []Target) []widget.State
	// Frame publishes the same elements for the frame just laid out, this time with the geometry they landed on.
	Frame(targets []Target)
}

// pass is the mutable state of a single layout call. Keeping it off the engine means a pair of goroutines can lay out against the
type pass struct {
	e       *Engine
	targets []*Box
	ids     map[string]syntax.Pos
}

// syncState tells every interactive widget how it is being interacted with, before anything is measured. A control
func (p *pass) syncState() {
	if p.e.opts.Interaction == nil {
		return
	}
	states := p.e.opts.Interaction.States(p.ring())
	for i, box := range p.targets {
		if i >= len(states) {
			break
		}
		box.State = states[i]
		if stateful, ok := box.Native.(widget.Stateful); ok {
			stateful.SetState(box.State)
		}
	}
}

// ring is the tracked elements without their geometry, which is all there is to report before the measure pass has run.
func (p *pass) ring() []Target {
	targets := make([]Target, 0, len(p.targets))
	for _, box := range p.targets {
		targets = append(targets, Target{ID: box.ID, Action: box.Action, Focus: box.focus})
	}
	return targets
}

// publish hands the frame's focus ring back with the geometry it landed on.
func (p *pass) publish() {
	if p.e.opts.Interaction == nil {
		return
	}
	targets := make([]Target, 0, len(p.targets))
	for _, box := range p.targets {
		// The clipped rect, not the box's own: a control scrolled out of its viewport is not on the screen, and clicking
		rect := intersect(box.Screen, box.Clip)
		if rect.W <= 0 || rect.H <= 0 {
			continue
		}
		targets = append(targets, Target{ID: box.ID, Action: box.Action, Focus: box.focus, Rect: rect, Scroll: box.scroll})
	}
	p.e.opts.Interaction.Frame(targets)
}

// track records a widget the user can reach: a widget that takes focus, or a widget that was given an id, which is how a
func (p *pass) track(box *Box) error {
	if box.ID != "" {
		if first, dup := p.ids[box.ID]; dup {
			return &syntax.Error{Pos: box.Pos, Message: fmt.Sprintf(
				"id %q is already used at %s; an id has to name one element", box.ID, first)}
		}
		if p.ids == nil {
			p.ids = map[string]syntax.Pos{}
		}
		p.ids[box.ID] = box.Pos
	}
	if focusable, ok := box.Native.(widget.Focusable); ok {
		// Implementing Focusable is a widget saying it takes input at all, so a widget that refuses focus is disabled: it is left
		box.focus = focusable.AcceptsFocus()
		if !box.focus {
			return nil
		}
	}
	if !box.focus && box.ID == "" {
		return nil
	}
	p.targets = append(p.targets, box)
	return nil
}

// setScreen converts the tree's parent-relative rects into viewport coordinates. A child's rect is relative to its
func setScreen(box *Box, originX, originY int, clip Rect) {
	margin := box.Style.Margin
	box.Screen = Rect{
		X: originX + box.Rect.X + margin.Left,
		Y: originY + box.Rect.Y + margin.Top,
		W: max(0, box.Rect.W-margin.Horizontal()),
		H: max(0, box.Rect.H-margin.Vertical()),
	}
	box.Clip = clip

	offsetX, offsetY := box.Style.ContentOffset()
	inner := clip
	if want := arrangement(box); want.FreeW || want.FreeH {
		// Everything below a scrolling region is confined to what it shows, however far its content runs past the edge.
		inner = intersect(clip, Rect{
			X: box.Screen.X + offsetX,
			Y: box.Screen.Y + offsetY,
			W: box.Content.W,
			H: box.Content.H,
		})
	}
	for _, child := range box.Children {
		setScreen(child, originX+box.Rect.X+offsetX, originY+box.Rect.Y+offsetY, inner)
	}
}

// intersect is the overlap of a pair of rects, or an empty rect when they do not touch at all.
func intersect(a, b Rect) Rect {
	x := max(a.X, b.X)
	y := max(a.Y, b.Y)
	return Rect{
		X: x,
		Y: y,
		W: max(0, min(a.X+a.W, b.X+b.W)-x),
		H: max(0, min(a.Y+a.H, b.Y+b.H)-y),
	}
}
