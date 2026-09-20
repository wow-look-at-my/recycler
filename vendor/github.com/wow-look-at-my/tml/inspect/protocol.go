package inspect

import (
	"time"

	"github.com/wow-look-at-my/tml/layout"
)

// Frame is a single painted frame, kept by the host so the inspector can answer questions about what is on screen right now.
type Frame struct {
	Seq           uint64
	At            time.Time
	Width, Height int
	Box           *layout.Box
	// State is the interaction state by id: focus, and where a scrolling region ended up.
	State map[string]layout.Target
	// ANSI is exactly what the program handed the terminal.
	ANSI string
}

// Source is a live program that can report the frame currently on screen. A program that renders through tml.View gets
type Source interface {
	Frame() (Frame, bool)
}

// Controller is the half of a program that the inspector can poke. It is optional: a Source alone answers every read,
type Controller interface {
	// Key delivers a keystroke by name, in the spelling the host's key map uses ("enter", "ctrl+c", "a").
	Key(key string) error
	// Click presses and releases at a viewport cell.
	Click(x, y int) error
	// Restyle overrides the style attributes of a single element until Reset. It is what makes the browser inspector an editor
	Restyle(id string, attrs map[string]string) error
	// Reset drops every override Restyle applied.
	Reset() error
}

// Request is a single operation. The socket carries a single JSON object per line, and the browser posts the same object to /rpc.
type Request struct {
	Op string `json:"op"`
	ID string `json:"id,omitempty"`
	// X and Y are viewport cells, for at and click.
	X int `json:"x,omitempty"`
	Y int `json:"y,omitempty"`
	// Key is the keystroke for op=key.
	Key string `json:"key,omitempty"`
	// Attrs are the style attributes for op=restyle.
	Attrs map[string]string `json:"attrs,omitempty"`
	// ANSI asks for styled text as well as plain.
	ANSI bool `json:"ansi,omitempty"`
	// Since makes op=frame wait for a frame newer than this sequence number, which is how a watcher follows a program
	Since uint64 `json:"since,omitempty"`
}

// FrameInfo is a frame without its box tree: what the preview draws.
type FrameInfo struct {
	Seq    uint64 `json:"seq"`
	At     string `json:"at"`
	Width  int    `json:"width"`
	Height int    `json:"height"`
	Text   string `json:"text"`
	ANSI   string `json:"ansi,omitempty"`
}

// Response is a single answer. Exactly a single field is set, except that Error is set alone.
type Response struct {
	Error    string     `json:"error,omitempty"`
	OK       bool       `json:"ok,omitempty"`
	Element  *Element   `json:"element,omitempty"`
	Elements []Element  `json:"elements,omitempty"`
	Tree     *Node      `json:"tree,omitempty"`
	Frame    *FrameInfo `json:"frame,omitempty"`
	IDs      []string   `json:"ids,omitempty"`
	// Hits is At's answer per cell, row by row, indexing the elements in document order. An empty cell is negative.
	Hits [][]int `json:"hits,omitempty"`
	// Hit is the id under a cell for op=at. It is empty when nothing covers the cell, which Found tells apart from "an
	Hit   string `json:"hit"`
	Found bool   `json:"found,omitempty"`
}

// Ops are every operation the server answers, which is also what --help and the browser's own error messages are built
var Ops = []string{"query", "elements", "ids", "tree", "frame", "at", "hits", "key", "click", "restyle", "reset"}
