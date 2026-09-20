// Package layout turns an expanded TML tree into sized, positioned boxes. A pair of passes, as in XAML: measure asks every
package layout

import (
	"fmt"
	"io/fs"
	"path"
	"strconv"
	"strings"

	"github.com/wow-look-at-my/go-containers/set"

	"github.com/wow-look-at-my/tml/sema"
	"github.com/wow-look-at-my/tml/style"
	"github.com/wow-look-at-my/tml/syntax"
	"github.com/wow-look-at-my/tml/widget"
)

// Size is a measurement in terminal cells.
type Size struct{ W, H int }

// Rect is a position and size relative to the parent's content origin.
type Rect struct{ X, Y, W, H int }

// Constraints is the space available to a node.
type Constraints struct{ MaxW, MaxH int }

// Overflow is what a Text does with a line wider than the space it is given.
type Overflow string

const (
	// OverflowWrap continues the line on the next row, which is lipgloss's own behavior and the default here.
	OverflowWrap Overflow = "wrap"
	// OverflowClip cuts the line at the edge.
	OverflowClip Overflow = "clip"
	// OverflowEllipsis cuts it earlier and marks the cut.
	OverflowEllipsis Overflow = "ellipsis"
)

// Box is a laid-out node.
type Box struct {
	Name string
	// Rect is the box's outer rect, margin included, relative to the parent's content origin.
	Rect Rect
	// Screen is the same box in viewport coordinates, with margin excluded so it covers the cells the box actually
	Screen Rect
	// Clip is the region an ancestor still shows. It is the viewport for anything outside a Scrollbox, and the visible
	Clip Rect
	// Content is the size available inside margin, border and padding. It is what a bound widget is told to render into.
	Content Size
	Style   style.Resolved
	Text    string
	// Overflow is what a Text does with more text than its width holds; empty on every other element.
	Overflow Overflow
	// Native is the widget behind this element, if any. Layout measures it and the renderer asks it to draw; TML never
	Native   widget.Native
	Children []*Box
	Pos      syntax.Pos

	// ID and Action identify an interactive element to the host: ID names it, Action is what it reports when activated.
	ID     string
	Action string
	// State is how this element renders in this frame -- focused, hovered, held.
	State widget.State

	// focus reports whether the keyboard can land here. An element with an id but no focus still answers the pointer.
	focus   bool
	attrs   map[string]string
	slot    string
	desired Size
	width   sema.Length
	height  sema.Length

	// Grid state: the track definitions, the sizes auto tracks measured, and this box's own placement within its parent
	cols, rows              []sema.Length
	autoWidths, autoHeights []int
	place                   placement

	// canvas is this box's placement within a parent Canvas; scroll is where a scrolling region's content ended up, which
	canvas canvasPlacement
	scroll Scroll
}

// Options configure an engine.
type Options struct {
	// Widgets resolves element names to widgets.
	Widgets *widget.Registry
	// FS is the filesystem the view was loaded from, handed to any widget that reads a file.
	FS fs.FS
	// Dark reports whether the view renders against a dark theme.
	Dark bool
	// Interaction carries focus and pointer state across frames. A nil value means nothing is focusable, which is what a
	Interaction Interaction
	// Measure is how wide a string is, in cells; nil means lipgloss.Width. See widget.Measurer.
	Measure widget.Measurer
	// Override supplies attributes that replace what the document wrote, for the element carrying the given id. It is
	Override func(id string) map[string]string
}

// Engine lays out expanded trees against a stylesheet.
type Engine struct {
	sheet *style.Sheet
	opts  Options
}

// New returns an engine that resolves named styles through sheet and widgets through opts.
func New(sheet *style.Sheet, opts Options) *Engine {
	return &Engine{sheet: sheet, opts: opts}
}

// SetOverride installs the per-element attribute override. An inspector calls it as soon as when it attaches; passing nil
func (e *Engine) SetOverride(fn func(id string) map[string]string) { e.opts.Override = fn }

