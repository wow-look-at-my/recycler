package inspect

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"
)

// Capture is a painted frame and the answers the inspector gives about it, frozen.
type Capture struct {
	Frame    FrameInfo `json:"frame"`
	Elements []Element `json:"elements"`
	Tree     *Node     `json:"tree"`
	// Hits is At's answer per cell, indexing Elements, so a written page resolves a click by lookup.
	Hits [][]int `json:"hits"`
}

// Snapshot asks h the questions the browser asks when it loads. A capture therefore answers through the same path the
// live page does, and cannot report a geometry the inspector does not.
func Snapshot(h Handler) (Capture, error) {
	frame := h.Handle(Request{Op: "frame", ANSI: true})
	if frame.Error != "" {
		return Capture{}, fmt.Errorf("cannot read the frame: %s", frame.Error)
	}
	if frame.Frame == nil {
		return Capture{}, fmt.Errorf("the answer to op=frame carried no frame")
	}
	elements := h.Handle(Request{Op: "elements", ANSI: true})
	if elements.Error != "" {
		return Capture{}, fmt.Errorf("cannot read the elements: %s", elements.Error)
	}
	tree := h.Handle(Request{Op: "tree"})
	if tree.Error != "" {
		return Capture{}, fmt.Errorf("cannot read the tree: %s", tree.Error)
	}
	if tree.Tree == nil {
		return Capture{}, fmt.Errorf("the answer to op=tree carried no tree")
	}
	hits := h.Handle(Request{Op: "hits"})
	if hits.Error != "" {
		return Capture{}, fmt.Errorf("cannot read the hit map: %s", hits.Error)
	}
	return Capture{
		Frame: *frame.Frame, Elements: elements.Elements, Tree: tree.Tree, Hits: hits.Hits,
	}, nil
}

// SnapshotFrame captures a frame the caller already holds, through the server the socket answers from.
func SnapshotFrame(f Frame) (Capture, error) { return Snapshot(NewServer(still{f})) }

// still is a Source over a frame that is not going to change.
type still struct{ frame Frame }

func (s still) Frame() (Frame, bool) { return s.frame, s.frame.Box != nil }

// The tags the live page loads its assets with. Each becomes the asset itself, and a marker that no longer matches
// fails the write.
const (
	styleTag  = `<link rel="stylesheet" href="inspector.css" />`
	scriptTag = `<script type="module" src="inspector.js"></script>`
)

// WriteCapture writes c as a single HTML file: the inspector's own page, stylesheet and script, plus the frozen
// answers. It fetches nothing when it opens, so it travels through an email or a comment box.
func WriteCapture(w io.Writer, c Capture) error {
	page, err := asset("index.html")
	if err != nil {
		return err
	}
	css, err := asset("inspector.css")
	if err != nil {
		return err
	}
	script, err := asset("inspector.js")
	if err != nil {
		return err
	}
	held, err := json.Marshal(c)
	if err != nil {
		return fmt.Errorf("inspect: cannot encode the capture: %w", err)
	}

	page, err = replace(page, styleTag, "<style>\n"+css+"\n\t\t</style>")
	if err != nil {
		return err
	}
	page, err = replace(page, scriptTag,
		`<script type="application/json" id="capture">`+string(held)+"</script>\n\t\t"+
			`<script type="module">`+"\n"+script+"\n\t\t</script>")
	if err != nil {
		return err
	}
	_, err = io.WriteString(w, page)
	return err
}

func asset(name string) (string, error) {
	body, err := uiFS.ReadFile("ui/" + name)
	if err != nil {
		return "", fmt.Errorf("inspect: cannot read the embedded %s: %w", name, err)
	}
	return string(body), nil
}

// replace swaps a marker and fails when it is absent, so a rename in the page breaks a test rather than shipping a
// capture that opens blank.
func replace(text, old, with string) (string, error) {
	if !strings.Contains(text, old) {
		return "", fmt.Errorf("inspect: the inspector page no longer contains %q, so a capture cannot be assembled", old)
	}
	return strings.Replace(text, old, with, 1), nil
}
