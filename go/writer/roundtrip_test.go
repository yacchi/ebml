package writer_test

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"testing"
	"time"

	"github.com/yacchi/ebml/parser"
	"github.com/yacchi/ebml/writer"
)

// event is one cursor event, recorded so a written document can be compared to the
// element sequence it was written as.
type event struct {
	op        string
	id        parser.ElementID
	depth     int
	offset    int64
	end       int64
	size      int64
	headerLen int
	payload   []byte
}

func (e event) String() string {
	if e.op == "payload" {
		return fmt.Sprintf("payload id=%s bytes=%X", e.id, e.payload)
	}
	return fmt.Sprintf("%s id=%s depth=%d size=%d hlen=%d", e.op, e.id, e.depth, e.size, e.headerLen)
}

// nodeEvent records what a node states about its element. Only these fields are
// kept, because a node is valid solely until the next pull.
func nodeEvent(op string, n parser.Node) event {
	return event{
		op:        op,
		id:        n.ID(),
		depth:     n.Depth(),
		offset:    n.Offset(),
		end:       n.End(),
		size:      n.Size(),
		headerLen: n.HeaderLen(),
	}
}

// scan pulls every event of doc from a cursor fed in chunks of the given size (the
// whole document at once when chunk <= 0). The boundary rule is the one a stream of
// concatenated unknown-size documents needs: a top-level idRoot header ends the open
// idRoot.
func scan(t *testing.T, doc []byte, chunk int) []event {
	t.Helper()

	c := parser.NewCursor(classify, parser.WithBoundary(
		func(open, next parser.ElementID) bool { return next == idRoot }))
	if chunk <= 0 {
		chunk = len(doc)
	}
	pos := 0
	feed := func() bool {
		if pos >= len(doc) {
			return false
		}
		end := min(pos+chunk, len(doc))
		c.Feed(doc[pos:end])
		pos = end
		return true
	}

	var events []event
	finalized := false
	for {
		node, err := c.Next()
		if err != nil {
			var needMore parser.NeedMoreData
			switch {
			case errors.As(err, &needMore):
				if feed() {
					continue
				}
				if finalized {
					t.Fatalf("NeedMoreData after Finalize at offset %d", c.Offset())
				}
				finalized = true
				if err := c.Finalize(); err != nil {
					t.Fatalf("Finalize: %v", err)
				}
				continue
			case errors.Is(err, io.EOF):
				return events
			default:
				t.Fatalf("Next at offset %d: %v", c.Offset(), err)
			}
		}

		switch n := node.(type) {
		case *parser.MasterNode:
			events = append(events, nodeEvent("master", n))
		case *parser.LeafNode:
			events = append(events, nodeEvent("leaf", n))
			payload, err := n.Payload()
			for {
				var needMore parser.NeedMoreData
				if !errors.As(err, &needMore) {
					break
				}
				if !feed() {
					t.Fatalf("payload of %s still needs data at end of input", n.ID())
				}
				payload, err = n.Payload()
			}
			if err != nil {
				t.Fatalf("Payload of %s: %v", n.ID(), err)
			}
			ev := nodeEvent("payload", n)
			// Payload views the cursor's buffer and the events outlive the scan,
			// so this is where they become the test's own bytes.
			ev.payload = bytes.Clone(payload)
			events = append(events, ev)
		case *parser.EndNode:
			events = append(events, nodeEvent("close", n))
		}
	}
}

func eventStrings(events []event) []string {
	out := make([]string, len(events))
	for i, e := range events {
		out[i] = e.String()
	}
	return out
}

// buildDocument writes two concatenated unknown-size documents that between them
// use all three size strategies, nested both ways round.
func buildDocument(t *testing.T) []byte {
	t.Helper()

	sink := &memSink{} // patchable: the Reserved master below writes straight through
	w := writer.New(sink)
	steps := []func() error{
		func() error { return w.StartMaster(idRoot, writer.UnknownSize()) },
		func() error { return w.StartMaster(idBranch, writer.Buffered()) },
		func() error { return w.Uint(idUintL, 42) },
		func() error { return w.String(idStrL, "kvs") },
		w.EndMaster,
		func() error { return w.StartMaster(idNest, writer.Reserved(4)) },
		func() error { return w.Binary(idBinL, []byte{1, 2, 3, 4, 5, 6}) },
		w.EndMaster,
		func() error { return w.Date(idDateL, testEpoch.Add(time.Second)) },
		w.EndMaster, // the unknown-size master: writes nothing, ends structurally
		func() error { return w.StartMaster(idRoot, writer.UnknownSize()) },
		func() error { return w.Uint(idUintL, 7) },
		w.EndMaster,
		w.Close,
	}
	for i, step := range steps {
		if err := step(); err != nil {
			t.Fatalf("document step %d: %v", i, err)
		}
	}
	return sink.b
}

