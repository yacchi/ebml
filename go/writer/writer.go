package writer

import (
	"bytes"
	"fmt"
	"io"
	"time"

	"github.com/yacchi/ebml/parser"
)

// Option configures a Writer at construction.
type Option func(*Writer)

// WithStartOffset tells the Writer that its first byte lands at absolute offset off
// in the sink, instead of 0.
//
// It matters for the Reserved strategy on an io.WriterAt sink: the placeholder is
// patched with io.WriterAt.WriteAt at an absolute offset, so a Writer appending to a
// file that already holds off bytes must count from there. A negative off is
// ignored. It has no effect on the bytes produced.
func WithStartOffset(off int64) Option {
	return func(w *Writer) {
		if off >= 0 {
			w.off = off
		}
	}
}

// frame is one open master. Which fields matter depends on its strategy: a
// buffered frame owns buf and remembers the target to restore, a reserved frame
// remembers where its size placeholder sits and in which target, and an
// unknown-size frame carries nothing but its identity.
type frame struct {
	id   parser.ElementID
	kind strategyKind

	buf  *bytes.Buffer // buffered: this master's subtree
	prev *bytes.Buffer // buffered: the target to restore on EndMaster (nil = the sink)

	checksum bool             // buffered: emit a CRC-32 element over buf at EndMaster
	crcID    parser.ElementID // buffered: the CRC-32 element ID the caller supplied

	target    *bytes.Buffer // reserved: where the placeholder was written (nil = the sink)
	width     int           // reserved: placeholder width in bytes
	sizeAt    int64         // reserved: position of the placeholder's first byte in target
	payloadAt int64         // reserved: position of the first payload byte in target
}

// Writer emits an EBML document to a sink. It is the dual of parser's cursor: the
// caller states elements, the Writer produces bytes.
//
// It holds no element knowledge — see the package documentation — and it is not
// safe for concurrent use.
//
// Bytes reach the sink during the call that produces them, except inside an open
// Buffered master, whose subtree is held in memory and written out as one
// contiguous run when it ends. A failure that left bytes behind (a sink write
// error, or a Reserved size that outgrew its reserved width) is terminal: every
// later call returns it. A rejected call that wrote nothing — an ill-formed ID, an
// unknown size on a leaf, an unbalanced EndMaster, a sink that cannot be patched —
// leaves the Writer usable, so the caller may correct it and continue.
type Writer struct {
	sink io.Writer
	at   io.WriterAt // sink, when it can be patched; nil otherwise

	// off is the absolute sink offset the next byte written to the SINK takes.
	// Bytes buffered by an open Buffered master do not advance it: they are not in
	// the sink yet, and their sink offsets are not even determined until that
	// master's size VINT width is known.
	off int64
	// cur is the innermost open Buffered master's subtree buffer, nil when writes
	// go to the sink.
	cur *bytes.Buffer

	open []frame

	err    error
	closed bool
}

// New returns a Writer that emits to sink.
//
// When sink also implements io.WriterAt, the Reserved strategy can patch its
// placeholders in it and is available at any depth; otherwise Reserved works only
// inside a Buffered master (see Reserved). A sink opened in append mode is not
// patchable in practice even though *os.File implements io.WriterAt, because a
// positional write to it fails.
//
// New panics when sink is nil: that is a programmer error at construction, not a
// condition any later call could report meaningfully.
func New(sink io.Writer, opts ...Option) *Writer {
	if sink == nil {
		panic("writer.New: sink must not be nil")
	}
	w := &Writer{sink: sink}
	if at, ok := sink.(io.WriterAt); ok {
		w.at = at
	}
	for _, opt := range opts {
		opt(w)
	}
	return w
}

// Depth reports how many masters are currently open, so a top-level element is
// written at depth 0.
func (w *Writer) Depth() int { return len(w.open) }

