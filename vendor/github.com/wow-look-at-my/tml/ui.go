package tml

import (
	"strconv"

	tea "charm.land/bubbletea/v2"

	"github.com/wow-look-at-my/tml/layout"
	"github.com/wow-look-at-my/tml/widget"
)

// Target is a single focusable control in the last rendered frame: what it is called, what it does, and where on the screen
type Target = layout.Target

// Scroll is how far a scrolling region's content is shifted, and how far it could be.
type Scroll = layout.Scroll

// EventKind is what happened to an interactive element.
type EventKind int

const (
	// Activated is a control being pressed: enter or space on the focused control, or a click released over it.
	Activated EventKind = iota
	// FocusMoved is the keyboard landing on a different control.
	FocusMoved
	// Scrolled is a wheel turn over a control, with Delta in notches: negative up, positive down.
	Scrolled
)

func (k EventKind) String() string {
	switch k {
	case Activated:
		return "activated"
	case FocusMoved:
		return "focus-moved"
	case Scrolled:
		return "scrolled"
	default:
		return "invalid"
	}
}

// Event is a single interaction, reported back to the host. Action is the element's action attribute: the string the
type Event struct {
	Kind   EventKind
	ID     string
	Action string
	Delta  int
	// X and Y are where the pointer was inside the control, in cells from its top-left. A widget that draws several
	X, Y int
}

// KeyMap is which keys drive the focus ring. Replace it when the host wants a key for itself -- a list that owns the
type KeyMap struct {
	Next     []string
	Prev     []string
	Activate []string
}

// DefaultKeyMap moves with tab and the arrows and fires on enter or space. The arrows are included because a row of
func DefaultKeyMap() KeyMap {
	return KeyMap{
		Next:     []string{"tab", "down", "right"},
		Prev:     []string{"shift+tab", "up", "left"},
		Activate: []string{"enter", "space"},
	}
}

// UI is a view's interaction state. The cycle is a single frame long: laying out asks the UI how each control should draw,
type UI struct {
	keys    KeyMap
	focus   string
	hover   string
	press   string
	targets []layout.Target
}

// NewUI returns interaction state with the default key map.
func NewUI() *UI { return &UI{keys: DefaultKeyMap()} }

// SetKeyMap replaces the bindings.
func (u *UI) SetKeyMap(keys KeyMap) { u.keys = keys }

// States implements layout.Interaction.
func (u *UI) States(targets []layout.Target) []widget.State {
	// Nothing has been focused yet, so the keyboard starts on the frame's leading control rather than nowhere: a view whose
	focus := u.focus
	if focus == "" {
		focus = firstFocusable(targets)
	}

	states := make([]widget.State, 0, len(targets))
	for i, target := range targets {
		key := targetKey(i, target.ID)
		states = append(states, widget.State{
			Focused: target.Focus && focus == key,
			Hovered: u.hover == key,
			Pressed: u.press == key,
		})
	}
	return states
}

// Frame implements layout.Interaction.
func (u *UI) Frame(targets []layout.Target) {
	u.targets = targets
	// A control can disappear between frames -- a popup closes, an `if` flips. Falling back to the leading focusable keeps
	if index := u.indexOf(u.focus); index < 0 || !targets[index].Focus {
		u.focus = firstFocusable(targets)
	}
}

func firstFocusable(targets []layout.Target) string {
	for i, target := range targets {
		if target.Focus {
			return targetKey(i, target.ID)
		}
	}
	return ""
}

// Update feeds a Bubble Tea message through the focus ring and reports what the user did. Messages that mean nothing
func (u *UI) Update(msg tea.Msg) []Event {
	switch msg := msg.(type) {
	case tea.KeyPressMsg:
		return u.key(msg.Keystroke())
	case tea.MouseMotionMsg:
		u.hover = u.hit(msg.X, msg.Y)
		return nil
	case tea.MouseClickMsg:
		if msg.Button != tea.MouseLeft {
			return nil
		}
		target := u.hit(msg.X, msg.Y)
		u.press = target
		u.hover = target
		if target == "" {
			return nil
		}
		return u.focusKey(target)
	case tea.MouseReleaseMsg:
		pressed := u.press
		u.press = ""
		target := u.hit(msg.X, msg.Y)
		u.hover = target
		// Releasing somewhere else cancels, which is what every other button in every other toolkit does and what lets a
		if pressed == "" || target != pressed {
			return nil
		}
		return u.at(target, Activated, 0, msg.X, msg.Y)
	case tea.MouseWheelMsg:
		target := u.hit(msg.X, msg.Y)
		if target == "" {
			return nil
		}
		switch msg.Button {
		case tea.MouseWheelUp:
			return u.at(target, Scrolled, -1, msg.X, msg.Y)
		case tea.MouseWheelDown:
			return u.at(target, Scrolled, 1, msg.X, msg.Y)
		}
	}
	return nil
}

