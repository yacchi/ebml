package parser

import "fmt"

// Common constants used together with the parser.
const (
	UnknownSize int64 = -1

	MaxElementIDLength   = 4
	MaxElementSizeLength = 8
)

// ElementID is an EBML Element ID exactly as it appears on the wire: the VINT
// value *including* its length-marker bits, so IDs of different encoded lengths
// never collide and the encoded length is recoverable from the value alone.
//
// The parser deliberately knows no element at all -- no names, no value types,
// and not one element ID: an ElementID is only an element's opaque identity, and
// every decision about what an ID means comes from the KindClassifier the caller
// is required to supply. Package matroska owns the element registry that maps IDs
// to names, value types and kinds, and is the only place element IDs are written
// down.
type ElementID uint32

// String renders the ID in the conventional EBML notation: "0x" followed by
// uppercase hex covering exactly the ID's encoded bytes, with no padding beyond
// them -- a one-byte ID renders as two hex digits, a four-byte ID as eight.
func (id ElementID) String() string {
	digits := 2
	switch {
	case id > 0xFFFFFF:
		digits = 8
	case id > 0xFFFF:
		digits = 6
	case id > 0xFF:
		digits = 4
	}
	return fmt.Sprintf("0x%0*X", digits, uint32(id))
}
