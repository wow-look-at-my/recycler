package validator

import (
	"fmt"
	"math"
	"regexp"
	"strconv"
	"strings"
)

type BuiltinType struct {
	name     string
	validate func(string) error
}

func (b *BuiltinType) typeName() string { return b.name }

var builtinTypes = map[string]*BuiltinType{}

func init() {
	register := func(name string, fn func(string) error) {
		builtinTypes[name] = &BuiltinType{name: name, validate: fn}
	}

	register("anyType", func(string) error { return nil })
	register("anySimpleType", func(string) error { return nil })
	register("string", func(string) error { return nil })
	register("normalizedString", validateNormalizedString)
	register("token", validateToken)
	register("boolean", validateBoolean)
	register("decimal", validateDecimal)
	register("integer", validateInteger)
	register("nonNegativeInteger", validateNonNegativeInteger)
	register("positiveInteger", validatePositiveInteger)
	register("nonPositiveInteger", validateNonPositiveInteger)
	register("negativeInteger", validateNegativeInteger)
	register("long", makeIntRange("long", math.MinInt64, math.MaxInt64))
	register("int", makeIntRange("int", math.MinInt32, math.MaxInt32))
	register("short", makeIntRange("short", -32768, 32767))
	register("byte", makeIntRange("byte", -128, 127))
	register("unsignedLong", makeUintRange("unsignedLong", math.MaxUint64))
	register("unsignedInt", makeUintRange("unsignedInt", math.MaxUint32))
	register("unsignedShort", makeUintRange("unsignedShort", 65535))
	register("unsignedByte", makeUintRange("unsignedByte", 255))
	register("float", validateFloat)
	register("double", validateDouble)
	register("date", validateDate)
	register("dateTime", validateDateTime)
	register("time", validateTime)
	register("duration", validateDuration)
	register("gYear", validateGYear)
	register("gMonth", validateGMonth)
	register("gDay", validateGDay)
	register("gYearMonth", validateGYearMonth)
	register("gMonthDay", validateGMonthDay)
	register("hexBinary", validateHexBinary)
	register("base64Binary", validateBase64Binary)
	register("anyURI", func(string) error { return nil })
	register("QName", validateQNameValue)
	register("NCName", validateNCName)
	register("Name", validateNameValue)
	register("NMTOKEN", validateNMTOKEN)
	register("language", validateLanguage)
	register("ID", validateNCName)
	register("IDREF", validateNCName)
	register("IDREFS", validateIDREFS)
	register("NMTOKENS", validateNMTOKENS)
	register("ENTITY", validateNCName)
	register("ENTITIES", validateIDREFS)
}

func resolveBuiltinType(name string) *BuiltinType {
	return builtinTypes[name]
}

func validateBuiltinValue(typeName, value string) error {
	bt := builtinTypes[typeName]
	if bt == nil {
		return fmt.Errorf("unknown built-in type %q", typeName)
	}
	return bt.validate(value)
}

func validateNormalizedString(s string) error {
	if strings.ContainsAny(s, "\r\n\t") {
		return fmt.Errorf("normalizedString must not contain CR, LF, or TAB")
	}
	return nil
}

func validateToken(s string) error {
	if err := validateNormalizedString(s); err != nil {
		return err
	}
	if s != strings.TrimSpace(s) || strings.Contains(s, "  ") {
		return fmt.Errorf("token must not have leading/trailing spaces or internal double spaces")
	}
	return nil
}

func validateBoolean(s string) error {
	switch s {
	case "true", "false", "1", "0":
		return nil
	}
	return fmt.Errorf("%q is not a valid boolean", s)
}

func validateDecimal(s string) error {
	s = strings.TrimSpace(s)
	if s == "" {
		return fmt.Errorf("empty decimal")
	}
	i := 0
	if s[0] == '+' || s[0] == '-' {
		i++
	}
	dotSeen := false
	digits := false
	for ; i < len(s); i++ {
		if s[i] == '.' {
			if dotSeen {
				return fmt.Errorf("%q is not a valid decimal", s)
			}
			dotSeen = true
		} else if s[i] >= '0' && s[i] <= '9' {
			digits = true
		} else {
			return fmt.Errorf("%q is not a valid decimal", s)
		}
	}
	if !digits {
		return fmt.Errorf("%q is not a valid decimal", s)
	}
	return nil
}

func validateInteger(s string) error {
	s = strings.TrimSpace(s)
	if s == "" {
		return fmt.Errorf("empty integer")
	}
	i := 0
	if s[0] == '+' || s[0] == '-' {
		i++
	}
	if i >= len(s) {
		return fmt.Errorf("%q is not a valid integer", s)
	}
	for ; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return fmt.Errorf("%q is not a valid integer", s)
		}
	}
	return nil
}

