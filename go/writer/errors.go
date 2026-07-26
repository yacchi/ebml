package writer

import (
	"errors"
	"fmt"
	"time"

	"github.com/yacchi/ebml/parser"
)

// Sentinels for the failures this package reports. Every typed error below
// unwraps to exactly one of them, so a caller can classify with errors.Is without
// enumerating concrete types, and inspect the concrete type with errors.As when it
// needs the details.
//
// There is deliberately no single "writer error" class marker: unlike a reader,
// which must tell a broken stream from a consumer's refusal, a writer's failures
// are all the same kind of thing — a call that cannot be carried out — and the
// useful question is always which one.
var (
	// ErrInvalidID reports an element ID that is not a well-formed EBML element ID
	// VINT of 1 to 4 bytes; see ValidID.
	ErrInvalidID = errors.New("ill-formed EBML element ID")
	// ErrSizeRange reports a payload size that is negative or larger than the
	// maximum an 8-byte size VINT can express (MaxKnownSize).
	ErrSizeRange = errors.New("payload size out of range")
	// ErrSizeWidth reports a size-VINT width outside 1 to 8 bytes.
	ErrSizeWidth = errors.New("size VINT width out of range")
	// ErrSizeOverflow reports a payload size that does not fit the size-VINT width
	// it must be written in — an explicit width, or the width Reserved set aside.
	ErrSizeOverflow = errors.New("payload size does not fit its size VINT width")
	// ErrUnknownSizeLeaf reports the UnknownSize strategy on a leaf element. EBML
	// reserves the unknown size for masters: an unbounded leaf has no end, so
	// neither its payload nor the element after it could be located on read.
	ErrUnknownSizeLeaf = errors.New("unknown-size element is not a master")
	// ErrFloatSize reports a float width other than Float32 or Float64.
	ErrFloatSize = errors.New("float size is neither 32 nor 64 bits")
	// ErrStringNUL reports a NUL byte in a string value. EBML permits a string
	// payload to be zero-padded, so a reader stops at the first NUL: a value
	// carrying one could not be read back unchanged.
	ErrStringNUL = errors.New("string value contains a NUL byte")
	// ErrDateRange reports a date outside the ±292 years around the EBML epoch
	// that a signed 64-bit nanosecond count can represent.
	ErrDateRange = errors.New("date out of representable range")
	// ErrNoOpenMaster reports EndMaster with no master open.
	ErrNoOpenMaster = errors.New("no open master")
	// ErrOpenMaster reports a master still open where the document had to be at a
	// top-level boundary.
	ErrOpenMaster = errors.New("master still open")
	// ErrNotPatchable reports the Reserved strategy on a sink that cannot be
	// patched.
	ErrNotPatchable = errors.New("sink cannot be patched")
	// ErrChecksumStrategy reports WithChecksum on a master whose size strategy
	// does not hold its children in memory. A CRC-32 element precedes the data it
	// covers, so that data must still be in hand when the value is computed.
	ErrChecksumStrategy = errors.New("checksum needs the Buffered size strategy")
	// ErrClosed reports use of a Writer after Close.
	ErrClosed = errors.New("writer is closed")
)

// InvalidIDError reports an element ID that cannot be written, because reading the
// bytes back would not yield the same ID: the length marker of its leading byte
// must agree with the number of bytes the ID value occupies. See ValidID.
type InvalidIDError struct {
	ID parser.ElementID
}

func (e *InvalidIDError) Error() string {
	return fmt.Sprintf("element ID 0x%X is not a well-formed 1 to 4 byte EBML element ID VINT", uint32(e.ID))
}

func (e *InvalidIDError) Unwrap() error { return ErrInvalidID }

// SizeRangeError reports a payload size no EBML size VINT can express: a negative
// size, or one above MaxKnownSize.
type SizeRangeError struct {
	Size int64
}

func (e *SizeRangeError) Error() string {
	return fmt.Sprintf("payload size %d is out of range: an EBML size VINT expresses 0 to %d", e.Size, MaxKnownSize)
}

func (e *SizeRangeError) Unwrap() error { return ErrSizeRange }

// SizeWidthError reports a size-VINT width outside the 1 to 8 bytes EBML allows.
// It is what Reserved reports for a width it cannot set aside.
type SizeWidthError struct {
	Width int
}

func (e *SizeWidthError) Error() string {
	return fmt.Sprintf("size VINT width %d is out of range: EBML allows 1 to %d bytes", e.Width, MaxSizeWidth)
}

func (e *SizeWidthError) Unwrap() error { return ErrSizeWidth }

// SizeOverflowError reports a payload that outgrew the size VINT width it has to
// be written in. For a Reserved master that is discovered at EndMaster, once the
// payload has already been emitted, so the Writer cannot recover: Max is the
// largest size the reserved Width could have expressed.
type SizeOverflowError struct {
	Size  int64
	Width int
	Max   int64
}

func (e *SizeOverflowError) Error() string {
	return fmt.Sprintf("payload size %d does not fit a %d-byte size VINT (maximum %d)", e.Size, e.Width, e.Max)
}

func (e *SizeOverflowError) Unwrap() error { return ErrSizeOverflow }

