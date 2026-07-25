package parser

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"testing"
)

// Element IDs of the KVS fixture topology, spelled out here because package
// parser holds no element registry (see classifier_test.go).
const (
	curIDEBMLHeader  ElementID = 0x1A45DFA3
	curIDSegment     ElementID = 0x18538067
	curIDTags        ElementID = 0x1254C367
	curIDCluster     ElementID = 0x1F43B675
	curIDTagString   ElementID = 0x4487
	curIDSimpleBlock ElementID = 0xA3
)

// cursorTopologyBasicEvents is the complete event sequence of
// fixtures/kvs/topology_basic with every master descended into and no leaf payload
// asked for -- that is, with every decision left to the cursor's lazy defaults.
// Extents are contiguous: each element's end offset is the next sibling's start
// offset, and a master's children exactly fill its declared extent.
//
// The unknown-size Segment reports an unknown extent on its header event and a
// concrete end (the stream length, 696) when EOF closes it, while the known-size
// Cluster inside it reports its end as soon as its declared payload is consumed.
const cursorTopologyBasicEvents = `
master 0x1A45DFA3 d0 @0 h5 s19 e24
leaf 0x4286 d1 @5 h3 s1 e9
leaf 0x42F7 d1 @9 h3 s1 e13
leaf 0x4282 d1 @13 h3 s8 e24
end 0x1A45DFA3 d0 @0 h5 s19 e24
master 0x18538067 d0 @24 h12 sunknown eunknown
master 0x1549A966 d1 @36 h5 s26 e67
leaf 0x73A4 d2 @41 h3 s16 e60
leaf 0x2AD7B1 d2 @60 h4 s3 e67
end 0x1549A966 d1 @36 h5 s26 e67
master 0x1654AE6B d1 @67 h5 s126 e198
master 0xAE d2 @72 h2 s62 e136
leaf 0xD7 d3 @74 h2 s1 e77
leaf 0x83 d3 @77 h2 s1 e80
leaf 0x86 d3 @80 h2 s13 e95
leaf 0x536E d3 @95 h3 s19 e117
master 0xE1 d3 @117 h2 s17 e136
leaf 0xB5 d4 @119 h2 s8 e129
leaf 0x9F d4 @129 h2 s1 e132
leaf 0x6264 d4 @132 h3 s1 e136
end 0xE1 d3 @117 h2 s17 e136
end 0xAE d2 @72 h2 s62 e136
master 0xAE d2 @136 h2 s60 e198
leaf 0xD7 d3 @138 h2 s1 e141
leaf 0x83 d3 @141 h2 s1 e144
leaf 0x86 d3 @144 h2 s13 e159
leaf 0x536E d3 @159 h3 s17 e179
master 0xE1 d3 @179 h2 s17 e198
leaf 0xB5 d4 @181 h2 s8 e191
leaf 0x9F d4 @191 h2 s1 e194
leaf 0x6264 d4 @194 h3 s1 e198
end 0xE1 d3 @179 h2 s17 e198
end 0xAE d2 @136 h2 s60 e198
end 0x1654AE6B d1 @67 h5 s126 e198
master 0x1254C367 d1 @198 h6 s370 e574
master 0x7373 d2 @204 h4 s366 e574
master 0x63C0 d3 @208 h3 s0 e211
end 0x63C0 d3 @208 h3 s0 e211
master 0x67C8 d3 @211 h3 s55 e269
leaf 0x45A3 d4 @214 h3 s35 e252
leaf 0x4487 d4 @252 h3 s14 e269
end 0x67C8 d3 @211 h3 s55 e269
master 0x67C8 d3 @269 h3 s85 e357
leaf 0x45A3 d4 @272 h3 s32 e307
leaf 0x4487 d4 @307 h3 s47 e357
end 0x67C8 d3 @269 h3 s85 e357
master 0x67C8 d3 @357 h3 s105 e465
leaf 0x45A3 d4 @360 h3 s35 e398
leaf 0x4487 d4 @398 h3 s64 e465
end 0x67C8 d3 @357 h3 s105 e465
master 0x67C8 d3 @465 h3 s51 e519
leaf 0x45A3 d4 @468 h3 s9 e480
leaf 0x4487 d4 @480 h3 s36 e519
end 0x67C8 d3 @465 h3 s51 e519
master 0x67C8 d3 @519 h3 s52 e574
leaf 0x45A3 d4 @522 h3 s10 e535
leaf 0x4487 d4 @535 h3 s36 e574
end 0x67C8 d3 @519 h3 s52 e574
end 0x7373 d2 @204 h4 s366 e574
end 0x1254C367 d1 @198 h6 s370 e574
master 0x1F43B675 d1 @574 h5 s117 e696
leaf 0xE7 d2 @579 h2 s1 e582
leaf 0xA3 d2 @582 h2 s36 e620
leaf 0xA3 d2 @620 h2 s36 e658
leaf 0xA3 d2 @658 h2 s36 e696
end 0x1F43B675 d1 @574 h5 s117 e696
end 0x18538067 d0 @24 h12 sunknown e696
`

