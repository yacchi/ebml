package writer_test

import (
	"bytes"
	"encoding/binary"
	"errors"
	"hash/crc32"
	"math/rand"
	"testing"

	"github.com/yacchi/ebml/impl/go/crc"
	"github.com/yacchi/ebml/impl/go/parser"
	"github.com/yacchi/ebml/impl/go/tree"
	"github.com/yacchi/ebml/impl/go/writer"
)

// idCRC2 is a second, distinct well-formed ID, used only to prove which of two
// WithChecksum options actually won -- reusing idCRC for both applications would
// leave the question unanswered.
const idCRC2 parser.ElementID = 0x9F

// recordingSink is a sink whose every Write call is logged individually, not just
// concatenated into a byte slice. A pre-write failure path is proven by asserting
// zero LOGGED CALLS, which a bug that wrote and then rolled back bytes (impossible
// with the current append-only memSink, but not with a future sink) could still
// slip past a mere "len(sink.b) == 0" check.
type recordingSink struct {
	calls [][]byte
}

func (s *recordingSink) Write(p []byte) (int, error) {
	cp := append([]byte(nil), p...)
	s.calls = append(s.calls, cp)
	return len(p), nil
}

// independentChecksum recomputes an EBML CRC-32 straight from hash/crc32 and
// encoding/binary, deliberately bypassing package crc entirely. Verifying a
// crc-package-produced value against a crc-package-independent computation is
// what catches a bug shared between the value under test and its own verifier.
func independentChecksum(t *testing.T, data []byte) uint32 {
	t.Helper()
	return crc32.ChecksumIEEE(data)
}

func independentDecode(t *testing.T, b []byte) uint32 {
	t.Helper()
	if len(b) != 4 {
		t.Fatalf("stored CRC-32 payload is %d bytes, want 4", len(b))
	}
	return binary.LittleEndian.Uint32(b)
}

// TestChecksumCoversALargeNestedBufferedChild proves that coverage is the WHOLE
// buffered subtree, not just the first child written into it: a bug that summed
// only up to the first Buffered flush, or only a fixed prefix, would pass every
// existing checksum test (their children are a handful of bytes) yet fail here,
// where the inner master alone holds tens of thousands of bytes.
func TestChecksumCoversALargeNestedBufferedChild(t *testing.T) {
	rng := rand.New(rand.NewSource(1))
	big := make([]byte, 70000) // comfortably past any plausible internal buffer/prefix size
	rng.Read(big)

	build := func(w *writer.Writer) error {
		if err := w.StartMaster(idBranch, writer.Buffered()); err != nil {
			return err
		}
		if err := w.Binary(idBinL, big); err != nil {
			return err
		}
		if err := w.EndMaster(); err != nil {
			return err
		}
		return w.String(idStrL, "tail")
	}

	doc := buildDoc(t, func(w *writer.Writer) error {
		if err := w.StartMaster(idRoot, writer.Buffered(), writer.WithChecksum(idCRC)); err != nil {
			return err
		}
		if err := build(w); err != nil {
			return err
		}
		return w.EndMaster()
	})
	wantChildren := buildDoc(t, build)

	events := scan(t, doc, 0)
	root := masterEvent(t, events, idRoot)
	payload := doc[root.offset+int64(root.headerLen) : root.end]

	head := checksumHeader()
	if !bytes.HasPrefix(payload, head) {
		t.Fatalf("payload does not start with the CRC-32 element header %X", head)
	}
	stored := independentDecode(t, payload[len(head):len(head)+crc.Size])
	covered := payload[len(head)+crc.Size:]

	if !bytes.Equal(covered, wantChildren) {
		t.Fatalf("covered bytes are %d bytes, want the %d-byte children verbatim (large child truncated or mis-covered)",
			len(covered), len(wantChildren))
	}
	if got := independentChecksum(t, covered); got != stored {
		t.Fatalf("stored checksum 0x%08X does not match an independently computed 0x%08X over the %d covered bytes",
			stored, got, len(covered))
	}
}