func (u *UI) key(stroke string) []Event {
	switch {
	case contains(u.keys.Next, stroke):
		return u.move(1)
	case contains(u.keys.Prev, stroke):
		return u.move(-1)
	case contains(u.keys.Activate, stroke):
		if u.focus == "" {
			return nil
		}
		return u.activate(u.focus)
	}
	return nil
}

// move steps the ring, wrapping at both ends so tab never dead-ends. Elements that only answer the pointer are stepped
func (u *UI) move(step int) []Event {
	ring := u.ring()
	if len(ring) == 0 {
		return nil
	}
	at := 0
	for i, index := range ring {
		if targetKey(index, u.targets[index].ID) == u.focus {
			at = (i + step + len(ring)) % len(ring)
			break
		}
	}
	index := ring[at]
	return u.focusKey(targetKey(index, u.targets[index].ID))
}

// ring is the indices of the targets the keyboard can reach, in document order.
func (u *UI) ring() []int {
	var ring []int
	for i, target := range u.targets {
		if target.Focus {
			ring = append(ring, i)
		}
	}
	return ring
}

// Focus moves the keyboard to the control with the given id. It reports whether such a control took focus in the last
func (u *UI) Focus(id string) bool {
	for i, target := range u.targets {
		if target.ID == id && id != "" && target.Focus {
			u.focus = targetKey(i, target.ID)
			return true
		}
	}
	return false
}

// Focused is the id and action of the control the keyboard is on.
func (u *UI) Focused() (id, action string) {
	index := u.indexOf(u.focus)
	if index < 0 {
		return "", ""
	}
	return u.targets[index].ID, u.targets[index].Action
}

// Targets is every interactive element in the last frame, in document order: the focus ring plus anything the pointer
func (u *UI) Targets() []layout.Target { return u.targets }

// Target is the element with the given id in the last frame. A host reads it to find out where something landed -- how
func (u *UI) Target(id string) (layout.Target, bool) {
	for _, target := range u.targets {
		if target.ID == id && id != "" {
			return target, true
		}
	}
	return layout.Target{}, false
}

func (u *UI) focusKey(key string) []Event {
	index := u.indexOf(key)
	if u.focus == key || index < 0 || !u.targets[index].Focus {
		return nil
	}
	u.focus = key
	id, action := u.Focused()
	return []Event{{Kind: FocusMoved, ID: id, Action: action, X: -1, Y: -1}}
}

// activate is the keyboard firing a control, so there is no pointer to report.
func (u *UI) activate(key string) []Event {
	return u.event(key, Activated, 0, -1, -1)
}

// at is a pointer event, with the screen point turned into an offset inside the control it landed on.
func (u *UI) at(key string, kind EventKind, delta, screenX, screenY int) []Event {
	index := u.indexOf(key)
	if index < 0 {
		return nil
	}
	rect := u.targets[index].Rect
	return u.event(key, kind, delta, screenX-rect.X, screenY-rect.Y)
}

func (u *UI) event(key string, kind EventKind, delta, x, y int) []Event {
	index := u.indexOf(key)
	if index < 0 {
		return nil
	}
	return []Event{{
		Kind:   kind,
		ID:     u.targets[index].ID,
		Action: u.targets[index].Action,
		Delta:  delta,
		X:      x,
		Y:      y,
	}}
}

// hit finds the control under a point. Later targets win, because that is the order they are painted in and so the
func (u *UI) hit(x, y int) string {
	found := ""
	for i, target := range u.targets {
		r := target.Rect
		if x >= r.X && x < r.X+r.W && y >= r.Y && y < r.Y+r.H {
			found = targetKey(i, target.ID)
		}
	}
	return found
}

func (u *UI) indexOf(key string) int {
	if key == "" {
		return -1
	}
	for i, target := range u.targets {
		if targetKey(i, target.ID) == key {
			return i
		}
	}
	return -1
}

// targetKey identifies a control across frames. An id survives the control moving; without an id, position in the ring
func targetKey(index int, id string) string {
	if id != "" {
		return "#" + id
	}
	return "@" + strconv.Itoa(index)
}

func contains(haystack []string, needle string) bool {
	for _, s := range haystack {
		if s == needle {
			return true
		}
	}
	return false
}