// cursorLine renders one event as text -- which variant it is, plus identity and
// extent -- so a whole event sequence can be compared as lines.
func cursorLine(n Node) string {
	kind := "?"
	switch n.(type) {
	case *MasterNode:
		kind = "master"
	case *LeafNode:
		kind = "leaf"
	case *EndNode:
		kind = "end"
	}
	size, end := fmt.Sprint(n.Size()), fmt.Sprint(n.End())
	if n.Size() == UnknownSize {
		size = "unknown"
	}
	if n.End() == UnknownSize {
		end = "unknown"
	}
	return fmt.Sprintf("%s %s d%d @%d h%d s%s e%s",
		kind, n.ID(), n.Depth(), n.Offset(), n.HeaderLen(), size, end)
}

func cursorWantLines(s string) []string {
	return strings.Split(strings.TrimSpace(s), "\n")
}

func assertCursorLines(t *testing.T, got, want []string) {
	t.Helper()
	for i := 0; i < len(got) && i < len(want); i++ {
		if got[i] != want[i] {
			t.Fatalf("event %d:\n got: %s\nwant: %s", i, got[i], want[i])
		}
	}
	if len(got) != len(want) {
		t.Fatalf("got %d events, want %d\n--- got ---\n%s\n--- want ---\n%s",
			len(got), len(want), strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}

func isNeedMore(err error) bool {
	var needMore NeedMoreData
	return errors.As(err, &needMore)
}

// driveCursor pulls every event of raw, fed in chunk-sized pieces, and renders one
// line per event. decide is called on each node before the next pull, so a test can
// exercise Skip/Payload; a nil decide leaves every decision to the lazy defaults. Its
// feed argument pushes the next chunk, which is how a decision that reports
// NeedMoreData (a Payload call) is retried.
func driveCursor(t *testing.T, raw []byte, chunk int, decide func(t *testing.T, n Node, feed func() bool), opts ...Option) ([]string, *Cursor) {
	t.Helper()
	c := NewCursor(testKindClassifier, opts...)
	var lines []string
	pos := 0
	finalized := false
	feed := func() bool {
		if pos >= len(raw) {
			return false
		}
		end := min(pos+chunk, len(raw))
		c.Feed(raw[pos:end])
		pos = end
		return true
	}
	for {
		n, err := c.Next()
		if err != nil {
			switch {
			case isNeedMore(err):
				if feed() {
					continue
				}
				if finalized {
					t.Fatalf("Next reported NeedMoreData after Finalize at offset %d", c.Offset())
				}
				finalized = true
				if err := c.Finalize(); err != nil {
					t.Fatalf("Finalize: %v", err)
				}
				continue
			case errors.Is(err, io.EOF):
				if !finalized {
					t.Fatal("Next reported io.EOF before Finalize")
				}
				return lines, c
			default:
				t.Fatalf("Next at offset %d: %v", c.Offset(), err)
			}
		}
		lines = append(lines, cursorLine(n))
		if decide != nil {
			decide(t, n, feed)
		}
	}
}

// TestCursorEventSequenceIsSplitInvariant checks the whole event sequence over a
// committed fixture and, feeding the same bytes in 1-, 7- and 4096-byte chunks,
// that it does not depend on how the stream was chunked.
func TestCursorEventSequenceIsSplitInvariant(t *testing.T) {
	raw := loadHexFixture(t, "fixtures/kvs/known_size_cluster.ebml.hex")
	want := cursorWantLines(cursorTopologyBasicEvents)

	for _, chunk := range []int{1, 7, 4096} {
		t.Run(fmt.Sprintf("chunk_%d", chunk), func(t *testing.T) {
			got, c := driveCursor(t, raw, chunk, nil)
			assertCursorLines(t, got, want)
			if depth := c.Depth(); depth != 0 {
				t.Fatalf("Depth after Finalize = %d, want 0", depth)
			}
			if got, want := c.Offset(), int64(len(raw)); got != want {
				t.Fatalf("Offset after Finalize = %d, want %d", got, want)
			}
			if err := c.Err(); !errors.Is(err, io.EOF) {
				t.Fatalf("Err after the stream ended = %v, want io.EOF", err)
			}
		})
	}
}

// TestCursorNodeKindReportsTheClassifierVerdict checks the field spec/SPEC.md
// section 3 requires on every event: a node reports the KIND the classifier
// returned for its element, so a consumer sees the verdict the cursor read the
// element by instead of only which node variant it was handed.
//
// The distinction that matters is among the LEAF kinds. The variant alone already
// says "leaf", so a Kind that collapsed every non-master verdict into one value
// would tell the consumer nothing it did not already know -- hence the requirement
// below that two different leaf kinds actually be observed.
func TestCursorNodeKindReportsTheClassifierVerdict(t *testing.T) {
	raw := loadHexFixture(t, "fixtures/kvs/topology_basic.ebml.hex")

	c := NewCursor(testKindClassifier)
	c.Feed(raw)
	seen := map[Kind]int{}
	finalized := false
	for {
		n, err := c.Next()
		if err != nil {
			if isNeedMore(err) {
				if finalized {
					t.Fatal("NeedMoreData after Finalize")
				}
				finalized = true
				if err := c.Finalize(); err != nil {
					t.Fatalf("Finalize: %v", err)
				}
				continue
			}
			if errors.Is(err, io.EOF) {
				break
			}
			t.Fatalf("Next at offset %d: %v", c.Offset(), err)
		}

		got := n.Kind()
		seen[got]++
		switch n.(type) {
		case *EndNode:
			// The kind of the observation: the master ended here.
			if got != KindEndMaster {
				t.Fatalf("EndNode for %s reports kind %s, want %s", n.ID(), got, KindEndMaster)
			}
			continue
		case *MasterNode:
			if got != KindMaster {
				t.Fatalf("MasterNode %s reports kind %s, want %s", n.ID(), got, KindMaster)
			}
		case *LeafNode:
			if got == KindMaster {
				t.Fatalf("LeafNode %s reports kind %s", n.ID(), got)
			}
		}
		if want := testKindClassifier(n.ID()); got != want {
			t.Fatalf("node %s reports kind %s, want the classifier's %s", n.ID(), got, want)
		}
	}

	for _, want := range []Kind{KindMaster, KindEndMaster, KindUint, KindBinary} {
		if seen[want] == 0 {
			t.Fatalf("no event reported kind %s, so the verdict was not observed: %v", want, seen)
		}
	}
}

// endSnapshot is what a test keeps from an EndNode. Every node method rejects a
// stale node -- and a node COPY carries the generation it was copied in -- so what
// outlives the event has to be the values, not the node.
type endSnapshot struct {
	id        ElementID
	depth     int
	offset    int64
	start     int64
	headerLen int
	size      int64
	end       int64
}

func snapshotEnd(n *EndNode) endSnapshot {
	return endSnapshot{
		id:        n.ID(),
		depth:     n.Depth(),
		offset:    n.Offset(),
		start:     n.Start(),
		headerLen: n.HeaderLen(),
		size:      n.Size(),
		end:       n.End(),
	}
}

// TestCursorKnownSizeEndBeforeUnknownSizeParentCloses is the property this library
// exists for: the Cluster's end is COMPUTED from its declared size, so it is
// reported while the unknown-size Segment holding it is still open -- the last
// fragment of a live stream need not wait for the connection to close. The Segment
// itself reports an unknown extent on its header and a concrete one only when
// Finalize closes it.
func TestCursorKnownSizeEndBeforeUnknownSizeParentCloses(t *testing.T) {
	raw := loadHexFixture(t, "fixtures/kvs/known_size_cluster.ebml.hex")
	c := NewCursor(testKindClassifier)
	c.Feed(raw)

	var clusterEnd, segmentEnd *endSnapshot
	var clusterEndDepth int
	finalized := false
	for {
		n, err := c.Next()
		if err != nil {
			if isNeedMore(err) {
				if finalized {
					t.Fatal("NeedMoreData after Finalize")
				}
				finalized = true
				if clusterEnd == nil {
					t.Fatal("the Cluster end was not reported before Finalize")
				}
				if err := c.Finalize(); err != nil {
					t.Fatalf("Finalize: %v", err)
				}
				continue
			}
			if errors.Is(err, io.EOF) {
				break
			}
			t.Fatalf("Next: %v", err)
		}
		switch node := n.(type) {
		case *MasterNode:
			if node.ID() == curIDSegment {
				if node.Size() != UnknownSize {
					t.Fatalf("Segment Size = %d, want UnknownSize", node.Size())
				}
				if node.End() != UnknownSize {
					t.Fatalf("Segment End = %d, want UnknownSize", node.End())
				}
			}
		case *EndNode:
			// A node -- and a copy of one -- is valid only until the next Next, so
			// what the loop wants is read off it here.
			switch node.ID() {
			case curIDCluster:
				snap := snapshotEnd(node)
				clusterEnd, clusterEndDepth = &snap, c.Depth()
			case curIDSegment:
				snap := snapshotEnd(node)
				segmentEnd = &snap
			}
		}
	}

	if clusterEnd == nil || segmentEnd == nil {
		t.Fatalf("ends observed: cluster=%v segment=%v", clusterEnd, segmentEnd)
	}
	if clusterEnd.size == UnknownSize {
		t.Fatal("the Cluster declares a size; its EndNode reported UnknownSize")
	}
	if got, want := clusterEnd.end, clusterEnd.start+int64(clusterEnd.headerLen)+clusterEnd.size; got != want {
		t.Fatalf("Cluster end offset = %d, want its declared end %d", got, want)
	}
	// The Segment was still open when the Cluster's end was reported.
	if clusterEndDepth != 1 {
		t.Fatalf("depth at the Cluster end = %d, want 1 (the Segment still open)", clusterEndDepth)
	}
	if segmentEnd.size != UnknownSize {
		t.Fatalf("Segment EndNode Size = %d, want UnknownSize", segmentEnd.size)
	}
	if got, want := segmentEnd.end, int64(len(raw)); got != want {
		t.Fatalf("Segment closed at %d, want the stream length %d", got, want)
	}
	if got := segmentEnd.start; got != segmentEnd.offset {
		t.Fatalf("EndNode Start = %d, Offset = %d; they name the same byte", got, segmentEnd.offset)
	}
	if depth := c.Depth(); depth != 0 {
		t.Fatalf("Depth after Finalize = %d, want 0", depth)
	}
}

// TestCursorSkipSubtreeLeavesFollowingEventsUntouched skips the Tags subtree and
// checks that the events before and after it are exactly the ones of an untouched
// scan: a skipped master reports no descendant and no end of its own, and nothing
// else moves.
func TestCursorSkipSubtreeLeavesFollowingEventsUntouched(t *testing.T) {
	raw := loadHexFixture(t, "fixtures/kvs/known_size_cluster.ebml.hex")

	// The expectation is the full sequence minus the skipped subtree: its master
	// header stays, its descendants and its own end go.
	full := cursorWantLines(cursorTopologyBasicEvents)
	masterLine := fmt.Sprintf("master %s ", curIDTags)
	endLine := fmt.Sprintf("end %s ", curIDTags)
	var want []string
	skipping := false
	for _, line := range full {
		switch {
		case strings.HasPrefix(line, masterLine):
			want = append(want, line)
			skipping = true
		case skipping && strings.HasPrefix(line, endLine):
			skipping = false
		case skipping:
		default:
			want = append(want, line)
		}
	}
	if len(want) == len(full) {
		t.Fatal("the expectation was not filtered; the fixture no longer has a Tags subtree")
	}

	for _, chunk := range []int{1, 7, 4096} {
		t.Run(fmt.Sprintf("chunk_%d", chunk), func(t *testing.T) {
			got, _ := driveCursor(t, raw, chunk, func(t *testing.T, n Node, _ func() bool) {
				if m, ok := n.(*MasterNode); ok && m.ID() == curIDTags {
					m.Skip()
				}
			})
			assertCursorLines(t, got, want)
		})
	}
}

// TestCursorPayloadIsPayForWhatYouUse checks the leaf half of the lazy defaults: a
// leaf nobody touches has its payload skipped, and asking for one returns exactly
// the element's declared payload bytes. Asking twice returns the same bytes rather
// than reading the stream a second time.
//
// That the untouched payloads are never materialised at all is asserted by
// TestCursorScanIsAllocationFree, which scans this fixture without a single
// allocation per event.
func TestCursorPayloadIsPayForWhatYouUse(t *testing.T) {
	raw := loadHexFixture(t, "fixtures/kvs/known_size_cluster.ebml.hex")
	want := cursorWantLines(cursorTopologyBasicEvents)

	for _, chunk := range []int{1, 7, 4096} {
		t.Run(fmt.Sprintf("chunk_%d", chunk), func(t *testing.T) {
			reads := 0
			got, _ := driveCursor(t, raw, chunk, func(t *testing.T, n Node, feed func() bool) {
				leaf, ok := n.(*LeafNode)
				if !ok || leaf.ID() != curIDTagString {
					return
				}
				// The node stays valid across Feed, so a payload that has not
				// arrived yet is simply retried.
				payload, err := leaf.Payload()
				for isNeedMore(err) {
					if !feed() {
						t.Fatalf("Payload of %s still needs data at end of input", leaf.ID())
					}
					payload, err = leaf.Payload()
				}
				if err != nil {
					t.Fatalf("Payload of %s at %d: %v", leaf.ID(), leaf.Offset(), err)
				}
				start := leaf.Offset() + int64(leaf.HeaderLen())
				if wantBytes := raw[start:leaf.End()]; !bytes.Equal(payload, wantBytes) {
					t.Fatalf("Payload at %d = %x, want %x", leaf.Offset(), payload, wantBytes)
				}
				again, err := leaf.Payload()
				if err != nil {
					t.Fatalf("second Payload of %s: %v", leaf.ID(), err)
				}
				if len(again) == 0 || &again[0] != &payload[0] {
					t.Fatal("second Payload read the stream again instead of returning the same bytes")
				}
				reads++
			})
			// Materialising payloads must not change the event sequence.
			assertCursorLines(t, got, want)
			if reads != 5 {
				t.Fatalf("payloads read = %d, want the fixture's 5 TagString leaves", reads)
			}
		})
	}
}

// TestCursorPayloadSurvivesNeedMoreDataRetry feeds one byte at a time and reads
// every leaf payload through the documented retry loop: a leaf is reported on its
// header, so Payload may report NeedMoreData, and the node stays valid across Feed
// until the bytes are there.
func TestCursorPayloadSurvivesNeedMoreDataRetry(t *testing.T) {
	raw := loadHexFixture(t, "fixtures/kvs/known_size_cluster.ebml.hex")
	c := NewCursor(testKindClassifier)
	pos := 0
	feed := func() bool {
		if pos >= len(raw) {
			return false
		}
		c.Feed(raw[pos : pos+1])
		pos++
		return true
	}

	var lines []string
	retries := 0
	finalized := false
	for {
		n, err := c.Next()
		if err != nil {
			if isNeedMore(err) {
				if feed() {
					continue
				}
				if finalized {
					t.Fatal("NeedMoreData after Finalize")
				}
				finalized = true
				if err := c.Finalize(); err != nil {
					t.Fatalf("Finalize: %v", err)
				}
				continue
			}
			if errors.Is(err, io.EOF) {
				break
			}
			t.Fatalf("Next: %v", err)
		}
		lines = append(lines, cursorLine(n))

		leaf, ok := n.(*LeafNode)
		if !ok {
			continue
		}
		for {
			payload, err := leaf.Payload()
			if err == nil {
				start := leaf.Offset() + int64(leaf.HeaderLen())
				if wantBytes := raw[start:leaf.End()]; !bytes.Equal(payload, wantBytes) {
					t.Fatalf("Payload at %d = %x, want %x", leaf.Offset(), payload, wantBytes)
				}
				break
			}
			if !isNeedMore(err) {
				t.Fatalf("Payload at %d: %v", leaf.Offset(), err)
			}
			retries++
			if !feed() {
				t.Fatalf("Payload at %d still needs data at end of input", leaf.Offset())
			}
		}
	}

	assertCursorLines(t, lines, cursorWantLines(cursorTopologyBasicEvents))
	if retries == 0 {
		t.Fatal("no Payload call needed a retry; the byte-by-byte feed was not exercised")
	}
}

// TestCursorBoundaryClosesUnknownSizeMaster drives a stream of concatenated
// unknown-size Segments. The boundary rule is cursor-wide policy, so every Segment
// but the last closes structurally, on the header of the next fragment's EBML
// element; without the rule the Segments nest and only EOF closes them.
func TestCursorBoundaryClosesUnknownSizeMaster(t *testing.T) {
	raw := loadHexFixture(t, "fixtures/kvs/multi_segment.ebml.hex")

	countSegmentEnds := func(t *testing.T, chunk int, opts ...Option) (ends, maxDepth int) {
		t.Helper()
		lines, _ := driveCursor(t, raw, chunk, func(t *testing.T, n Node, _ func() bool) {
			if d := n.Depth(); d > maxDepth {
				maxDepth = d
			}
		}, opts...)
		for _, line := range lines {
			if strings.HasPrefix(line, fmt.Sprintf("end %s ", curIDSegment)) {
				ends++
			}
		}
		return ends, maxDepth
	}

	for _, chunk := range []int{1, 7, 4096} {
		t.Run(fmt.Sprintf("chunk_%d", chunk), func(t *testing.T) {
			var asked []ElementID
			boundary := WithBoundary(func(open, next ElementID) bool {
				if open != curIDSegment && open != curIDCluster {
					t.Fatalf("boundary asked about %s, want Segment or Cluster", open)
				}
				asked = append(asked, next)
				return next == curIDEBMLHeader || next == curIDSegment
			})
			ends, maxDepth := countSegmentEnds(t, chunk, boundary)
			if ends != 4 {
				t.Fatalf("Segment ends with a boundary rule = %d, want the fixture's 4", ends)
			}
			if len(asked) == 0 {
				t.Fatal("the boundary rule was never consulted")
			}
			withRule := maxDepth

			ends, maxDepth = countSegmentEnds(t, chunk)
			if ends != 4 {
				t.Fatalf("Segment ends without a boundary rule = %d, want 4 (all closed at EOF)", ends)
			}
			if maxDepth <= withRule {
				t.Fatalf("max depth without a boundary rule = %d, want deeper than %d: the Segments should nest", maxDepth, withRule)
			}
		})
	}
}

// TestCursorFinalizeBeforeDrainingKeepsEvents checks the documented deferred path:
// Finalize on a cursor whose buffered bytes still complete events does not close
// anything yet, because closing would discard events the consumer has not seen. The
// events are reported first, and the masters close when Next reaches the end of the
// bytes.
func TestCursorFinalizeBeforeDrainingKeepsEvents(t *testing.T) {
	raw := loadHexFixture(t, "fixtures/kvs/known_size_cluster.ebml.hex")
	c := NewCursor(testKindClassifier)
	c.Feed(raw)
	if err := c.Finalize(); err != nil {
		t.Fatalf("Finalize before pulling any event: %v", err)
	}

	var lines []string
	for {
		n, err := c.Next()
		if err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			t.Fatalf("Next: %v", err)
		}
		lines = append(lines, cursorLine(n))
	}
	assertCursorLines(t, lines, cursorWantLines(cursorTopologyBasicEvents))
	if depth := c.Depth(); depth != 0 {
		t.Fatalf("Depth after Finalize = %d, want 0", depth)
	}
}

// TestCursorFinalizeReportsTruncation checks that a stream ending inside a declared
// payload is reported as a structural TruncatedError rather than accepted.
func TestCursorFinalizeReportsTruncation(t *testing.T) {
	raw := loadHexFixture(t, "fixtures/kvs/topology_basic.ebml.hex")
	c := NewCursor(testKindClassifier)
	c.Feed(raw[:len(raw)-5])

	for {
		_, err := c.Next()
		if err == nil {
			continue
		}
		if !isNeedMore(err) {
			t.Fatalf("Next: %v", err)
		}
		break
	}
	err := c.Finalize()
	var truncated TruncatedError
	if !errors.As(err, &truncated) {
		t.Fatalf("Finalize of a truncated stream = %v, want TruncatedError", err)
	}
	if !IsStructural(err) {
		t.Fatalf("IsStructural(%v) = false, want true", err)
	}
	if _, next := c.Next(); !errors.Is(next, err) {
		t.Fatalf("Next after a structural failure = %v, want the same failure", next)
	}
}

// TestCursorHeadersMatchCommittedGolden ties the pull API to the conformance
// corpus: every element header the cursor reports must be the element the golden
// op-trace peeked, with the same offset, depth, declared size and header length.
// The goldens are recorded from the low-level Parser, so this is what keeps the two
// layers from drifting apart.
func TestCursorHeadersMatchCommittedGolden(t *testing.T) {
	got, _ := driveCursor(t, loadHexFixture(t, "fixtures/kvs/known_size_cluster.ebml.hex"), 4096, nil)

	var want []logEvent
	for _, line := range strings.Split(string(loadGolden(t, "golden/kvs/known_size_cluster.jsonl")), "\n") {
		var ev logEvent
		if err := json.Unmarshal([]byte(line), &ev); err != nil {
			t.Fatalf("decode golden line %q: %v", line, err)
		}
		if ev.Op == "peek" && ev.ID != "" {
			want = append(want, ev)
		}
	}

	// The golden records element headers only; a master's end is a "leave" op there
	// and an EndNode here, so the header events are what the two have in common.
	var headers []string
	for _, line := range got {
		if !strings.HasPrefix(line, "end ") {
			headers = append(headers, line)
		}
	}
	if len(headers) != len(want) {
		t.Fatalf("cursor reported %d element headers, golden has %d", len(headers), len(want))
	}
	for i, w := range want {
		kind := "leaf"
		if w.Kind == string(KindMaster) {
			kind = "master"
		}
		size, end := fmt.Sprint(*w.Size), fmt.Sprint(w.Offset+int64(w.HeaderLen)+*w.Size)
		if *w.Size == UnknownSize {
			size, end = "unknown", "unknown"
		}
		// The golden records IDs as bare hex, which is what FormatID renders, so the
		// comparison goes through the cursor's own line minus its "0x" notation.
		gotLine := strings.Replace(headers[i], " 0x", " ", 1)
		wantLine := fmt.Sprintf("%s %s d%d @%d h%d s%s e%s",
			kind, w.ID, w.Depth, w.Offset, w.HeaderLen, size, end)
		if !strings.EqualFold(gotLine, wantLine) {
			t.Fatalf("header %d:\n got: %s\nwant: %s (from the golden)", i, gotLine, wantLine)
		}
	}
}

// TestCursorUnconsumedAndStartOffsetResumeTheStream covers what post-failure
// recovery is built on (see ext/fragment.WithResync): the bytes a cursor had not
// consumed are recoverable, and a fresh cursor told where it starts reports the
// ORIGINAL stream's offsets rather than counting from zero again.
func TestCursorUnconsumedAndStartOffsetResumeTheStream(t *testing.T) {
	raw := loadHexFixture(t, "fixtures/kvs/topology_basic.ebml.hex")

	// Stop pulling at the Cluster's header, leaving the rest of the stream
	// unconsumed -- exactly the state a failed cursor is abandoned in.
	c := NewCursor(testKindClassifier)
	c.Feed(raw)
	var clusterOffset int64
	for {
		n, err := c.Next()
		if err != nil {
			t.Fatalf("Next: %v", err)
		}
		if m, ok := n.(*MasterNode); ok && m.ID() == curIDCluster {
			clusterOffset = m.Offset()
			break
		}
	}
	// The header has been consumed, so the cursor sits at the Cluster's payload; what
	// recovery needs is exactly the bytes it has not consumed yet.
	rest := c.Unconsumed()
	if want := raw[c.Offset():]; !bytes.Equal(rest, want) {
		t.Fatalf("Unconsumed() returned %d bytes, want the %d from offset %d on", len(rest), len(want), c.Offset())
	}
	rest[0] ^= 0xFF
	if again := c.Unconsumed(); again[0] == rest[0] {
		t.Fatal("Unconsumed() must return a copy, not the cursor's own buffer")
	}

	// A fresh cursor over the bytes from the Cluster header on reports the original
	// stream's offsets.
	resumed := NewCursor(testKindClassifier, WithStartOffset(clusterOffset))
	resumed.Feed(raw[clusterOffset:])
	first, err := resumed.Next()
	if err != nil {
		t.Fatalf("resumed Next: %v", err)
	}
	if first.ID() != curIDCluster || first.Offset() != clusterOffset {
		t.Fatalf("resumed scan starts at %s @%d, want the Cluster @%d", first.ID(), first.Offset(), clusterOffset)
	}
	if err := drainCursor(resumed); err != nil {
		t.Fatalf("resumed drain: %v", err)
	}
	if got := resumed.Offset(); got != int64(len(raw)) {
		t.Fatalf("resumed Offset at EOF = %d, want the stream length %d", got, len(raw))
	}
	if got := resumed.Unconsumed(); got != nil {
		t.Fatalf("Unconsumed() after a complete scan = %x, want nil", got)
	}
}

// TestCursorFeedAfterFinalizeIsRejected checks that a stream declared over cannot be
// continued: Finalize is idempotent, and feeding after it is a programmer error the
// next pull reports.
func TestCursorFeedAfterFinalizeIsRejected(t *testing.T) {
	raw := loadHexFixture(t, "fixtures/kvs/topology_basic.ebml.hex")
	c := NewCursor(testKindClassifier)
	c.Feed(raw)
	if err := drainCursor(c); err != nil {
		t.Fatalf("drain: %v", err)
	}
	if err := c.Finalize(); err != nil {
		t.Fatalf("second Finalize = %v, want nil (idempotent)", err)
	}

	c.Feed([]byte{0xEC, 0x80})
	_, err := c.Next()
	var invalid Invalid
	if !errors.As(err, &invalid) {
		t.Fatalf("Next after feeding a finalized cursor = %v, want Invalid", err)
	}
	if !IsStructural(err) {
		t.Fatalf("IsStructural(%v) = false, want true", err)
	}
}

func wantPanic(t *testing.T, what string, f func()) {
	t.Helper()
	defer func() {
		r := recover()
		if r == nil {
			t.Fatalf("%s did not panic", what)
		}
		msg, ok := r.(string)
		if !ok {
			t.Fatalf("%s panicked with %T, want a string message", what, r)
		}
		if !strings.HasPrefix(msg, "parser: ") {
			t.Fatalf("%s panic message = %q, want a parser-prefixed message", what, msg)
		}
	}()
	f()
}

// TestCursorStaleNodeCallPanics checks that acting on a node the cursor has moved
// past, or deciding twice on one node, fails loudly instead of silently applying to
// the wrong element. Both are programmer errors no input can repair.
func TestCursorStaleNodeCallPanics(t *testing.T) {
	raw := loadHexFixture(t, "fixtures/kvs/topology_basic.ebml.hex")

	t.Run("descend_after_next", func(t *testing.T) {
		c := NewCursor(testKindClassifier)
		c.Feed(raw)
		n, err := c.Next()
		if err != nil {
			t.Fatalf("Next: %v", err)
		}
		master, ok := n.(*MasterNode)
		if !ok {
			t.Fatalf("first event is %T, want *MasterNode", n)
		}
		if _, err := c.Next(); err != nil { // the master is descended into; a leaf follows
			t.Fatalf("Next: %v", err)
		}
		wantPanic(t, "Descend on a stale MasterNode", master.Descend)
		wantPanic(t, "Skip on a stale MasterNode", master.Skip)
	})

	t.Run("payload_after_next", func(t *testing.T) {
		c := NewCursor(testKindClassifier)
		c.Feed(raw)
		// Walk to the last leaf of the EBML header, whose next event is that
		// master's end: the cursor then no longer holds a live leaf, which is
		// what makes the stale call detectable.
		var leaf *LeafNode
		for {
			n, err := c.Next()
			if err != nil {
				t.Fatalf("Next: %v", err)
			}
			if l, ok := n.(*LeafNode); ok {
				leaf = l
				continue
			}
			if _, ok := n.(*EndNode); ok {
				break
			}
		}
		if leaf == nil {
			t.Fatal("no leaf was reported before the first end")
		}
		wantPanic(t, "Payload on a stale LeafNode", func() { _, _ = leaf.Payload() })
		wantPanic(t, "Skip on a stale LeafNode", leaf.Skip)
	})

	t.Run("two_decisions", func(t *testing.T) {
		c := NewCursor(testKindClassifier)
		c.Feed(raw)
		n, err := c.Next()
		if err != nil {
			t.Fatalf("Next: %v", err)
		}
		master := n.(*MasterNode)
		master.Descend()
		wantPanic(t, "a second decision on a MasterNode", master.Skip)
	})

	t.Run("payload_after_skip", func(t *testing.T) {
		c := NewCursor(testKindClassifier)
		c.Feed(raw)
		var leaf *LeafNode
		for leaf == nil {
			n, err := c.Next()
			if err != nil {
				t.Fatalf("Next: %v", err)
			}
			leaf, _ = n.(*LeafNode)
		}
		leaf.Skip()
		wantPanic(t, "Payload after Skip", func() { _, _ = leaf.Payload() })
	})
}

// TestCursorScanAllocatesOneNodePerEvent is the measured price of the node validity
// guarantee: the cursor allocates a NEW node for every event -- which is what leaves
// an abandoned node's generation stamp behind, so that every retention of it is
// caught -- and it allocates nothing else, so a scan that materialises no payload
// costs exactly one object per event. BenchmarkCursorScan and BenchmarkParserScan
// put that number against the low-level Parser, which hands out no node at all.
func TestCursorScanAllocatesOneNodePerEvent(t *testing.T) {
	raw := loadHexFixture(t, "fixtures/kvs/topology_basic.ebml.hex")
	c := warmCursor(t, raw)

	var last Node
	allocs := testing.AllocsPerRun(30, func() {
		n, err := c.Next()
		if err != nil {
			t.Fatalf("Next: %v", err)
		}
		last = n
	})
	if last == nil {
		t.Fatal("no event was pulled")
	}
	if allocs != allocsPerEvent {
		t.Fatalf("allocations per event = %v, want %d (the node the event hands out)", allocs, allocsPerEvent)
	}
}

// scanWithCursor pulls every event of a fully buffered stream through a Cursor and
// reports how many it saw. It is the shape a consumer writes.
func scanWithCursor(raw []byte) (int, error) {
	c := NewCursor(testKindClassifier)
	c.Feed(raw)
	events, finalized := 0, false
	for {
		n, err := c.Next()
		if err != nil {
			switch {
			case isNeedMore(err):
				if finalized {
					return events, errors.New("NeedMoreData after Finalize")
				}
				finalized = true
				if err := c.Finalize(); err != nil {
					return events, err
				}
			case errors.Is(err, io.EOF):
				return events, nil
			default:
				return events, err
			}
			continue
		}
		if n.ID() == 0 {
			return events, errors.New("event with a zero element ID")
		}
		events++
	}
}

// scanWithParser is the same scan driven by the low-level engine: Peek reports an
// ElementHeader BY VALUE and the loop keeps the flow control and the master
// bookkeeping itself, so no node is handed out and the Cursor's per-event node is not
// paid for. The event count must match scanWithCursor's -- header events plus one end
// event per entered master, including the ones FinalizeEOF closes -- or the two
// benchmarks would not be comparing the same work.
func scanWithParser(raw []byte) (int, error) {
	p := New(testKindClassifier)
	p.Feed(raw)
	events := 0
	for {
		h, err := p.Peek()
		if err != nil {
			if isNeedMore(err) {
				break
			}
			return events, err
		}
		if h.Kind == KindEndMaster {
			if err := p.LeaveMaster(); err != nil {
				return events, err
			}
			events++
			continue
		}
		if _, err := p.ConsumeHeader(); err != nil {
			return events, err
		}
		events++
		if h.Kind == KindMaster {
			if err := p.EnterMaster(); err != nil {
				return events, err
			}
			continue
		}
		if err := p.SkipPayload(); err != nil {
			return events, err
		}
	}
	closed, err := p.FinalizeEOF()
	if err != nil {
		return events, err
	}
	return events + len(closed), nil
}

// BenchmarkCursorScan and BenchmarkParserScan are the same full scan of the same
// fixture driven two ways, so the cost of the Cursor's freshness guarantee is on the
// record rather than argued about: the Cursor allocates one node per event (see
// TestCursorScanAllocatesOneNodePerEvent) in exchange for a validity rule with no
// exception, while the Parser reports each header by value into the caller's own loop
// and allocates nothing per element -- at the price of doing the flow control and the
// open-master bookkeeping by hand. Run them together:
//
//	go test ./parser/ -run '^$' -bench Scan -benchmem
func BenchmarkCursorScan(b *testing.B) {
	benchmarkScan(b, scanWithCursor)
}

func BenchmarkParserScan(b *testing.B) {
	benchmarkScan(b, scanWithParser)
}

func benchmarkScan(b *testing.B, scan func([]byte) (int, error)) {
	raw := loadHexFixture(b, "fixtures/kvs/topology_basic.ebml.hex")
	want := len(cursorWantLines(cursorTopologyBasicEvents))
	b.SetBytes(int64(len(raw)))
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		events, err := scan(raw)
		if err != nil {
			b.Fatal(err)
		}
		if events != want {
			b.Fatalf("scan reported %d events, want the fixture's %d", events, want)
		}
	}
}

