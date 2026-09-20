package validator

import "github.com/wow-look-at-my/xml-validator/reader"

// Reading a document -- decoding its bytes, the character classes, the tree
// model and the tree parser -- lives in the reader module, which validation
// builds on and a program that only needs to READ XML can import on its own.
//
// These aliases keep this package's surface unchanged: validator.Document and
// reader.Document are one type, not two that convert.
type (
	Document = reader.Document
	Element  = reader.Element
	Attr     = reader.Attr
	CharData = reader.CharData
	Node     = reader.Node
	Error    = reader.Error
)

// ParseTree parses a document into a tree without validating it.
var ParseTree = reader.ParseTree

// The XML 1.1 character classes, re-exported for callers that had them here.
var (
	IsChar            = reader.IsChar
	IsCharRefValue    = reader.IsCharRefValue
	IsRestrictedChar  = reader.IsRestrictedChar
	IsWhitespace      = reader.IsWhitespace
	IsNameStartChar   = reader.IsNameStartChar
	IsNameChar        = reader.IsNameChar
	IsNCNameStartChar = reader.IsNCNameStartChar
	IsNCNameChar      = reader.IsNCNameChar
)