// StartMaster begins a master element with the given size strategy and makes the
// elements written next its children, until the matching EndMaster.
//
// Buffered (also the zero SizeStrategy) writes nothing yet: the master's header
// and payload are emitted together at EndMaster. Reserved and UnknownSize write the
// header immediately, so the children stream straight through.
//
// A MasterOption configures this master alone; see WithChecksum, which makes it
// emit a CRC-32 element as its first child. Options are resolved and validated
// before the strategy switch runs, because Reserved writes its header
// immediately: an option rejected afterwards would already have left bytes behind.
//
// It reports *InvalidIDError for an ill-formed ID — the master's, or the one an
// option names — *SizeWidthError for a Reserved width outside 1 to 8 bytes,
// *NotPatchableError for Reserved on a sink that cannot be patched with no
// enclosing Buffered master, and *ChecksumStrategyError for WithChecksum on
// anything but Buffered — in each case before writing a byte, leaving the Writer
// unchanged.
func (w *Writer) StartMaster(id parser.ElementID, size SizeStrategy, opts ...MasterOption) error {
	if err := w.usable(); err != nil {
		return err
	}
	if !ValidID(id) {
		return &InvalidIDError{ID: id}
	}
	mo, err := applyMasterOptions(size, opts)
	if err != nil {
		return err
	}

	switch size.kind {
	case strategyReserved:
		if size.width < 1 || size.width > MaxSizeWidth {
			return &SizeWidthError{Width: size.width}
		}
		if w.cur == nil && w.at == nil {
			return &NotPatchableError{SinkType: fmt.Sprintf("%T", w.sink)}
		}
		placeholder, err := EncodeSizeWidth(0, size.width)
		if err != nil {
			return err
		}
		idBytes := EncodeID(id)
		f := frame{
			id:     id,
			kind:   strategyReserved,
			target: w.cur,
			width:  size.width,
			sizeAt: w.pos() + int64(len(idBytes)),
		}
		if err := w.writeAll(idBytes, placeholder); err != nil {
			return err
		}
		f.payloadAt = w.pos()
		w.open = append(w.open, f)
		return nil

	case strategyUnknown:
		if err := w.writeAll(EncodeID(id), UnknownSizeVINT()); err != nil {
			return err
		}
		w.open = append(w.open, frame{id: id, kind: strategyUnknown})
		return nil

	default: // strategyBuffered, including the zero SizeStrategy
		buf := new(bytes.Buffer)
		w.open = append(w.open, frame{
			id:       id,
			kind:     strategyBuffered,
			buf:      buf,
			prev:     w.cur,
			checksum: mo.checksum,
			crcID:    mo.crcID,
		})
		w.cur = buf
		return nil
	}
}

// EndMaster closes the innermost open master.
//
// What it does depends on that master's strategy: a Buffered master's header and
// buffered subtree are written out now, a Reserved master's size placeholder is
// patched with the payload length just measured, and an UnknownSize master needs
// nothing, since its end is not in the bytes at all (see UnknownSize).
//
// A Buffered master opened WithChecksum computes its CRC-32 here, over the
// buffered subtree, and emits the CRC-32 element ahead of it; the declared size
// covers that element too.
//
// It reports *NoOpenMasterError when no master is open, leaving the Writer usable.
// A Reserved master whose payload outgrew its reserved width is reported as
// *SizeOverflowError, and a Buffered master larger than MaxKnownSize as
// *SizeRangeError; both are terminal, because the emitted bytes can no longer be
// made into a valid document.
func (w *Writer) EndMaster() error {
	if err := w.usable(); err != nil {
		return err
	}
	if len(w.open) == 0 {
		return &NoOpenMasterError{}
	}
	f := w.open[len(w.open)-1]
	w.open = w.open[:len(w.open)-1]

	switch f.kind {
	case strategyReserved:
		// The frame's target is the current one again: every master opened inside
		// it has been closed, so its payload ends here.
		n := w.pos() - f.payloadAt
		sizeBytes, err := EncodeSizeWidth(n, f.width)
		if err != nil {
			return w.fail(err)
		}
		if f.target != nil {
			copy(f.target.Bytes()[f.sizeAt:f.sizeAt+int64(f.width)], sizeBytes)
			return nil
		}
		if _, err := w.at.WriteAt(sizeBytes, f.sizeAt); err != nil {
			return w.fail(err)
		}
		return nil

	case strategyUnknown:
		return nil

	default: // strategyBuffered
		w.cur = f.prev
		payload := f.buf.Bytes()
		if f.checksum {
			// Summed before the element is prepended, which is the coverage rule
			// itself: the CRC-32 element's own bytes are not part of what it covers.
			// The declared size below then spans both, because the element is
			// ordinary Element Data of this master.
			payload = append(checksumElement(f.crcID, payload), payload...)
		}
		if int64(len(payload)) > MaxKnownSize {
			return w.fail(&SizeRangeError{Size: int64(len(payload))})
		}
		return w.writeAll(EncodeID(f.id), EncodeSize(int64(len(payload))), payload)
	}
}

