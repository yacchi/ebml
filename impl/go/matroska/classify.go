package matroska

import "github.com/yacchi/ebml/impl/go/parser"

// KindForElementID is the classifier a cursor is constructed with (parser.New,
// parser.NewCursor): registered masters become parser.KindMaster, so the cursor
// descends into them, and everything else becomes a leaf.
//
// The parser has no distinct kinds for non-binary scalar values, so every
// registered non-master type other than TypeUint is reported as a binary leaf.
// An ID no registry in the chain knows is a binary leaf too: an unknown element
// never stops the reader, its declared size is honoured, and its bytes are
// readable -- see the package documentation.
//
// This is the method a consumer with vendor elements passes to the cursor:
//
//	reg := matroska.NewRegistry(matroska.Default())
//	_ = reg.Register(matroska.ElementInfo{ID: 0x4000, Name: "VendorBox", Type: matroska.TypeMaster})
//	p := parser.New(reg.KindForElementID)
func (r *Registry) KindForElementID(id parser.ElementID) parser.Kind {
	info, ok := r.Lookup(id)
	if !ok {
		return parser.KindBinary
	}
	switch info.Type {
	case TypeMaster:
		return parser.KindMaster
	case TypeUint:
		return parser.KindUint
	default:
		return parser.KindBinary
	}
}

// KindForElementID classifies the built-in element set. It is what a cursor over
// a standard Matroska stream is constructed with -- parser.New takes the
// classifier as a required argument, because the cursor itself knows no element
// IDs. See Registry.KindForElementID; a consumer with vendor elements uses that
// method on its own registry instead.
func KindForElementID(id parser.ElementID) parser.Kind {
	return Default().KindForElementID(id)
}

// StreamBoundary is the parser.BoundaryFunc for a stream of concatenated EBML
// documents, which is what a KVS GetMedia body is. It answers in two halves:
//
//   - a new top-level element (EBML, Segment) ends whatever master is open, so
//     consecutive documents follow one another instead of nesting; and
//   - otherwise the RFC 9559 child rule applies, so an unknown-size master such
//     as a Cluster ends at the first element that cannot be its child.
//
// The second half is deny-only -- see Registry.EndsUnknownSizeMaster -- so an
// element this registry does not know never ends a master. The rule is driven
// entirely by element structure and never by scanning payload bytes for the
// EBML magic, which PCM may contain.
//
// This is the whole boundary policy for such a stream, in one place: it is what
// ext/fragment's assembler and the ebml CLI are both built on, so neither can
// drift from the other.
func (r *Registry) StreamBoundary(open, next parser.ElementID) bool {
	if next == IDEBML || next == IDSegment {
		return true
	}
	return r.EndsUnknownSizeMaster(open, next)
}

// StreamBoundary applies the built-in element set; see Registry.StreamBoundary.
func StreamBoundary(open, next parser.ElementID) bool {
	return Default().StreamBoundary(open, next)
}
