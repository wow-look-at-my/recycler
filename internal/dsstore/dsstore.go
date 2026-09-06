package dsstore

// Codec for the Finder's ".DS_Store" files. On macOS that is where the Trash
// keeps the "Put Back" location of every item in it: "ptbL" holds the original
// parent directory relative to the volume root, and "ptbN" the original name.
//
// Apple has never documented the format, but it has been reverse engineered
// often enough to be reliable. A file is a B-tree of records, stored in blocks
// managed by a buddy allocator. The header carries a fixed prefix, the magic
// "Bud1", and the offset and length of the allocator's bookkeeping block. That
// offset appears again further along, and the Finder rejects a file whose
// copies disagree. The tail of the header has no known purpose.
//
// Every offset in the file is relative to the end of the prefix, which is what
// the offset adjustment below accounts for. The bookkeeping block holds the block address
// table, where an entry packs an offset together with the block's width in its
// low bits. It also holds a directory mapping "DSDB" to the block number of the
// B-tree master block, and a free list per width.
//
// A B-tree node opens with P and a record count. A leaf leaves P empty and its
// records follow directly. An internal node holds that many (child block
// number, record) entries, with P as the rightmost child.
//
// This file deliberately has no build constraint: keeping the codec portable
// keeps it testable on any platform.

import (
	"encoding/binary"
	"errors"
	"fmt"
	"github.com/wow-look-at-my/go-containers/set"
	"sort"
	"strings"
	"unicode/utf16"
)

const (
	FileName    = ".DS_Store"
	dsMagic     = "Bud1"
	dsHeaderLen = 36     // the prefix plus the header
	dsPageSize  = 0x1000 // B-tree node size, and what the master block records
	dsMinWidth  = 5      // the floor width, set by how a block offset is aligned
	dsMaxWidth  = 31     // the widest block the allocator manages

	dsFirstNodeBlock = 2
)

// dsUnknownHeader fills the header bytes whose purpose is unknown.
var dsUnknownHeader = []byte{
	0x00, 0x00, 0x10, 0x0c, 0x00, 0x00, 0x00, 0x87,
	0x00, 0x00, 0x20, 0x0b, 0x00, 0x00, 0x00, 0x00,
}

// errBadDSStore is returned for a .DS_Store file that cannot.
var errBadDSStore = errors.New("recycler: malformed .DS_Store")

// A Record is a property of a file in the directory the .DS_Store describes.
type Record struct {
	Name string // the file the record is about
	Code string // structure id, "ptbL" for a put back location
	Type string // data type, "ustr" for a Unicode string
	Data []byte // raw value, including any length prefix
}

// Ustr builds a record holding a Unicode string, the type every put back
// record uses.
func Ustr(name, code, value string) Record {
	chars := utf16.Encode([]rune(value))
	data := make([]byte, 4+len(chars)*2)
	binary.BigEndian.PutUint32(data[0:4], uint32(len(chars)))
	for i, c := range chars {
		binary.BigEndian.PutUint16(data[4+i*2:6+i*2], c)
	}
	return Record{Name: name, Code: code, Type: "ustr", Data: data}
}

// ustr decodes a "ustr" record's value. It reports false for a record of any
// other type.
func (r Record) Ustr() (string, bool) {
	if r.Type != "ustr" || len(r.Data) < 4 {
		return "", false
	}
	count := int(binary.BigEndian.Uint32(r.Data[0:4]))
	if count < 0 || 4+count*2 > len(r.Data) {
		return "", false
	}
	chars := make([]uint16, count)
	for i := range chars {
		chars[i] = binary.BigEndian.Uint16(r.Data[4+i*2 : 6+i*2])
	}
	return string(utf16.Decode(chars)), true
}

// size returns the number of bytes the record occupies inside a node.
func (r Record) size() int {
	return 4 + len(utf16.Encode([]rune(r.Name)))*2 + 8 + len(r.Data)
}

