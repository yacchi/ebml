package writer_test

import (
	"bufio"
	"bytes"
	"errors"
	"io"
	"testing"

	"github.com/yacchi/ebml/impl/go/parser"
	"github.com/yacchi/ebml/impl/go/writer"
)

// The element IDs used throughout these tests are arbitrary well-formed EBML IDs,
// deliberately unrelated to the Matroska registry: the writer knows no element, so
// its tests must not depend on one either. Real code passes matroska.ID* constants.
const (
	idRoot   parser.ElementID = 0x1FFFFFF0 // four-byte master
	idBranch parser.ElementID = 0x3F0001   // three-byte master
	idNest   parser.ElementID = 0x4001     // two-byte master
	idUintL  parser.ElementID = 0x80       // one-byte leaves
	idIntL   parser.ElementID = 0x81
	idFloatL parser.ElementID = 0x82
	idStrL   parser.ElementID = 0x83
	idBinL   parser.ElementID = 0x84
	idDateL  parser.ElementID = 0x85
)

// classify is the KindClassifier for the IDs above; the cursor requires one.
func classify(id parser.ElementID) parser.Kind {
	switch id {
	case idRoot, idBranch, idNest:
		return parser.KindMaster
	case idUintL:
		return parser.KindUint
	default:
		return parser.KindBinary
	}
}

// memSink is an in-memory sink that can be patched, i.e. it implements io.WriterAt
// as well as io.Writer -- what the Reserved strategy needs of a sink.
type memSink struct {
	b []byte
}

func (m *memSink) Write(p []byte) (int, error) {
	m.b = append(m.b, p...)
	return len(p), nil
}

// WriteAt patches bytes already written. It deliberately refuses to write past the
// end, so a patch at a wrong offset fails the test loudly instead of appending.
func (m *memSink) WriteAt(p []byte, off int64) (int, error) {
	if off < 0 || off+int64(len(p)) > int64(len(m.b)) {
		return 0, errors.New("memSink: patch outside the bytes written")
	}
	copy(m.b[off:], p)
	return len(p), nil
}