// UnknownSizeLeafError reports the UnknownSize strategy on a leaf element. It is
// the write-side counterpart of parser.UnknownSizeLeafError, which the cursor
// raises for the same shape on read.
type UnknownSizeLeafError struct {
	ID parser.ElementID
}

func (e *UnknownSizeLeafError) Error() string {
	return fmt.Sprintf("leaf element %s cannot declare an unknown size: EBML reserves it for masters", e.ID)
}

func (e *UnknownSizeLeafError) Unwrap() error { return ErrUnknownSizeLeaf }

// FloatSizeError reports a float width other than Float32 or Float64. EBML floats
// are IEEE 754 binary32 or binary64; no other width is representable.
type FloatSizeError struct {
	Size FloatSize
}

func (e *FloatSizeError) Error() string {
	return fmt.Sprintf("float size %d is neither %d nor %d bits", int(e.Size), int(Float32), int(Float64))
}

func (e *FloatSizeError) Unwrap() error { return ErrFloatSize }

// StringNULError reports a NUL byte in a string value, at byte offset Index. An
// EBML string element cannot carry one: a reader treats the first NUL as the end of
// the value, since EBML allows a string payload to be zero-padded, so writing the
// value would not survive being read back. A payload that genuinely needs arbitrary
// bytes is a binary value — Writer.Leaf or Writer.Binary — not a string.
type StringNULError struct {
	Index int
}

func (e *StringNULError) Error() string {
	return fmt.Sprintf("string value contains a NUL byte at index %d: an EBML string element cannot carry one, because a reader stops there", e.Index)
}

func (e *StringNULError) Unwrap() error { return ErrStringNUL }

// DateRangeError reports a date an EBML date element cannot carry. The payload is
// signed nanoseconds relative to 2001-01-01T00:00:00 UTC, so 64 bits reach roughly
// ±292 years around that epoch. Saturating to the nearest representable instant
// would make the value read back as a different date, so it is refused instead.
type DateRangeError struct {
	Time time.Time
}

func (e *DateRangeError) Error() string {
	return fmt.Sprintf("date %s is outside the range an EBML date can represent: signed nanoseconds since 2001-01-01T00:00:00Z do not fit in 64 bits", e.Time.Format(time.RFC3339))
}

func (e *DateRangeError) Unwrap() error { return ErrDateRange }

// NoOpenMasterError reports EndMaster with no master open, i.e. one EndMaster too
// many for the StartMaster calls made.
type NoOpenMasterError struct{}

func (e *NoOpenMasterError) Error() string {
	return "EndMaster: no open master; every EndMaster needs a matching StartMaster"
}

func (e *NoOpenMasterError) Unwrap() error { return ErrNoOpenMaster }

// OpenMasterError reports an operation that requires the document to be at a
// top-level boundary while a master is still open: Flush and Close. ID and Depth
// identify the innermost open master, Depth counted as the cursor counts it, so a
// top-level master is at depth 0.
type OpenMasterError struct {
	Op    string
	ID    parser.ElementID
	Depth int
}

func (e *OpenMasterError) Error() string {
	return fmt.Sprintf("%s: master %s at depth %d is still open", e.Op, e.ID, e.Depth)
}

func (e *OpenMasterError) Unwrap() error { return ErrOpenMaster }

// NotPatchableError reports the Reserved strategy on a sink whose already-written
// bytes cannot be rewritten. Reserved emits a size placeholder and patches it once
// the payload is complete, so it needs either an io.WriterAt sink or an enclosing
// Buffered master, which holds the bytes in memory and can be patched there.
//
// SinkType is the dynamic type of the sink, so the diagnosis names what was
// passed. A *bytes.Buffer, for instance, cannot be patched; an *os.File not opened
// with O_APPEND can.
type NotPatchableError struct {
	SinkType string
}

func (e *NotPatchableError) Error() string {
	return fmt.Sprintf("size strategy Reserved needs an io.WriterAt sink or an enclosing Buffered master; sink %s is neither", e.SinkType)
}

func (e *NotPatchableError) Unwrap() error { return ErrNotPatchable }

// ChecksumStrategyError reports WithChecksum combined with a size strategy that
// cannot produce one. RFC 8794 requires the CRC-32 element to be the FIRST ordered
// child of the master it covers, so the value has to be known before any sibling
// is emitted — while the value is computed FROM those siblings. Only Buffered
// resolves that: it holds the whole subtree until EndMaster, where the checksum is
// taken over the buffer and the element prepended to it.
//
// Reserved and UnknownSize both stream their children straight to the sink, so by
// the time the last one is written the earlier bytes are gone and the place the
// CRC-32 element would have to occupy has already been filled. Strategy is the
// offending strategy, so the diagnosis names what was asked for; the fix is either
// Buffered or no checksum on this master.
type ChecksumStrategyError struct {
	Strategy SizeStrategy
}

func (e *ChecksumStrategyError) Error() string {
	return fmt.Sprintf("size strategy %s cannot carry a CRC-32 element: it must be the master's first child, so the children it covers have to still be in memory when it is computed — only Buffered holds them", e.Strategy)
}

func (e *ChecksumStrategyError) Unwrap() error { return ErrChecksumStrategy }
