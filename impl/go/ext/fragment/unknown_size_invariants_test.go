package fragment_test

import (
	"testing"

	"github.com/yacchi/ebml/impl/go/ext/fragment"
	"github.com/yacchi/ebml/impl/go/internal/ebmltest"
	"github.com/yacchi/ebml/impl/go/matroska"
	"github.com/yacchi/ebml/impl/go/parser"
)

// f4Document wraps an unknown-size Cluster in the real GetMedia shape: an EBML
// header, an unknown-size Segment carrying Info/Tracks/Tags, the Cluster under
// test, and a following Tags element that is not a legal Cluster child -- the
// only thing that should end the Cluster.
func f4Document(cluster ebmltest.Node) []byte {
	return ebmltest.Encode(
		synEBMLHeader(),
		ebmltest.UnknownMaster(matroska.IDSegment,
			synInfo(),
			synTracks(synTrackEntry(1, "AUDIO_FROM_CUSTOMER")),
			synTags("producer", "test"),
			cluster,
			synTags("next", "document"),
		),
	)
}

func mustOneFragmentTwoBlocks(t *testing.T, raw []byte) *fragment.Fragment {
	t.Helper()
	frags := runWhole(t, raw)
	if len(frags) != 1 {
		t.Fatalf("Feed returned %d fragments, want exactly 1 (a false boundary split the Cluster)", len(frags))
	}
	if len(frags[0].Blocks) != 2 {
		t.Fatalf("Blocks = %d, want 2 (a false boundary dropped a SimpleBlock)", len(frags[0].Blocks))
	}
	return frags[0]
}

// TestBlockGroupDoesNotEndACluster: BlockGroup is a legal Cluster child. A
// false "ends the master" verdict here would truncate every stream using the
// BlockGroup-wrapped Block form instead of bare SimpleBlock.
func TestBlockGroupDoesNotEndACluster(t *testing.T) {
	blockGroup := ebmltest.Master(matroska.IDBlockGroup,
		ebmltest.Leaf(matroska.IDBlock, []byte{0x81, 0x00, 0x02, 0x00, 3, 4}),
	)
	cluster := ebmltest.UnknownMaster(matroska.IDCluster,
		ebmltest.Uint(matroska.IDTimestamp, 0),
		synSimpleBlock(1, 0, []byte{1}),
		blockGroup,
		synSimpleBlock(1, 1, []byte{2}),
	)
	frag := mustOneFragmentTwoBlocks(t, f4Document(cluster))
	if got := frag.Cluster.ChildrenByID(matroska.IDBlockGroup); len(got) != 1 {
		t.Fatalf("Cluster retained %d BlockGroup children, want 1 (BlockGroup was cut off by a false boundary)", len(got))
	}
}

// TestPositionAndPrevSizeDoNotEndACluster covers two legal Cluster children the
// worker's own suite never exercises together.
func TestPositionAndPrevSizeDoNotEndACluster(t *testing.T) {
	cluster := ebmltest.UnknownMaster(matroska.IDCluster,
		ebmltest.Uint(matroska.IDTimestamp, 0),
		synSimpleBlock(1, 0, []byte{1}),
		ebmltest.Uint(matroska.IDPosition, 12345),
		ebmltest.Uint(matroska.IDPrevSize, 678),
		synSimpleBlock(1, 1, []byte{2}),
	)
	frag := mustOneFragmentTwoBlocks(t, f4Document(cluster))
	if got := frag.Cluster.ChildrenByID(matroska.IDPosition); len(got) != 1 {
		t.Fatalf("Cluster retained %d Position children, want 1", len(got))
	}
	if got := frag.Cluster.ChildrenByID(matroska.IDPrevSize); len(got) != 1 {
		t.Fatalf("Cluster retained %d PrevSize children, want 1", len(got))
	}
}

// TestCRC32AsFirstChildOfClusterDoesNotEndIt places the global CRC-32 element
// BEFORE Timestamp -- the worker's own global-element test only puts globals
// in the middle, never as the very first child, which is the position where a
// containment-list bug (checking "children[0] == next" style logic) would most
// plausibly misfire.
func TestCRC32AsFirstChildOfClusterDoesNotEndIt(t *testing.T) {
	cluster := ebmltest.UnknownMaster(matroska.IDCluster,
		ebmltest.Leaf(matroska.IDCRC32, []byte{0, 0, 0, 0}),
		ebmltest.Uint(matroska.IDTimestamp, 0),
		synSimpleBlock(1, 0, []byte{1}),
		synSimpleBlock(1, 1, []byte{2}),
	)
	mustOneFragmentTwoBlocks(t, f4Document(cluster))
}

// TestVoidBetweenSimpleBlocksDoesNotEndACluster: Void padding is a real-world
// occurrence between audio blocks (alignment padding), not just at the edges.
func TestVoidBetweenSimpleBlocksDoesNotEndACluster(t *testing.T) {
	cluster := ebmltest.UnknownMaster(matroska.IDCluster,
		ebmltest.Uint(matroska.IDTimestamp, 0),
		synSimpleBlock(1, 0, []byte{1}),
		ebmltest.Leaf(matroska.IDVoid, []byte{0, 0, 0}),
		synSimpleBlock(1, 1, []byte{2}),
	)
	mustOneFragmentTwoBlocks(t, f4Document(cluster))
}

