package dsstore

// Writing .DS_Store files: packing records into B-tree nodes, and the buddy
// allocator that decides where those nodes land in the file. See dsstore.go for
// the format itself.
//
// A file is always written whole rather than edited in place, which is what
// keeps the allocator's bookkeeping honest without having to implement freeing
// and coalescing.

import (
	"encoding/binary"
	"fmt"
	"sort"
	"unicode/utf16"
)

// Build writes a complete .DS_Store file holding exactly these records.
// The whole file is rebuilt rather than edited in place, which is what keeps
// the buddy allocator's bookkeeping honest.
func Build(records []Record) ([]byte, error) {
	sorted := make([]Record, len(records))
	copy(sorted, records)
	sortDSRecords(sorted)
	for _, r := range sorted {
		if len(r.Code) != 4 || len(r.Type) != 4 {
			return nil, fmt.Errorf("%w: record %q has a %d-byte code and %d-byte type", errBadDSStore, r.Name, len(r.Code), len(r.Type))
		}
		size, err := dsValueLen(r.Type, r.Data)
		if err != nil {
			return nil, err
		}
		if size != len(r.Data) {
			return nil, fmt.Errorf("%w: %s record for %q holds %d bytes, not the %d its type implies", errBadDSStore, r.Code, r.Name, len(r.Data), size)
		}
	}

	levels, err := dsBuildLevels(sorted)
	if err != nil {
		return nil, err
	}
	nodes := dsEncodeNodes(levels)

	// The bookkeeping block is block 0, but its size depends on how much the
	// allocator ends up doing, so lay the file out with an estimate and repeat
	// until the estimate holds.
	bookSize := 1264 // what an empty store needs, and enough for a small one
	for attempt := 0; ; attempt++ {
		file, actual, err := dsLayout(nodes, len(levels)-1, len(sorted), bookSize)
		if err != nil {
			return nil, err
		}
		if actual <= bookSize {
			return file, nil
		}
		if attempt == 4 {
			return nil, fmt.Errorf("%w: the allocator bookkeeping will not settle", errBadDSStore)
		}
		bookSize = actual
	}
}

// dsBuildLevels packs records into B-tree nodes, bottom up. levels[0] holds the
// leaves; each later level holds the records that did not fit in the level
// below, which become the pivots between its nodes.
func dsBuildLevels(records []Record) ([][][]Record, error) {
	if len(records) == 0 {
		return [][][]Record{{{}}}, nil
	}
	current := records
	pointerSize := 0
	var levels [][][]Record
	for {
		var (
			nodes [][]Record
			node  []Record
			total = 8 // the node's own P and count
			next  []Record
		)
		for _, r := range current {
			// Each record in an internal node is preceded by a child pointer.
			size := total + pointerSize + r.size()
			if size > dsPageSize {
				if len(node) == 0 {
					return nil, fmt.Errorf("%w: the record for %q does not fit in a %d-byte node", errBadDSStore, r.Name, dsPageSize)
				}
				// The record that did not fit becomes the pivot between this
				// node and the next, one level up.
				nodes = append(nodes, node)
				next = append(next, r)
				node, total = nil, 8
				continue
			}
			total = size
			node = append(node, r)
		}
		if len(node) > 0 {
			nodes = append(nodes, node)
		}
		levels = append(levels, nodes)
		if len(nodes) == 1 {
			return levels, nil
		}
		current, pointerSize = next, 4
	}
}

// dsEncodeNodes turns packed levels into the bytes of each B-tree node, in the
// order they are laid out: children before parents, so the root comes last. The
// child pointers a node holds are allocator block numbers, which is why the
// caller must allocate the nodes starting at dsFirstNodeBlock.
func dsEncodeNodes(levels [][][]Record) [][]byte {
	var (
		encoded  [][]byte
		previous []int // block numbers of the level below, left to right
	)
	for _, level := range levels {
		var numbers []int
		child := 0
		for _, node := range level {
			var buf []byte
			if previous == nil {
				buf = binary.BigEndian.AppendUint32(buf, 0) // a leaf
				buf = binary.BigEndian.AppendUint32(buf, uint32(len(node)))
				for _, r := range node {
					buf = appendDSRecord(buf, r)
				}
			} else {
				// The rightmost child goes in the node's P field.
				buf = binary.BigEndian.AppendUint32(buf, uint32(previous[child+len(node)]))
				buf = binary.BigEndian.AppendUint32(buf, uint32(len(node)))
				for i, r := range node {
					buf = binary.BigEndian.AppendUint32(buf, uint32(previous[child+i]))
					buf = appendDSRecord(buf, r)
				}
				child += len(node) + 1
			}
			encoded = append(encoded, buf)
			numbers = append(numbers, dsFirstNodeBlock+len(encoded)-1)
		}
		previous = numbers
	}
	return encoded
}

func appendDSRecord(buf []byte, r Record) []byte {
	chars := utf16.Encode([]rune(r.Name))
	buf = binary.BigEndian.AppendUint32(buf, uint32(len(chars)))
	for _, c := range chars {
		buf = binary.BigEndian.AppendUint16(buf, c)
	}
	buf = append(buf, r.Code...)
	buf = append(buf, r.Type...)
	return append(buf, r.Data...)
}