// TestRoundTripEventSequence writes a document and reads it back with the cursor:
// the events must be exactly the elements that were written, at the depths they
// were written at.
func TestRoundTripEventSequence(t *testing.T) {
	doc := buildDocument(t)
	events := scan(t, doc, 0)

	want := []string{
		"master id=0x1FFFFFF0 depth=0 size=-1 hlen=12",
		"master id=0x3F0001 depth=1 size=8 hlen=4",
		"leaf id=0x80 depth=2 size=1 hlen=2",
		"payload id=0x80 bytes=2A",
		"leaf id=0x83 depth=2 size=3 hlen=2",
		"payload id=0x83 bytes=6B7673",
		"close id=0x3F0001 depth=1 size=8 hlen=4",
		"master id=0x4001 depth=1 size=8 hlen=6",
		"leaf id=0x84 depth=2 size=6 hlen=2",
		"payload id=0x84 bytes=010203040506",
		"close id=0x4001 depth=1 size=8 hlen=6",
		"leaf id=0x85 depth=1 size=8 hlen=2",
		"payload id=0x85 bytes=000000003B9ACA00",
		"close id=0x1FFFFFF0 depth=0 size=-1 hlen=12",
		"master id=0x1FFFFFF0 depth=0 size=-1 hlen=12",
		"leaf id=0x80 depth=1 size=1 hlen=2",
		"payload id=0x80 bytes=07",
		"close id=0x1FFFFFF0 depth=0 size=-1 hlen=12",
	}
	got := eventStrings(events)
	if len(got) != len(want) {
		t.Fatalf("recorded %d events, want %d:\n got %v\nwant %v", len(got), len(want), got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("event %d = %q, want %q", i, got[i], want[i])
		}
	}

	// The first unknown-size master closes exactly where the second one starts, and
	// the last one closes at end of input.
	firstClose, secondStart, lastClose := events[13], events[14], events[17]
	if firstClose.end != secondStart.offset {
		t.Errorf("the first master closed at %d, want the second master's offset %d",
			firstClose.end, secondStart.offset)
	}
	if lastClose.end != int64(len(doc)) {
		t.Errorf("the last master closed at %d, want the document length %d", lastClose.end, len(doc))
	}
}

// TestRoundTripIsSplitInvariant: what the writer produced must read back
// identically however the reader is fed, which is the cursor's core promise.
func TestRoundTripIsSplitInvariant(t *testing.T) {
	doc := buildDocument(t)
	want := eventStrings(scan(t, doc, 0))
	for _, chunk := range []int{1, 2, 3, 7, 13} {
		got := eventStrings(scan(t, doc, chunk))
		if len(got) != len(want) {
			t.Fatalf("chunk %d: recorded %d events, want %d", chunk, len(got), len(want))
		}
		for i := range want {
			if got[i] != want[i] {
				t.Errorf("chunk %d: event %d = %q, want %q", chunk, i, got[i], want[i])
			}
		}
	}
}

// TestRoundTripNonMinimalSizeIsAccepted: a size VINT wider than necessary is legal
// EBML, and the cursor reports the wider header through its header length while
// reading the same size and payload.
func TestRoundTripNonMinimalSizeIsAccepted(t *testing.T) {
	var buf bytes.Buffer
	w := writer.New(&buf)
	payload := []byte{1, 2, 3}
	if err := w.LeafWith(idBinL, payload, writer.Reserved(4)); err != nil {
		t.Fatalf("LeafWith: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	sizeBytes, err := writer.EncodeSizeWidth(int64(len(payload)), 4)
	if err != nil {
		t.Fatalf("EncodeSizeWidth: %v", err)
	}
	if got := buf.Bytes()[1:5]; !bytes.Equal(got, sizeBytes) {
		t.Errorf("size VINT = % X, want % X", got, sizeBytes)
	}

	events := scan(t, buf.Bytes(), 1)
	want := []string{
		"leaf id=0x84 depth=0 size=3 hlen=5",
		"payload id=0x84 bytes=010203",
	}
	got := eventStrings(events)
	if len(got) != len(want) {
		t.Fatalf("recorded %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("event %d = %q, want %q", i, got[i], want[i])
		}
	}
}

// TestRoundTripAllValueTypes checks that each typed leaf helper is read back as the
// value written, using the cursor for the reading and the parser's decode helpers
// for the values -- the inverse property end to end.
func TestRoundTripAllValueTypes(t *testing.T) {
	var buf bytes.Buffer
	w := writer.New(&buf)
	date := time.Date(1999, time.June, 1, 12, 0, 0, 0, time.UTC)

	if err := w.StartMaster(idBranch, writer.Buffered()); err != nil {
		t.Fatalf("StartMaster: %v", err)
	}
	for i, step := range []func() error{
		func() error { return w.Uint(idUintL, 1<<40) },
		func() error { return w.Int(idIntL, -4242) },
		func() error { return w.Float(idFloatL, -0.25, writer.Float64) },
		func() error { return w.UTF8(idStrL, "caf\u00e9 \u20ac \U0001F3AC") },
		func() error { return w.Date(idDateL, date) },
	} {
		if err := step(); err != nil {
			t.Fatalf("value %d: %v", i, err)
		}
	}
	if err := w.EndMaster(); err != nil {
		t.Fatalf("EndMaster: %v", err)
	}

	payloads := map[parser.ElementID][]byte{}
	for _, e := range scan(t, buf.Bytes(), 5) {
		if e.op == "payload" {
			payloads[e.id] = e.payload
		}
	}
	if got, err := parser.DecodeUint(payloads[idUintL]); err != nil || got != 1<<40 {
		t.Errorf("uint = %d, %v; want %d", got, err, uint64(1)<<40)
	}
	if got, err := parser.DecodeInt(payloads[idIntL]); err != nil || got != -4242 {
		t.Errorf("int = %d, %v; want -4242", got, err)
	}
	if got, err := parser.DecodeFloat(payloads[idFloatL]); err != nil || got != -0.25 {
		t.Errorf("float = %v, %v; want -0.25", got, err)
	}
	if got := parser.DecodeString(payloads[idStrL]); got != "caf\u00e9 \u20ac \U0001F3AC" {
		t.Errorf("utf-8 string = %q, want %q", got, "caf\u00e9 \u20ac \U0001F3AC")
	}
	ns, err := parser.DecodeInt(payloads[idDateL])
	if err != nil {
		t.Fatalf("date: %v", err)
	}
	if got := testEpoch.Add(time.Duration(ns)); !got.Equal(date) {
		t.Errorf("date = %v, want %v", got, date)
	}
}
