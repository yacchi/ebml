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
// never collide (SimpleBlock is 0xA3, Cluster is 0x1F43B675).
//
// The parser deliberately knows no element names or value types; an ElementID is
// only an element's identity. Package matroska owns the element registry that
// maps IDs to names, value types and kinds.
type ElementID uint32

// String renders the ID in the conventional EBML notation: "0x" followed by
// uppercase hex covering exactly the ID's encoded bytes, with no padding beyond
// them -- ElementID(0xA3) is "0xA3", ElementID(0x1F43B675) is "0x1F43B675".
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

// defaultKindClassifier is used when New is called without WithKindClassifier.
// It knows only the two elements that structurally frame every EBML document --
// the EBML header and Segment -- and reports every other ID as a binary leaf,
// because the parser holds no element registry of its own. Callers reading real
// Matroska streams should pass matroska.KindForElementID, which classifies the
// full standard element set (without it, e.g. a Cluster is read as one opaque
// binary blob instead of being entered as a master).
func defaultKindClassifier(id ElementID) Kind {
	switch id {
	case 0x1A45DFA3, // EBML header
		0x18538067: // Segment
		return KindMaster
	default:
		return KindBinary
	}
}
