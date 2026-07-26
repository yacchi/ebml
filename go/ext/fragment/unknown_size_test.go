package fragment_test

import (
	"bytes"
	"testing"

	"github.com/yacchi/ebml/ext/fragment"
	"github.com/yacchi/ebml/internal/ebmltest"
	"github.com/yacchi/ebml/matroska"
	"github.com/yacchi/ebml/parser"
)

func unknownSizeCluster(blocks ...ebmltest.Node) ebmltest.Node {
	children := append([]ebmltest.Node{ebmltest.Uint(matroska.IDTimestamp, 0)}, blocks...)
	return ebmltest.UnknownMaster(matroska.IDCluster, children...)
}

func unknownClusterDocument(cluster, tail ebmltest.Node) []byte {
	return ebmltest.Encode(
		synEBMLHeader(),
		ebmltest.UnknownMaster(matroska.IDSegment,
			synInfo(),
			synTracks(synTrackEntry(1, "AUDIO_FROM_CUSTOMER")),
			synTags("producer", "test"),
			cluster,
			tail,
		),
	)
}

// eagerEmit is the documented idiom for taking a fragment at its Cluster's end
// instead of waiting for its Segment-level metadata to settle. The tests below use
// it because their subject is WHERE THE CLUSTER CLOSES -- a structural question the
// delivery rule does not touch -- and closing during Feed is only observable
// without the wait.
func eagerEmit() fragment.Option {
	return fragment.WithMetadataComplete(
		func(*fragment.Fragment, parser.ElementID) bool { return true },
	)
}

func TestUnknownSizeClusterEmitsBeforeTheNextDocument(t *testing.T) {
	raw := unknownClusterDocument(
		unknownSizeCluster(synSimpleBlock(1, 0, []byte{1}), synSimpleBlock(1, 1, []byte{2})),
		synTags("next", "document"),
	)
	a := fragment.New(eagerEmit())
	frags, err := a.Feed(raw)
	if err != nil {
		t.Fatalf("Feed: %v", err)
	}
	if len(frags) != 1 {
		t.Fatalf("Feed returned %d fragments, want 1", len(frags))
	}
	if len(frags[0].Blocks) != 2 {
		t.Fatalf("Blocks = %d, want 2", len(frags[0].Blocks))
	}
}

func TestUnknownSizeClusterEndsAtTheNextCluster(t *testing.T) {
	first := unknownSizeCluster(synSimpleBlock(1, 0, []byte{1}))
	second := unknownSizeCluster(synSimpleBlock(1, 1, []byte{2}))
	prefix := ebmltest.Encode(synEBMLHeader(), ebmltest.UnknownMaster(matroska.IDSegment, first))
	next := ebmltest.Encode(second)
	a := fragment.New()
	if frags, err := a.Feed(prefix); err != nil {
		t.Fatalf("first Feed: %v", err)
	} else if len(frags) != 0 {
		t.Fatalf("first Feed returned %d fragments, want 0", len(frags))
	}
	frags, err := a.Feed(next)
	if err != nil {
		t.Fatalf("second Feed: %v", err)
	}
	if len(frags) != 1 || len(frags[0].Blocks) != 1 {
		t.Fatalf("second Feed returned %d fragments with %d blocks, want 1 with 1",
			len(frags), len(frags[0].Blocks))
	}
}

func TestUnregisteredElementDoesNotEndACluster(t *testing.T) {
	raw := unknownClusterDocument(
		unknownSizeCluster(
			synSimpleBlock(1, 0, []byte{1}),
			ebmltest.Uint(ebmltest.UnassignedLeafID, 42),
			synSimpleBlock(1, 1, []byte{2}),
		),
		synTags("next", "document"),
	)
	frags := runWhole(t, raw)
	if len(frags) != 1 || len(frags[0].Blocks) != 2 {
		t.Fatalf("got %d fragments and %d blocks, want 1 and 2", len(frags), len(frags[0].Blocks))
	}
}

func TestGlobalElementDoesNotEndACluster(t *testing.T) {
	raw := unknownClusterDocument(
		unknownSizeCluster(
			synSimpleBlock(1, 0, []byte{1}),
			ebmltest.Leaf(matroska.IDVoid, []byte{0}),
			ebmltest.Leaf(matroska.IDCRC32, []byte{0, 0, 0, 0}),
			synSimpleBlock(1, 1, []byte{2}),
		),
		synTags("next", "document"),
	)
	frags := runWhole(t, raw)
	if len(frags) != 1 || len(frags[0].Blocks) != 2 {
		t.Fatalf("got %d fragments and %d blocks, want 1 and 2", len(frags), len(frags[0].Blocks))
	}
}

func TestKnownSizeClusterStillEmitsAtItsDeclaredEnd(t *testing.T) {
	raw := loadHex(t, "known_size_cluster")
	a := fragment.New(eagerEmit())
	frags, err := a.Feed(raw)
	if err != nil {
		t.Fatalf("Feed: %v", err)
	}
	if len(frags) != 1 || len(frags[0].Blocks) != 3 {
		t.Fatalf("got %d fragments and %d blocks, want 1 and 3", len(frags), len(frags[0].Blocks))
	}
}

