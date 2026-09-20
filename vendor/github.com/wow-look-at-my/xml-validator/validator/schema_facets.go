package validator

// Facet checking: the constraints an xs:restriction states over a value.

import (
	"encoding/base64"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

// validateLengthFacet applies minLength, maxLength and length.
//
// The unit is characters for a string type and octets for the binary types --
// never the bytes of the UTF-8 encoding, which counted a 256-character value
// as 384 as soon as one character was outside ASCII.
func validateLengthFacet(value, baseTypeName string, f Facet) error {
	n, err := strconv.Atoi(f.Value)
	if err != nil || n < 0 {
		return fmt.Errorf("%s facet value %q is not a non-negative integer", f.Kind, f.Value)
	}
	length, err := facetLength(value, baseTypeName)
	if err != nil {
		return err
	}
	switch f.Kind {
	case "minLength":
		if length < n {
			return fmt.Errorf("value length %d is less than minLength %d", length, n)
		}
	case "maxLength":
		if length > n {
			return fmt.Errorf("value length %d exceeds maxLength %d", length, n)
		}
	case "length":
		if length != n {
			return fmt.Errorf("value length %d does not equal required length %d", length, n)
		}
	}
	return nil
}

// facetLength measures a value in the unit its type states. A hexBinary digit
// pair and a base64 quantum both stand for octets, so a length facet on either
// counts what they decode to.
func facetLength(value, baseTypeName string) (int, error) {
	switch baseTypeName {
	case "hexBinary":
		if len(value)%2 != 0 {
			return 0, fmt.Errorf("hexBinary value has an odd number of digits")
		}
		return len(value) / 2, nil
	case "base64Binary":
		decoded, err := base64.StdEncoding.DecodeString(strings.Join(strings.Fields(value), ""))
		if err != nil {
			return 0, fmt.Errorf("base64Binary value does not decode: %v", err)
		}
		return len(decoded), nil
	default:
		return utf8.RuneCountInString(value), nil
	}
}

func validateFacet(value, baseTypeName string, f Facet) error {
	switch f.Kind {
	case "enumeration":
		if value != f.Value {
			return nil // checked collectively
		}
	case "pattern":
		re, err := regexp.Compile("^(?:" + f.Value + ")$")
		if err != nil {
			return fmt.Errorf("invalid pattern facet %q: %v", f.Value, err)
		}
		if !re.MatchString(value) {
			return fmt.Errorf("value %q does not match pattern %q", value, f.Value)
		}
	case "minLength", "maxLength", "length":
		return validateLengthFacet(value, baseTypeName, f)
	case "minInclusive":
		if err := compareFacet(value, f.Value, baseTypeName, ">="); err != nil {
			return err
		}
	case "maxInclusive":
		if err := compareFacet(value, f.Value, baseTypeName, "<="); err != nil {
			return err
		}
	case "minExclusive":
		if err := compareFacet(value, f.Value, baseTypeName, ">"); err != nil {
			return err
		}
	case "maxExclusive":
		if err := compareFacet(value, f.Value, baseTypeName, "<"); err != nil {
			return err
		}
	case "totalDigits":
		n, _ := strconv.Atoi(f.Value)
		digits := countDigits(value)
		if digits > n {
			return fmt.Errorf("value has %d digits, exceeds totalDigits %d", digits, n)
		}
	case "fractionDigits":
		n, _ := strconv.Atoi(f.Value)
		frac := countFractionDigits(value)
		if frac > n {
			return fmt.Errorf("value has %d fraction digits, exceeds fractionDigits %d", frac, n)
		}
	case "whiteSpace":
		// handled during normalization, not validation
	}
	return nil
}

func validateEnumerationFacets(value string, facets []Facet) error {
	var enums []string
	for _, f := range facets {
		if f.Kind == "enumeration" {
			enums = append(enums, f.Value)
			if f.Value == value {
				return nil
			}
		}
	}
	if len(enums) > 0 {
		return fmt.Errorf("value %q is not one of the allowed values: %s", value, strings.Join(enums, ", "))
	}
	return nil
}

func compareFacet(value, facetVal, baseTypeName, op string) error {
	switch {
	case isNumericType(baseTypeName):
		v, err1 := strconv.ParseFloat(strings.TrimSpace(value), 64)
		f, err2 := strconv.ParseFloat(strings.TrimSpace(facetVal), 64)
		if err1 != nil || err2 != nil {
			return nil
		}
		if !compareOp(v, f, op) {
			return fmt.Errorf("value %s must be %s %s", value, op, facetVal)
		}
	case isDateType(baseTypeName):
		vt, err1 := time.Parse("2006-01-02", strings.TrimSpace(value))
		ft, err2 := time.Parse("2006-01-02", strings.TrimSpace(facetVal))
		if err1 != nil || err2 != nil {
			return nil
		}
		diff := vt.Compare(ft)
		if !compareIntOp(diff, op) {
			return fmt.Errorf("value %s must be %s %s", value, op, facetVal)
		}
	}
	return nil
}

func isNumericType(name string) bool {
	switch name {
	case "decimal", "integer", "nonNegativeInteger", "positiveInteger",
		"nonPositiveInteger", "negativeInteger", "long", "int", "short", "byte",
		"unsignedLong", "unsignedInt", "unsignedShort", "unsignedByte",
		"float", "double":
		return true
	}
	return false
}

func isDateType(name string) bool {
	return name == "date" || name == "dateTime"
}

func compareOp(a, b float64, op string) bool {
	switch op {
	case ">=":
		return a >= b
	case "<=":
		return a <= b
	case ">":
		return a > b
	case "<":
		return a < b
	}
	return false
}

func compareIntOp(cmp int, op string) bool {
	switch op {
	case ">=":
		return cmp >= 0
	case "<=":
		return cmp <= 0
	case ">":
		return cmp > 0
	case "<":
		return cmp < 0
	}
	return false
}

func countDigits(s string) int {
	count := 0
	for _, c := range s {
		if c >= '0' && c <= '9' {
			count++
		}
	}
	return count
}

func countFractionDigits(s string) int {
	idx := strings.Index(s, ".")
	if idx < 0 {
		return 0
	}
	count := 0
	for _, c := range s[idx+1:] {
		if c >= '0' && c <= '9' {
			count++
		}
	}
	return count
}
