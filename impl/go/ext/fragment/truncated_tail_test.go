package fragment_test

import (
	"errors"
	"testing"

	"github.com/yacchi/ebml/impl/go/ext/fragment"
	"github.com/yacchi/ebml/impl/go/internal/ebmltest"
	"github.com/yacchi/ebml/impl/go/matroska"
	"github.com/yacchi/ebml/impl/go/parser"
)

// ---- Salvaging the Cluster a truncated stream left open ----
//
// A live GetMedia connection does not end at an element boundary; it ends wherever
// the socket died, which is normally INSIDE a payload. The blocks that decoded
// completely before that point are the whole subject here: Finalize reports the
// truncation exactly as it always did, AND hands over the Cluster that was still
// open, marked Truncated.
//
// The Cluster is UNKNOWN-SIZE in every case below, because that is the only shape
// KVS sends. A corpus built from the easier known-size shape would be validating an
// assumption rather than the world -- which is the mistake these tests exist after.

// synUnknownCluster is synCluster's unknown-size twin: the Cluster header declares
// no size, so nothing but the next element or EOF can end it.
func synUnknownCluster(clusterTS uint64, children ...ebmltest.Node) ebmltest.Node {
	all := append([]ebmltest.Node{ebmltest.Uint(matroska.IDTimestamp, clusterTS)}, children...)
	return ebmltest.UnknownMaster(matroska.IDCluster, all...)
}

// cutInside returns the stream with n bytes removed from the end, so it stops
// inside whatever element the last bytes belonged to.
func cutInside(raw []byte, n int) []byte {
	return raw[:len(raw)-n]
}

// feedAndFinalize runs one whole chunk through an Assembler and returns the
// fragments Feed produced, those Finalize produced, and Finalize's error -- which
// these tests assert on rather than fail on.
func feedAndFinalize(t *testing.T, raw []byte) (fromFeed, fromFinalize []*fragment.Fragment, err error) {
	t.Helper()
	a := fragment.New()
	fromFeed, feedErr := a.Feed(raw)
	if feedErr != nil {
		t.Fatalf("Feed of a truncated stream = %v; the bytes so far are readable", feedErr)
	}
	fromFinalize, err = a.Finalize()
	return fromFeed, fromFinalize, err
}

// TestTruncatedTailSalvagesDecodedBlocks is the case a dropped connection actually
// produces: two blocks decoded, the third was cut in half. The two that decoded are
// not made worthless by the third, so they arrive -- with the truncation still
// reported.
func TestTruncatedTailSalvagesDecodedBlocks(t *testing.T) {
	raw := synFragment(
		synTracks(synTrackEntry(1, "AUDIO_FROM_CUSTOMER")),
		synUnknownCluster(0,
			synSimpleBlock(1, 0, []byte{0x01, 0x02, 0x03, 0x04}),
			synSimpleBlock(1, 20, []byte{0x05, 0x06, 0x07, 0x08}),
			synSimpleBlock(1, 40, []byte{0x09, 0x0A, 0x0B, 0x0C}),
		),
	)

	fromFeed, tail, err := feedAndFinalize(t, cutInside(raw, 3))
	if !errors.Is(err, parser.ErrTruncated) {
		t.Fatalf("Finalize = %v, want the truncation still reported", err)
	}
	if len(fromFeed) != 0 {
		t.Fatalf("Feed emitted %d fragments; the Cluster never closed", len(fromFeed))
	}
	if len(tail) != 1 {
		t.Fatalf("Finalize returned %d fragments, want the 1 salvaged Cluster", len(tail))
	}
	frag := tail[0]
	if !frag.Truncated {
		t.Fatal("salvaged fragment is not marked Truncated; a consumer cannot tell it from a complete one")
	}
	if len(frag.Blocks) != 2 {
		t.Fatalf("salvaged %d blocks, want the 2 that decoded before the cut", len(frag.Blocks))
	}
	for i, want := range [][]byte{{0x01, 0x02, 0x03, 0x04}, {0x05, 0x06, 0x07, 0x08}} {
		if got := frag.Blocks[i].Frames[0]; string(got) != string(want) {
			t.Errorf("block %d frame = %x, want %x", i, got, want)
		}
	}

	// The cut element keeps its place in the tree: the Cluster's SHAPE still
	// accounts for the bytes, even though no block was decoded from them.
	blocks := frag.Values(matroska.IDSimpleBlock)
	if len(blocks) != 3 {
		t.Fatalf("Cluster retains %d SimpleBlock elements, want all 3 including the cut one", len(blocks))
	}
	if cut := blocks[2]; !cut.Truncated || cut.Payload != nil {
		t.Errorf("cut SimpleBlock: Truncated=%v Payload=%v, want true and nil", cut.Truncated, cut.Payload)
	}

	// A salvaged fragment is an ordinary Fragment: both trees are there, so the
	// Segment-level metadata that preceded the Cluster is readable as always.
	if frag.Segment == nil || frag.Cluster == nil {
		t.Fatal("salvaged fragment must carry both trees")
	}
	if track, ok := frag.TrackByName("AUDIO_FROM_CUSTOMER"); !ok || track == nil {
		t.Error("salvaged fragment lost the Segment-level track metadata")
	}
}