func validateNonNegativeInteger(s string) error {
	if err := validateInteger(s); err != nil {
		return err
	}
	s = strings.TrimSpace(s)
	if s[0] == '-' && s != "-0" {
		return fmt.Errorf("%q is not a non-negative integer", s)
	}
	return nil
}

func validatePositiveInteger(s string) error {
	if err := validateNonNegativeInteger(s); err != nil {
		return err
	}
	s = strings.TrimSpace(s)
	for _, c := range s {
		if c != '0' && c != '+' {
			return nil
		}
	}
	return fmt.Errorf("%q is not a positive integer", s)
}

func validateNonPositiveInteger(s string) error {
	if err := validateInteger(s); err != nil {
		return err
	}
	s = strings.TrimSpace(s)
	if s[0] != '-' && s[0] != '0' {
		clean := strings.TrimLeft(s, "+0")
		if clean != "" {
			return fmt.Errorf("%q is not a non-positive integer", s)
		}
	}
	return nil
}

func validateNegativeInteger(s string) error {
	if err := validateInteger(s); err != nil {
		return err
	}
	s = strings.TrimSpace(s)
	if s[0] != '-' {
		return fmt.Errorf("%q is not a negative integer", s)
	}
	for _, c := range s[1:] {
		if c != '0' {
			return nil
		}
	}
	return fmt.Errorf("%q is not a negative integer", s)
}

func makeIntRange(name string, min, max int64) func(string) error {
	return func(s string) error {
		if err := validateInteger(s); err != nil {
			return err
		}
		v, err := strconv.ParseInt(strings.TrimSpace(s), 10, 64)
		if err != nil {
			return fmt.Errorf("%q overflows %s", s, name)
		}
		if v < min || v > max {
			return fmt.Errorf("%q is out of range for %s", s, name)
		}
		return nil
	}
}

func makeUintRange(name string, max uint64) func(string) error {
	return func(s string) error {
		if err := validateNonNegativeInteger(s); err != nil {
			return err
		}
		v, err := strconv.ParseUint(strings.TrimSpace(s), 10, 64)
		if err != nil {
			return fmt.Errorf("%q overflows %s", s, name)
		}
		if v > max {
			return fmt.Errorf("%q is out of range for %s", s, name)
		}
		return nil
	}
}

func validateFloat(s string) error {
	s = strings.TrimSpace(s)
	if s == "INF" || s == "-INF" || s == "NaN" {
		return nil
	}
	_, err := strconv.ParseFloat(s, 32)
	if err != nil {
		return fmt.Errorf("%q is not a valid float", s)
	}
	return nil
}

func validateDouble(s string) error {
	s = strings.TrimSpace(s)
	if s == "INF" || s == "-INF" || s == "NaN" {
		return nil
	}
	_, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return fmt.Errorf("%q is not a valid double", s)
	}
	return nil
}

var dateRE = regexp.MustCompile(`^-?\d{4,}-\d{2}-\d{2}(Z|[+-]\d{2}:\d{2})?$`)

func validateDate(s string) error {
	s = strings.TrimSpace(s)
	if !dateRE.MatchString(s) {
		return fmt.Errorf("%q is not a valid date", s)
	}
	return nil
}

var dateTimeRE = regexp.MustCompile(`^-?\d{4,}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}(\.\d+)?(Z|[+-]\d{2}:\d{2})?$`)

func validateDateTime(s string) error {
	s = strings.TrimSpace(s)
	if !dateTimeRE.MatchString(s) {
		return fmt.Errorf("%q is not a valid dateTime", s)
	}
	return nil
}

var timeRE = regexp.MustCompile(`^\d{2}:\d{2}:\d{2}(\.\d+)?(Z|[+-]\d{2}:\d{2})?$`)

func validateTime(s string) error {
	s = strings.TrimSpace(s)
	if !timeRE.MatchString(s) {
		return fmt.Errorf("%q is not a valid time", s)
	}
	return nil
}

var durationRE = regexp.MustCompile(`^-?P(\d+Y)?(\d+M)?(\d+D)?(T(\d+H)?(\d+M)?(\d+(\.\d+)?S)?)?$`)

func validateDuration(s string) error {
	s = strings.TrimSpace(s)
	if !durationRE.MatchString(s) || s == "P" || s == "-P" || strings.HasSuffix(s, "T") {
		return fmt.Errorf("%q is not a valid duration", s)
	}
	return nil
}

var gYearRE = regexp.MustCompile(`^-?\d{4,}(Z|[+-]\d{2}:\d{2})?$`)

func validateGYear(s string) error {
	s = strings.TrimSpace(s)
	if !gYearRE.MatchString(s) {
		return fmt.Errorf("%q is not a valid gYear", s)
	}
	return nil
}

