package fragment_test

import (
	"bytes"
	"testing"

	"github.com/yacchi/ebml/ext/fragment"
	"github.com/yacchi/ebml/internal/ebmltest"
	"github.com/yacchi/ebml/matroska"
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

func TestUnknownSizeClusterEmitsBeforeTheNextDocument(t *testing.T) {
	raw := unknownClusterDocument(
		unknownSizeCluster(synSimpleBlock(1, 0, []byte{1}), synSimpleBlock(1, 1, []byte{2})),
		synTags("next", "document"),
	)
	a := fragment.New()
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
	a := fragment.New()
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
	a := fragment.New()
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

// Q3 in KVS-CONSUMER-FEEDBACK.md is an open design question, not an endorsed
// limitation: post-Cluster metadata is outside the emitted Fragment's scope.
func TestPostClusterTagsAreNotInTheEmittedFragment(t *testing.T) {
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
	frags, err := fragment.New().Feed(raw[:offset+1])
	if err != nil {
		t.Fatalf("Feed: %v", err)
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
	} {
		if _, ok := tags[name]; !ok {
			t.Errorf("Tags() missing pre-Cluster key %q", name)
		}
	}
	for _, name := range []string{
		"AWS_KINESISVIDEO_MILLIS_BEHIND_NOW",
		"AWS_KINESISVIDEO_CONTINUATION_TOKEN",
	} {
		if _, ok := tags[name]; ok {
			t.Errorf("Tags() contains post-Cluster key %q", name)
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
			if len(got) != 1 || len(got[0].Blocks) != 2 {
				t.Fatalf("got %d fragments and %d blocks, want 1 and 2", len(got), len(got[0].Blocks))
			}
		})
	}
}