// TestTruncatedTailCutInsideAHeader is the other place a socket can die: between
// an element's first byte and its complete header, which the cursor diagnoses as a
// truncated HEADER rather than a truncated payload. The salvage condition is the
// same one either way -- the Cluster was open -- so the blocks that decoded arrive
// here too, and the element whose header never finished is not in the tree at all,
// there being no extent to record.
func TestTruncatedTailCutInsideAHeader(t *testing.T) {
	raw := synFragment(
		synTracks(synTrackEntry(1, "AUDIO_FROM_CUSTOMER")),
		synUnknownCluster(0,
			synSimpleBlock(1, 0, []byte{0x01, 0x02, 0x03, 0x04}),
			synSimpleBlock(1, 20, []byte{0x05, 0x06, 0x07, 0x08}),
		),
	)

	// One SimpleBlock here is 1 ID byte + 1 size byte + 8 content bytes; dropping 9
	// leaves its ID alone, so the header is incomplete.
	_, tail, err := feedAndFinalize(t, cutInside(raw, 9))
	if !errors.Is(err, parser.ErrTruncated) {
		t.Fatalf("Finalize = %v, want the truncation reported", err)
	}
	if len(tail) != 1 || !tail[0].Truncated {
		t.Fatalf("Finalize returned %d fragments, want 1 marked Truncated", len(tail))
	}
	if len(tail[0].Blocks) != 1 {
		t.Errorf("salvaged %d blocks, want the 1 that decoded", len(tail[0].Blocks))
	}
	if n := len(tail[0].Values(matroska.IDSimpleBlock)); n != 1 {
		t.Errorf("Cluster retains %d SimpleBlock elements, want 1: the second has no complete header", n)
	}
}

// TestTruncatedTailLatchesTheError pins that salvage did not soften terminality:
// the error is returned, latched, and reported again by every later call.
func TestTruncatedTailLatchesTheError(t *testing.T) {
	raw := synFragment(
		synTracks(synTrackEntry(1, "AUDIO_FROM_CUSTOMER")),
		synUnknownCluster(0,
			synSimpleBlock(1, 0, []byte{0x01, 0x02, 0x03, 0x04}),
			synSimpleBlock(1, 20, []byte{0x05, 0x06, 0x07, 0x08}),
		),
	)

	a := fragment.New()
	if _, err := a.Feed(cutInside(raw, 3)); err != nil {
		t.Fatalf("Feed: %v", err)
	}
	tail, err := a.Finalize()
	if !errors.Is(err, parser.ErrTruncated) {
		t.Fatalf("Finalize = %v, want the truncation diagnosis", err)
	}
	if len(tail) != 1 || !tail[0].Truncated {
		t.Fatalf("Finalize returned %d fragments, want 1 marked Truncated", len(tail))
	}
	if _, err2 := a.Feed(nil); !errors.Is(err2, parser.ErrTruncated) {
		t.Fatalf("Feed after the truncation = %v, want the latched error", err2)
	}
	frags, err3 := a.Finalize()
	if !errors.Is(err3, parser.ErrTruncated) {
		t.Fatalf("Finalize again = %v, want the latched error", err3)
	}
	if len(frags) != 0 {
		t.Fatalf("the salvaged fragment was emitted twice: %d fragments on the second Finalize", len(frags))
	}
}

