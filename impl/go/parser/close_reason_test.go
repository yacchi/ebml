package parser_test

import (
	"io"
	"testing"

	"github.com/yacchi/ebml/impl/go/internal/ebmltest"
	. "github.com/yacchi/ebml/impl/go/parser"
)

const (
	crSegment ElementID = 0x18538067
	crCluster ElementID = 0x1F43B675
	crLeaf    ElementID = 0x4487
)

func crClassifier(id ElementID) Kind {
	switch id {
	case crSegment, crCluster:
		return KindMaster
	}
	return KindBinary
}

// crBoundary is the shape a live stream needs: a Segment ends where the next
// top-level element begins. Nothing else ends anything.
func crBoundary(open, next ElementID) bool {
	return open == crSegment && next == crSegment
}

type closeEvent struct {
	id     ElementID
	reason CloseReason
	end    int64
}

// closesOf drains a cursor over raw and returns every master end it reported, in
// order, with the reason each carried.
func closesOf(t *testing.T, raw []byte) []closeEvent {
	t.Helper()
	c := NewCursor(crClassifier, WithBoundary(crBoundary))
	c.Feed(raw)
	if err := c.Finalize(); err != nil {
		t.Fatalf("Finalize: %v", err)
	}
	var got []closeEvent
	for {
		n, err := c.Next()
		if err == io.EOF {
			return got
		}
		if err != nil {
			t.Fatalf("Next: %v", err)
		}
		if end, ok := n.(*EndNode); ok {
			got = append(got, closeEvent{end.ID(), end.Reason(), end.End()})
		}
	}
}

// TestCloseReasonCoversEveryWay drives ONE stream that closes a master each of the
// three ways there are, so the reasons are checked against each other rather than
// one at a time. Two concatenated unknown-size Segments, each holding a known-size
// Cluster: every Cluster reaches its declared end, the first Segment is ended by
// the boundary rule when the second one's header arrives, and the second Segment is
// still open when the input runs out.
func TestCloseReasonCoversEveryWay(t *testing.T) {
	segment := func(payload byte) []byte {
		cluster := ebmltest.Master(crCluster, ebmltest.Leaf(crLeaf, []byte{payload}))
		return ebmltest.Encode(ebmltest.UnknownMaster(crSegment, cluster))
	}
	first := segment(0x01)
	raw := append(append([]byte{}, first...), segment(0x02)...)

	got := closesOf(t, raw)
	want := []closeEvent{
		{crCluster, ClosedByDeclaredEnd, 0},
		{crSegment, ClosedByBoundary, 0},
		{crCluster, ClosedByDeclaredEnd, 0},
		{crSegment, ClosedByEndOfInput, 0},
	}
	if len(got) != len(want) {
		t.Fatalf("closes = %v, want %d of them", got, len(want))
	}
	for i := range want {
		if got[i].id != want[i].id || got[i].reason != want[i].reason {
			t.Errorf("close %d = %s closed by %s, want %s closed by %s",
				i, got[i].id, got[i].reason, want[i].id, want[i].reason)
		}
	}

	// The boundary close does not swallow the element that triggered it: the first
	// Segment ends exactly where the second document's first byte is, so the
	// trigger is still ahead of the cursor and gets its own event.
	if got[1].end != int64(len(first)) {
		t.Errorf("the boundary close ended at %d, want %d (the offset the second Segment's header starts at)",
			got[1].end, len(first))
	}
	if got[3].end != int64(len(raw)) {
		t.Errorf("the EOF close ended at %d, want %d (all the bytes fed)", got[3].end, len(raw))
	}
}

// TestClosedByEndOfInputIsNotATruncationVerdict pins what the doc promises: a
// complete live stream ends with its unknown-size Segment closed this way, so a
// consumer reading ClosedByEndOfInput as "bytes were lost" would be wrong on every
// well-formed capture. Whether anything was lost is the error's question.
func TestClosedByEndOfInputIsNotATruncationVerdict(t *testing.T) {
	raw := ebmltest.Encode(ebmltest.UnknownMaster(crSegment,
		ebmltest.Master(crCluster, ebmltest.Leaf(crLeaf, []byte{0x01}))))

	c := NewCursor(crClassifier, WithBoundary(crBoundary))
	c.Feed(raw)
	if err := c.Finalize(); err != nil {
		t.Fatalf("Finalize of a COMPLETE stream reported %v; the input is not truncated", err)
	}
	var sawSegmentEnd bool
	for {
		n, err := c.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("Next: %v", err)
		}
		if end, ok := n.(*EndNode); ok && end.ID() == crSegment {
			sawSegmentEnd = true
			if end.Reason() != ClosedByEndOfInput {
				t.Errorf("Segment closed by %s, want %s", end.Reason(), ClosedByEndOfInput)
			}
		}
	}
	if !sawSegmentEnd {
		t.Fatal("the Segment never reported an end")
	}
}

// TestCloseReasonStringsAreDistinct keeps the three from collapsing into one
// another in a log, which is the use the reason exists for.
func TestCloseReasonStringsAreDistinct(t *testing.T) {
	seen := map[string]CloseReason{}
	for _, r := range []CloseReason{ClosedByDeclaredEnd, ClosedByBoundary, ClosedByEndOfInput} {
		s := r.String()
		if s == "" {
			t.Errorf("CloseReason(%d) has no String", uint8(r))
		}
		if prev, dup := seen[s]; dup {
			t.Errorf("CloseReason(%d) and CloseReason(%d) both print %q", uint8(prev), uint8(r), s)
		}
		seen[s] = r
	}
}