func TestBufferedMasterEmitsMinimalSizeOnEnd(t *testing.T) {
	var buf bytes.Buffer
	w := writer.New(&buf)

	// Nothing may reach the sink while the buffered master is open.
	if err := w.StartMaster(idNest, writer.Buffered()); err != nil {
		t.Fatalf("StartMaster: %v", err)
	}
	if err := w.Uint(idUintL, 1); err != nil {
		t.Fatalf("Uint: %v", err)
	}
	if err := w.String(idStrL, "ab"); err != nil {
		t.Fatalf("String: %v", err)
	}
	if buf.Len() != 0 {
		t.Fatalf("sink holds % X while a Buffered master is open; want nothing yet", buf.Bytes())
	}
	if got := w.Depth(); got != 1 {
		t.Errorf("Depth() = %d, want 1", got)
	}
	if err := w.EndMaster(); err != nil {
		t.Fatalf("EndMaster: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	want := []byte{
		0x40, 0x01, 0x87, // idNest, size 7 in the minimal width
		0x80, 0x81, 0x01, // idUintL = 1
		0x83, 0x82, 'a', 'b', // idStrL = "ab"
	}
	if !bytes.Equal(buf.Bytes(), want) {
		t.Errorf("document = % X, want % X", buf.Bytes(), want)
	}
}

// TestZeroSizeStrategyIsBuffered pins the documented default.
func TestZeroSizeStrategyIsBuffered(t *testing.T) {
	var zero, explicit bytes.Buffer
	for _, c := range []struct {
		buf      *bytes.Buffer
		strategy writer.SizeStrategy
	}{{&zero, writer.SizeStrategy{}}, {&explicit, writer.Buffered()}} {
		w := writer.New(c.buf)
		if err := w.StartMaster(idNest, c.strategy); err != nil {
			t.Fatalf("StartMaster(%v): %v", c.strategy, err)
		}
		if err := w.Uint(idUintL, 1); err != nil {
			t.Fatalf("Uint: %v", err)
		}
		if err := w.EndMaster(); err != nil {
			t.Fatalf("EndMaster: %v", err)
		}
	}
	if !bytes.Equal(zero.Bytes(), explicit.Bytes()) {
		t.Errorf("zero SizeStrategy produced % X, Buffered() produced % X", zero.Bytes(), explicit.Bytes())
	}
	if got := writer.Buffered().String(); got != "buffered" {
		t.Errorf("Buffered().String() = %q", got)
	}
}

func TestReservedPatchesThePlaceholderInTheSink(t *testing.T) {
	sink := &memSink{}
	w := writer.New(sink)

	if err := w.StartMaster(idNest, writer.Reserved(4)); err != nil {
		t.Fatalf("StartMaster: %v", err)
	}
	// The header must already be in the sink: Reserved streams, it does not buffer.
	if want := []byte{0x40, 0x01, 0x10, 0x00, 0x00, 0x00}; !bytes.Equal(sink.b, want) {
		t.Fatalf("sink after StartMaster = % X, want % X (placeholder)", sink.b, want)
	}
	if err := w.Uint(idUintL, 0x1234); err != nil {
		t.Fatalf("Uint: %v", err)
	}
	if err := w.EndMaster(); err != nil {
		t.Fatalf("EndMaster: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	want := []byte{
		0x40, 0x01, // idNest
		0x10, 0x00, 0x00, 0x04, // size 4, patched into the four reserved bytes
		0x80, 0x82, 0x12, 0x34, // idUintL = 0x1234
	}
	if !bytes.Equal(sink.b, want) {
		t.Errorf("document = % X, want % X", sink.b, want)
	}
	if got := writer.Reserved(4).String(); got != "reserved(4)" {
		t.Errorf("Reserved(4).String() = %q", got)
	}
}

// TestReservedInsideBufferedNeedsNoPatchableSink: the enclosing Buffered master
// holds the bytes, so the placeholder is patched in memory and a plain
// *bytes.Buffer sink is enough.
func TestReservedInsideBufferedNeedsNoPatchableSink(t *testing.T) {
	var buf bytes.Buffer
	w := writer.New(&buf)

	if err := w.StartMaster(idRoot, writer.Buffered()); err != nil {
		t.Fatalf("StartMaster(root): %v", err)
	}
	if err := w.StartMaster(idNest, writer.Reserved(2)); err != nil {
		t.Fatalf("StartMaster(nest, Reserved(2)): %v", err)
	}
	if err := w.Uint(idUintL, 1); err != nil {
		t.Fatalf("Uint: %v", err)
	}
	if err := w.EndMaster(); err != nil {
		t.Fatalf("EndMaster(nest): %v", err)
	}
	if err := w.EndMaster(); err != nil {
		t.Fatalf("EndMaster(root): %v", err)
	}

	want := []byte{
		0x1F, 0xFF, 0xFF, 0xF0, 0x87, // idRoot, size 7 (minimal)
		0x40, 0x01, 0x40, 0x03, // idNest, size 3 in the two reserved bytes
		0x80, 0x81, 0x01, // idUintL = 1
	}
	if !bytes.Equal(buf.Bytes(), want) {
		t.Errorf("document = % X, want % X", buf.Bytes(), want)
	}
}

// TestBufferedInsideReserved is the other nesting order: the inner master's bytes
// arrive at the sink in one run and must still be counted into the outer reserved
// size.
func TestBufferedInsideReserved(t *testing.T) {
	sink := &memSink{}
	w := writer.New(sink)

	if err := w.StartMaster(idRoot, writer.Reserved(3)); err != nil {
		t.Fatalf("StartMaster(root, Reserved(3)): %v", err)
	}
	if err := w.StartMaster(idNest, writer.Buffered()); err != nil {
		t.Fatalf("StartMaster(nest): %v", err)
	}
	if err := w.Uint(idUintL, 1); err != nil {
		t.Fatalf("Uint: %v", err)
	}
	if err := w.EndMaster(); err != nil {
		t.Fatalf("EndMaster(nest): %v", err)
	}
	if err := w.EndMaster(); err != nil {
		t.Fatalf("EndMaster(root): %v", err)
	}

	want := []byte{
		0x1F, 0xFF, 0xFF, 0xF0, 0x20, 0x00, 0x06, // idRoot, size 6 in three reserved bytes
		0x40, 0x01, 0x83, // idNest, size 3
		0x80, 0x81, 0x01, // idUintL = 1
	}
	if !bytes.Equal(sink.b, want) {
		t.Errorf("document = % X, want % X", sink.b, want)
	}
}

// TestReservedPatchHonoursStartOffset: the placeholder is patched at an absolute
// sink offset, so a Writer appending after existing bytes must be told where it
// starts.
func TestReservedPatchHonoursStartOffset(t *testing.T) {
	sink := &memSink{b: []byte{0xAA, 0xBB, 0xCC}}
	w := writer.New(sink, writer.WithStartOffset(int64(len(sink.b))))

	if err := w.StartMaster(idNest, writer.Reserved(1)); err != nil {
		t.Fatalf("StartMaster: %v", err)
	}
	if err := w.Uint(idUintL, 1); err != nil {
		t.Fatalf("Uint: %v", err)
	}
	if err := w.EndMaster(); err != nil {
		t.Fatalf("EndMaster: %v", err)
	}

	want := []byte{
		0xAA, 0xBB, 0xCC, // bytes the sink already held
		0x40, 0x01, 0x83, // idNest, size 3 in one reserved byte
		0x80, 0x81, 0x01,
	}
	if !bytes.Equal(sink.b, want) {
		t.Errorf("sink = % X, want % X", sink.b, want)
	}
}

func TestReservedRejectsUnpatchableSink(t *testing.T) {
	var buf bytes.Buffer // an io.Writer, not an io.WriterAt
	w := writer.New(&buf)

	err := w.StartMaster(idNest, writer.Reserved(4))
	var pe *writer.NotPatchableError
	if !errors.As(err, &pe) || !errors.Is(err, writer.ErrNotPatchable) {
		t.Fatalf("StartMaster(Reserved) on a *bytes.Buffer = %v, want *NotPatchableError", err)
	}
	if pe.SinkType != "*bytes.Buffer" {
		t.Errorf("NotPatchableError.SinkType = %q, want %q", pe.SinkType, "*bytes.Buffer")
	}
	if buf.Len() != 0 {
		t.Errorf("sink holds % X after the rejected call; want nothing written", buf.Bytes())
	}
	// The rejection wrote nothing, so the Writer is still usable: fall back to
	// Buffered.
	if err := w.StartMaster(idNest, writer.Buffered()); err != nil {
		t.Fatalf("StartMaster(Buffered) after the rejection: %v", err)
	}
	if err := w.EndMaster(); err != nil {
		t.Fatalf("EndMaster: %v", err)
	}
	if want := []byte{0x40, 0x01, 0x80}; !bytes.Equal(buf.Bytes(), want) {
		t.Errorf("document = % X, want % X", buf.Bytes(), want)
	}
}

func TestReservedWidthOutOfRange(t *testing.T) {
	for _, width := range []int{-1, 0, 9} {
		sink := &memSink{}
		w := writer.New(sink)
		err := w.StartMaster(idNest, writer.Reserved(width))
		var we *writer.SizeWidthError
		if !errors.As(err, &we) || !errors.Is(err, writer.ErrSizeWidth) {
			t.Errorf("StartMaster(Reserved(%d)) = %v, want *SizeWidthError", width, err)
		}
		if len(sink.b) != 0 {
			t.Errorf("StartMaster(Reserved(%d)) wrote % X; want nothing", width, sink.b)
		}
	}
}

// TestReservedOverflowIsTerminal: the payload has already been emitted when the
// size turns out not to fit, so the document cannot be repaired.
func TestReservedOverflowIsTerminal(t *testing.T) {
	sink := &memSink{}
	w := writer.New(sink)

	if err := w.StartMaster(idNest, writer.Reserved(1)); err != nil {
		t.Fatalf("StartMaster: %v", err)
	}
	if err := w.Binary(idBinL, make([]byte, 200)); err != nil {
		t.Fatalf("Binary: %v", err)
	}
	err := w.EndMaster()
	var oe *writer.SizeOverflowError
	if !errors.As(err, &oe) || !errors.Is(err, writer.ErrSizeOverflow) {
		t.Fatalf("EndMaster = %v, want *SizeOverflowError", err)
	}
	if oe.Size != 203 || oe.Width != 1 || oe.Max != 126 {
		t.Errorf("SizeOverflowError = %+v, want size 203, width 1, max 126", *oe)
	}
	// Terminal: every later call reports the same failure.
	if again := w.Uint(idUintL, 1); !errors.Is(again, writer.ErrSizeOverflow) {
		t.Errorf("Uint after the overflow = %v, want the stored failure", again)
	}
	if again := w.Close(); !errors.Is(again, writer.ErrSizeOverflow) {
		t.Errorf("Close after the overflow = %v, want the stored failure", again)
	}
}

func TestUnknownSizeMasterWritesTheMarkerAndNothingOnEnd(t *testing.T) {
	var buf bytes.Buffer
	w := writer.New(&buf)

	if err := w.StartMaster(idRoot, writer.UnknownSize()); err != nil {
		t.Fatalf("StartMaster: %v", err)
	}
	header := append(append([]byte(nil), 0x1F, 0xFF, 0xFF, 0xF0), 0x01, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF)
	if !bytes.Equal(buf.Bytes(), header) {
		t.Fatalf("sink after StartMaster = % X, want % X", buf.Bytes(), header)
	}
	if err := w.Uint(idUintL, 1); err != nil {
		t.Fatalf("Uint: %v", err)
	}
	before := buf.Len()
	if err := w.EndMaster(); err != nil {
		t.Fatalf("EndMaster: %v", err)
	}
	if buf.Len() != before {
		t.Errorf("EndMaster on an UnknownSize master wrote %d bytes; want none, its end is structural", buf.Len()-before)
	}
	if got := writer.UnknownSize().String(); got != "unknown_size" {
		t.Errorf("UnknownSize().String() = %q", got)
	}
}

func TestUnknownSizeRejectedForLeaf(t *testing.T) {
	var buf bytes.Buffer
	w := writer.New(&buf)

	err := w.LeafWith(idBinL, []byte{1, 2, 3}, writer.UnknownSize())
	var ue *writer.UnknownSizeLeafError
	if !errors.As(err, &ue) || !errors.Is(err, writer.ErrUnknownSizeLeaf) {
		t.Fatalf("LeafWith(UnknownSize) = %v, want *UnknownSizeLeafError", err)
	}
	if ue.ID != idBinL {
		t.Errorf("UnknownSizeLeafError.ID = %s, want %s", ue.ID, idBinL)
	}
	if buf.Len() != 0 {
		t.Errorf("the rejected leaf wrote % X; want nothing", buf.Bytes())
	}
	if err := w.Leaf(idBinL, []byte{1, 2, 3}); err != nil {
		t.Fatalf("Leaf after the rejection: %v", err)
	}
}

func TestLeafWithExplicitWidth(t *testing.T) {
	var buf bytes.Buffer
	w := writer.New(&buf)

	// Reserved on a leaf is just an explicit, possibly non-minimal, size width; the
	// size is already known, so no patchable sink is involved.
	if err := w.LeafWith(idBinL, []byte{1, 2, 3}, writer.Reserved(4)); err != nil {
		t.Fatalf("LeafWith: %v", err)
	}
	want := []byte{0x84, 0x10, 0x00, 0x00, 0x03, 0x01, 0x02, 0x03}
	if !bytes.Equal(buf.Bytes(), want) {
		t.Errorf("leaf = % X, want % X", buf.Bytes(), want)
	}

	err := w.LeafWith(idBinL, make([]byte, 200), writer.Reserved(1))
	var oe *writer.SizeOverflowError
	if !errors.As(err, &oe) {
		t.Errorf("LeafWith(200 bytes, Reserved(1)) = %v, want *SizeOverflowError", err)
	}
	if buf.Len() != len(want) {
		t.Errorf("the rejected leaf wrote %d bytes; want none", buf.Len()-len(want))
	}
}

func TestTypedLeafHelpers(t *testing.T) {
	var buf bytes.Buffer
	w := writer.New(&buf)

	if err := w.Int(idIntL, -2); err != nil {
		t.Fatalf("Int: %v", err)
	}
	if err := w.Float(idFloatL, 1.5, writer.Float32); err != nil {
		t.Fatalf("Float: %v", err)
	}
	if err := w.UTF8(idStrL, "ok"); err != nil {
		t.Fatalf("UTF8: %v", err)
	}
	if err := w.Leaf(idBinL, nil); err != nil {
		t.Fatalf("Leaf(nil): %v", err)
	}
	want := []byte{
		0x81, 0x81, 0xFE, // int -2
		0x82, 0x84, 0x3F, 0xC0, 0x00, 0x00, // float32 1.5
		0x83, 0x82, 'o', 'k', // utf-8 "ok"
		0x84, 0x80, // empty binary
	}
	if !bytes.Equal(buf.Bytes(), want) {
		t.Errorf("document = % X, want % X", buf.Bytes(), want)
	}

	err := w.Float(idFloatL, 1, writer.FloatSize(16))
	if !errors.Is(err, writer.ErrFloatSize) {
		t.Errorf("Float with a 16-bit size = %v, want ErrFloatSize", err)
	}
	if buf.Len() != len(want) {
		t.Errorf("the rejected float wrote %d bytes; want none", buf.Len()-len(want))
	}
}

func TestEndMasterWithoutStartMaster(t *testing.T) {
	var buf bytes.Buffer
	w := writer.New(&buf)

	err := w.EndMaster()
	var ne *writer.NoOpenMasterError
	if !errors.As(err, &ne) || !errors.Is(err, writer.ErrNoOpenMaster) {
		t.Fatalf("EndMaster with nothing open = %v, want *NoOpenMasterError", err)
	}
	// Unbalanced calls do not poison the Writer.
	if err := w.StartMaster(idNest, writer.Buffered()); err != nil {
		t.Fatalf("StartMaster: %v", err)
	}
	if err := w.EndMaster(); err != nil {
		t.Fatalf("EndMaster: %v", err)
	}
	if err := w.EndMaster(); !errors.Is(err, writer.ErrNoOpenMaster) {
		t.Errorf("second EndMaster = %v, want ErrNoOpenMaster", err)
	}
}

func TestInvalidElementID(t *testing.T) {
	var buf bytes.Buffer
	w := writer.New(&buf)

	for _, id := range []parser.ElementID{0x00, 0x1234, 0x40} {
		var ie *writer.InvalidIDError
		if err := w.StartMaster(id, writer.Buffered()); !errors.As(err, &ie) || !errors.Is(err, writer.ErrInvalidID) {
			t.Errorf("StartMaster(0x%X) = %v, want *InvalidIDError", uint32(id), err)
		}
		if err := w.Leaf(id, nil); !errors.As(err, &ie) {
			t.Errorf("Leaf(0x%X) = %v, want *InvalidIDError", uint32(id), err)
		}
		if err := w.Uint(id, 1); !errors.As(err, &ie) {
			t.Errorf("Uint(0x%X) = %v, want *InvalidIDError", uint32(id), err)
		}
	}
	if buf.Len() != 0 {
		t.Errorf("rejected IDs wrote % X; want nothing", buf.Bytes())
	}
	if w.Depth() != 0 {
		t.Errorf("Depth() = %d after rejected StartMaster calls, want 0", w.Depth())
	}
}

func TestFlushRequiresATopLevelBoundary(t *testing.T) {
	var buf bytes.Buffer
	w := writer.New(&buf)

	if err := w.Flush(); err != nil {
		t.Fatalf("Flush on an empty document: %v", err)
	}
	if err := w.StartMaster(idRoot, writer.UnknownSize()); err != nil {
		t.Fatalf("StartMaster(root): %v", err)
	}
	err := w.Flush()
	var oe *writer.OpenMasterError
	if !errors.As(err, &oe) || !errors.Is(err, writer.ErrOpenMaster) {
		t.Fatalf("Flush with a master open = %v, want *OpenMasterError", err)
	}
	if oe.ID != idRoot || oe.Depth != 0 || oe.Op != "Flush" {
		t.Errorf("OpenMasterError = %+v, want Flush on %s at depth 0", *oe, idRoot)
	}
	if err := w.StartMaster(idNest, writer.Buffered()); err != nil {
		t.Fatalf("StartMaster(nest): %v", err)
	}
	if err := w.Flush(); errors.As(err, &oe) && oe.Depth != 1 {
		t.Errorf("OpenMasterError.Depth = %d for the inner master, want 1", oe.Depth)
	}
	// Still usable: finish the document and flush for real.
	if err := w.EndMaster(); err != nil {
		t.Fatalf("EndMaster(nest): %v", err)
	}
	if err := w.EndMaster(); err != nil {
		t.Fatalf("EndMaster(root): %v", err)
	}
	if err := w.Flush(); err != nil {
		t.Errorf("Flush at a top-level boundary: %v", err)
	}
}

func TestFlushForwardsToTheSink(t *testing.T) {
	var buf bytes.Buffer
	bw := bufio.NewWriter(&buf)
	w := writer.New(bw)

	if err := w.Uint(idUintL, 1); err != nil {
		t.Fatalf("Uint: %v", err)
	}
	if buf.Len() != 0 {
		t.Fatalf("the *bufio.Writer already passed % X through; the test needs it buffered", buf.Bytes())
	}
	if err := w.Flush(); err != nil {
		t.Fatalf("Flush: %v", err)
	}
	if want := []byte{0x80, 0x81, 0x01}; !bytes.Equal(buf.Bytes(), want) {
		t.Errorf("after Flush the sink holds % X, want % X", buf.Bytes(), want)
	}
}

func TestCloseSemantics(t *testing.T) {
	var buf bytes.Buffer
	w := writer.New(&buf)

	if err := w.Uint(idUintL, 1); err != nil {
		t.Fatalf("Uint: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Errorf("second Close = %v, want nil", err)
	}
	if err := w.Uint(idUintL, 2); !errors.Is(err, writer.ErrClosed) {
		t.Errorf("Uint after Close = %v, want ErrClosed", err)
	}
	if err := w.StartMaster(idNest, writer.Buffered()); !errors.Is(err, writer.ErrClosed) {
		t.Errorf("StartMaster after Close = %v, want ErrClosed", err)
	}
	if err := w.EndMaster(); !errors.Is(err, writer.ErrClosed) {
		t.Errorf("EndMaster after Close = %v, want ErrClosed", err)
	}
	if err := w.Flush(); !errors.Is(err, writer.ErrClosed) {
		t.Errorf("Flush after Close = %v, want ErrClosed", err)
	}

	// Closing with a master open reports it and still closes.
	var buf2 bytes.Buffer
	w2 := writer.New(&buf2)
	if err := w2.StartMaster(idNest, writer.Buffered()); err != nil {
		t.Fatalf("StartMaster: %v", err)
	}
	if err := w2.Close(); !errors.Is(err, writer.ErrOpenMaster) {
		t.Errorf("Close with a master open = %v, want ErrOpenMaster", err)
	}
	if err := w2.Uint(idUintL, 1); !errors.Is(err, writer.ErrClosed) {
		t.Errorf("Uint after the failed Close = %v, want ErrClosed", err)
	}
	if buf2.Len() != 0 {
		t.Errorf("the unfinished buffered master emitted % X; want nothing", buf2.Bytes())
	}
}

// failSink fails every write, like a full disk or a broken pipe.
type failSink struct{ err error }

func (f failSink) Write(p []byte) (int, error) { return 0, f.err }

// shortSink accepts one byte per call and reports no error, which io.Writer forbids
// and the Writer must turn into io.ErrShortWrite.
type shortSink struct{}

func (shortSink) Write(p []byte) (int, error) { return 1, nil }

func TestSinkFailureIsTerminal(t *testing.T) {
	boom := errors.New("disk full")
	w := writer.New(failSink{err: boom})
	if err := w.Uint(idUintL, 1); !errors.Is(err, boom) {
		t.Fatalf("Uint = %v, want the sink's error", err)
	}
	if err := w.Uint(idUintL, 1); !errors.Is(err, boom) {
		t.Errorf("Uint after the sink failure = %v, want the stored failure", err)
	}
	if err := w.StartMaster(idNest, writer.Buffered()); !errors.Is(err, boom) {
		t.Errorf("StartMaster after the sink failure = %v, want the stored failure", err)
	}

	w2 := writer.New(shortSink{})
	if err := w2.Binary(idBinL, []byte{1, 2, 3}); !errors.Is(err, io.ErrShortWrite) {
		t.Errorf("Binary on a short-writing sink = %v, want io.ErrShortWrite", err)
	}
}

func TestNewPanicsOnNilSink(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Error("New(nil) did not panic")
		}
	}()
	writer.New(nil)
}
