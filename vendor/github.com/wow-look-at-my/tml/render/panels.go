package render

import (
	"strings"

	"charm.land/lipgloss/v2"

	"github.com/wow-look-at-my/tml/layout"
)

// canvas paints children at the coordinates arrange gave them. Later children cover earlier ones, which is what makes
func canvas(box *layout.Box, parts []string) string {
	layers := make([]*lipgloss.Layer, 0, len(parts)+1)
	// A backdrop of the full size keeps the compositor's canvas at the size layout settled on, so a child pinned to a
	layers = append(layers, lipgloss.NewLayer(blank(box.Content.W, box.Content.H)).X(0).Y(0).Z(0))
	for i, child := range box.Children {
		layers = append(layers, lipgloss.NewLayer(parts[i]).
			X(child.Rect.X).
			Y(child.Rect.Y).
			Z(i+1))
	}
	return lipgloss.NewCompositor(layers...).Render()
}

func blank(w, h int) string {
	if w <= 0 || h <= 0 {
		return ""
	}
	row := strings.Repeat(" ", w)
	lines := make([]string, h)
	for i := range lines {
		lines[i] = row
	}
	return strings.Join(lines, "\n")
}