var gMonthRE = regexp.MustCompile(`^--\d{2}(Z|[+-]\d{2}:\d{2})?$`)

func validateGMonth(s string) error {
	s = strings.TrimSpace(s)
	if !gMonthRE.MatchString(s) {
		return fmt.Errorf("%q is not a valid gMonth", s)
	}
	return nil
}

var gDayRE = regexp.MustCompile(`^---\d{2}(Z|[+-]\d{2}:\d{2})?$`)

func validateGDay(s string) error {
	s = strings.TrimSpace(s)
	if !gDayRE.MatchString(s) {
		return fmt.Errorf("%q is not a valid gDay", s)
	}
	return nil
}

var gYearMonthRE = regexp.MustCompile(`^-?\d{4,}-\d{2}(Z|[+-]\d{2}:\d{2})?$`)

func validateGYearMonth(s string) error {
	s = strings.TrimSpace(s)
	if !gYearMonthRE.MatchString(s) {
		return fmt.Errorf("%q is not a valid gYearMonth", s)
	}
	return nil
}

var gMonthDayRE = regexp.MustCompile(`^--\d{2}-\d{2}(Z|[+-]\d{2}:\d{2})?$`)

func validateGMonthDay(s string) error {
	s = strings.TrimSpace(s)
	if !gMonthDayRE.MatchString(s) {
		return fmt.Errorf("%q is not a valid gMonthDay", s)
	}
	return nil
}

var hexBinaryRE = regexp.MustCompile(`^([0-9a-fA-F]{2})*$`)

func validateHexBinary(s string) error {
	s = strings.TrimSpace(s)
	if !hexBinaryRE.MatchString(s) {
		return fmt.Errorf("%q is not valid hexBinary", s)
	}
	return nil
}

var base64RE = regexp.MustCompile(`^[A-Za-z0-9+/\s]*(={0,2}\s*)?$`)

func validateBase64Binary(s string) error {
	clean := strings.Map(func(r rune) rune {
		if r == ' ' || r == '\n' || r == '\r' || r == '\t' {
			return -1
		}
		return r
	}, s)
	if len(clean)%4 != 0 {
		return fmt.Errorf("%q is not valid base64Binary", s)
	}
	if !base64RE.MatchString(s) {
		return fmt.Errorf("%q is not valid base64Binary", s)
	}
	return nil
}

func validateQNameValue(s string) error {
	s = strings.TrimSpace(s)
	parts := strings.SplitN(s, ":", 2)
	for _, p := range parts {
		if err := validateNCName(p); err != nil {
			return fmt.Errorf("%q is not a valid QName", s)
		}
	}
	return nil
}

func validateNCName(s string) error {
	s = strings.TrimSpace(s)
	if s == "" {
		return fmt.Errorf("empty NCName")
	}
	runes := []rune(s)
	if !IsNCNameStartChar(runes[0]) {
		return fmt.Errorf("%q is not a valid NCName", s)
	}
	for _, r := range runes[1:] {
		if !IsNCNameChar(r) {
			return fmt.Errorf("%q is not a valid NCName", s)
		}
	}
	return nil
}

func validateNameValue(s string) error {
	s = strings.TrimSpace(s)
	if s == "" {
		return fmt.Errorf("empty Name")
	}
	runes := []rune(s)
	if !IsNameStartChar(runes[0]) {
		return fmt.Errorf("%q is not a valid Name", s)
	}
	for _, r := range runes[1:] {
		if !IsNameChar(r) {
			return fmt.Errorf("%q is not a valid Name", s)
		}
	}
	return nil
}

func validateNMTOKEN(s string) error {
	s = strings.TrimSpace(s)
	if s == "" {
		return fmt.Errorf("empty NMTOKEN")
	}
	for _, r := range s {
		if !IsNameChar(r) {
			return fmt.Errorf("%q is not a valid NMTOKEN", s)
		}
	}
	return nil
}

func validateIDREFS(s string) error {
	s = strings.TrimSpace(s)
	if s == "" {
		return fmt.Errorf("empty list")
	}
	for _, token := range strings.Fields(s) {
		if err := validateNCName(token); err != nil {
			return err
		}
	}
	return nil
}

func validateNMTOKENS(s string) error {
	s = strings.TrimSpace(s)
	if s == "" {
		return fmt.Errorf("empty NMTOKENS")
	}
	for _, token := range strings.Fields(s) {
		if err := validateNMTOKEN(token); err != nil {
			return err
		}
	}
	return nil
}

var languageRE = regexp.MustCompile(`^[a-zA-Z]{1,8}(-[a-zA-Z0-9]{1,8})*$`)

func validateLanguage(s string) error {
	s = strings.TrimSpace(s)
	if !languageRE.MatchString(s) {
		return fmt.Errorf("%q is not a valid language tag", s)
	}
	return nil
}