// TestWithChecksumAppliedTwiceKeepsExactlyOneElement documents what StartMaster
// actually does when WithChecksum is passed twice on the same call: nothing in
// applyMasterOptions rejects the duplicate, so the options are folded in order and
// the LAST one wins, exactly like any other last-write-wins option resolution.
// This test exists to catch a regression in either direction -- an error being
// introduced, or two CRC-32 elements silently being emitted -- either of which
// would be a behavior change nobody else asserts today.
func TestWithChecksumAppliedTwiceKeepsExactlyOneElement(t *testing.T) {
	doc := buildDoc(t, func(w *writer.Writer) error {
		if err := w.StartMaster(idBranch, writer.Buffered(),
			writer.WithChecksum(idCRC), writer.WithChecksum(idCRC2)); err != nil {
			return err
		}
		if err := w.Uint(idUintL, 42); err != nil {
			return err
		}
		return w.EndMaster()
	})

	events := scan(t, doc, 0)
	var crcEvents []event
	for _, e := range events {
		if e.op == "leaf" && (e.id == idCRC || e.id == idCRC2) {
			crcEvents = append(crcEvents, e)
		}
	}
	if len(crcEvents) != 1 {
		t.Fatalf("document carries %d CRC-32-shaped elements, want exactly 1 (%v)", len(crcEvents), eventStrings(events))
	}
	if crcEvents[0].id != idCRC2 {
		t.Fatalf("emitted CRC-32 element has ID %s, want the LAST option's ID %s", crcEvents[0].id, idCRC2)
	}
}

