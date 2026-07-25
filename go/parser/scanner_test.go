package parser

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
)

// Element IDs of the KVS fixture topology, spelled out here because package
// parser holds no element registry (see classifier_test.go).
const (
	idEBMLHeader  ElementID = 0x1A45DFA3
	idSegment     ElementID = 0x18538067
	idTracks      ElementID = 0x1654AE6B
	idCluster     ElementID = 0x1F43B675
	idSimpleBlock ElementID = 0xA3
)

type scanEvent struct {
	op   string // "master", "leaf", "payload", "close"
	node Node
}

// recorder renders every scan event as one line, so a whole event sequence can
// be compared as text: identity, depth and extent of each element.
type recorder struct {
	lines    []string
	payloads int

	master   func(Node) (Action, error)
	leaf     func(Node) (Action, error)
	onClose  func(Node) error
	observed []Node      // every master/leaf node in order
	events   []scanEvent // every event in order, including closes
}

func (r *recorder) add(format string, args ...any) {
	r.lines = append(r.lines, fmt.Sprintf(format, args...))
}

func extent(n Node) string {
	size, end := fmt.Sprint(n.Size), fmt.Sprint(n.End)
	if n.Size == UnknownSize {
		size = "unknown"
	}
	if n.End == UnknownSize {
		end = "unknown"
	}
	return fmt.Sprintf("d%d @%d h%d s%s e%s", n.Depth, n.Offset, n.HeaderLen, size, end)
}

func (r *recorder) Master(n Node) (Action, error) {
	r.add("master %s %s", n.ID, extent(n))
	r.observed = append(r.observed, n)
	r.events = append(r.events, scanEvent{"master", n})
	if r.master != nil {
		return r.master(n)
	}
	return Descend, nil
}

func (r *recorder) Leaf(n Node) (Action, error) {
	r.add("leaf %s %s", n.ID, extent(n))
	r.observed = append(r.observed, n)
	r.events = append(r.events, scanEvent{"leaf", n})
	if r.leaf != nil {
		return r.leaf(n)
	}
	return ReadPayload, nil
}

func (r *recorder) Payload(n Node, payload []byte) error {
	r.payloads++
	if int64(len(payload)) != n.Size {
		return fmt.Errorf("payload of %s: got %d bytes, want %d", n.ID, len(payload), n.Size)
	}
	r.add("payload %s len=%d", n.ID, len(payload))
	r.events = append(r.events, scanEvent{"payload", n})
	return nil
}

func (r *recorder) Close(n Node) error {
	r.add("close %s %s", n.ID, extent(n))
	r.events = append(r.events, scanEvent{"close", n})
	if r.onClose != nil {
		return r.onClose(n)
	}
	return nil
}

func topologyBasic(t *testing.T) []byte {
	t.Helper()
	return loadHexFixture(t, "fixtures/kvs/topology_basic.ebml.hex")
}

// feedChunks feeds raw to s in chunks of the given size.
func feedChunks(s *Scanner, raw []byte, chunk int) error {
	for i := 0; i < len(raw); i += chunk {
		end := i + chunk
		if end > len(raw) {
			end = len(raw)
		}
		if err := s.Feed(raw[i:end]); err != nil {
			return err
		}
	}
	return nil
}

// scanChunks runs a full scan of raw in fixed-size chunks and checks the cursor
// came to rest: every master closed, every byte consumed.
func scanChunks(t *testing.T, h Handler, raw []byte, chunk int) error {
	t.Helper()
	s := NewScanner(h, testKindClassifier)
	if err := feedChunks(s, raw, chunk); err != nil {
		return err
	}
	if err := s.Finalize(); err != nil {
		return err
	}
	if got := s.Depth(); got != 0 {
		t.Fatalf("depth after Finalize = %d, want 0", got)
	}
	if got, want := s.Offset(), int64(len(raw)); got != want {
		t.Fatalf("offset after Finalize = %d, want %d", got, want)
	}
	return nil
}