// Leaf writes a leaf element whose payload is exactly the bytes given, with a
// minimal-width size VINT. A nil or empty payload writes a zero-length element,
// which EBML reads as the value type's default.
//
// The payload is written verbatim: Leaf is the escape hatch for a value this
// package does not encode, and for reproducing bytes that were read from a stream.
// It reports *InvalidIDError for an ill-formed ID, having written nothing.
func (w *Writer) Leaf(id parser.ElementID, payload []byte) error {
	return w.LeafWith(id, payload, Buffered())
}

// LeafWith writes a leaf element with an explicit size strategy.
//
// Buffered — what Leaf uses — writes the minimal size VINT. Reserved(n) writes the
// size in exactly n bytes, which EBML permits even when narrower would do: it is
// how a caller reproduces an element's original, non-minimal header byte for byte.
// Nothing is patched, since a leaf payload is complete when it is written, so
// Reserved needs no patchable sink here.
//
// UnknownSize is rejected with *UnknownSizeLeafError: EBML reserves the unknown
// size for masters, and a reader could not locate the end of an unbounded leaf, nor
// the element after it. The other reports are *InvalidIDError, *SizeWidthError for a
// width outside 1 to 8 bytes and *SizeOverflowError when the payload does not fit
// the requested width; each is reported before any byte is written.
func (w *Writer) LeafWith(id parser.ElementID, payload []byte, size SizeStrategy) error {
	if err := w.usable(); err != nil {
		return err
	}
	if !ValidID(id) {
		return &InvalidIDError{ID: id}
	}

	n := int64(len(payload))
	var sizeBytes []byte
	switch size.kind {
	case strategyUnknown:
		return &UnknownSizeLeafError{ID: id}
	case strategyReserved:
		b, err := EncodeSizeWidth(n, size.width)
		if err != nil {
			return err
		}
		sizeBytes = b
	default: // strategyBuffered
		width, err := SizeWidth(n)
		if err != nil {
			return err
		}
		b, err := EncodeSizeWidth(n, width)
		if err != nil {
			return err
		}
		sizeBytes = b
	}
	return w.writeAll(EncodeID(id), sizeBytes, payload)
}

// Uint writes an unsigned-integer leaf, encoded by EncodeUint.
func (w *Writer) Uint(id parser.ElementID, v uint64) error {
	return w.Leaf(id, EncodeUint(v))
}

// Int writes a signed-integer leaf, encoded by EncodeInt.
func (w *Writer) Int(id parser.ElementID, v int64) error {
	return w.Leaf(id, EncodeInt(v))
}

// Float writes a float leaf in the requested IEEE 754 width, encoded by
// EncodeFloat. It reports *FloatSizeError for a width other than Float32 or
// Float64, having written nothing.
func (w *Writer) Float(id parser.ElementID, v float64, size FloatSize) error {
	payload, err := EncodeFloat(v, size)
	if err != nil {
		return err
	}
	return w.Leaf(id, payload)
}

// String writes a string leaf: the bytes of s, with no terminator or padding,
// encoded by EncodeString.
//
// It is the method for EBML's printable-ASCII string type; UTF8 is the same
// encoding for the UTF-8 type, and both exist so the call states which type the
// caller means. Neither validates the repertoire. A NUL byte anywhere in s is
// rejected as *StringNULError, having written nothing: a reader stops at the first
// NUL, so such a value could not be read back. Write arbitrary bytes as a binary
// value with Leaf or Binary instead.
func (w *Writer) String(id parser.ElementID, s string) error {
	payload, err := EncodeString(s)
	if err != nil {
		return err
	}
	return w.Leaf(id, payload)
}