// layoutAttrs are consumed by the engine; every other attribute is styling, unless a widget claims it. Attached
var layoutAttrs = set.Of(
	"width", "height", "orientation", "gap", "style", "overflow",
	"columns", "rows", "id", "action",
	"offset", "offsetX", "scrollbar",
	"title", "titleAlign",
)

func isLayoutAttr(name string) bool {
	return layoutAttrs.Contains(name) || strings.Contains(name, ".")
}

// Layout measures and arranges the tree inside a viewport.
func (e *Engine) Layout(node *sema.Node, width, height int) (*Box, error) {
	p := &pass{e: e}
	box, err := p.build(node)
	if err != nil {
		return nil, err
	}
	p.syncState()

	e.measure(box, Constraints{MaxW: width, MaxH: height})

	// The root fills the viewport unless it asked for a specific size. Filling is what a view wants by default, and it is
	rect := Rect{W: width, H: height}
	if box.width.Kind == sema.LengthCells {
		rect.W = min(box.desired.W, width)
	}
	if box.height.Kind == sema.LengthCells {
		rect.H = min(box.desired.H, height)
	}
	e.arrange(box, rect)
	setScreen(box, 0, 0, Rect{W: width, H: height})
	p.publish()
	return box, nil
}

func (p *pass) build(node *sema.Node) (*Box, error) {
	e := p.e
	if node.Kind == syntax.TextNode {
		return &Box{Name: "#text", Text: node.Text, Pos: node.Pos, attrs: map[string]string{}}, nil
	}

	// A node carrying a component's name is never a widget, however the component happens to be called.
	factory, hasFactory := e.opts.Widgets.Factory(node.Name)
	hasFactory = hasFactory && !node.Component
	claimed := set.New[string]()
	if hasFactory {
		claimed.AddRange(factory.Attributes()...)
	}

	attrs := make(map[string]string, len(node.Attrs))
	for name, value := range node.Attrs {
		attrs[name] = value.String()
	}
	// An override replaces what the document said, for a single element, until the host drops it. It lands before anything
	if e.opts.Override != nil {
		for name, value := range e.opts.Override(attrs["id"]) {
			attrs[name] = value
		}
	}
	inline := map[string]string{}
	for name, value := range attrs {
		if !isLayoutAttr(name) && !claimed.Contains(name) {
			inline[name] = value
		}
	}
	resolved, err := e.sheet.Resolve(attrs["style"], inline)
	if err != nil {
		return nil, &syntax.Error{Pos: node.Pos, Message: fmt.Sprintf("<%s>: %v", node.Name, err)}
	}

	box := &Box{
		Name:   node.Name,
		Style:  resolved,
		Pos:    node.Pos,
		attrs:  attrs,
		slot:   node.Slot,
		ID:     attrs["id"],
		Action: attrs["action"],
	}
	if box.width, err = lengthAttr(node, attrs, "width"); err != nil {
		return nil, err
	}
	if box.height, err = lengthAttr(node, attrs, "height"); err != nil {
		return nil, err
	}

	// Text collapses its character data into a single string; every other element keeps its children as boxes.
	if node.Name == "Text" {
		box.Text = textOf(node)
		if box.Overflow, err = overflowAttr(node, attrs); err != nil {
			return nil, err
		}
		return box, nil
	}
	if native, ok := e.opts.Widgets.Lookup(node.Name); ok && !node.Component {
		box.Native = native
	} else if hasFactory {
		native, err := factory.Build(widget.Context{
			Attrs:   widget.NewAttrs(node.Name, node.Attrs, node.Order),
			FS:      e.opts.FS,
			Dir:     path.Dir(node.Pos.File),
			Dark:    e.opts.Dark,
			Measure: e.opts.Measure,
		})
		if err != nil {
			return nil, &syntax.Error{Pos: node.Pos, Message: err.Error()}
		}
		box.Native = native
	}
	if box.Native != nil {
		if err := p.track(box); err != nil {
			return nil, err
		}
		// A composer keeps its children: they are laid out inside whatever space it insets for itself, and handed back to it
		if _, wraps := box.Native.(widget.Composer); !wraps {
			return box, nil
		}
	}
	for _, child := range node.Children {
		built, err := p.build(child)
		if err != nil {
			return nil, err
		}
		box.Children = append(box.Children, built)
	}

	switch box.Name {
	case "Grid":
		if err := initGrid(box); err != nil {
			return nil, err
		}
	case "Canvas":
		if err := initCanvas(box); err != nil {
			return nil, err
		}
	default:
		if err := rejectAttachedProperties(box); err != nil {
			return nil, err
		}
	}
	return box, nil
}