// topologyBasicEvents is the complete event sequence of fixtures/kvs/topology_basic
// with every master descended into and every leaf payload read. Extents are
// contiguous: each element's end offset is the next sibling's start offset, and a
// master's children exactly fill its declared extent. The unknown-size Segment
// reports an unknown extent on its header event and a concrete end (the stream
// length, 696) when EOF closes it.
const topologyBasicEvents = `
master 0x1A45DFA3 d0 @0 h5 s19 e24
leaf 0x4286 d1 @5 h3 s1 e9
payload 0x4286 len=1
leaf 0x42F7 d1 @9 h3 s1 e13
payload 0x42F7 len=1
leaf 0x4282 d1 @13 h3 s8 e24
payload 0x4282 len=8
close 0x1A45DFA3 d0 @0 h5 s19 e24
master 0x18538067 d0 @24 h12 sunknown eunknown
master 0x1549A966 d1 @36 h5 s26 e67
leaf 0x73A4 d2 @41 h3 s16 e60
payload 0x73A4 len=16
leaf 0x2AD7B1 d2 @60 h4 s3 e67
payload 0x2AD7B1 len=3
close 0x1549A966 d1 @36 h5 s26 e67
master 0x1654AE6B d1 @67 h5 s126 e198
master 0xAE d2 @72 h2 s62 e136
leaf 0xD7 d3 @74 h2 s1 e77
payload 0xD7 len=1
leaf 0x83 d3 @77 h2 s1 e80
payload 0x83 len=1
leaf 0x86 d3 @80 h2 s13 e95
payload 0x86 len=13
leaf 0x536E d3 @95 h3 s19 e117
payload 0x536E len=19
master 0xE1 d3 @117 h2 s17 e136
leaf 0xB5 d4 @119 h2 s8 e129
payload 0xB5 len=8
leaf 0x9F d4 @129 h2 s1 e132
payload 0x9F len=1
leaf 0x6264 d4 @132 h3 s1 e136
payload 0x6264 len=1
close 0xE1 d3 @117 h2 s17 e136
close 0xAE d2 @72 h2 s62 e136
master 0xAE d2 @136 h2 s60 e198
leaf 0xD7 d3 @138 h2 s1 e141
payload 0xD7 len=1
leaf 0x83 d3 @141 h2 s1 e144
payload 0x83 len=1
leaf 0x86 d3 @144 h2 s13 e159
payload 0x86 len=13
leaf 0x536E d3 @159 h3 s17 e179
payload 0x536E len=17
master 0xE1 d3 @179 h2 s17 e198
leaf 0xB5 d4 @181 h2 s8 e191
payload 0xB5 len=8
leaf 0x9F d4 @191 h2 s1 e194
payload 0x9F len=1
leaf 0x6264 d4 @194 h3 s1 e198
payload 0x6264 len=1
close 0xE1 d3 @179 h2 s17 e198
close 0xAE d2 @136 h2 s60 e198
close 0x1654AE6B d1 @67 h5 s126 e198
master 0x1254C367 d1 @198 h6 s370 e574
master 0x7373 d2 @204 h4 s366 e574
master 0x63C0 d3 @208 h3 s0 e211
close 0x63C0 d3 @208 h3 s0 e211
master 0x67C8 d3 @211 h3 s55 e269
leaf 0x45A3 d4 @214 h3 s35 e252
payload 0x45A3 len=35
leaf 0x4487 d4 @252 h3 s14 e269
payload 0x4487 len=14
close 0x67C8 d3 @211 h3 s55 e269
master 0x67C8 d3 @269 h3 s85 e357
leaf 0x45A3 d4 @272 h3 s32 e307
payload 0x45A3 len=32
leaf 0x4487 d4 @307 h3 s47 e357
payload 0x4487 len=47
close 0x67C8 d3 @269 h3 s85 e357
master 0x67C8 d3 @357 h3 s105 e465
leaf 0x45A3 d4 @360 h3 s35 e398
payload 0x45A3 len=35
leaf 0x4487 d4 @398 h3 s64 e465
payload 0x4487 len=64
close 0x67C8 d3 @357 h3 s105 e465
master 0x67C8 d3 @465 h3 s51 e519
leaf 0x45A3 d4 @468 h3 s9 e480
payload 0x45A3 len=9
leaf 0x4487 d4 @480 h3 s36 e519
payload 0x4487 len=36
close 0x67C8 d3 @465 h3 s51 e519
master 0x67C8 d3 @519 h3 s52 e574
leaf 0x45A3 d4 @522 h3 s10 e535
payload 0x45A3 len=10
leaf 0x4487 d4 @535 h3 s36 e574
payload 0x4487 len=36
close 0x67C8 d3 @519 h3 s52 e574
close 0x7373 d2 @204 h4 s366 e574
close 0x1254C367 d1 @198 h6 s370 e574
master 0x1F43B675 d1 @574 h5 s117 e696
leaf 0xE7 d2 @579 h2 s1 e582
payload 0xE7 len=1
leaf 0xA3 d2 @582 h2 s36 e620
payload 0xA3 len=36
leaf 0xA3 d2 @620 h2 s36 e658
payload 0xA3 len=36
leaf 0xA3 d2 @658 h2 s36 e696
payload 0xA3 len=36
close 0x1F43B675 d1 @574 h5 s117 e696
close 0x18538067 d0 @24 h12 sunknown e696
`

func wantLines(s string) []string {
	return strings.Split(strings.TrimSpace(s), "\n")
}

