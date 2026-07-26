package writer

import "fmt"

// strategyKind enumerates how an element's size VINT is produced. Buffered is the
// zero value, so the zero SizeStrategy is the default one.
type strategyKind int

const (
	strategyBuffered strategyKind = iota
	strategyReserved
	strategyUnknown
)

// SizeStrategy states how an element's size VINT is produced. A master's size is
// only known once its children are written, yet the size VINT precedes the
// payload, so a writer needs one of three answers to that: buffer the subtree,
// reserve the width and patch it, or declare the size unknown and let the reader
// find the end structurally.
//
// Construct one with Buffered, Reserved or UnknownSize. The zero value is
// Buffered, so StartMaster(id, writer.SizeStrategy{}) buffers.
type SizeStrategy struct {
	kind  strategyKind
	width int
}

// Buffered returns the default strategy: hold the element's payload in memory and
// emit its exact size, in the minimal VINT width, when the element ends.
//
// Trade-offs: it works with any io.Writer, needs no patching, and produces the
// smallest possible header, but the whole subtree is held in memory until
// EndMaster, and none of its bytes reach the sink before then. Prefer it for
// small-to-moderate masters, and for any sink that cannot be patched.
//
// It applies to leaves as well: Writer.Leaf uses it, since a leaf payload is
// already complete when it is written and only its width is at stake.
func Buffered() SizeStrategy {
	return SizeStrategy{kind: strategyBuffered}
}

// Reserved returns a strategy that emits a size VINT of exactly width bytes
// immediately and patches it with the real size when the element ends. width must
// be 1 to MaxSizeWidth (8) bytes; StartMaster reports *SizeWidthError otherwise.
//
// Nothing is buffered — the payload streams straight to the sink — and the header
// stays a fixed width whatever the payload turns out to be. EBML permits a
// non-minimal size VINT, so the reserved width is spec-legal and no bytes ever have
// to be shifted; a reader reports the wider header through its header length.
//
// Two constraints follow. First, the bytes already written must be reachable again:
// the sink must implement io.WriterAt, unless an enclosing Buffered master is
// holding the bytes in memory, where the patch happens in that buffer instead.
// StartMaster reports *NotPatchableError when neither holds — before writing
// anything — so the caller can fall back to Buffered. Second, the final size must
// fit the reserved width: EndMaster reports *SizeOverflowError otherwise, and since
// the payload has already been emitted by then, that failure is terminal. Size the
// reservation from the maximum payload the caller can produce (a 4-byte width
// covers about 256 MiB, 8 bytes cover any legal element).
//
// On a leaf, Reserved simply means "write the size in exactly this many bytes":
// the size is already known, so nothing is patched and no patchable sink is needed.
// That is how an element's original, possibly non-minimal, header is reproduced
// byte for byte.
func Reserved(width int) SizeStrategy {
	return SizeStrategy{kind: strategyReserved, width: width}
}

// UnknownSize returns a strategy that emits the all-ones unknown-size marker (see
// UnknownSizeVINT) and patches nothing: the element declares no end at all, and
// EndMaster writes no bytes.
//
// It is valid for MASTERS only. An unbounded leaf could not be read past — neither
// its payload nor the element after it would have a locatable end — so
// Writer.LeafWith rejects it with *UnknownSizeLeafError, exactly as the cursor
// rejects the shape on read.
//
// Termination is then structural, and that is the point: the reader closes the
// master when it meets an element that cannot be its child, or at end of input. It
// is what a live stream of concatenated documents needs, because the producer can
// emit a master's children as they are produced without knowing how many will
// follow — Amazon Kinesis Video Streams GetMedia returns exactly that, one
// unknown-size Segment per fragment.
//
// The cost is that the end is not in the bytes: a reader needs element knowledge
// (a boundary rule) to place it, and many readers accept an unknown size only on
// the elements where the Matroska specification expects it, Segment and Cluster.
// Use it there, and prefer Buffered or Reserved everywhere else.
func UnknownSize() SizeStrategy {
	return SizeStrategy{kind: strategyUnknown}
}

// String renders the strategy for diagnostics.
func (s SizeStrategy) String() string {
	switch s.kind {
	case strategyReserved:
		return fmt.Sprintf("reserved(%d)", s.width)
	case strategyUnknown:
		return "unknown_size"
	default:
		return "buffered"
	}
}
