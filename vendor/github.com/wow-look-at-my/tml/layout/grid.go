package layout

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/wow-look-at-my/go-containers/set"

	"github.com/wow-look-at-my/tml/sema"
	"github.com/wow-look-at-my/tml/syntax"
)

// Grid places children on a track grid. Tracks are declared on the Grid and placement is declared on each child
var attachedGrid = set.Of("Grid.row", "Grid.column", "Grid.rowSpan", "Grid.columnSpan")

// placement is where a child sits on the grid. It defaults to the leading cell, spanning a single track each way.
type placement struct {
	row, column int
	rowSpan     int
	columnSpan  int
}

// readPlacement validates and reads a child's attached properties. parent is the element the attached properties must
func readPlacement(box *Box, parent string) (placement, error) {
	p := placement{rowSpan: 1, columnSpan: 1}
	for name, raw := range box.attrs {
		owner, _, dotted := strings.Cut(name, ".")
		if !dotted {
			continue
		}
		if owner != parent {
			return p, &syntax.Error{Pos: box.Pos, Message: fmt.Sprintf(
				"attached property %q only applies to a child of <%s>, but this is inside <%s>", name, owner, parent)}
		}
		if !attachedGrid.Contains(name) {
			return p, &syntax.Error{Pos: box.Pos, Message: fmt.Sprintf(
				"unknown attached property %q; want Grid.row, Grid.column, Grid.rowSpan or Grid.columnSpan", name)}
		}
		n, err := strconv.Atoi(raw)
		if err != nil || n < 0 {
			return p, &syntax.Error{Pos: box.Pos, Message: fmt.Sprintf(
				"%s must be a whole number, got %q", name, raw)}
		}
		switch name {
		case "Grid.row":
			p.row = n
		case "Grid.column":
			p.column = n
		case "Grid.rowSpan":
			p.rowSpan = max(1, n)
		case "Grid.columnSpan":
			p.columnSpan = max(1, n)
		}
	}
	return p, nil
}

// parseTracks reads a track list of auto, fixed and star entries. An empty list is a single auto track, so a Grid with no columns
func parseTracks(spec string) ([]sema.Length, error) {
	if strings.TrimSpace(spec) == "" {
		return []sema.Length{{Kind: sema.LengthAuto}}, nil
	}
	var tracks []sema.Length
	for _, field := range strings.Split(spec, ",") {
		length, err := sema.ParseLength(strings.TrimSpace(field))
		if err != nil {
			return nil, err
		}
		tracks = append(tracks, length)
	}
	return tracks, nil
}

// initGrid parses the track lists and reads every child's placement. Tracks are widened to cover a child placed past
func initGrid(box *Box) error {
	cols, err := parseTracks(box.attrs["columns"])
	if err != nil {
		return &syntax.Error{Pos: box.Pos, Message: fmt.Sprintf("<Grid> columns: %v", err)}
	}
	rows, err := parseTracks(box.attrs["rows"])
	if err != nil {
		return &syntax.Error{Pos: box.Pos, Message: fmt.Sprintf("<Grid> rows: %v", err)}
	}

	for _, child := range box.Children {
		child.place, err = readPlacement(child, "Grid")
		if err != nil {
			return err
		}
		for len(cols) < child.place.column+child.place.columnSpan {
			cols = append(cols, sema.Length{Kind: sema.LengthAuto})
		}
		for len(rows) < child.place.row+child.place.rowSpan {
			rows = append(rows, sema.Length{Kind: sema.LengthAuto})
		}
	}
	box.cols, box.rows = cols, rows
	return nil
}

func (e *Engine) measureGrid(box *Box, inner Constraints) Size {
	gap := box.Gap()
	for _, child := range box.Children {
		e.measure(child, inner)
	}

	// An auto track is as wide as the widest child confined to it. A child that spans several tracks is left out of this:
	box.autoWidths = fixedSizes(box.cols)
	box.autoHeights = fixedSizes(box.rows)
	for _, child := range box.Children {
		if child.place.columnSpan == 1 && box.cols[child.place.column].Kind == sema.LengthAuto {
			box.autoWidths[child.place.column] = max(box.autoWidths[child.place.column], child.desired.W)
		}
		if child.place.rowSpan == 1 && box.rows[child.place.row].Kind == sema.LengthAuto {
			box.autoHeights[child.place.row] = max(box.autoHeights[child.place.row], child.desired.H)
		}
	}

	content := Size{
		W: sum(box.autoWidths) + gap*(len(box.cols)-1),
		H: sum(box.autoHeights) + gap*(len(box.rows)-1),
	}
	// A star track divides whatever the grid is given, so the grid asks for all the space available on that axis.
	if hasStar(box.cols) {
		content.W = max(content.W, inner.MaxW)
	}
	if hasStar(box.rows) {
		content.H = max(content.H, inner.MaxH)
	}
	return content
}

func (e *Engine) arrangeGrid(box *Box) {
	gap := box.Gap()
	widths := solveTracks(box.cols, box.autoWidths, box.Content.W, gap)
	heights := solveTracks(box.rows, box.autoHeights, box.Content.H, gap)

	x := offsets(widths, gap)
	y := offsets(heights, gap)

	for _, child := range box.Children {
		e.arrange(child, Rect{
			X: x[child.place.column],
			Y: y[child.place.row],
			W: span(widths, child.place.column, child.place.columnSpan, gap),
			H: span(heights, child.place.row, child.place.rowSpan, gap),
		})
	}
}

// solveTracks turns track definitions into cell counts: a fixed track takes what it asked for, an auto track what it
func solveTracks(tracks []sema.Length, measured []int, available, gap int) []int {
	sizes := make([]int, len(tracks))
	used, weight := 0, 0

	for i, track := range tracks {
		switch track.Kind {
		case sema.LengthCells, sema.LengthAuto:
			sizes[i] = measured[i]
			used += sizes[i]
		case sema.LengthStar:
			weight += track.Weight
		}
	}
	if weight == 0 {
		return sizes
	}

	leftover := max(0, available-used-gap*(len(tracks)-1))
	remaining, remainingWeight := leftover, weight
	for i, track := range tracks {
		if track.Kind != sema.LengthStar {
			continue
		}
		if remainingWeight == track.Weight {
			sizes[i] = remaining
		} else {
			sizes[i] = leftover * track.Weight / weight
		}
		remaining -= sizes[i]
		remainingWeight -= track.Weight
	}
	return sizes
}

func offsets(sizes []int, gap int) []int {
	out := make([]int, len(sizes))
	at := 0
	for i, size := range sizes {
		out[i] = at
		at += size + gap
	}
	return out
}

// span is the size of a run of tracks, including the gaps swallowed between them.
func span(sizes []int, start, count, gap int) int {
	total := 0
	for i := start; i < start+count && i < len(sizes); i++ {
		if i > start {
			total += gap
		}
		total += sizes[i]
	}
	return total
}

// fixedSizes seeds the track sizes with the ones already known from the declaration; auto tracks start at nothing and
func fixedSizes(tracks []sema.Length) []int {
	sizes := make([]int, len(tracks))
	for i, track := range tracks {
		if track.Kind == sema.LengthCells {
			sizes[i] = track.Cells
		}
	}
	return sizes
}

func sum(sizes []int) int {
	out := 0
	for _, size := range sizes {
		out += size
	}
	return out
}

func hasStar(tracks []sema.Length) bool {
	for _, track := range tracks {
		if track.Kind == sema.LengthStar {
			return true
		}
	}
	return false
}