func assertLines(t *testing.T, got, want []string) {
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

// TestScannerEventSequenceIsSplitInvariant checks the whole event sequence over a
// committed fixture and, feeding the same bytes in 1-, 7- and 4096-byte chunks,
// that it does not depend on how the stream was chunked.
func TestScannerEventSequenceIsSplitInvariant(t *testing.T) {
	raw := topologyBasic(t)
	want := wantLines(topologyBasicEvents)

	for _, chunk := range []int{1, 7, 4096} {
		t.Run(fmt.Sprintf("chunk_%d", chunk), func(t *testing.T) {
			var r recorder
			if err := scanChunks(t, &r, raw, chunk); err != nil {
				t.Fatalf("scan: %v", err)
			}
			assertLines(t, r.lines, want)
		})
	}
}

// TestScannerExtentsAreConsistent states the event model's invariants without
// hard-coding any fixture offset, so it keeps its meaning if the corpus is
// regenerated: events walk the stream forward without gaps or overlaps, every
// child lies inside its parent's payload, Depth equals the number of open
// masters, and every descended master gets exactly one Close whose End is its
// declared end (or, for an unknown-size master, the offset it closed at).
func TestScannerExtentsAreConsistent(t *testing.T) {
	// Single-Segment fixtures: an unknown-size master is closed at EOF, so a
	// stream of concatenated Segments needs the boundary rule documented on
	// Scanner and is not scanned here.
	for _, fx := range []string{"topology_basic", "multi_cluster", "false_ebml_magic_in_pcm"} {
		t.Run(fx, func(t *testing.T) {
			raw := loadHexFixture(t, "fixtures/kvs/"+fx+".ebml.hex")
			var r recorder
			if err := scanChunks(t, &r, raw, 7); err != nil {
				t.Fatalf("scan: %v", err)
			}

			var stack []Node
			cursor := int64(0)
			for i, ev := range r.events {
				switch ev.op {
				case "master", "leaf":
					n := ev.node
					if n.Depth != len(stack) {
						t.Fatalf("event %d (%s %s): Depth = %d, open masters = %d", i, ev.op, n.ID, n.Depth, len(stack))
					}
					if n.Offset != cursor {
						t.Fatalf("event %d (%s %s): Offset = %d, want %d (previous element's end)", i, ev.op, n.ID, n.Offset, cursor)
					}
					if n.Size == UnknownSize {
						if n.End != UnknownSize {
							t.Fatalf("event %d (%s %s): End = %d, want UnknownSize", i, ev.op, n.ID, n.End)
						}
					} else if want := n.Offset + int64(n.HeaderLen) + n.Size; n.End != want {
						t.Fatalf("event %d (%s %s): End = %d, want %d", i, ev.op, n.ID, n.End, want)
					}
					if len(stack) > 0 {
						parent := stack[len(stack)-1]
						if n.Offset < parent.Offset+int64(parent.HeaderLen) {
							t.Fatalf("event %d (%s %s) starts at %d, before parent %s payload", i, ev.op, n.ID, n.Offset, parent.ID)
						}
						if parent.End != UnknownSize && n.End != UnknownSize && n.End > parent.End {
							t.Fatalf("event %d (%s %s) ends at %d, past parent %s end %d", i, ev.op, n.ID, n.End, parent.ID, parent.End)
						}
					}
					got := n.OpenMasters()
					if len(got) != len(stack) {
						t.Fatalf("event %d (%s %s): OpenMasters = %v, want %d entries", i, ev.op, n.ID, got, len(stack))
					}
					for d, open := range stack {
						if got[d] != open.ID {
							t.Fatalf("event %d (%s %s): OpenMasters[%d] = %s, want %s", i, ev.op, n.ID, d, got[d], open.ID)
						}
					}
					if ev.op == "master" {
						stack = append(stack, n)
						cursor = n.Offset + int64(n.HeaderLen)
					} else {
						cursor = n.End
					}
				case "payload":
					// Payload follows its own leaf event.
					if i == 0 || r.events[i-1].op != "leaf" || r.events[i-1].node.Offset != ev.node.Offset {
						t.Fatalf("event %d: payload of %s does not follow its leaf event", i, ev.node.ID)
					}
				case "close":
					if len(stack) == 0 {
						t.Fatalf("event %d: close of %s with no open master", i, ev.node.ID)
					}
					open := stack[len(stack)-1]
					stack = stack[:len(stack)-1]
					n := ev.node
					if n.ID != open.ID || n.Offset != open.Offset || n.Depth != open.Depth {
						t.Fatalf("event %d: close of %s @%d does not match open master %s @%d", i, n.ID, n.Offset, open.ID, open.Offset)
					}
					if n.End == UnknownSize {
						t.Fatalf("event %d: close of %s has End = UnknownSize", i, n.ID)
					}
					if open.End != UnknownSize && n.End != open.End {
						t.Fatalf("event %d: close of %s at %d, declared end %d", i, n.ID, n.End, open.End)
					}
					if n.End != cursor {
						t.Fatalf("event %d: close of %s at %d, cursor at %d", i, n.ID, n.End, cursor)
					}
					cursor = n.End
				}
			}
			if len(stack) != 0 {
				t.Fatalf("%d masters never closed", len(stack))
			}
			if cursor != int64(len(raw)) {
				t.Fatalf("events covered %d bytes, stream is %d", cursor, len(raw))
			}
		})
	}
}

// TestScannerHeadersMatchCommittedGolden cross-checks the scanner against the
// committed cursor trace (produced by the low-level driver): the scanner's
// master/leaf events must be exactly the golden's element headers, in order,
// with the same ID, offset, depth, size and header length.
func TestScannerHeadersMatchCommittedGolden(t *testing.T) {
	var r recorder
	if err := scanChunks(t, &r, topologyBasic(t), 4096); err != nil {
		t.Fatalf("scan: %v", err)
	}

	var want []logEvent
	for _, line := range strings.Split(string(loadGolden(t, "golden/kvs/topology_basic.jsonl")), "\n") {
		var ev logEvent
		if err := json.Unmarshal([]byte(line), &ev); err != nil {
			t.Fatalf("decode golden line %q: %v", line, err)
		}
		if ev.Op == "peek" && ev.ID != "" {
			want = append(want, ev)
		}
	}

	if len(r.observed) != len(want) {
		t.Fatalf("scanner reported %d element headers, golden has %d", len(r.observed), len(want))
	}
	for i, n := range r.observed {
		w := want[i]
		if FormatID(n.ID) != w.ID || n.Offset != w.Offset || n.Depth != w.Depth ||
			n.Size != *w.Size || n.HeaderLen != w.HeaderLen {
			t.Fatalf("header %d: got id=%s offset=%d depth=%d size=%d header_len=%d, golden id=%s offset=%d depth=%d size=%d header_len=%d",
				i, FormatID(n.ID), n.Offset, n.Depth, n.Size, n.HeaderLen,
				w.ID, w.Offset, w.Depth, *w.Size, w.HeaderLen)
		}
	}
}

// TestScannerUnknownSizeExtentAndTailFix covers the property the library exists
// for: the unknown-size Segment reports End == UnknownSize while the Cluster
// nested inside it reports a concrete End, and the Cluster's Close fires while
// the stream is still being fed — the Segment's Close waits for Finalize.
func TestScannerUnknownSizeExtentAndTailFix(t *testing.T) {
	raw := topologyBasic(t)

	var (
		segmentHeader, clusterHeader Node
		closedBeforeFinalize         []ElementID
		closedAtFinalize             []ElementID
		finalized                    bool
	)
	h := &HandlerFuncs{
		MasterFunc: func(n Node) (Action, error) {
			switch n.ID {
			case idSegment:
				segmentHeader = n
			case idCluster:
				clusterHeader = n
			}
			return Descend, nil
		},
		CloseFunc: func(n Node) error {
			if finalized {
				closedAtFinalize = append(closedAtFinalize, n.ID)
			} else {
				closedBeforeFinalize = append(closedBeforeFinalize, n.ID)
			}
			return nil
		},
	}

	s := NewScanner(h, testKindClassifier)
	if err := feedChunks(s, raw, 7); err != nil {
		t.Fatalf("feed: %v", err)
	}
	finalized = true
	if err := s.Finalize(); err != nil {
		t.Fatalf("finalize: %v", err)
	}
	if got := s.Depth(); got != 0 {
		t.Fatalf("depth after Finalize = %d, want 0", got)
	}

	if segmentHeader.Size != UnknownSize || segmentHeader.End != UnknownSize {
		t.Fatalf("Segment header: size=%d end=%d, want both UnknownSize", segmentHeader.Size, segmentHeader.End)
	}
	if want := int64(696); clusterHeader.End != want {
		t.Fatalf("Cluster header End = %d, want %d", clusterHeader.End, want)
	}

	// The Cluster closed on its declared size, without waiting for EOF.
	if len(closedBeforeFinalize) == 0 || closedBeforeFinalize[len(closedBeforeFinalize)-1] != idCluster {
		t.Fatalf("Cluster must close before Finalize, closes seen while feeding: %v", closedBeforeFinalize)
	}
	for _, id := range closedBeforeFinalize {
		if id == idSegment {
			t.Fatal("unknown-size Segment must not close before Finalize")
		}
	}
	if len(closedAtFinalize) != 1 || closedAtFinalize[0] != idSegment {
		t.Fatalf("closes at Finalize = %v, want exactly [Segment]", closedAtFinalize)
	}
}

// TestScannerBoundaryClosesUnknownSizeMaster covers the one thing a stream of
// concatenated unknown-size masters needs: with a BoundaryDecider, each Segment of
// a multi-fragment stream closes where the next fragment's header begins, while
// feeding is still in progress, so nothing nests and nothing waits for EOF.
func TestScannerBoundaryClosesUnknownSizeMaster(t *testing.T) {
	raw := loadHexFixture(t, "fixtures/kvs/multi_segment.ebml.hex")

	for _, chunk := range []int{1, 7, 4096} {
		t.Run(fmt.Sprintf("chunk_%d", chunk), func(t *testing.T) {
			var (
				segmentCloses  []Node
				boundaryAsked  []ElementID
				topLevelDepths []int
				finalized      bool
				closedAtFinish []ElementID
			)
			h := &HandlerFuncs{
				MasterFunc: func(n Node) (Action, error) {
					if n.ID == idSegment || n.ID == idEBMLHeader {
						topLevelDepths = append(topLevelDepths, n.Depth)
					}
					return Descend, nil
				},
				CloseFunc: func(n Node) error {
					if finalized {
						closedAtFinish = append(closedAtFinish, n.ID)
					}
					if n.ID == idSegment {
						segmentCloses = append(segmentCloses, n)
					}
					return nil
				},
				BoundaryFunc: func(open, next Node) bool {
					if open.ID != idSegment {
						t.Fatalf("boundary asked about %s, want only the unknown-size Segment", open.ID)
					}
					boundaryAsked = append(boundaryAsked, next.ID)
					return next.ID == idEBMLHeader || next.ID == idSegment
				},
			}

			s := NewScanner(h, testKindClassifier)
			if err := feedChunks(s, raw, chunk); err != nil {
				t.Fatalf("feed: %v", err)
			}
			// Three of the four Segments closed structurally, before EOF.
			if len(segmentCloses) != 3 {
				t.Fatalf("Segment closes before Finalize = %d, want 3", len(segmentCloses))
			}
			finalized = true
			if err := s.Finalize(); err != nil {
				t.Fatalf("finalize: %v", err)
			}
			if len(segmentCloses) != 4 {
				t.Fatalf("Segment closes = %d, want 4 (the last one at EOF)", len(segmentCloses))
			}
			if len(closedAtFinish) != 1 || closedAtFinish[0] != idSegment {
				t.Fatalf("closes at Finalize = %v, want exactly [Segment]", closedAtFinish)
			}

			// Each Segment ends exactly where the next fragment's header begins, and
			// none of them nested: without the boundary rule every Segment after the
			// first would be reported inside its predecessor.
			for i, closed := range segmentCloses {
				if closed.Depth != 0 {
					t.Fatalf("Segment %d closed at depth %d, want 0", i, closed.Depth)
				}
				if closed.Size != UnknownSize {
					t.Fatalf("Segment %d Close Size = %d, want UnknownSize", i, closed.Size)
				}
				if i > 0 && closed.Offset <= segmentCloses[i-1].End {
					t.Fatalf("Segment %d starts at %d, not after the previous end %d", i, closed.Offset, segmentCloses[i-1].End)
				}
			}
			if got := segmentCloses[3].End; got != int64(len(raw)) {
				t.Fatalf("last Segment closed at %d, want the stream length %d", got, len(raw))
			}
			// Every top-level element stays at depth 0: without the boundary rule
			// each Segment would be reported inside its predecessor.
			for i, depth := range topLevelDepths {
				if depth != 0 {
					t.Fatalf("top-level element %d reported at depth %d, want 0 (Segments must not nest)", i, depth)
				}
			}
			if len(topLevelDepths) != 8 {
				t.Fatalf("saw %d top-level elements, want 8 (4 headers + 4 Segments)", len(topLevelDepths))
			}
			// The rule is only ever consulted while a Segment is open, and only about
			// elements that could be its children.
			if len(boundaryAsked) == 0 {
				t.Fatal("the boundary rule was never consulted")
			}
		})
	}
}

// TestScannerWithoutBoundaryKeepsUnknownSizeMasterOpen states the default the
// BoundaryDecider overrides: a handler that does not implement it sees the next
// fragment's header as a child of the still-open Segment.
func TestScannerWithoutBoundaryKeepsUnknownSizeMasterOpen(t *testing.T) {
	raw := loadHexFixture(t, "fixtures/kvs/multi_segment.ebml.hex")

	var headerDepths []int
	h := &HandlerFuncs{MasterFunc: func(n Node) (Action, error) {
		if n.ID == idEBMLHeader {
			headerDepths = append(headerDepths, n.Depth)
		}
		return Descend, nil
	}}
	s := NewScanner(h, testKindClassifier)
	if err := feedChunks(s, raw, 4096); err != nil {
		t.Fatalf("feed: %v", err)
	}
	if err := s.Finalize(); err != nil {
		t.Fatalf("finalize: %v", err)
	}
	if len(headerDepths) != 4 {
		t.Fatalf("saw %d EBML headers, want 4", len(headerDepths))
	}
	if want := []int{0, 1, 2, 3}; !reflect.DeepEqual(headerDepths, want) {
		t.Fatalf("EBML header depths = %v, want %v (each Segment stays open)", headerDepths, want)
	}
}

// TestScannerUnconsumedAndStartOffsetResumeTheStream covers the pair of
// primitives a consumer needs to resynchronize after a hard failure: the bytes the
// failed cursor had not consumed, and a fresh cursor that keeps counting offsets
// in the same stream.
func TestScannerUnconsumedAndStartOffsetResumeTheStream(t *testing.T) {
	raw := topologyBasic(t)

	// Stop the scan at the Cluster by refusing it, leaving the rest unconsumed.
	var clusterOffset int64
	stop := errors.New("stop at the Cluster")
	h := &HandlerFuncs{MasterFunc: func(n Node) (Action, error) {
		if n.ID == idCluster {
			clusterOffset = n.Offset
			return 0, stop
		}
		return Descend, nil
	}}
	s := NewScanner(h, testKindClassifier)
	if err := s.Feed(raw); !errors.Is(err, stop) {
		t.Fatalf("Feed error = %v, want %v", err, stop)
	}
	if got := s.Offset(); got != clusterOffset {
		t.Fatalf("Offset after failure = %d, want the Cluster header at %d", got, clusterOffset)
	}
	rest := s.Unconsumed()
	if want := raw[clusterOffset:]; !bytes.Equal(rest, want) {
		t.Fatalf("Unconsumed() returned %d bytes, want the %d from the Cluster on", len(rest), len(want))
	}
	rest[0] ^= 0xFF
	if again := s.Unconsumed(); again[0] == rest[0] {
		t.Fatal("Unconsumed() must return a copy, not the parser's own buffer")
	}

	// A fresh cursor over exactly those bytes reports the original stream's offsets.
	var resumed recorder
	s2 := NewScanner(&resumed, testKindClassifier, WithStartOffset(clusterOffset))
	if err := s2.Feed(raw[clusterOffset:]); err != nil {
		t.Fatalf("resumed Feed: %v", err)
	}
	if err := s2.Finalize(); err != nil {
		t.Fatalf("resumed Finalize: %v", err)
	}
	if len(resumed.observed) == 0 {
		t.Fatal("the resumed scan reported nothing")
	}
	first := resumed.observed[0]
	if first.ID != idCluster || first.Offset != clusterOffset {
		t.Fatalf("resumed scan starts at %s @%d, want the Cluster @%d", first.ID, first.Offset, clusterOffset)
	}
	if got := s2.Offset(); got != int64(len(raw)) {
		t.Fatalf("resumed Offset at EOF = %d, want the stream length %d", got, len(raw))
	}
	if got := s2.Unconsumed(); got != nil {
		t.Fatalf("Unconsumed() after a complete scan = %x, want nil", got)
	}
}

// TestScannerCloseEndOfUnknownSizeMaster checks the Close event of the
// unknown-size Segment carries the offset where it actually closed, not
// UnknownSize.
func TestScannerCloseEndOfUnknownSizeMaster(t *testing.T) {
	raw := topologyBasic(t)
	var closed Node
	h := &HandlerFuncs{CloseFunc: func(n Node) error {
		if n.ID == idSegment {
			closed = n
		}
		return nil
	}}
	if err := scanChunks(t, h, raw, 4096); err != nil {
		t.Fatalf("scan: %v", err)
	}
	if closed.Size != UnknownSize {
		t.Fatalf("Segment Close Size = %d, want UnknownSize (declared size is unchanged)", closed.Size)
	}
	if want := int64(len(raw)); closed.End != want {
		t.Fatalf("Segment Close End = %d, want %d", closed.End, want)
	}
	if closed.Offset != 24 || closed.Depth != 0 {
		t.Fatalf("Segment Close offset=%d depth=%d, want 24 and 0", closed.Offset, closed.Depth)
	}
}

// TestScannerOpenMastersAtSimpleBlock checks a handler can judge an element by
// its ancestry alone: at a SimpleBlock the open masters are exactly
// [Segment, Cluster], outermost first.
func TestScannerOpenMastersAtSimpleBlock(t *testing.T) {
	seen := 0
	h := &HandlerFuncs{LeafFunc: func(n Node) (Action, error) {
		if n.ID != idSimpleBlock {
			return SkipPayload, nil
		}
		seen++
		got := n.OpenMasters()
		want := []ElementID{idSegment, idCluster}
		if len(got) != len(want) {
			return 0, fmt.Errorf("OpenMasters = %v, want %v", got, want)
		}
		for i := range want {
			if got[i] != want[i] {
				return 0, fmt.Errorf("OpenMasters = %v, want %v", got, want)
			}
		}
		if len(got) != n.Depth {
			return 0, fmt.Errorf("len(OpenMasters) = %d, want Depth = %d", len(got), n.Depth)
		}
		return SkipPayload, nil
	}}
	if err := scanChunks(t, h, topologyBasic(t), 4096); err != nil {
		t.Fatalf("scan: %v", err)
	}
	if seen != 3 {
		t.Fatalf("saw %d SimpleBlocks, want 3", seen)
	}
}

// TestScannerOpenMastersIsCallerOwned checks the ancestry slice handed to a
// handler is a copy: mutating it cannot corrupt the scanner's own state.
func TestScannerOpenMastersIsCallerOwned(t *testing.T) {
	h := &HandlerFuncs{LeafFunc: func(n Node) (Action, error) {
		open := n.OpenMasters()
		for i := range open {
			open[i] = 0
		}
		if n.Depth > 0 && n.OpenMasters()[0] == 0 {
			return 0, errors.New("OpenMasters returned an aliased slice")
		}
		return SkipPayload, nil
	}}
	if err := scanChunks(t, h, topologyBasic(t), 4096); err != nil {
		t.Fatalf("scan: %v", err)
	}
}

// TestScannerSkipSubtree checks declining a master removes every descendant
// event and nothing else: the elements after the skipped subtree are reported
// exactly as in a full scan.
func TestScannerSkipSubtree(t *testing.T) {
	raw := topologyBasic(t)

	var full recorder
	if err := scanChunks(t, &full, raw, 4096); err != nil {
		t.Fatalf("full scan: %v", err)
	}

	for _, chunk := range []int{1, 7, 4096} {
		t.Run(fmt.Sprintf("chunk_%d", chunk), func(t *testing.T) {
			r := recorder{master: func(n Node) (Action, error) {
				if n.ID == idTracks {
					return SkipSubtree, nil
				}
				return Descend, nil
			}}
			if err := scanChunks(t, &r, raw, chunk); err != nil {
				t.Fatalf("scan: %v", err)
			}

			// Every Tracks descendant event is gone, and so is the skipped
			// master's own Close: a declined subtree yields exactly one event.
			assertLines(t, r.lines, withoutTracksSubtree(full.lines))

			// The bytes after the skipped subtree are still read correctly.
			if !containsLine(r.lines, "master 0x1254C367 d1 @198 h6 s370 e574") ||
				!containsLine(r.lines, "master 0x1F43B675 d1 @574 h5 s117 e696") ||
				!containsLine(r.lines, "close 0x18538067 d0 @24 h12 sunknown e696") {
				t.Fatalf("events after the skipped subtree are wrong:\n%s", strings.Join(r.lines, "\n"))
			}
		})
	}
}

// withoutTracksSubtree removes the events between the Tracks header and the Tags
// header — the descendants and the Tracks Close — i.e. everything a declined
// subtree must not report.
func withoutTracksSubtree(lines []string) []string {
	out := make([]string, 0, len(lines))
	inTracks := false
	for _, line := range lines {
		switch {
		case strings.HasPrefix(line, "master 0x1654AE6B"):
			inTracks = true
			out = append(out, line)
		case inTracks && strings.HasPrefix(line, "master 0x1254C367"):
			inTracks = false
			out = append(out, line)
		case inTracks:
			continue
		default:
			out = append(out, line)
		}
	}
	return out
}

func containsLine(lines []string, want string) bool {
	for _, line := range lines {
		if line == want {
			return true
		}
	}
	return false
}

// TestScannerSkipPayloadDeliversNothing checks declining a leaf payload never
// calls Payload yet still advances the cursor over every element.
func TestScannerSkipPayloadDeliversNothing(t *testing.T) {
	raw := topologyBasic(t)

	var read recorder
	if err := scanChunks(t, &read, raw, 4096); err != nil {
		t.Fatalf("reading scan: %v", err)
	}

	for _, chunk := range []int{1, 7, 4096} {
		t.Run(fmt.Sprintf("chunk_%d", chunk), func(t *testing.T) {
			r := recorder{leaf: func(Node) (Action, error) { return SkipPayload, nil }}
			if err := scanChunks(t, &r, raw, chunk); err != nil {
				t.Fatalf("scan: %v", err)
			}
			if r.payloads != 0 {
				t.Fatalf("Payload called %d times, want 0", r.payloads)
			}
			want := make([]string, 0, len(read.lines))
			for _, line := range read.lines {
				if strings.HasPrefix(line, "payload ") {
					continue
				}
				want = append(want, line)
			}
			assertLines(t, r.lines, want)
		})
	}
}

// TestScannerHandlerErrorPropagates checks a handler error aborts the scan and
// surfaces from whichever call was running, and that the scanner stays failed.
func TestScannerHandlerErrorPropagates(t *testing.T) {
	raw := topologyBasic(t)
	sentinel := errors.New("handler says stop")

	t.Run("leaf_error_from_feed", func(t *testing.T) {
		var events int
		h := &HandlerFuncs{
			LeafFunc:    func(Node) (Action, error) { return 0, sentinel },
			PayloadFunc: func(Node, []byte) error { events++; return nil },
		}
		s := NewScanner(h, testKindClassifier)
		err := feedChunks(s, raw, 7)
		if !errors.Is(err, sentinel) {
			t.Fatalf("Feed error = %v, want %v", err, sentinel)
		}
		if events != 0 {
			t.Fatalf("handler kept receiving events after its error (%d payloads)", events)
		}
		if err := s.Feed([]byte{0x00}); !errors.Is(err, sentinel) {
			t.Fatalf("Feed after failure = %v, want %v", err, sentinel)
		}
		if err := s.Finalize(); !errors.Is(err, sentinel) {
			t.Fatalf("Finalize after failure = %v, want %v", err, sentinel)
		}
	})

	t.Run("payload_error_from_feed", func(t *testing.T) {
		h := &HandlerFuncs{
			LeafFunc:    func(Node) (Action, error) { return ReadPayload, nil },
			PayloadFunc: func(Node, []byte) error { return sentinel },
		}
		s := NewScanner(h, testKindClassifier)
		if err := feedChunks(s, raw, 7); !errors.Is(err, sentinel) {
			t.Fatalf("Feed error = %v, want %v", err, sentinel)
		}
	})

	t.Run("close_error_from_finalize", func(t *testing.T) {
		// Only the unknown-size Segment closes at EOF, so this error can only
		// surface from Finalize.
		h := &HandlerFuncs{CloseFunc: func(n Node) error {
			if n.ID == idSegment {
				return sentinel
			}
			return nil
		}}
		s := NewScanner(h, testKindClassifier)
		if err := feedChunks(s, raw, 7); err != nil {
			t.Fatalf("Feed error = %v", err)
		}
		if err := s.Finalize(); !errors.Is(err, sentinel) {
			t.Fatalf("Finalize error = %v, want %v", err, sentinel)
		}
	})

	t.Run("master_error_from_feed", func(t *testing.T) {
		h := &HandlerFuncs{MasterFunc: func(Node) (Action, error) { return 0, sentinel }}
		s := NewScanner(h, testKindClassifier)
		if err := feedChunks(s, raw, 4096); !errors.Is(err, sentinel) {
			t.Fatalf("Feed error = %v, want %v", err, sentinel)
		}
	})
}

// TestScannerRejectsMismatchedAction checks a decision that does not fit the
// element is a clear error rather than undefined behaviour.
func TestScannerRejectsMismatchedAction(t *testing.T) {
	raw := topologyBasic(t)

	cases := []struct {
		name string
		h    Handler
	}{
		{"read_payload_for_master", &HandlerFuncs{
			MasterFunc: func(Node) (Action, error) { return ReadPayload, nil },
		}},
		{"skip_payload_for_master", &HandlerFuncs{
			MasterFunc: func(Node) (Action, error) { return SkipPayload, nil },
		}},
		{"descend_for_leaf", &HandlerFuncs{
			LeafFunc: func(Node) (Action, error) { return Descend, nil },
		}},
		{"skip_subtree_for_leaf", &HandlerFuncs{
			LeafFunc: func(Node) (Action, error) { return SkipSubtree, nil },
		}},
		{"zero_action_for_master", &HandlerFuncs{
			MasterFunc: func(Node) (Action, error) { return 0, nil },
		}},
		{"zero_action_for_leaf", &HandlerFuncs{
			LeafFunc: func(Node) (Action, error) { return 0, nil },
		}},
		{"skip_subtree_of_unknown_size_master", &HandlerFuncs{
			MasterFunc: func(n Node) (Action, error) {
				if n.ID == idSegment {
					return SkipSubtree, nil
				}
				return Descend, nil
			},
		}},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			s := NewScanner(c.h, testKindClassifier)
			err := feedChunks(s, raw, 4096)
			if err == nil {
				err = s.Finalize()
			}
			var invalid Invalid
			if !errors.As(err, &invalid) {
				t.Fatalf("error = %v (%T), want Invalid", err, err)
			}
		})
	}
}

