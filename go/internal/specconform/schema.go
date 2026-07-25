// Package specconform checks the hand-written go/matroska registry against an
// official EBML Schema document.
//
// The schema documents are NOT vendored into this repository. They are
// CC-BY-4.0 works maintained by the IETF CELLAR working group, and this
// package is only useful during development, so the checker takes a PATH and
// the caller fetches the document it wants to check against. See
// .claude/skills/conformance-check for the retrieval step.
//
// The registry stays hand-written on purpose: it is a deliberate selection with
// deliberate omissions (deprecated elements are absent from the containment
// lists so they can never trigger an early boundary), and generating it would
// erase that intent. This package is what keeps a hand-written table honest --
// it reports where the registry CONTRADICTS the schema (a defect) apart from
// where it is merely SILENT (coverage work).
//
// Nothing in this package copies schema prose. It reads structural facts --
// element IDs, names, types, paths -- and reports differences; element
// documentation text is never carried into this repository.
package specconform

import (
	"encoding/xml"
	"fmt"
	"os"
	"regexp"
	"strconv"
	"strings"

	"github.com/yacchi/ebml/parser"
)

// Schema is one parsed EBML Schema document, in the XML form defined by
// RFC 8794 Section 11.
type Schema struct {
	// Source is the file the schema was read from, for reporting.
	Source string
	// DocType is the schema's declared document type ("ebml", "matroska").
	DocType string
	// Version is the schema's declared docType version.
	Version string
	// Elements is every element the schema declares, in document order.
	Elements []SchemaElement
}

// SchemaElement is one <element> declaration.
type SchemaElement struct {
	Name string
	ID   parser.ElementID
	// Type is the raw schema type token ("master", "uinteger", "utf-8", ...),
	// kept unmapped so an unrecognized token is reported rather than silently
	// coerced.
	Type string
	// Path is the declared path, verbatim, including the recursive "+" marker
	// and the global-element parenthesis form.
	Path string
	// Parent is the path of the master this element sits directly inside, or
	// the empty string for a global element, which has no single parent. A
	// root element's parent is `\`.
	Parent string
	// Global reports the `\(n-m\)Name` form: the element may occur inside any
	// master at the declared depth range.
	Global bool
	// GlobalMinLevel is the lower bound of a global element's depth range, or
	// -1 when the schema left it open.
	GlobalMinLevel int
	// Recursive reports the `+Name` marker: the element may nest inside itself,
	// so it is also its own direct child.
	Recursive bool
	// UnknownSizeAllowed reports unknownsizeallowed="1": the master may be
	// written with an unknown size, which is what makes a containment-derived
	// boundary rule necessary for it.
	UnknownSizeAllowed bool
	// Range and Default are kept for the EBML header constraints; they are
	// unused for Matroska body elements.
	Range   string
	Default string
}

type xmlSchema struct {
	XMLName  xml.Name     `xml:"EBMLSchema"`
	DocType  string       `xml:"docType,attr"`
	Version  string       `xml:"version,attr"`
	Elements []xmlElement `xml:"element"`
}

type xmlElement struct {
	Name               string `xml:"name,attr"`
	Path               string `xml:"path,attr"`
	ID                 string `xml:"id,attr"`
	Type               string `xml:"type,attr"`
	Range              string `xml:"range,attr"`
	Default            string `xml:"default,attr"`
	UnknownSizeAllowed string `xml:"unknownsizeallowed,attr"`
}

// LoadSchema reads and parses an EBML Schema document from path.
func LoadSchema(path string) (*Schema, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return ParseSchema(path, data)
}

// ParseSchema parses an EBML Schema document. source names the origin for
// reporting only.
func ParseSchema(source string, data []byte) (*Schema, error) {
	var doc xmlSchema
	if err := xml.Unmarshal(data, &doc); err != nil {
		return nil, fmt.Errorf("%s: %w", source, err)
	}
	schema := &Schema{Source: source, DocType: doc.DocType, Version: doc.Version}
	for _, e := range doc.Elements {
		id, err := parseSchemaID(e.ID)
		if err != nil {
			return nil, fmt.Errorf("%s: element %s: %w", source, e.Name, err)
		}
		element := SchemaElement{
			Name:               e.Name,
			ID:                 id,
			Type:               e.Type,
			Path:               e.Path,
			UnknownSizeAllowed: e.UnknownSizeAllowed == "1",
			Range:              e.Range,
			Default:            e.Default,
		}
		if err := element.parsePath(); err != nil {
			return nil, fmt.Errorf("%s: element %s: %w", source, e.Name, err)
		}
		schema.Elements = append(schema.Elements, element)
	}
	return schema, nil
}

func parseSchemaID(s string) (parser.ElementID, error) {
	if !strings.HasPrefix(s, "0x") && !strings.HasPrefix(s, "0X") {
		return 0, fmt.Errorf("element ID %q is not hexadecimal", s)
	}
	v, err := strconv.ParseUint(s[2:], 16, 32)
	if err != nil {
		return 0, fmt.Errorf("element ID %q: %w", s, err)
	}
	return parser.ElementID(v), nil
}

// globalPathRe matches the global-element path form of RFC 8794 Section 11.1.6.2,
// `\(n-m\)Name`, where both bounds may be omitted. The leading group is the
// path of the subtree the element is global within, which for both Matroska
// global elements is the document root.
var globalPathRe = regexp.MustCompile(`^(\\.*?)\((\d*)-(\d*)\\\)([^\\]+)$`)

func (e *SchemaElement) parsePath() error {
	if e.Path == "" {
		return fmt.Errorf("empty path")
	}
	if m := globalPathRe.FindStringSubmatch(e.Path); m != nil {
		e.Global = true
		e.GlobalMinLevel = -1
		if m[2] != "" {
			level, err := strconv.Atoi(m[2])
			if err != nil {
				return fmt.Errorf("path %q: %w", e.Path, err)
			}
			e.GlobalMinLevel = level
		}
		if name := m[4]; name != e.Name {
			return fmt.Errorf("path %q ends in %q, not the element name", e.Path, name)
		}
		return nil
	}
	cut := strings.LastIndex(e.Path, `\`)
	if cut < 0 {
		return fmt.Errorf("path %q has no separator", e.Path)
	}
	leaf := e.Path[cut+1:]
	e.Recursive = strings.HasPrefix(leaf, "+")
	if strings.TrimPrefix(leaf, "+") != e.Name {
		return fmt.Errorf("path %q ends in %q, not the element name", e.Path, leaf)
	}
	e.Parent = e.Path[:cut]
	if e.Parent == "" {
		// A root element: its parent is the document itself.
		e.Parent = `\`
	}
	return nil
}

// Children returns the elements declared as direct children of the master at
// path, in schema order. A recursive element is a child of itself as well as of
// its declared parent.
func (s *Schema) Children(path string) []SchemaElement {
	var children []SchemaElement
	for _, e := range s.Elements {
		if e.Global {
			continue
		}
		if e.Parent == path || (e.Recursive && e.Path == path) {
			children = append(children, e)
		}
	}
	return children
}

// ByID returns the declaration of id.
func (s *Schema) ByID(id parser.ElementID) (SchemaElement, bool) {
	for _, e := range s.Elements {
		if e.ID == id {
			return e, true
		}
	}
	return SchemaElement{}, false
}

// Globals returns every element the schema declares as global.
func (s *Schema) Globals() []SchemaElement {
	var globals []SchemaElement
	for _, e := range s.Elements {
		if e.Global {
			globals = append(globals, e)
		}
	}
	return globals
}