// dsValueLen returns the length.
func dsValueLen(typ string, data []byte) (int, error) {
	fixed := func(n int) (int, error) {
		if len(data) < n {
			return 0, fmt.Errorf("%w: truncated %s value", errBadDSStore, typ)
		}
		return n, nil
	}
	switch typ {
	case "bool":
		return fixed(1)
	case "long", "shor", "type":
		return fixed(4)
	case "comp", "dutc":
		return fixed(8)
	case "blob", "ustr":
		if len(data) < 4 {
			return 0, fmt.Errorf("%w: truncated %s header", errBadDSStore, typ)
		}
		count := int(binary.BigEndian.Uint32(data[0:4]))
		width := 1
		if typ == "ustr" {
			width = 2
		}
		if count < 0 || count > (len(data)-4)/width {
			return 0, fmt.Errorf("%w: %s value of %d runs past the block", errBadDSStore, typ, count)
		}
		return 4 + count*width, nil
	default:
		return 0, fmt.Errorf("%w: unknown data type %q", errBadDSStore, typ)
	}
}

// dsReader reads big-endian fields out.
type dsReader struct {
	buf []byte
	pos int
}

func (r *dsReader) u32() (uint32, error) {
	b, err := r.take(4)
	if err != nil {
		return 0, err
	}
	return binary.BigEndian.Uint32(b), nil
}

func (r *dsReader) byte() (byte, error) {
	b, err := r.take(1)
	if err != nil {
		return 0, err
	}
	return b[0], nil
}

func (r *dsReader) take(n int) ([]byte, error) {
	if n < 0 || r.pos+n > len(r.buf) {
		return nil, fmt.Errorf("%w: wanted %d bytes with %d left", errBadDSStore, n, len(r.buf)-r.pos)
	}
	b := r.buf[r.pos : r.pos+n]
	r.pos += n
	return b, nil
}

// utf16Str reads a big-endian Unicode string of count characters.
func (r *dsReader) utf16Str(count int) (string, error) {
	b, err := r.take(count * 2)
	if err != nil {
		return "", err
	}
	chars := make([]uint16, count)
	for i := range chars {
		chars[i] = binary.BigEndian.Uint16(b[i*2 : i*2+2])
	}
	return string(utf16.Decode(chars)), nil
}

// Parse decodes every record in a .DS_Store file, in the order they are stored.
func Parse(data []byte) ([]Record, error) {
	if len(data) < dsHeaderLen {
		return nil, fmt.Errorf("%w: %d bytes is too short", errBadDSStore, len(data))
	}
	if binary.BigEndian.Uint32(data[0:4]) != 1 || string(data[4:8]) != dsMagic {
		return nil, fmt.Errorf("%w: not a buddy allocator file", errBadDSStore)
	}
	offset := binary.BigEndian.Uint32(data[8:12])
	size := binary.BigEndian.Uint32(data[12:16])
	if binary.BigEndian.Uint32(data[16:20]) != offset {
		return nil, fmt.Errorf("%w: the two copies of the bookkeeping offset differ", errBadDSStore)
	}

	book, err := dsSlice(data, offset, size)
	if err != nil {
		return nil, err
	}
	addresses, directory, err := parseDSBookkeeping(book)
	if err != nil {
		return nil, err
	}
	master, ok := directory["DSDB"]
	if !ok {
		return nil, nil
	}

	block := func(number uint32) (*dsReader, error) {
		if int(number) >= len(addresses) {
			return nil, fmt.Errorf("%w: block %d is not in the address table", errBadDSStore, number)
		}
		addr := addresses[number]
		buf, err := dsSlice(data, addr&^0x1f, 1<<(addr&0x1f))
		if err != nil {
			return nil, err
		}
		return &dsReader{buf: buf}, nil
	}

	head, err := block(master)
	if err != nil {
		return nil, err
	}
	root, err := head.u32()
	if err != nil {
		return nil, err
	}

	var records []Record
	visited := set.New[uint32]()
	var walk func(number uint32) error
	walk = func(number uint32) error {
		if visited.Contains(number) {
			return fmt.Errorf("%w: block %d appears twice in the tree", errBadDSStore, number)
		}
		visited.Add(number)

		node, err := block(number)
		if err != nil {
			return err
		}
		next, err := node.u32()
		if err != nil {
			return err
		}
		count, err := node.u32()
		if err != nil {
			return err
		}
		for i := uint32(0); i < count; i++ {
			if next != 0 {
				child, err := node.u32()
				if err != nil {
					return err
				}
				if err := walk(child); err != nil {
					return err
				}
			}
			record, err := readDSRecord(node)
			if err != nil {
				return err
			}
			records = append(records, record)
		}
		if next != 0 {
			return walk(next)
		}
		return nil
	}
	if err := walk(root); err != nil {
		return nil, err
	}
	return records, nil
}

