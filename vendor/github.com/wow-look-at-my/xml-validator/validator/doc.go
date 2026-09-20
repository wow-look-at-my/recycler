// Package validator is a strict XML 1.1 validator with optional XSD schema
// validation. The same package powers the xml-validator command-line tool and
// can be embedded directly in Go programs.
//
// # Well-formedness validation
//
// Use [Validate] to verify that an input stream is a well-formed XML 1.1
// document. Anything the validator does not recognise (DOCTYPE, undeclared
// general entities, XML 1.0, an encoding that is neither UTF-8 nor
// ISO-8859-1) is a hard error:
//
//	err := validator.Validate(strings.NewReader(`<?xml version="1.1"?><r/>`))
//	if err != nil {
//	    // err is a *validator.Error with Line/Col/Message
//	    log.Fatal(err)
//	}
//
// # Schema validation
//
// Use [ValidateWithSchema] (io.Reader) or [ValidateWithSchemaBytes] to also
// validate the document against an XSD schema. Both helpers run XML
// well-formedness first and only then enforce the schema:
//
//	err := validator.ValidateWithSchemaBytes(xmlData, xsdData)
//
// If the schema uses xs:import directives with a schemaLocation, use
// [ValidateWithSchemaFile] to resolve them from the filesystem, or
// [ValidateWithSchemaResolver] to supply a custom [SchemaResolver].
//
// # Lower-level building blocks
//
// For callers that want to parse once and validate many times, or build their
// own pipelines, the package exposes [ParseTree], [ParseSchema] and
// [ValidateSchema] directly:
//
//	xmlDoc, err := validator.ParseTree(xmlReader)
//	xsdDoc, _ := validator.ParseTree(xsdReader)
//	schema, _ := validator.ParseSchema(xsdDoc)
//	err = validator.ValidateSchema(xmlDoc, schema)
//
// All validation errors are returned as *[Error], which carries the 1-based
// line and column of the offending construct.
package validator
