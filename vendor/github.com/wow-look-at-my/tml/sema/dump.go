package sema

import (
	"sort"
	"strconv"
	"strings"

	"github.com/wow-look-at-my/tml/syntax"
)

// Dump renders an expanded tree as indented text. It backs `tml tree` and the expansion golden files, so its output is
func (n *Node) Dump() string {
	var b strings.Builder
	n.dump(&b, 0)
	return b.String()
}

func (n *Node) dump(b *strings.Builder, depth int) {
	b.WriteString(strings.Repeat("  ", depth))
	if n.Kind == syntax.TextNode {
		b.WriteString(strconv.Quote(n.Text))
		b.WriteByte('\n')
		return
	}

	b.WriteString(n.Name)
	names := make([]string, 0, len(n.Attrs))
	for name := range n.Attrs {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		b.WriteByte(' ')
		b.WriteString(name)
		b.WriteByte('=')
		b.WriteString(strconv.Quote(n.Attrs[name].String()))
	}
	b.WriteByte('\n')

	for _, child := range n.Children {
		child.dump(b, depth+1)
	}
}