// TestScannerFinalizeRejectsTruncatedStream checks a stream that ends inside an
// element is reported, not silently accepted.
func TestScannerFinalizeRejectsTruncatedStream(t *testing.T) {
	raw := topologyBasic(t)
	s := NewScanner(&HandlerFuncs{}, testKindClassifier)
	if err := feedChunks(s, raw[:len(raw)-5], 7); err != nil {
		t.Fatalf("Feed error = %v", err)
	}
	if err := s.Finalize(); !errors.Is(err, ErrTruncated) {
		t.Fatalf("Finalize error = %v, want ErrTruncated", err)
	}
}

// TestScannerFeedAfterFinalize checks the scan cannot be continued past EOF.
func TestScannerFeedAfterFinalize(t *testing.T) {
	s := NewScanner(&HandlerFuncs{}, testKindClassifier)
	if err := feedChunks(s, topologyBasic(t), 4096); err != nil {
		t.Fatalf("Feed error = %v", err)
	}
	if err := s.Finalize(); err != nil {
		t.Fatalf("Finalize error = %v", err)
	}
	if err := s.Finalize(); err != nil {
		t.Fatalf("second Finalize error = %v, want nil (idempotent)", err)
	}
	var invalid Invalid
	if err := s.Feed([]byte{0xec, 0x80}); !errors.As(err, &invalid) {
		t.Fatalf("Feed after Finalize = %v, want Invalid", err)
	}
}