// UTF8 writes a UTF-8 string leaf. See String, whose encoding and NUL rule it
// shares.
func (w *Writer) UTF8(id parser.ElementID, s string) error {
	payload, err := EncodeString(s)
	if err != nil {
		return err
	}
	return w.Leaf(id, payload)
}

// Date writes a date leaf: 8 bytes of signed nanoseconds relative to
// 2001-01-01T00:00:00 UTC, encoded by EncodeDate.
func (w *Writer) Date(id parser.ElementID, t time.Time) error {
	payload, err := EncodeDate(t)
	if err != nil {
		return err
	}
	return w.Leaf(id, payload)
}

// Binary writes a binary leaf. It is Leaf under the name of the value type, for
// callers that spell out every element's type at the call site.
func (w *Writer) Binary(id parser.ElementID, payload []byte) error {
	return w.Leaf(id, payload)
}

// Flush verifies that the document is at a top-level boundary and pushes any
// buffering the sink does of its own.
//
// A Writer has no output buffer to drain: its bytes are already in the sink unless
// a Buffered master is open, and such a master cannot be emitted early, since its
// size is not yet determined. So Flush reports *OpenMasterError when any master is
// still open, and otherwise forwards to the sink's Flush method when it has one
// (*bufio.Writer, *gzip.Writer, ...). The Writer stays usable either way.
func (w *Writer) Flush() error {
	if err := w.usable(); err != nil {
		return err
	}
	if len(w.open) > 0 {
		f := w.open[len(w.open)-1]
		return &OpenMasterError{Op: "Flush", ID: f.id, Depth: len(w.open) - 1}
	}
	if fl, ok := w.sink.(interface{ Flush() error }); ok {
		if err := fl.Flush(); err != nil {
			return w.fail(err)
		}
	}
	return nil
}

// Close finishes the document: it flushes (see Flush) and marks the Writer
// finished, after which every method reports ErrClosed. A second Close is a no-op.
//
// It does NOT close the sink, even when the sink is an io.Closer: the sink belongs
// to the caller, who may well have more to write after this document.
//
// Closing with a master still open reports *OpenMasterError. The Writer is closed
// anyway — the document is already unfinished, and continuing would only append
// bytes to a truncated master — so the error must be checked rather than deferred
// away.
func (w *Writer) Close() error {
	if w.err != nil {
		return w.err
	}
	if w.closed {
		return nil
	}
	err := w.Flush()
	w.closed = true
	return err
}

// usable reports the Writer's terminal state, if any: a stored failure first, then
// closure.
func (w *Writer) usable() error {
	if w.err != nil {
		return w.err
	}
	if w.closed {
		return ErrClosed
	}
	return nil
}

// fail stores err as terminal and returns it. The first failure is kept: it is the
// one that describes what went wrong.
func (w *Writer) fail(err error) error {
	if w.err == nil {
		w.err = err
	}
	return err
}

// pos reports the position the next byte takes in the CURRENT target: an offset in
// the innermost Buffered master's buffer, or the absolute sink offset when there is
// none. Positions recorded for a frame are always compared against the same target,
// so the two address spaces never mix.
func (w *Writer) pos() int64 {
	if w.cur != nil {
		return int64(w.cur.Len())
	}
	return w.off
}

// writeAll writes the parts in order, stopping at the first failure.
func (w *Writer) writeAll(parts ...[]byte) error {
	for _, p := range parts {
		if err := w.write(p); err != nil {
			return err
		}
	}
	return nil
}

// write emits b to the current target. A sink write failure is terminal: the
// document is truncated at an arbitrary byte, so nothing further can repair it.
func (w *Writer) write(b []byte) error {
	if len(b) == 0 {
		return nil
	}
	if w.cur != nil {
		w.cur.Write(b) // bytes.Buffer.Write never fails
		return nil
	}
	n, err := w.sink.Write(b)
	w.off += int64(n)
	if err == nil && n != len(b) {
		err = io.ErrShortWrite
	}
	if err != nil {
		return w.fail(err)
	}
	return nil
}
