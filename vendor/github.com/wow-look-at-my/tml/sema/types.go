package sema

import (
	"fmt"
	"strconv"
	"strings"
)

// Kind is the class of a property type.
type Kind int

const (
	KindString Kind = iota
	KindInt
	KindBool
	KindColor
	KindLength
	KindThickness
	KindEnum
	// KindRecord is a value with named fields. It is the only kind a host can build but a document cannot write as a
	KindRecord
)

func (k Kind) String() string {
	switch k {
	case KindString:
		return "string"
	case KindInt:
		return "int"
	case KindBool:
		return "bool"
	case KindColor:
		return "color"
	case KindLength:
		return "length"
	case KindThickness:
		return "thickness"
	case KindEnum:
		return "enum"
	case KindRecord:
		return "record"
	default:
		return "invalid"
	}
}

// Type is a declared property type, optionally a list of that type.
type Type struct {
	Kind   Kind
	Enum   []string
	IsList bool
}

func (t Type) String() string {
	base := t.Kind.String()
	if t.Kind == KindEnum {
		base = "enum(" + strings.Join(t.Enum, "|") + ")"
	}
	if t.IsList {
		return base + "[]"
	}
	return base
}

// ParseType reads a type as written in a type attribute: "string", "int[]", "enum(left|center|right)". The list suffix
func ParseType(src string) (Type, error) {
	var t Type
	if rest, ok := strings.CutSuffix(src, "[]"); ok {
		t.IsList = true
		src = rest
	}
	if body, ok := cutEnum(src); ok {
		t.Kind = KindEnum
		for _, member := range strings.Split(body, "|") {
			member = strings.TrimSpace(member)
			if member == "" {
				return Type{}, fmt.Errorf("enum type has an empty member")
			}
			t.Enum = append(t.Enum, member)
		}
		if len(t.Enum) < 2 {
			return Type{}, fmt.Errorf("enum type needs at least two members")
		}
		return t, nil
	}
	switch src {
	case "string":
		t.Kind = KindString
	case "int":
		t.Kind = KindInt
	case "bool":
		t.Kind = KindBool
	case "color":
		t.Kind = KindColor
	case "length":
		t.Kind = KindLength
	case "thickness":
		t.Kind = KindThickness
	case "record":
		// A record has no literal spelling: `default="..."` cannot write a record, and an attribute cannot either. It is the type
		t.Kind = KindRecord
	default:
		return Type{}, fmt.Errorf("unknown type %q", src)
	}
	return t, nil
}

func cutEnum(src string) (string, bool) {
	if !strings.HasPrefix(src, "enum(") || !strings.HasSuffix(src, ")") {
		return "", false
	}
	return src[len("enum(") : len(src)-1], true
}

// LengthKind distinguishes the several ways a size can be expressed.
type LengthKind int

const (
	// LengthAuto sizes to content.
	LengthAuto LengthKind = iota
	// LengthCells is a fixed number of terminal cells.
	LengthCells
	// LengthStar takes a weighted share of whatever space is left.
	LengthStar
)

// Length is a size along a single axis.
type Length struct {
	Kind   LengthKind
	Cells  int
	Weight int
}

func (l Length) String() string {
	switch l.Kind {
	case LengthAuto:
		return "auto"
	case LengthCells:
		return strconv.Itoa(l.Cells)
	case LengthStar:
		if l.Weight == 1 {
			return "*"
		}
		return strconv.Itoa(l.Weight) + "*"
	default:
		return "invalid"
	}
}

// ParseLength reads "auto", a cell count, or a star share such as "*".
func ParseLength(src string) (Length, error) {
	if src == "auto" {
		return Length{Kind: LengthAuto}, nil
	}
	if weight, ok := strings.CutSuffix(src, "*"); ok {
		if weight == "" {
			return Length{Kind: LengthStar, Weight: 1}, nil
		}
		n, err := strconv.Atoi(weight)
		if err != nil || n < 1 {
			return Length{}, fmt.Errorf("star weight must be a positive whole number, got %q", src)
		}
		return Length{Kind: LengthStar, Weight: n}, nil
	}
	n, err := strconv.Atoi(src)
	if err != nil || n < 0 {
		return Length{}, fmt.Errorf("length must be auto, a cell count, or a star share, got %q", src)
	}
	return Length{Kind: LengthCells, Cells: n}, nil
}

// Thickness is a per-side measurement: padding, margin, or a gap pair.
type Thickness struct {
	Top, Right, Bottom, Left int
}

func (t Thickness) String() string {
	return fmt.Sprintf("%d %d %d %d", t.Top, t.Right, t.Bottom, t.Left)
}

// Horizontal is the total space consumed on the horizontal axis.
func (t Thickness) Horizontal() int { return t.Left + t.Right }

// Vertical is the total space consumed on the vertical axis.
func (t Thickness) Vertical() int { return t.Top + t.Bottom }

// ParseThickness reads the CSS-style shorthand: a single value for all sides, a pair of for vertical then horizontal, or several
func ParseThickness(src string) (Thickness, error) {
	fields := strings.Fields(src)
	nums := make([]int, 0, len(fields))
	for _, f := range fields {
		n, err := strconv.Atoi(f)
		if err != nil || n < 0 {
			return Thickness{}, fmt.Errorf("thickness values must be whole numbers of cells, got %q", f)
		}
		nums = append(nums, n)
	}
	switch len(nums) {
	case 1:
		return Thickness{nums[0], nums[0], nums[0], nums[0]}, nil
	case 2:
		return Thickness{nums[0], nums[1], nums[0], nums[1]}, nil
	case 4:
		return Thickness{nums[0], nums[1], nums[2], nums[3]}, nil
	default:
		return Thickness{}, fmt.Errorf("thickness takes 1, 2 or 4 values, got %d", len(nums))
	}
}
