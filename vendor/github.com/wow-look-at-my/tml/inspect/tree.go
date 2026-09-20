package inspect

import (
	"strconv"
	"strings"

	"github.com/charmbracelet/x/ansi"

	"github.com/wow-look-at-my/tml/layout"
	"github.com/wow-look-at-my/tml/render"
)

// Node is a single box of the frame's tree, id-bearing or not. Elements() answers "what can a test address"; this answers
type Node struct {
	// Path is the node's position from the root, as child indexes joined by dots. It addresses a node that has
	Path    string `json:"path"`
	ID      string `json:"id,omitempty"`
	Element string `json:"element"`
	Action  string `json:"action,omitempty"`
	Rect    Rect   `json:"rect"`
	Content Size   `json:"content"`
	Clip    Rect   `json:"clip"`
	// Text is what this node drew, escapes removed and truncated to a single line, so a tree stays readable. The full text is
	Text     string `json:"text,omitempty"`
	Focus    bool   `json:"focus"`
	Children []Node `json:"children,omitempty"`
	// Source is where the element was written, so the inspector can point an editor at it.
	Source string `json:"source,omitempty"`
	Line   int    `json:"line,omitempty"`
}

// Tree describes the whole frame as nested nodes.
func Tree(box *layout.Box, state map[string]layout.Target) Node {
	return node(box, state, "0")
}

func node(box *layout.Box, state map[string]layout.Target, path string) Node {
	n := Node{
		Path:    path,
		ID:      box.ID,
		Element: box.Name,
		Action:  box.Action,
		Rect:    toRect(box.Screen),
		Content: Size{W: box.Content.W, H: box.Content.H},
		Clip:    toRect(box.Clip),
		Source:  box.Pos.File,
		Line:    box.Pos.Line,
	}
	if t, ok := state[box.ID]; ok && box.ID != "" {
		n.Focus = t.Focus
	}
	// A leaf's own text is worth showing; a container's is every descendant's text concatenated, which says nothing and
	if len(box.Children) == 0 {
		n.Text = summary(box)
	}
	for i, child := range box.Children {
		n.Children = append(n.Children, node(child, state, path+"."+strconv.Itoa(i)))
	}
	return n
}

// summary is a node's own drawn text on a single line, cut to something a tree can show without wrapping.
func summary(box *layout.Box) string {
	text := ansi.Strip(render.Render(box))
	text = strings.ReplaceAll(text, "\n", " ")
	text = strings.Join(strings.Fields(text), " ")
	const limit = 60
	if len(text) > limit {
		return text[:limit] + "..."
	}
	return text
}
