// Package tml loads and renders Terminal Markup Language views. A .tml file declares a reusable component: typed
package tml

import (
	"io/fs"

	"github.com/wow-look-at-my/tml/layout"
	"github.com/wow-look-at-my/tml/render"
	"github.com/wow-look-at-my/tml/sema"
	"github.com/wow-look-at-my/tml/style"
	"github.com/wow-look-at-my/tml/syntax"
	"github.com/wow-look-at-my/tml/widget"
	"github.com/wow-look-at-my/tml/widgets"
)

// Props are the arguments passed to the entry component.
type Props = map[string]sema.Value

// Options configure loading.
type Options struct {
	// Dark selects the dark half of every adaptive theme token.
	Dark bool
	// Widgets are the host's own elements, layered over the built-in library. A name bound here wins, so a host can
	Widgets *widget.Registry
	// Bare drops the built-in widget library, leaving only the host's own bindings. A view that never uses the library
	Bare bool
	// Measure is how wide a string is, in cells; nil means lipgloss.Width. See widget.Measurer for why a host would have
	Measure widget.Measurer
}

// View is a loaded, checked view ready to render.
type View struct {
	program *sema.Program
	engine  *layout.Engine
	dark    bool
	ui      *UI
	// frames is the last painted frame, kept only while an inspector is attached. A view without an inspector pays a single atomic
	frames *frameRecord
}

// UI is the view's interaction state: which element has focus, which element the pointer is over, and what the last
func (v *View) UI() *UI { return v.ui }

// Load parses, checks and prepares the view rooted at entry. Everything that can fail without knowing the caller's
func Load(fsys fs.FS, entry string, opts Options) (*View, error) {
	unit, err := syntax.Load(fsys, entry)
	if err != nil {
		return nil, err
	}
	registry := opts.Widgets
	if !opts.Bare {
		registry = opts.Widgets.Merge(widgets.Library())
	}
	program, err := sema.Analyze(unit, sema.Options{
		Natives: registry.Names(),
		Slots:   registry.SlotNames(),
	})
	if err != nil {
		return nil, err
	}
	sheet, err := style.NewSheet(unit.Themes, program.Tokens(sema.ExpandOptions{Dark: opts.Dark}))
	if err != nil {
		return nil, err
	}
	view := &View{program: program, dark: opts.Dark, ui: NewUI()}
	view.engine = layout.New(sheet, layout.Options{
		Widgets:     registry,
		FS:          fsys,
		Dark:        opts.Dark,
		Interaction: view.ui,
		Measure:     opts.Measure,
	})
	// Every view this library builds is inspectable, with nothing asked of the caller, and a view that cannot be is not
	if err := inspection.adopt(view); err != nil {
		return nil, err
	}
	return view, nil
}

// Expand instantiates the view and returns the expanded element tree, with components, slots and control flow resolved
func (v *View) Expand(props Props) (*sema.Node, error) {
	return v.program.Expand(props, sema.ExpandOptions{Dark: v.dark})
}

// Layout instantiates the view and lays it out in a viewport.
func (v *View) Layout(props Props, width, height int) (*layout.Box, error) {
	node, err := v.Expand(props)
	if err != nil {
		return nil, err
	}
	return v.engine.Layout(node, width, height)
}

// Render produces the terminal output for a viewport. The result is a styled string ready to hand to tea.NewView.
func (v *View) Render(props Props, width, height int) (string, error) {
	box, err := v.Layout(props, width, height)
	if err != nil {
		return "", err
	}
	out := render.Render(box)
	v.record(box, out, width, height)
	// Readable is half. A view still painting after the grace window with nothing able to drive it is a program the
	drives.painted(inspection.isDriven())
	return out, nil
}