// TestTruncatedTailWithNothingDecoded is decision 2's corollary: the cut falls in
// the FIRST block, so there is nothing to salvage but the Cluster itself. It is
// still emitted. Whether an almost-empty fragment is worth anything is a judgement
// about content, made by the caller with len(Blocks); whether one exists at all is
// structural, and the Cluster was open.
func TestTruncatedTailWithNothingDecoded(t *testing.T) {
	raw := synFragment(
		synTracks(synTrackEntry(1, "AUDIO_FROM_CUSTOMER")),
		synUnknownCluster(7, synSimpleBlock(1, 0, []byte{0x01, 0x02, 0x03, 0x04})),
	)

	_, tail, err := feedAndFinalize(t, cutInside(raw, 3))
	if !errors.Is(err, parser.ErrTruncated) {
		t.Fatalf("Finalize = %v, want the truncation still reported", err)
	}
	if len(tail) != 1 {
		t.Fatalf("Finalize returned %d fragments, want the 1 open Cluster", len(tail))
	}
	frag := tail[0]
	if !frag.Truncated {
		t.Error("fragment is not marked Truncated")
	}
	if len(frag.Blocks) != 0 {
		t.Errorf("salvaged %d blocks, want none: the only block was cut", len(frag.Blocks))
	}
	// The Cluster metadata that DID arrive is still there, which is the reason an
	// empty fragment is not nothing.
	if ts := frag.ClusterTimestamp(); ts != 7 {
		t.Errorf("ClusterTimestamp = %d, want 7", ts)
	}
}

// TestTruncationBeforeAnyClusterSalvagesNothing is the other side of the same
// condition: the stream died inside Segment-level metadata, so no Cluster was open
// and there is no fragment to hand over. The error is all there is.
func TestTruncationBeforeAnyClusterSalvagesNothing(t *testing.T) {
	raw := ebmltest.Encode(
		synEBMLHeader(),
		ebmltest.UnknownMaster(matroska.IDSegment,
			synTracks(synTrackEntry(1, "AUDIO_FROM_CUSTOMER")),
		),
	)

	_, tail, err := feedAndFinalize(t, cutInside(raw, 3))
	if !errors.Is(err, parser.ErrTruncated) {
		t.Fatalf("Finalize = %v, want the truncation reported", err)
	}
	if len(tail) != 0 {
		t.Fatalf("Finalize returned %d fragments; no Cluster was ever open", len(tail))
	}
}

// TestTruncatedTailKeepsEarlierFragments is the shape the consumer measured: a run
// of complete documents followed by one that stops mid-element. The complete ones
// are unaffected and unmarked; the salvaged one is the single extra fragment.
func TestTruncatedTailKeepsEarlierFragments(t *testing.T) {
	complete := synFragment(
		synTracks(synTrackEntry(1, "AUDIO_FROM_CUSTOMER")),
		synUnknownCluster(0, synSimpleBlock(1, 0, []byte{0x01, 0x02, 0x03, 0x04})),
	)
	cut := cutInside(synFragment(
		synTracks(synTrackEntry(1, "AUDIO_FROM_CUSTOMER")),
		synUnknownCluster(100,
			synSimpleBlock(1, 0, []byte{0x05, 0x06, 0x07, 0x08}),
			synSimpleBlock(1, 20, []byte{0x09, 0x0A, 0x0B, 0x0C}),
		),
	), 3)

	var raw []byte
	raw = append(raw, complete...)
	raw = append(raw, complete...)
	raw = append(raw, cut...)

	fromFeed, tail, err := feedAndFinalize(t, raw)
	if !errors.Is(err, parser.ErrTruncated) {
		t.Fatalf("Finalize = %v, want the truncation reported", err)
	}
	if len(fromFeed) != 2 {
		t.Fatalf("Feed emitted %d fragments, want the 2 complete documents", len(fromFeed))
	}
	for i, f := range fromFeed {
		if f.Truncated {
			t.Errorf("complete fragment %d is marked Truncated", i)
		}
	}
	if len(tail) != 1 || !tail[0].Truncated {
		t.Fatalf("Finalize returned %d fragments, want the 1 salvaged one", len(tail))
	}
	if len(tail[0].Blocks) != 1 {
		t.Errorf("salvaged %d blocks, want the 1 that decoded", len(tail[0].Blocks))
	}
}