// TestHandlerFuncsDefaults checks the zero HandlerFuncs is a usable handler:
// masters are descended into and leaf payloads are skipped.
func TestHandlerFuncsDefaults(t *testing.T) {
	var h HandlerFuncs
	n := Node{ID: idEBMLHeader, Kind: KindMaster}
	if a, err := h.Master(n); a != Descend || err != nil {
		t.Fatalf("Master = (%v, %v), want (%v, nil)", a, err, Descend)
	}
	if a, err := h.Leaf(n); a != SkipPayload || err != nil {
		t.Fatalf("Leaf = (%v, %v), want (%v, nil)", a, err, SkipPayload)
	}
	if err := h.Payload(n, nil); err != nil {
		t.Fatalf("Payload = %v, want nil", err)
	}
	if err := h.Close(n); err != nil {
		t.Fatalf("Close = %v, want nil", err)
	}

	// The default handler walks the whole tree without retaining any payload.
	if err := scanChunks(t, &h, topologyBasic(t), 7); err != nil {
		t.Fatalf("scan with default handler: %v", err)
	}
}

// TestActionString checks actions render readably in error messages.
func TestActionString(t *testing.T) {
	for _, c := range []struct {
		a    Action
		want string
	}{
		{Descend, "descend"},
		{SkipSubtree, "skip_subtree"},
		{ReadPayload, "read_payload"},
		{SkipPayload, "skip_payload"},
		{0, "action(0)"},
	} {
		if got := c.a.String(); got != c.want {
			t.Fatalf("Action(%d).String() = %q, want %q", int(c.a), got, c.want)
		}
	}
}