// dsLayout allocates blocks for the master block and every node, then writes
// the whole file. It also returns how many bytes the bookkeeping block actually
// needed, so the caller can lay the file out again if the reservation was too
// small.
func dsLayout(nodes [][]byte, internalLevels, records, bookSize int) ([]byte, int, error) {
	alloc := newDSAllocator()
	book, err := alloc.allocate(bookSize) // block 0, by convention
	if err != nil {
		return nil, 0, err
	}
	master, err := alloc.allocate(20)
	if err != nil {
		return nil, 0, err
	}
	numbers := make([]int, len(nodes))
	for i := range nodes {
		if numbers[i], err = alloc.allocate(dsPageSize); err != nil {
			return nil, 0, err
		}
		if numbers[i] != dsFirstNodeBlock+i {
			return nil, 0, fmt.Errorf("%w: node %d landed in block %d, but its parent points at block %d", errBadDSStore, i, numbers[i], dsFirstNodeBlock+i)
		}
	}

	// The root is the last node, because children are numbered before parents.
	root := numbers[len(numbers)-1]
	masterBlock := make([]byte, 0, 20)
	masterBlock = binary.BigEndian.AppendUint32(masterBlock, uint32(root))
	masterBlock = binary.BigEndian.AppendUint32(masterBlock, uint32(internalLevels))
	masterBlock = binary.BigEndian.AppendUint32(masterBlock, uint32(records))
	masterBlock = binary.BigEndian.AppendUint32(masterBlock, uint32(len(nodes)))
	masterBlock = binary.BigEndian.AppendUint32(masterBlock, dsPageSize)

	bookBlock := alloc.bookkeeping(map[string]int{"DSDB": master})
	if len(bookBlock) > alloc.size(book) {
		return nil, len(bookBlock), nil
	}

	file := make([]byte, dsHeaderLen)
	binary.BigEndian.PutUint32(file[0:4], 1)
	copy(file[4:8], dsMagic)
	binary.BigEndian.PutUint32(file[8:12], alloc.offset(book))
	binary.BigEndian.PutUint32(file[12:16], uint32(alloc.size(book)))
	binary.BigEndian.PutUint32(file[16:20], alloc.offset(book))
	copy(file[20:36], dsUnknownHeader)

	write := func(number int, data []byte) {
		start := int(alloc.offset(number)) + 4
		if grow := start + alloc.size(number) - len(file); grow > 0 {
			file = append(file, make([]byte, grow)...)
		}
		copy(file[start:], data)
	}
	write(book, bookBlock)
	write(master, masterBlock)
	for i, node := range nodes {
		write(numbers[i], node)
	}
	return file, len(bookBlock), nil
}

// dsAllocator is the buddy allocator a .DS_Store is built with. Blocks are
// never released here: every file is written from scratch.
type dsAllocator struct {
	free      [32][]uint32 // offsets of the free blocks of each width
	addresses []uint32     // offset packed with width, indexed by block number
}

// newDSAllocator starts from the state of an empty file: the 32-byte header
// block at offset 0 is allocated but never listed, which splits the address
// space into exactly one free block of every width from 5 to 30.
func newDSAllocator() *dsAllocator {
	a := &dsAllocator{}
	for width := dsMinWidth; width < dsMaxWidth; width++ {
		a.free[width] = []uint32{1 << width}
	}
	return a
}

// allocate reserves a block of at least size bytes and returns its number.
func (a *dsAllocator) allocate(size int) (int, error) {
	width := dsMinWidth
	for width < dsMaxWidth && 1<<width < size {
		width++
	}
	offset, err := a.take(width)
	if err != nil {
		return 0, err
	}
	a.addresses = append(a.addresses, offset|uint32(width))
	return len(a.addresses) - 1, nil
}

// take removes a free block of the given width, splitting a larger one when
// there is none.
func (a *dsAllocator) take(width int) (uint32, error) {
	found := width
	for found < len(a.free) && len(a.free[found]) == 0 {
		found++
	}
	if found == len(a.free) {
		return 0, fmt.Errorf("%w: the allocator ran out of space", errBadDSStore)
	}
	offset := a.free[found][0]
	a.free[found] = a.free[found][1:]
	for found > width {
		found--
		// Splitting a block frees its upper half, which is its buddy.
		a.free[found] = append(a.free[found], offset^(1<<found))
	}
	return offset, nil
}

func (a *dsAllocator) offset(block int) uint32 { return a.addresses[block] &^ 0x1f }
func (a *dsAllocator) size(block int) int      { return 1 << (a.addresses[block] & 0x1f) }

// bookkeeping serialises the allocator's state: the block address table, the
// directory naming the B-tree master block, and the free lists.
func (a *dsAllocator) bookkeeping(directory map[string]int) []byte {
	buf := make([]byte, 0, 1264)
	buf = binary.BigEndian.AppendUint32(buf, uint32(len(a.addresses)))
	buf = binary.BigEndian.AppendUint32(buf, 0)
	for _, addr := range a.addresses {
		buf = binary.BigEndian.AppendUint32(buf, addr)
	}
	for padding := (256 - len(a.addresses)%256) % 256; padding > 0; padding-- {
		buf = binary.BigEndian.AppendUint32(buf, 0)
	}

	names := make([]string, 0, len(directory))
	for name := range directory {
		names = append(names, name)
	}
	sort.Strings(names)
	buf = binary.BigEndian.AppendUint32(buf, uint32(len(names)))
	for _, name := range names {
		buf = append(buf, byte(len(name)))
		buf = append(buf, name...)
		buf = binary.BigEndian.AppendUint32(buf, uint32(directory[name]))
	}

	for width := range a.free {
		offsets := a.free[width]
		sort.Slice(offsets, func(i, j int) bool { return offsets[i] < offsets[j] })
		buf = binary.BigEndian.AppendUint32(buf, uint32(len(offsets)))
		for _, offset := range offsets {
			buf = binary.BigEndian.AppendUint32(buf, offset)
		}
	}
	return buf
}