// rejectAttachedProperties catches Grid.row and friends written on a child of something that is not a Grid. Ignoring
func rejectAttachedProperties(box *Box) error {
	for _, child := range box.Children {
		for name := range child.attrs {
			owner, _, dotted := strings.Cut(name, ".")
			if !dotted {
				continue
			}
			return &syntax.Error{Pos: child.Pos, Message: fmt.Sprintf(
				"attached property %q only applies to a child of <%s>, but this is inside <%s>", name, owner, box.Name)}
		}
	}
	return nil
}

func textOf(node *sema.Node) string {
	var b strings.Builder
	for _, child := range node.Children {
		if child.Kind == syntax.TextNode {
			b.WriteString(child.Text)
		}
	}
	return b.String()
}

// overflowAttr reads what a Text does with more text than its width holds. Wrapping is the default, because that loses
// nothing. A line the reader scans rather than reads -- a log tail in a card -- says clip instead, and then the box is
// as tall as the text has lines however narrow it gets.
func overflowAttr(node *sema.Node, attrs map[string]string) (Overflow, error) {
	raw, ok := attrs["overflow"]
	if !ok {
		value, present := node.Attr("overflow")
		if !present {
			return OverflowWrap, nil
		}
		raw = value.String()
	}
	switch Overflow(raw) {
	case OverflowWrap, OverflowClip, OverflowEllipsis:
		return Overflow(raw), nil
	default:
		return "", &syntax.Error{Pos: node.Pos, Message: fmt.Sprintf(
			"<Text> overflow: %q is not wrap, clip or ellipsis", raw)}
	}
}

// lengthAttr reads a size off the node, or off the merged attributes when an override supplied a size. The override wins,
func lengthAttr(node *sema.Node, attrs map[string]string, name string) (sema.Length, error) {
	raw, overridden := attrs[name]
	value, ok := node.Attr(name)
	switch {
	case !ok && !overridden:
		return sema.Length{Kind: sema.LengthAuto}, nil
	case ok && !overridden:
		if value.Type().Kind == sema.KindLength {
			return value.Length(), nil
		}
		raw = value.String()
	}
	length, err := sema.ParseLength(raw)
	if err != nil {
		return sema.Length{}, &syntax.Error{Pos: node.Pos, Message: fmt.Sprintf("<%s> %s: %v", node.Name, name, err)}
	}
	return length, nil
}

// outer is the space a box consumes beyond its content: margin plus the frame lipgloss draws (padding and border).
func (b *Box) outer() (w, h int) {
	frameW, frameH := b.Style.Frame()
	return frameW + b.Style.Margin.Horizontal(), frameH + b.Style.Margin.Vertical()
}

// Vertical reports the stack direction. Vertical is the default because a column is the common case in a terminal.
func (b *Box) Vertical() bool {
	if value, ok := b.attrs["orientation"]; ok {
		return value != "horizontal"
	}
	return true
}

// Gap is the blank run between stacked children.
func (b *Box) Gap() int { return b.gap() }

func (b *Box) vertical() bool { return b.Vertical() }

func (b *Box) gap() int {
	value, ok := b.attrs["gap"]
	if !ok {
		return 0
	}
	n, err := strconv.Atoi(value)
	if err != nil || n < 0 {
		return 0
	}
	return n
}
