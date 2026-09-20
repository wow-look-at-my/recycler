// Package inspect exposes a running TML program's current frame: what each element is, where it landed, what it drew,
package inspect

import (
	"strings"

	"github.com/charmbracelet/x/ansi"

	"github.com/wow-look-at-my/tml/layout"
	"github.com/wow-look-at-my/tml/render"
)

// Rect is a position and size in viewport cells.
type Rect struct {
	X int `json:"x"`
	Y int `json:"y"`
	W int `json:"w"`
	H int `json:"h"`
}

// Size is a measurement in cells.
type Size struct {
	W int `json:"w"`
	H int `json:"h"`
}

// Scroll is where a scrolling region's content sits, and how far it could go.
type Scroll struct {
	X    int `json:"x"`
	Y    int `json:"y"`
	MaxX int `json:"maxX"`
	MaxY int `json:"maxY"`
}

// Element is a single id-bearing element of a frame.
type Element struct {
	ID      string `json:"id"`
	Element string `json:"element"`
	Action  string `json:"action,omitempty"`
	// Focus reports whether the keyboard is on this element. Hover and Held are the pointer's doing.
	Focus bool `json:"focus"`
	Hover bool `json:"hover"`
	Held  bool `json:"held"`
	// Rect is where the element paints, in viewport coordinates. Content is the space inside its margin, border and
	Rect    Rect   `json:"rect"`
	Content Size   `json:"content"`
	Clip    Rect   `json:"clip"`
	Scroll  Scroll `json:"scroll"`
	// Text is everything this element drew, escape sequences removed. Lines is the same text split, which is what a
	Text  string   `json:"text"`
	Lines []string `json:"lines"`
	// ANSI is the styled version, present only when the caller asked for it.
	ANSI string `json:"ansi,omitempty"`
}

func toRect(r layout.Rect) Rect { return Rect{X: r.X, Y: r.Y, W: r.W, H: r.H} }

// Options change what a description carries.
type Options struct {
	// ANSI fills each element's ANSI field. It is off by default because an assertion on styled text is an assertion on
	ANSI bool
}

// Describe renders box on its own and reports where it landed. state supplies the interaction flags, which the box
func Describe(box *layout.Box, state map[string]layout.Target, opts Options) Element {
	styled := render.Render(box)
	text := ansi.Strip(styled)

	lines := []string{}
	if text != "" {
		lines = strings.Split(text, "\n")
	}

	el := Element{
		ID:      box.ID,
		Element: box.Name,
		Action:  box.Action,
		Rect:    toRect(box.Screen),
		Content: Size{W: box.Content.W, H: box.Content.H},
		Clip:    toRect(box.Clip),
		Text:    text,
		Lines:   lines,
	}
	if opts.ANSI {
		el.ANSI = styled
	}
	if t, ok := state[box.ID]; ok {
		el.Focus = t.Focus
		el.Scroll = Scroll{X: t.Scroll.X, Y: t.Scroll.Y, MaxX: t.Scroll.MaxX, MaxY: t.Scroll.MaxY}
	}
	return el
}

// Find returns the box carrying id, searching depth leading so the outermost match wins when a component reuses an id
func Find(box *layout.Box, id string) *layout.Box {
	if box == nil {
		return nil
	}
	if box.ID == id {
		return box
	}
	for _, child := range box.Children {
		if hit := Find(child, id); hit != nil {
			return hit
		}
	}
	return nil
}

// IDs lists every id in the frame, in document order.
func IDs(box *layout.Box) []string {
	var out []string
	collectIDs(box, &out)
	return out
}

func collectIDs(box *layout.Box, out *[]string) {
	if box == nil {
		return
	}
	if box.ID != "" {
		*out = append(*out, box.ID)
	}
	for _, child := range box.Children {
		collectIDs(child, out)
	}
}

// Elements describes every id-bearing element of the frame, in document order.
func Elements(box *layout.Box, state map[string]layout.Target, opts Options) []Element {
	out := []Element{}
	for _, id := range IDs(box) {
		hit := Find(box, id)
		if hit == nil {
			continue
		}
		out = append(out, Describe(hit, state, opts))
	}
	return out
}

// At returns the id of the innermost element covering the cell, or "" when nothing does. It is how a click in the
func At(box *layout.Box, x, y int) string {
	hit := ""
	var walk func(*layout.Box)
	walk = func(b *layout.Box) {
		if b == nil {
			return
		}
		if b.ID != "" && covers(b.Screen, x, y) && covers(b.Clip, x, y) {
			hit = b.ID
		}
		for _, child := range b.Children {
			walk(child)
		}
	}
	walk(box)
	return hit
}

func covers(r Rectangle, x, y int) bool {
	return x >= r.X && x < r.X+r.W && y >= r.Y && y < r.Y+r.H
}

// Rectangle is layout's rect under a name this package can range over. It exists so covers takes a single type whichever
type Rectangle = layout.Rect