// TestUnregisteredVendorIDAsFirstChildDoesNotEndACluster places an
// unregistered ID as the very FIRST child of the Cluster -- before the worker
// places its unregistered case in the middle only -- pinning the safe
// direction at the position most likely to trip an implementation that treats
// "first unrecognized element" specially.
func TestUnregisteredVendorIDAsFirstChildDoesNotEndACluster(t *testing.T) {
	cluster := ebmltest.UnknownMaster(matroska.IDCluster,
		ebmltest.Uint(parser.ElementID(0x00A9), 7), // unregistered, low-value-looking ID
		ebmltest.Uint(matroska.IDTimestamp, 0),
		synSimpleBlock(1, 0, []byte{1}),
		synSimpleBlock(1, 1, []byte{2}),
	)
	mustOneFragmentTwoBlocks(t, f4Document(cluster))
}

// TestNestedSegmentInSegmentCascade: a second EBML+Segment header arrives while
// BOTH a Cluster and its enclosing Segment are open. The outward cascade must
// still hold -- the Cluster closes, then the Segment closes, and the new
// Segment (and its Cluster) start as an entirely separate, correctly-scoped
// document. This targets the interaction between the pre-existing
// EBML/Segment top-level rule and the new Cluster-containment rule, not either
// rule in isolation.
func TestNestedSegmentInSegmentCascade(t *testing.T) {
	first := ebmltest.Encode(
		synEBMLHeader(),
		ebmltest.UnknownMaster(matroska.IDSegment,
			synInfo(),
			synTracks(synTrackEntry(1, "AUDIO_FROM_CUSTOMER")),
			ebmltest.UnknownMaster(matroska.IDCluster,
				ebmltest.Uint(matroska.IDTimestamp, 0),
				synSimpleBlock(1, 0, []byte{1}),
			),
		),
	)
	second := ebmltest.Encode(
		synEBMLHeader(),
		ebmltest.UnknownMaster(matroska.IDSegment,
			synInfo(),
			synTracks(synTrackEntry(2, "AUDIO_FROM_AGENT")),
			ebmltest.UnknownMaster(matroska.IDCluster,
				ebmltest.Uint(matroska.IDTimestamp, 0),
				synSimpleBlock(2, 0, []byte{9}),
			),
		),
	)
	a := fragment.New()
	if frags, err := a.Feed(first); err != nil {
		t.Fatalf("first Feed: %v", err)
	} else if len(frags) != 0 {
		t.Fatalf("first Feed returned %d fragments, want 0 (nothing should close before the second document arrives)", len(frags))
	}
	frags, err := a.Feed(second)
	if err != nil {
		t.Fatalf("second Feed: %v", err)
	}
	// The first Segment's Cluster must be emitted (cascaded outward through the
	// Segment boundary), and the second Segment's Cluster must NOT be emitted yet
	// -- it is still open, waiting for its own boundary.
	if len(frags) != 1 {
		t.Fatalf("second Feed returned %d fragments, want exactly 1 (the first document's Cluster)", len(frags))
	}
	if len(frags[0].Blocks) != 1 || frags[0].Blocks[0].TrackNumber != 1 {
		t.Fatalf("emitted fragment carries the wrong Cluster: %d blocks, track %v",
			len(frags[0].Blocks), frags[0].Blocks)
	}
	// Finalize should now close out the second document's still-open Cluster.
	tail, err := a.Finalize()
	if err != nil {
		t.Fatalf("Finalize: %v", err)
	}
	if len(tail) != 1 || len(tail[0].Blocks) != 1 || tail[0].Blocks[0].TrackNumber != 2 {
		t.Fatalf("Finalize returned %d fragments, want exactly 1 with track 2's block", len(tail))
	}
}

// TestBlockGroupInsideClusterSplitInvariance is split invariance for a case the
// worker's own TestUnknownSizeClusterSplitInvariance does not cover: a legal
// Cluster child (BlockGroup) whose header may straddle a chunk boundary, which
// is exactly where a boundary decision made on a partially-fed header would
// misfire.
func TestBlockGroupInsideClusterSplitInvariance(t *testing.T) {
	blockGroup := ebmltest.Master(matroska.IDBlockGroup,
		ebmltest.Leaf(matroska.IDBlock, []byte{0x81, 0x00, 0x02, 0x00, 3, 4}),
	)
	cluster := ebmltest.UnknownMaster(matroska.IDCluster,
		ebmltest.Uint(matroska.IDTimestamp, 0),
		synSimpleBlock(1, 0, []byte{1}),
		blockGroup,
		synSimpleBlock(1, 1, []byte{2}),
	)
	raw := f4Document(cluster)
	patterns := []struct {
		name   string
		chunks [][]byte
	}{
		{"one_byte", splitOneByte(raw)},
		{"fibonacci", splitFibonacci(raw)},
		{"random", splitRandom(raw, 424242, 5)},
	}
	for _, pattern := range patterns {
		t.Run(pattern.name, func(t *testing.T) {
			a := fragment.New()
			var got []*fragment.Fragment
			for _, chunk := range pattern.chunks {
				frags, err := a.Feed(chunk)
				if err != nil {
					t.Fatalf("Feed: %v", err)
				}
				got = append(got, frags...)
			}
			// The document's trailing Tags is the last thing in it, so the fragment
			// is still waiting on its Segment-level metadata until the input ends.
			tail, err := a.Finalize()
			if err != nil {
				t.Fatalf("Finalize: %v", err)
			}
			got = append(got, tail...)
			if len(got) != 1 || len(got[0].Blocks) != 2 {
				t.Fatalf("got %d fragments and %d blocks, want 1 and 2", len(got), len(got[0].Blocks))
			}
			// Contents are split-invariant too, and that now includes the metadata
			// written AFTER the Cluster: it is in the view whatever the chunking was.
			if tag, ok := fragTag(got[0], "next"); !ok || tag != "document" {
				t.Fatalf("trailing tag = %q, %v; want it settled before delivery", tag, ok)
			}
		})
	}
}