// iterateCursor collects the whole event sequence through the Nodes iterator. When
// breakAt is not negative it breaks out of the range loop right after that event
// (0-based) and resumes with a fresh range, which must not disturb the sequence.
func iterateCursor(t *testing.T, raw []byte, breakAt int) []string {
	t.Helper()
	c := NewCursor(testKindClassifier)
	c.Feed(raw)

	var lines []string
	pendingBreak := breakAt >= 0
	finalized := false
	for {
		broke := false
		for n := range c.Nodes() {
			lines = append(lines, cursorLine(n))
			if pendingBreak && len(lines) == breakAt+1 {
				pendingBreak, broke = false, true
				break
			}
		}
		if broke {
			// Breaking out is not a failure, so nothing ended iteration.
			if err := c.Err(); err != nil {
				t.Fatalf("Err after breaking out at event %d = %v, want nil", breakAt, err)
			}
			continue
		}
		err := c.Err()
		switch {
		case errors.Is(err, io.EOF):
			return lines
		case isNeedMore(err):
			if finalized {
				t.Fatal("the iterator stopped for want of data after Finalize")
			}
			finalized = true
			if err := c.Finalize(); err != nil {
				t.Fatalf("Finalize: %v", err)
			}
		default:
			t.Fatalf("Err = %v, want NeedMoreData or io.EOF", err)
		}
	}
}

// TestCursorNodesIteratorResumesAtEveryEvent is exhaustive: for every event index
// in turn it breaks out of the range loop there, resumes with a fresh range, and
// requires the concatenated sequence to equal the uninterrupted one. Breaking out
// of a range-over-func loop must therefore leave the cursor exactly where it
// stopped -- including the decision left on the node the loop broke on.
func TestCursorNodesIteratorResumesAtEveryEvent(t *testing.T) {
	raw := loadHexFixture(t, "fixtures/kvs/known_size_cluster.ebml.hex")

	full := iterateCursor(t, raw, -1)
	assertCursorLines(t, full, cursorWantLines(cursorTopologyBasicEvents))

	for k := range full {
		got := iterateCursor(t, raw, k)
		if len(got) != len(full) {
			t.Fatalf("breaking after event %d yielded %d events, want %d", k, len(got), len(full))
		}
		for i := range full {
			if got[i] != full[i] {
				t.Fatalf("breaking after event %d, event %d:\n got: %s\nwant: %s", k, i, got[i], full[i])
			}
		}
	}
}