func TestConnectRealShapeUnknownClusterEmitsWithoutFinalize(t *testing.T) {
	raw := loadHex(t, "connect_real_shape")
	a := fragment.New(eagerEmit())
	frags, err := a.Feed(raw)
	if err != nil {
		t.Fatalf("Feed: %v", err)
	}
	if len(frags) != 1 {
		t.Fatalf("Feed returned %d fragments, want 1", len(frags))
	}
	if len(frags[0].Blocks) != 4 {
		t.Fatalf("Blocks = %d, want 4", len(frags[0].Blocks))
	}
	seen := map[uint64]bool{}
	for _, b := range frags[0].Blocks {
		seen[b.TrackNumber] = true
	}
	if !seen[1] || !seen[2] {
		t.Fatalf("track numbers = %v, want both 1 and 2", seen)
	}
}

// Q3 in KVS-CONSUMER-FEEDBACK.md asked how metadata written AFTER the Cluster it
// describes should reach a consumer, and this test carries the ANSWER, which has
// changed: it reaches the consumer in the fragment itself, because delivery waits
// for the Segment-level metadata to settle.
//
// The earlier answer was that a Fragment is a snapshot taken when its Cluster
// closed, so post-Cluster metadata was outside it by construction and a consumer
// wanting it went to ext/scope. Field measurement retired that: a consumer reading
// tags at delivery lost the keys its stream had already stated -- 112 fragments
// became 27 with the identity tags missing -- and the loss was read-size dependent,
// so it did not reproduce in tests that fed a whole capture at once. A layout every
// consumer must compensate for belongs in the library.
//
// connect_real_shape is the real Amazon Connect layout: two Tags before the Cluster
// and two after, with MILLIS_BEHIND_NOW and CONTINUATION_TOKEN only in the latter.
func TestPostClusterTagsAreInTheDeliveredFragment(t *testing.T) {
	frags := runWhole(t, loadHex(t, "connect_real_shape"))
	if len(frags) != 1 {
		t.Fatalf("got %d fragments, want 1", len(frags))
	}
	tags := frags[0].Tags()
	for _, name := range []string{
		"ContactId",
		"InstanceId",
		"MimeType",
		"AUDIO_TO_CUSTOMER",
		"AUDIO_FROM_CUSTOMER",
		"AWS_KINESISVIDEO_FRAGMENT_NUMBER",
		"AWS_KINESISVIDEO_SERVER_TIMESTAMP",
		"AWS_KINESISVIDEO_PRODUCER_TIMESTAMP",
		// Written AFTER the Cluster, and present all the same.
		"AWS_KINESISVIDEO_MILLIS_BEHIND_NOW",
		"AWS_KINESISVIDEO_CONTINUATION_TOKEN",
	} {
		if _, ok := tags[name]; !ok {
			t.Errorf("Tags() missing key %q", name)
		}
	}
}

// TestEagerEmissionStillExcludesPostClusterTags is the other half of the same
// answer: a consumer that asks for the Cluster's-end snapshot gets exactly that,
// post-Cluster tags included in nothing. The old default is one option away, and
// its consequence is documented rather than hidden.
func TestEagerEmissionStillExcludesPostClusterTags(t *testing.T) {
	raw := loadHex(t, "connect_real_shape")
	tagsID := []byte{0x12, 0x54, 0xc3, 0x67}
	offset := 0
	for i := 0; i < 4; i++ {
		next := bytes.Index(raw[offset:], tagsID)
		if next < 0 {
			t.Fatal("fixture does not contain the expected pre-Cluster Tags elements")
		}
		offset += next + len(tagsID)
	}
	frags, err := fragment.New(eagerEmit()).Feed(raw[:offset+1])
	if err != nil {
		t.Fatalf("Feed: %v", err)
	}
	if len(frags) != 1 {
		t.Fatalf("got %d fragments, want 1", len(frags))
	}
	tags := frags[0].Tags()
	if _, ok := tags["AWS_KINESISVIDEO_FRAGMENT_NUMBER"]; !ok {
		t.Error("Tags() missing a pre-Cluster key")
	}
	for _, name := range []string{
		"AWS_KINESISVIDEO_MILLIS_BEHIND_NOW",
		"AWS_KINESISVIDEO_CONTINUATION_TOKEN",
	} {
		if _, ok := tags[name]; ok {
			t.Errorf("eager emission saw post-Cluster key %q", name)
		}
	}
}

func TestUnknownSizeClusterSplitInvariance(t *testing.T) {
	raw := unknownClusterDocument(
		unknownSizeCluster(synSimpleBlock(1, 0, []byte{1}), synSimpleBlock(1, 1, []byte{2})),
		synTags("next", "document"),
	)
	patterns := []struct {
		name   string
		chunks [][]byte
	}{
		{"one_byte", splitOneByte(raw)},
		{"fibonacci", splitFibonacci(raw)},
		{"random", splitRandom(raw, 12345, 7)},
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
			// The trailing Tags is the document's last element, so the fragment waits
			// for the input to end before its metadata can be called settled.
			tail, err := a.Finalize()
			if err != nil {
				t.Fatalf("Finalize: %v", err)
			}
			got = append(got, tail...)
			if len(got) != 1 || len(got[0].Blocks) != 2 {
				t.Fatalf("got %d fragments and %d blocks, want 1 and 2", len(got), len(got[0].Blocks))
			}
			if tag, ok := got[0].Tag("next"); !ok || tag != "document" {
				t.Fatalf("trailing tag = %q, %v; want it settled before delivery", tag, ok)
			}
		})
	}
}
