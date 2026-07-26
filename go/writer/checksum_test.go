package writer_test

import (
	"bytes"
	"errors"
	"testing"

	"github.com/yacchi/ebml/crc"
	"github.com/yacchi/ebml/parser"
	"github.com/yacchi/ebml/writer"
)

// idCRC stands in for matroska.IDCRC32 here. The writer knows no element, so its
// tests must not import the registry to name one: what matters is that the CALLER
// supplies the ID, not which ID it is. classify in writer_test.go answers
// KindBinary for it, which is what a CRC-32 element is on the wire.
const idCRC parser.ElementID = 0xBF

// buildDoc runs fn against a Writer over a patchable sink and returns the whole
// document, failing the test on the first error rather than at the end, so a
// broken step is named where it happened.
func buildDoc(t *testing.T, fn func(w *writer.Writer) error) []byte {
	t.Helper()
	sink := &memSink{}
	w := writer.New(sink)
	if err := fn(w); err != nil {
		t.Fatalf("build: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	return sink.b
}

// checksumHeader is the CRC-32 element's header as the writer must have emitted
// it: the caller's ID, then the fixed four-byte payload length.
func checksumHeader() []byte {
	return append(writer.EncodeID(idCRC), writer.EncodeSize(crc.Size)...)
}

// verifyChecksummedMaster applies RFC 8794 section 11.3.1 to one master's Element
// Data: the CRC-32 element must come FIRST, and its stored value must equal the
// checksum of everything after it -- the siblings as stored, with the CRC-32
// element's own header and payload excluded. It returns the covered bytes so a
// caller can assert what they are.
//
// The stored value is decoded with crc.Decode rather than compared byte by byte,
// because that is the storage layout under test: eyeballing four hex bytes would
// pass just as happily on a big-endian encoding.
func verifyChecksummedMaster(t *testing.T, where string, payload []byte) []byte {
	t.Helper()
	head := checksumHeader()
	if len(payload) < len(head)+crc.Size {
		t.Fatalf("%s: payload is %d bytes, too short to hold a CRC-32 element", where, len(payload))
	}
	if !bytes.HasPrefix(payload, head) {
		t.Fatalf("%s: payload starts %X, want the CRC-32 element header %X", where, payload[:len(head)], head)
	}
	stored, err := crc.Decode(payload[len(head) : len(head)+crc.Size])
	if err != nil {
		t.Fatalf("%s: crc.Decode: %v", where, err)
	}
	covered := payload[len(head)+crc.Size:]
	if err := crc.Verify(covered, stored); err != nil {
		t.Fatalf("%s: stored checksum does not cover the rest of the payload: %v", where, err)
	}
	return covered
}

// masterEvent returns the single master event for id, so a test can read the
// extents the cursor actually derived instead of recomputing them.
func masterEvent(t *testing.T, events []event, id parser.ElementID) event {
	t.Helper()
	var found []event
	for _, e := range events {
		if e.op == "master" && e.id == id {
			found = append(found, e)
		}
	}
	if len(found) != 1 {
		t.Fatalf("want exactly one master %s, got %d", id, len(found))
	}
	return found[0]
}

func TestChecksumIsTheFirstChildAndCoversTheSiblings(t *testing.T) {
	doc := buildDoc(t, func(w *writer.Writer) error {
		if err := w.StartMaster(idBranch, writer.Buffered(), writer.WithChecksum(idCRC)); err != nil {
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

	// The same two leaves written with no master around them: exactly the bytes the
	// checksum has to cover, produced by the same encoder.
	children := buildDoc(t, func(w *writer.Writer) error {
		if err := w.Uint(idUintL, 42); err != nil {
			return err
		}
		return w.String(idStrL, "kvs")
	})

	master := masterEvent(t, scan(t, doc, 0), idBranch)
	payload := doc[master.offset+int64(master.headerLen) : master.end]

	covered := verifyChecksummedMaster(t, "idBranch", payload)
	if !bytes.Equal(covered, children) {
		t.Fatalf("covered bytes %X, want the two leaves %X", covered, children)
	}
}

func TestChecksumIsInsideTheMastersDeclaredSize(t *testing.T) {
	doc := buildDoc(t, func(w *writer.Writer) error {
		if err := w.StartMaster(idBranch, writer.Buffered(), writer.WithChecksum(idCRC)); err != nil {
			return err
		}
		if err := w.Uint(idUintL, 42); err != nil {
			return err
		}
		return w.EndMaster()
	})

	// Reading the document back is the assertion: if the master's declared size had
	// omitted the CRC-32 element, the cursor would either end the master early or
	// run off its end.
	events := scan(t, doc, 0)
	master := masterEvent(t, events, idBranch)
	if master.end != int64(len(doc)) {
		t.Fatalf("master ends at %d, want the end of the document %d", master.end, len(doc))
	}

	var leaves []event
	for _, e := range events {
		if e.op == "leaf" {
			leaves = append(leaves, e)
		}
	}
	if len(leaves) != 2 {
		t.Fatalf("want two leaves inside the master, got %d (%v)", len(leaves), eventStrings(events))
	}
	first, second := leaves[0], leaves[1]
	if first.id != idCRC {
		t.Fatalf("first child is %s, want the CRC-32 element %s", first.id, idCRC)
	}
	if first.offset != master.offset+int64(master.headerLen) {
		t.Fatalf("CRC-32 element starts at %d, want the master's first payload byte %d",
			first.offset, master.offset+int64(master.headerLen))
	}
	if first.size != crc.Size {
		t.Fatalf("CRC-32 payload is %d bytes, want %d", first.size, crc.Size)
	}
	if second.offset != first.end {
		t.Fatalf("second child starts at %d, want right after the CRC-32 element at %d", second.offset, first.end)
	}
	if second.end != master.end {
		t.Fatalf("last child ends at %d, want the master's end %d", second.end, master.end)
	}
}

func TestChecksumRejectsStrategiesThatDoNotBuffer(t *testing.T) {
	cases := []struct {
		name string
		size writer.SizeStrategy
	}{
		{"reserved", writer.Reserved(4)},
		{"unknown_size", writer.UnknownSize()},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			sink := &memSink{} // patchable, so Reserved fails for the checksum and nothing else
			w := writer.New(sink)

			err := w.StartMaster(idBranch, tc.size, writer.WithChecksum(idCRC))
			var strategyErr *writer.ChecksumStrategyError
			if !errors.As(err, &strategyErr) {
				t.Fatalf("StartMaster: %v, want *ChecksumStrategyError", err)
			}
			if strategyErr.Strategy.String() != tc.size.String() {
				t.Fatalf("error names strategy %s, want the offending %s", strategyErr.Strategy, tc.size)
			}
			if !errors.Is(err, writer.ErrChecksumStrategy) {
				t.Fatalf("error does not unwrap to ErrChecksumStrategy: %v", err)
			}
			// Validation happens before the strategy switch, which is what keeps
			// Reserved -- whose header would already be out -- from leaving bytes behind.
			if len(sink.b) != 0 {
				t.Fatalf("sink received %d bytes, want none", len(sink.b))
			}
			if w.Depth() != 0 {
				t.Fatalf("Depth is %d, want 0: the rejected master must not be open", w.Depth())
			}

			// A rejected call leaves the Writer usable, so the caller may correct it.
			if err := w.StartMaster(idBranch, tc.size); err != nil {
				t.Fatalf("StartMaster after the rejection: %v", err)
			}
			if err := w.EndMaster(); err != nil {
				t.Fatalf("EndMaster after the rejection: %v", err)
			}
			if len(sink.b) == 0 {
				t.Fatal("the corrected master wrote nothing")
			}
		})
	}
}

func TestChecksumRejectsIllFormedChecksumID(t *testing.T) {
	const badID parser.ElementID = 0x00 // no VINT length marker at all

	sink := &memSink{}
	w := writer.New(sink)

	err := w.StartMaster(idBranch, writer.Buffered(), writer.WithChecksum(badID))
	var idErr *writer.InvalidIDError
	if !errors.As(err, &idErr) {
		t.Fatalf("StartMaster: %v, want *InvalidIDError", err)
	}
	// The rejected ID is the option's, not the master's: naming idBranch would send
	// the caller to fix a call that was correct.
	if idErr.ID != badID {
		t.Fatalf("error names ID %s, want the checksum ID %s", idErr.ID, badID)
	}
	if len(sink.b) != 0 {
		t.Fatalf("sink received %d bytes, want none", len(sink.b))
	}
	if err := w.StartMaster(idBranch, writer.Buffered(), writer.WithChecksum(idCRC)); err != nil {
		t.Fatalf("StartMaster after the rejection: %v", err)
	}
	if err := w.EndMaster(); err != nil {
		t.Fatalf("EndMaster after the rejection: %v", err)
	}
}

func TestNestedChecksumsCoverTheirOwnData(t *testing.T) {
	doc := buildDoc(t, func(w *writer.Writer) error {
		if err := w.StartMaster(idRoot, writer.Buffered(), writer.WithChecksum(idCRC)); err != nil {
			return err
		}
		if err := w.StartMaster(idBranch, writer.Buffered(), writer.WithChecksum(idCRC)); err != nil {
			return err
		}
		if err := w.Uint(idUintL, 7); err != nil {
			return err
		}
		if err := w.EndMaster(); err != nil {
			return err
		}
		if err := w.String(idStrL, "tail"); err != nil {
			return err
		}
		return w.EndMaster()
	})

	events := scan(t, doc, 0)
	outer := masterEvent(t, events, idRoot)
	inner := masterEvent(t, events, idBranch)

	outerPayload := doc[outer.offset+int64(outer.headerLen) : outer.end]
	outerCovered := verifyChecksummedMaster(t, "idRoot", outerPayload)

	innerElement := doc[inner.offset:inner.end]
	innerPayload := doc[inner.offset+int64(inner.headerLen) : inner.end]
	verifyChecksummedMaster(t, "idBranch", innerPayload)

	// The inner master is Element Data of the outer one, so the outer checksum
	// covers all of it -- including the inner CRC-32 element. Nothing about the
	// inner checksum makes it invisible to its parent.
	if !bytes.HasPrefix(outerCovered, innerElement) {
		t.Fatalf("outer coverage %X does not begin with the whole inner element %X", outerCovered, innerElement)
	}
	if !bytes.Contains(innerElement, checksumHeader()) {
		t.Fatal("the inner element carries no CRC-32 element, so this proves nothing")
	}
}

func TestChecksumOfEmptyMasterCoversNoBytes(t *testing.T) {
	doc := buildDoc(t, func(w *writer.Writer) error {
		if err := w.StartMaster(idBranch, writer.Buffered(), writer.WithChecksum(idCRC)); err != nil {
			return err
		}
		return w.EndMaster()
	})

	master := masterEvent(t, scan(t, doc, 0), idBranch)
	payload := doc[master.offset+int64(master.headerLen) : master.end]

	covered := verifyChecksummedMaster(t, "idBranch", payload)
	if len(covered) != 0 {
		t.Fatalf("covered %d bytes, want none: the master has no other children", len(covered))
	}
	// Checksum(nil) is the algorithm's value for no input, not a "not computed"
	// sentinel, so an empty master stores it rather than skipping the element.
	stored, err := crc.Decode(payload[len(checksumHeader()):])
	if err != nil {
		t.Fatalf("crc.Decode: %v", err)
	}
	if stored != crc.Checksum(nil) {
		t.Fatalf("stored 0x%08X, want Checksum(nil) 0x%08X", stored, crc.Checksum(nil))
	}
}

func TestMasterWithoutTheOptionEmitsNoChecksum(t *testing.T) {
	doc := buildDoc(t, func(w *writer.Writer) error {
		if err := w.StartMaster(idBranch, writer.Buffered()); err != nil {
			return err
		}
		if err := w.Uint(idUintL, 42); err != nil {
			return err
		}
		return w.EndMaster()
	})

	// Emission is opt-in: a master that did not ask for a checksum must be byte for
	// byte what it was before this feature existed.
	events := scan(t, doc, 0)
	for _, e := range events {
		if e.id == idCRC {
			t.Fatalf("document carries a CRC-32 element without the option: %v", eventStrings(events))
		}
	}
	master := masterEvent(t, events, idBranch)
	payload := doc[master.offset+int64(master.headerLen) : master.end]
	if bytes.HasPrefix(payload, checksumHeader()) {
		t.Fatalf("payload %X starts with a CRC-32 element header", payload)
	}
}