// dsSlice returns the bytes of the block at offset, remembering that offsets in the file.
func dsSlice(data []byte, offset, size uint32) ([]byte, error) {
	start, end := int(offset)+4, int(offset)+4+int(size)
	if offset > uint32(len(data)) || end > len(data) || start > end {
		return nil, fmt.Errorf("%w: block at %d+%d runs past the end of the file", errBadDSStore, offset, size)
	}
	return data[start:end], nil
}

// parseDSBookkeeping reads the allocator's block address table.
func parseDSBookkeeping(book []byte) ([]uint32, map[string]uint32, error) {
	r := &dsReader{buf: book}
	count, err := r.u32()
	if err != nil {
		return nil, nil, err
	}
	if _, err := r.u32(); err != nil { // unknown, and never set
		return nil, nil, err
	}
	if int(count) > len(book)/4 {
		return nil, nil, fmt.Errorf("%w: %d block addresses do not fit", errBadDSStore, count)
	}
	addresses := make([]uint32, count)
	for i := range addresses {
		if addresses[i], err = r.u32(); err != nil {
			return nil, nil, err
		}
	}
	// The table is padded with zeroes to a whole number of pages.
	if padding := (256 - int(count)%256) % 256; padding > 0 {
		if _, err := r.take(padding * 4); err != nil {
			return nil, nil, err
		}
	}

	entries, err := r.u32()
	if err != nil {
		return nil, nil, err
	}
	directory := make(map[string]uint32, entries)
	for i := uint32(0); i < entries; i++ {
		length, err := r.byte()
		if err != nil {
			return nil, nil, err
		}
		name, err := r.take(int(length))
		if err != nil {
			return nil, nil, err
		}
		number, err := r.u32()
		if err != nil {
			return nil, nil, err
		}
		directory[string(name)] = number
	}
	return addresses, directory, nil
}

// readDSRecord reads a record at the reader's current position.
func readDSRecord(r *dsReader) (Record, error) {
	length, err := r.u32()
	if err != nil {
		return Record{}, err
	}
	if int(length) > (len(r.buf)-r.pos)/2 {
		return Record{}, fmt.Errorf("%w: name of %d characters runs past the block", errBadDSStore, length)
	}
	name, err := r.utf16Str(int(length))
	if err != nil {
		return Record{}, err
	}
	head, err := r.take(8)
	if err != nil {
		return Record{}, err
	}
	code, typ := string(head[0:4]), string(head[4:8])
	size, err := dsValueLen(typ, r.buf[r.pos:])
	if err != nil {
		return Record{}, err
	}
	value, err := r.take(size)
	if err != nil {
		return Record{}, err
	}
	data := make([]byte, size)
	copy(data, value)
	return Record{Name: name, Code: code, Type: typ, Data: data}, nil
}

// sortDSRecords puts records into the order the Finder expects: by
// case-insensitive name, then by structure id.
func sortDSRecords(records []Record) {
	sort.SliceStable(records, func(i, j int) bool {
		a, b := records[i], records[j]
		al, bl := strings.ToLower(a.Name), strings.ToLower(b.Name)
		if al != bl {
			return al < bl
		}
		if a.Name != b.Name {
			return a.Name < b.Name
		}
		return a.Code < b.Code
	})
}
