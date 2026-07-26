// Package writer emits EBML documents. It is the dual of the streaming cursor in
// package parser: the cursor turns bytes into element events, and a Writer turns
// element calls into bytes.
//
// # No element knowledge
//
// Like the cursor, a Writer knows no element at all: no element table, no names,
// no value types, and not one element ID. An element ID is an opaque
// parser.ElementID the caller supplies, and the CALLER picks the value type by
// choosing the method — Uint, Int, Float, String, UTF8, Date, Binary, or Leaf for
// exact payload bytes. This mirrors the reader, where the consumer picks the
// decode (AsUint, AsString, ...) because only the consumer's registry knows what
// an element means. Package matroska remains the single place element IDs and
// value types are written down.
//
// # Structure
//
//	w := writer.New(sink)
//	w.StartMaster(matroska.IDEBML, writer.Buffered())
//	w.Uint(matroska.IDEBMLVersion, 1)
//	w.String(matroska.IDDocType, "matroska")
//	w.EndMaster()
//	w.Close()
//
// Masters nest: every StartMaster needs a matching EndMaster, and the leaves
// written in between become its payload. Nested masters may mix size strategies
// freely.
//
// # Size strategies
//
// An element's size VINT must be written before its payload, but a master's size
// is only known once its children have been written. The three strategies are the
// three ways out of that, and they differ in what they require of the sink and in
// what they cost:
//
//   - [Buffered] (the default, and the zero SizeStrategy) holds the master's
//     subtree in memory and emits the exact size, in the minimal VINT width, when
//     EndMaster is called. It works with any io.Writer and produces the most
//     compact output, at the cost of holding the subtree in memory.
//   - [Reserved] emits a fixed-width size placeholder immediately and patches it
//     on EndMaster, so the payload streams straight to the sink and nothing is
//     buffered. EBML permits non-minimal size VINTs, so the reserved width needs
//     no byte shifting and the result is spec-legal. It requires a sink that can
//     be patched (io.WriterAt) unless an enclosing Buffered master is already
//     holding the bytes, and the final size must fit the reserved width.
//   - [UnknownSize] emits the all-ones unknown-size marker and never patches
//     anything. Valid for MASTERS only. Termination is then structural: the reader
//     closes the master when an element appears that cannot be its child, or at
//     EOF. Many readers tolerate it only on Segment and Cluster.
//
// A non-seekable sink such as an io.Pipe adds one requirement, on the CALLER rather
// than on the Writer: the goroutine draining the read end must ALREADY be running
// before the first write reaches the sink, since an unread pipe write blocks
// forever. New writes nothing, so that first write is the first StartMaster — which
// UnknownSize on a pipe hits immediately, its header going out before any child is
// written.
//
// # When bytes reach the sink
//
// A Writer performs no output buffering of its own. Bytes reach the sink during
// the call that produces them, with exactly one exception: everything written
// inside an open Buffered master is held in memory and reaches the sink, as one
// contiguous run, during that master's EndMaster. So with no Buffered master open,
// each Leaf, StartMaster and EndMaster call writes its bytes through immediately;
// with one open, nothing reaches the sink until it ends.
//
// [Writer.Flush] therefore does not exist to drain a Writer — it verifies that the
// document is at a top-level boundary (no master left open) and forwards to the
// sink's own Flush method when it has one, e.g. a *bufio.Writer. [Writer.Close]
// flushes and marks the Writer finished; it never closes the sink.
//
// # Checksums
//
// A master can be asked to carry an EBML CRC-32 element over its own Element
// Data, with [WithChecksum] at [Writer.StartMaster]. It is opt-in per master and
// never implicit, and the caller supplies the CRC-32 element's ID like any other
// ID, since this package names no element. It requires [Buffered], because RFC
// 8794 puts the CRC-32 element FIRST while its value comes from the children after
// it — only a buffered subtree is still in hand at that point. The checksum itself
// lives in package crc, which the reading side uses too, so the two never drift.
//
// # Errors
//
// No method panics on caller misuse: an unbalanced EndMaster, an unknown size on a
// leaf, a size that does not fit its reserved width, a sink that cannot be
// patched, a string value carrying a NUL byte, a checksum asked of a strategy that
// does not buffer its children, and an ill-formed element ID are all
// reported as typed errors (see
// errors.go). Validation errors are reported before anything is written, so the
// caller may correct the call and retry. A failure that cannot be undone because
// bytes were already emitted — a sink write error, or a Reserved size overflow
// discovered at EndMaster — is terminal: the Writer keeps returning it.
//
// # Primitives
//
// The encoders the Writer is built on are exported so a caller can build headers
// without the Writer state machine — which is what a document model needs in order
// to reproduce an element's original header byte for byte: [EncodeID],
// [EncodeSize], [EncodeSizeWidth], [UnknownSizeVINT], [UnknownSizeVINTWidth], and
// the value encoders
// [EncodeUint], [EncodeInt], [EncodeFloat], [EncodeString] and [EncodeDate], each
// the exact inverse of the corresponding parser decode helper. That inverse is
// exact because a value the reader could not return unchanged is refused rather
// than encoded: [EncodeString] rejects a string carrying a NUL byte, at which a
// reader stops.
package writer
