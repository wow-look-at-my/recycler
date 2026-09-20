package widgets

import (
	"strings"

	"charm.land/lipgloss/v2"
)

// paint builds a style from an optional colour, so a widget can colour a single part of itself without the element's own
func paint(color string) lipgloss.Style {
	style := lipgloss.NewStyle()
	if color == "" {
		return style
	}
	return style.Foreground(lipgloss.Color(color))
}

// repeat draws n copies of r, guarding the negative counts that arithmetic on a too-small box produces.
func repeat(r rune, n int) string {
	if n <= 0 {
		return ""
	}
	return strings.Repeat(string(r), n)
}