// TestElementHeaderOffsetAndClosedMasterExtent checks the low-level cursor
// reports element extents too, so a caller never reconstructs offsets.
func TestElementHeaderOffsetAndClosedMasterExtent(t *testing.T) {
	raw := topologyBasic(t)
	p := New(testKindClassifier)
	p.Feed(raw)

	h, err := p.Peek()
	if err != nil {
		t.Fatalf("Peek: %v", err)
	}
	if h.Offset != 0 || h.ID != idEBMLHeader {
		t.Fatalf("first header = %s offset=%d, want %s at 0", h.ID, h.Offset, idEBMLHeader)
	}
	if _, err := p.ConsumeHeader(); err != nil {
		t.Fatalf("ConsumeHeader: %v", err)
	}
	if err := p.EnterMaster(); err != nil {
		t.Fatalf("EnterMaster: %v", err)
	}
	h, err = p.Peek()
	if err != nil {
		t.Fatalf("Peek child: %v", err)
	}
	if h.Offset != 5 {
		t.Fatalf("child header offset = %d, want 5", h.Offset)
	}

	// FinalizeEOF's own report carries the closed master's extent.
	p2 := New(testKindClassifier)
	p2.Feed(raw)
	if err := drainDefectTest(p2); err != nil {
		t.Fatalf("drain: %v", err)
	}
	closed, err := p2.FinalizeEOF()
	if err != nil {
		t.Fatalf("FinalizeEOF: %v", err)
	}
	if len(closed) != 1 {
		t.Fatalf("FinalizeEOF closed %d masters, want 1", len(closed))
	}
	cm := closed[0]
	if cm.ID != idSegment || cm.Start != 24 || cm.End != int64(len(raw)) || cm.Depth != 1 {
		t.Fatalf("closed master = %+v, want Segment start=24 end=%d depth=1", cm, len(raw))
	}
}