// TestChecksumAcceptsTheZeroSizeStrategyValue proves that WithChecksum works with
// writer.SizeStrategy{}, the zero value, and not merely with the writer.Buffered()
// constructor. StartMaster's own doc says Buffered is the zero value and the two
// must be interchangeable everywhere Buffered is accepted; a strategy-kind check
// written as "size.kind == strategyBuffered" would pass this, but a check written
// against a wrong sentinel or against the Buffered() call site specifically could
// silently reject the zero value while accepting the named constructor.
func TestChecksumAcceptsTheZeroSizeStrategyValue(t *testing.T) {
	sink := &memSink{}
	w := writer.New(sink)

	if err := w.StartMaster(idBranch, writer.SizeStrategy{}, writer.WithChecksum(idCRC)); err != nil {
		t.Fatalf("StartMaster with the zero SizeStrategy value and WithChecksum: %v, want acceptance", err)
	}
	if err := w.Uint(idUintL, 9); err != nil {
		t.Fatalf("Uint: %v", err)
	}
	if err := w.EndMaster(); err != nil {
		t.Fatalf("EndMaster: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	payload := verifyBranchPayload(t, sink.b)
	verifyChecksummedMaster(t, "idBranch (zero SizeStrategy)", payload)
}

// verifyBranchPayload isolates idBranch's Element Data from a document holding
// exactly that one top-level master, so callers can feed it straight to
// verifyChecksummedMaster.
func verifyBranchPayload(t *testing.T, doc []byte) []byte {
	t.Helper()
	master := masterEvent(t, scan(t, doc, 0), idBranch)
	return doc[master.offset+int64(master.headerLen) : master.end]
}

// TestChecksumMasterContainingAReservedChild proves that a Buffered-and-checksummed
// master may contain a child written with Reserved, and that Reserved works there
// purely through the enclosing Buffered master's in-memory patch path: the sink
// here is a bare *bytes.Buffer, which implements neither io.WriterAt, so Reserved
// could only have succeeded by patching w.cur rather than the sink. The checksum
// must then cover the child's non-minimal, reserved-width header too, since that
// header is as-stored Element Data of the checksummed master.
func TestChecksumMasterContainingAReservedChild(t *testing.T) {
	var buf bytes.Buffer // NOT an io.WriterAt: proves Reserved used the buffered target
	w := writer.New(&buf)

	if err := w.StartMaster(idBranch, writer.Buffered(), writer.WithChecksum(idCRC)); err != nil {
		t.Fatalf("StartMaster: %v", err)
	}
	// A non-minimal, 4-byte reserved size VINT for a 1-byte payload: only visible in
	// the stored bytes, so this also proves the checksum covers the header as
	// written, not some minimal-width recomputation of it.
	if err := w.LeafWith(idUintL, writer.EncodeUint(5), writer.Reserved(4)); err != nil {
		t.Fatalf("LeafWith Reserved: %v", err)
	}
	if err := w.EndMaster(); err != nil {
		t.Fatalf("EndMaster: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	payload := verifyBranchPayload(t, buf.Bytes())
	covered := verifyChecksummedMaster(t, "idBranch (Reserved child)", payload)

	sizeBytes, err := writer.EncodeSizeWidth(1, 4)
	if err != nil {
		t.Fatalf("EncodeSizeWidth: %v", err)
	}
	wantHeader := append(writer.EncodeID(idUintL), sizeBytes...)
	if !bytes.HasPrefix(covered, wantHeader) {
		t.Fatalf("covered bytes %X do not start with the reserved-width child header %X", covered, wantHeader)
	}
}

// TestChecksummedDocumentParsesCleanlyWithTreeParse feeds a checksummed document
// through tree.Parse, the retained-model entry point real consumers use, rather
// than the package's own scan/event test scaffolding. The CRC-32 element must show
// up as an ORDINARY retained leaf child -- Parse has no checksum awareness and none
// is asked for here, per "verification is explicit only" -- first among idRoot's
// children, with its stored bytes intact.
func TestChecksummedDocumentParsesCleanlyWithTreeParse(t *testing.T) {
	doc := buildDoc(t, func(w *writer.Writer) error {
		if err := w.StartMaster(idRoot, writer.Buffered(), writer.WithChecksum(idCRC)); err != nil {
			return err
		}
		if err := w.Uint(idUintL, 42); err != nil {
			return err
		}
		if err := w.String(idStrL, "kvs"); err != nil {
			return err
		}
		return w.EndMaster()
	})

	elems, err := tree.Parse(doc, tree.WithClassifier(classify))
	if err != nil {
		t.Fatalf("tree.Parse: %v", err)
	}
	if len(elems) != 1 {
		t.Fatalf("got %d top-level elements, want 1", len(elems))
	}
	root := elems[0]
	if root.ID != idRoot {
		t.Fatalf("top-level element is %s, want %s", root.ID, idRoot)
	}
	if len(root.Children) != 3 {
		t.Fatalf("idRoot has %d children, want 3 (CRC-32, uint, string): %+v", len(root.Children), root.Children)
	}

	crcChild := root.Children[0]
	if crcChild.ID != idCRC {
		t.Fatalf("first child is %s, want the CRC-32 element %s", crcChild.ID, idCRC)
	}
	if len(crcChild.Payload) != crc.Size {
		t.Fatalf("CRC-32 child payload is %d bytes, want %d", len(crcChild.Payload), crc.Size)
	}
	stored := independentDecode(t, crcChild.Payload)

	// Recompute coverage from the RAW document bytes right after the CRC-32
	// element's own end, through the master's end -- independent of tree's own
	// Offset/End bookkeeping being what wrote them.
	coveredStart := crcChild.Offset + int64(crcChild.HeaderLen) + int64(len(crcChild.Payload))
	covered := doc[coveredStart:root.End()]
	if got := independentChecksum(t, covered); got != stored {
		t.Fatalf("stored 0x%08X != independently computed 0x%08X over tree-derived coverage", stored, got)
	}

	uintChild, strChild := root.Children[1], root.Children[2]
	if uintChild.ID != idUintL || strChild.ID != idStrL {
		t.Fatalf("remaining children are %s, %s; want %s, %s", uintChild.ID, strChild.ID, idUintL, idStrL)
	}
}

// TestChecksumRejectionsWriteNothingAtAll strengthens the existing "sink received
// 0 bytes" assertions with a sink that logs every individual Write call: a
// pre-write failure must mean StartMaster never invoked Write at all, not merely
// that whatever it wrote happened to net out to zero bytes.
func TestChecksumRejectionsWriteNothingAtAll(t *testing.T) {
	t.Run("bad_strategy", func(t *testing.T) {
		rs := &recordingSink{}
		w := writer.New(rs)
		err := w.StartMaster(idBranch, writer.UnknownSize(), writer.WithChecksum(idCRC))
		var strategyErr *writer.ChecksumStrategyError
		if !errors.As(err, &strategyErr) {
			t.Fatalf("StartMaster: %v, want *ChecksumStrategyError", err)
		}
		if len(rs.calls) != 0 {
			t.Fatalf("sink logged %d Write call(s), want 0: %v", len(rs.calls), rs.calls)
		}
	})

	t.Run("bad_id", func(t *testing.T) {
		rs := &recordingSink{}
		w := writer.New(rs)
		const badID parser.ElementID = 0x00
		err := w.StartMaster(idBranch, writer.Buffered(), writer.WithChecksum(badID))
		var idErr *writer.InvalidIDError
		if !errors.As(err, &idErr) {
			t.Fatalf("StartMaster: %v, want *InvalidIDError", err)
		}
		if len(rs.calls) != 0 {
			t.Fatalf("sink logged %d Write call(s), want 0: %v", len(rs.calls), rs.calls)
		}
	})
}