// TestCleanStreamIsNeverTruncated pins the flag's other half: on a stream that ends
// at an element boundary, no fragment carries it.
func TestCleanStreamIsNeverTruncated(t *testing.T) {
	raw := synFragment(
		synTracks(synTrackEntry(1, "AUDIO_FROM_CUSTOMER")),
		synUnknownCluster(0, synSimpleBlock(1, 0, []byte{0x01, 0x02, 0x03, 0x04})),
	)

	for i, f := range runWhole(t, raw) {
		if f.Truncated {
			t.Errorf("fragment %d of a complete stream is marked Truncated", i)
		}
	}
}

// TestTruncatedNonBlockLeafIsMarked covers the retained-payload path rather than
// the decoded one: the cut falls inside an ordinary leaf whose bytes the assembler
// was going to KEEP. The element must not be left looking complete-but-empty, since
// a nil Payload with Truncated unset is how an element with no bytes reads.
func TestTruncatedNonBlockLeafIsMarked(t *testing.T) {
	raw := synFragment(
		synTracks(synTrackEntry(1, "AUDIO_FROM_CUSTOMER")),
		synUnknownCluster(0,
			synSimpleBlock(1, 0, []byte{0x01, 0x02, 0x03, 0x04}),
			ebmltest.Leaf(matroska.IDVoid, []byte{0xAA, 0xBB, 0xCC, 0xDD, 0xEE, 0xFF}),
		),
	)

	_, tail, err := feedAndFinalize(t, cutInside(raw, 3))
	if !errors.Is(err, parser.ErrTruncated) {
		t.Fatalf("Finalize = %v, want the truncation reported", err)
	}
	if len(tail) != 1 {
		t.Fatalf("Finalize returned %d fragments, want 1", len(tail))
	}
	voids := tail[0].Values(matroska.IDVoid)
	if len(voids) != 1 {
		t.Fatalf("Cluster retains %d Void elements, want the 1 that was cut", len(voids))
	}
	if v := voids[0]; !v.Truncated || v.Payload != nil {
		t.Errorf("cut Void: Truncated=%v Payload=%v, want true and nil", v.Truncated, v.Payload)
	}
}

// TestTruncatedTailIsSplitInvariant holds salvage to the same standard as every
// other property of this package: the fragments, their blocks and their Truncated
// flags do not depend on how the bytes were chunked across Feed calls.
func TestTruncatedTailIsSplitInvariant(t *testing.T) {
	raw := cutInside(synFragment(
		synTracks(synTrackEntry(1, "AUDIO_FROM_CUSTOMER")),
		synUnknownCluster(0,
			synSimpleBlock(1, 0, []byte{0x01, 0x02, 0x03, 0x04}),
			synSimpleBlock(1, 20, []byte{0x05, 0x06, 0x07, 0x08}),
			synSimpleBlock(1, 40, []byte{0x09, 0x0A, 0x0B, 0x0C}),
		),
	), 3)

	for _, size := range []int{1, 2, 3, 7, 16, 64, len(raw)} {
		a := fragment.New()
		var got []*fragment.Fragment
		for off := 0; off < len(raw); off += size {
			end := off + size
			if end > len(raw) {
				end = len(raw)
			}
			frags, err := a.Feed(raw[off:end])
			if err != nil {
				t.Fatalf("chunk %d: Feed: %v", size, err)
			}
			got = append(got, frags...)
		}
		tail, err := a.Finalize()
		if !errors.Is(err, parser.ErrTruncated) {
			t.Fatalf("chunk %d: Finalize = %v, want the truncation reported", size, err)
		}
		got = append(got, tail...)

		if len(got) != 1 {
			t.Fatalf("chunk %d: got %d fragments, want 1", size, len(got))
		}
		if !got[0].Truncated {
			t.Errorf("chunk %d: fragment is not marked Truncated", size)
		}
		if len(got[0].Blocks) != 2 {
			t.Errorf("chunk %d: got %d blocks, want 2", size, len(got[0].Blocks))
		}
		if n := len(got[0].Values(matroska.IDSimpleBlock)); n != 3 {
			t.Errorf("chunk %d: Cluster retains %d SimpleBlock elements, want 3", size, n)
		}
	}
}
