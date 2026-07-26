package fragment_test

import (
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/yacchi/ebml/impl/go/ext/fragment"
	"github.com/yacchi/ebml/impl/go/internal/ebmltest"
	"github.com/yacchi/ebml/impl/go/matroska"
	"github.com/yacchi/ebml/impl/go/parser"
)

// ---- Self-contained stream shapes, built through internal/ebmltest. ----
//
// The SHAPES here are deliberately NOT shared with internal/kvsgen: streams built
// here are independent of the generator, so a bug shared between the generator and
// the assembler cannot hide behind green tests. They also let one test state exactly
// which elements are present and which are absent -- the cases a fixture corpus
// would need a fixture each for. The ENCODING, by contrast, is package writer's, the
// library's only EBML encoder (ebmltest is a thin shaping layer over it), so no test
// carries an encoder of its own -- not even for the unknown-size Segment, which is
// the writer's UnknownSize strategy rather than a hand-assembled header.

func synEBMLHeader() ebmltest.Node {
	return ebmltest.Master(matroska.IDEBML,
		ebmltest.Uint(matroska.IDEBMLVersion, 1),
		ebmltest.Uint(matroska.IDEBMLReadVersion, 1),
		ebmltest.String(matroska.IDDocType, "matroska"),
	)
}

// synTrackEntry builds one TrackEntry. Passing name == "" OMITS the Name element
// entirely, as opposed to emitting an empty one, so the element-absent path is
// testable distinctly from an empty-but-present element.
func synTrackEntry(number uint64, name string) ebmltest.Node {
	children := []ebmltest.Node{
		ebmltest.Uint(matroska.IDTrackNumber, number),
		ebmltest.Uint(matroska.IDTrackType, 2),
		ebmltest.String(matroska.IDCodecID, "A_PCM/INT/LIT"),
	}
	if name != "" {
		children = append(children, ebmltest.UTF8(matroska.IDName, name))
	}
	return ebmltest.Master(matroska.IDTrackEntry, children...)
}

func synTracks(entries ...ebmltest.Node) ebmltest.Node {
	return ebmltest.Master(matroska.IDTracks, entries...)
}

func synSimpleBlock(trackNumber byte, timecode int16, audio []byte) ebmltest.Node {
	content := []byte{0x80 | trackNumber}
	content = append(content, byte(uint16(timecode)>>8), byte(uint16(timecode)))
	content = append(content, 0x00) // flags
	content = append(content, audio...)
	return ebmltest.Leaf(matroska.IDSimpleBlock, content)
}

func synCluster(clusterTS uint64, blocks ...ebmltest.Node) ebmltest.Node {
	children := append([]ebmltest.Node{ebmltest.Uint(matroska.IDTimestamp, clusterTS)}, blocks...)
	return ebmltest.Master(matroska.IDCluster, children...)
}

// synInfo builds an Info element from arbitrary children, so a test selects exactly
// which of SegmentUUID/TimestampScale are present.
func synInfo(children ...ebmltest.Node) ebmltest.Node {
	return ebmltest.Master(matroska.IDInfo, children...)
}

func synSegmentUUID(uid []byte) ebmltest.Node {
	return ebmltest.Leaf(matroska.IDSegmentUUID, uid)
}

func synTimestampScale(scale uint64) ebmltest.Node {
	return ebmltest.Uint(matroska.IDTimestampScale, scale)
}

// synTags builds a Tags element holding one Tag with a single SimpleTag, which is
// all these tests need to observe metadata attribution.
func synTags(name, value string) ebmltest.Node {
	return ebmltest.Master(matroska.IDTags,
		ebmltest.Master(matroska.IDTag,
			ebmltest.Master(matroska.IDSimpleTag,
				ebmltest.UTF8(matroska.IDTagName, name),
				ebmltest.UTF8(matroska.IDTagString, value),
			),
		),
	)
}

// synFragment encodes the given Segment children as a full EBML-header +
// unknown-size Segment unit, matching a real KVS fragment.
func synFragment(segmentChildren ...ebmltest.Node) []byte {
	return ebmltest.Encode(
		synEBMLHeader(),
		ebmltest.UnknownMaster(matroska.IDSegment, segmentChildren...),
	)
}

// runWhole feeds a byte slice in a single Feed call plus Finalize. These tests are
// about the semantics of specific element combinations, not split invariance
// (covered by TestSplitInvariance), so one chunk keeps each case legible.
func runWhole(t *testing.T, raw []byte, opts ...fragment.Option) []*fragment.Fragment {
	t.Helper()
	return run(t, [][]byte{raw}, opts...).all()
}

// TestInfoAbsent covers the default path: a Segment with NO Info element at all
// falls back to the spec TimestampScale and has no SegmentUUID to report.
func TestInfoAbsent(t *testing.T) {
	raw := synFragment(
		synTracks(synTrackEntry(1, "AUDIO_FROM_CUSTOMER")),
		synCluster(0, synSimpleBlock(1, 0, []byte{0x01, 0x02})),
	)
	frags := runWhole(t, raw)
	if len(frags) != 1 {
		t.Fatalf("want 1 fragment, got %d", len(frags))
	}
	f := frags[0]
	if f.Segment.Find(matroska.IDInfo).Exists() {
		t.Fatal("Segment has an Info element it never declared")
	}
	if got := f.Value(matroska.IDSegmentUUID); got != nil {
		t.Fatalf("SegmentUUID = %v, want nil (no Info element at all)", got)
	}
	if got := f.TimestampScale(); got != fragment.DefaultTimestampScale {
		t.Fatalf("TimestampScale = %d, want the %d default", got, fragment.DefaultTimestampScale)
	}
}

// TestInfoPresentTimestampScaleAbsent covers the partial case: Info is present but
// declares only SegmentUUID, so the scale still falls back while the element that
// IS present is readable.
func TestInfoPresentTimestampScaleAbsent(t *testing.T) {
	uid := []byte{0xAA, 0xBB, 0xCC, 0xDD}
	raw := synFragment(
		synInfo(synSegmentUUID(uid)), // no TimestampScale child
		synTracks(synTrackEntry(1, "AUDIO_FROM_CUSTOMER")),
		synCluster(0, synSimpleBlock(1, 0, []byte{0x01, 0x02})),
	)
	f := runWhole(t, raw)[0]
	if got := f.Segment.Find(matroska.IDInfo, matroska.IDSegmentUUID).Bytes(); !reflect.DeepEqual(got, uid) {
		t.Fatalf("SegmentUUID = %x, want %x", got, uid)
	}
	if got := f.TimestampScale(); got != fragment.DefaultTimestampScale {
		t.Fatalf("TimestampScale = %d, want the default (the element is absent)", got)
	}
}

// TestNoStateLeaksAcrossSegments targets the Segment reset directly: three
// Segments where the MIDDLE one has no Info at all, sandwiched between two that
// declare distinct synthetic UUIDs and scales. If entering a Segment did not drop
// the previous Segment's state, the middle fragment would inherit Segment 1's
// values instead of falling back, and Segment 3's would read as Segment 1's.
func TestNoStateLeaksAcrossSegments(t *testing.T) {
	uidA := []byte{0x01, 0x02, 0x03, 0x04, 0x05, 0x06, 0x07, 0x08}
	uidC := []byte{0xF1, 0xF2, 0xF3, 0xF4, 0xF5, 0xF6, 0xF7, 0xF8}

	frag1 := synFragment(
		synInfo(synSegmentUUID(uidA), synTimestampScale(1_000_000)),
		synTracks(synTrackEntry(1, "AUDIO_FROM_CUSTOMER")),
		synCluster(0, synSimpleBlock(1, 0, []byte{0x01})),
	)
	frag2 := synFragment( // deliberately NO Info element
		synTracks(synTrackEntry(1, "AUDIO_FROM_CUSTOMER")),
		synCluster(0, synSimpleBlock(1, 0, []byte{0x02})),
	)
	frag3 := synFragment(
		synInfo(synSegmentUUID(uidC), synTimestampScale(2_000_000)),
		synTracks(synTrackEntry(1, "AUDIO_FROM_CUSTOMER")),
		synCluster(0, synSimpleBlock(1, 0, []byte{0x03})),
	)
	raw := ebmltest.Concat(frag1, frag2, frag3)

	// Every chunking, because closing one Segment on the next fragment's header is
	// exactly where a boundary could be missed.
	for _, sp := range []struct {
		label  string
		chunks [][]byte
	}{
		{"whole", [][]byte{raw}},
		{"one_byte", splitOneByte(raw)},
		{"fibonacci", splitFibonacci(raw)},
	} {
		t.Run(sp.label, func(t *testing.T) {
			frags := run(t, sp.chunks).all()
			if len(frags) != 3 {
				t.Fatalf("want 3 fragments, got %d", len(frags))
			}
			if got := frags[0].Value(matroska.IDSegmentUUID).Bytes(); !reflect.DeepEqual(got, uidA) {
				t.Fatalf("fragment 0 SegmentUUID = %x, want %x", got, uidA)
			}
			if got := frags[0].TimestampScale(); got != 1_000_000 {
				t.Fatalf("fragment 0 TimestampScale = %d", got)
			}
			if got := frags[1].Value(matroska.IDSegmentUUID); got != nil {
				t.Fatalf("fragment 1 SegmentUUID = %x, want none (must not leak Segment 1's)", got.Bytes())
			}
			if got := frags[1].TimestampScale(); got != fragment.DefaultTimestampScale {
				t.Fatalf("fragment 1 TimestampScale = %d, want the default (must not leak Segment 1's)", got)
			}
			if got := frags[2].Value(matroska.IDSegmentUUID).Bytes(); !reflect.DeepEqual(got, uidC) {
				t.Fatalf("fragment 2 SegmentUUID = %x, want %x (leaked from an earlier Segment?)", got, uidC)
			}
			if got := frags[2].TimestampScale(); got != 2_000_000 {
				t.Fatalf("fragment 2 TimestampScale = %d, want 2000000 (leaked?)", got)
			}
			// Each Segment closes at its boundary, so its tree stands alone.
			for i, f := range frags {
				if got := len(f.Segment.ChildrenByID(matroska.IDTracks)); got != 1 {
					t.Fatalf("fragment %d retained %d Tracks elements, want 1", i, got)
				}
			}
		})
	}
}

// TestTrackNameAbsent checks a TrackEntry with no Name element at all: the lookup
// misses instead of matching an empty name, and nothing panics.
func TestTrackNameAbsent(t *testing.T) {
	raw := synFragment(
		synTracks(synTrackEntry(1, "")), // "" => Name element omitted entirely
		synCluster(0, synSimpleBlock(1, 0, []byte{0x01})),
	)
	f := runWhole(t, raw)[0]
	tracks := f.Tracks()
	if len(tracks) != 1 {
		t.Fatalf("want 1 track, got %d", len(tracks))
	}
	if got := tracks[0].Find(matroska.IDName).AsString(); got != "" {
		t.Fatalf("Name = %q, want \"\" (no Name element present)", got)
	}
	if _, ok := f.TrackByName(""); ok {
		t.Fatal("TrackByName(\"\") must not match a track that declares no Name")
	}
	if _, ok := f.Track(1); !ok {
		t.Fatal("Track(1) should still be found")
	}
}

// TestBinaryPayloadIsNotStringMangled checks a binary payload is retained as raw
// bytes: a SegmentUUID containing NUL and invalid UTF-8 must come back unaltered,
// which it would not if the retained bytes had passed through a string decoder.
func TestBinaryPayloadIsNotStringMangled(t *testing.T) {
	// 0x00 (would truncate a C string), 0xFF/0x80/0xC0 (invalid standalone UTF-8
	// lead/continuation bytes), plus a plain ASCII byte for contrast.
	uid := []byte{0x00, 0xFF, 0x80, 0xC0, 0x41, 0x00, 0xFE}
	raw := synFragment(
		synInfo(synSegmentUUID(uid), synTimestampScale(1_000_000)),
		synTracks(synTrackEntry(1, "AUDIO_FROM_CUSTOMER")),
		synCluster(0, synSimpleBlock(1, 0, []byte{0x01})),
	)
	f := runWhole(t, raw)[0]
	got := f.Value(matroska.IDSegmentUUID).Bytes()
	if len(got) != len(uid) {
		t.Fatalf("SegmentUUID length = %d, want %d (truncated? %x vs %x)", len(got), len(uid), got, uid)
	}
	if !reflect.DeepEqual(got, uid) {
		t.Fatalf("SegmentUUID = %x, want %x (bytes altered -- must be a raw copy)", got, uid)
	}
}

// TestLateMetadataIsNotAttributedToAnEarlierFragment pins the emission-time
// semantics of a Segment whose metadata does NOT all precede its first Cluster: a
// Fragment carries what had completed when its Cluster closed, so the Tags that
// arrive between the two Clusters are absent from the first Fragment when it is
// delivered and present in the second.
//
// It also pins the documented consequence of the two Fragments SHARING one
// Segment node, which is why this test inspects the first Fragment at the moment
// it is delivered.
// TestSegmentMetadataHasSettledOnDelivery is the emission rule: a fragment is
// assembled at its Cluster's end and HELD until its Segment-level metadata can no
// longer grow before the next Cluster, so metadata written after the Cluster is in
// the view the caller is handed.
//
// This is deliberately the opposite of attributing tags by position. RFC 9559 makes
// Segment tags cumulative and POSITIONLESS, and every fragment of a Segment shares
// its tree, so a tag that differs between two fragments of one Segment was only ever
// an artifact of WHEN the consumer looked. Emitting at the Cluster's end made that
// artifact the default and cost a consumer the tags its stream had already stated.
func TestSegmentMetadataHasSettledOnDelivery(t *testing.T) {
	tracks := synTracks(synTrackEntry(1, "AUDIO_FROM_CUSTOMER"))
	cluster1 := synCluster(0, synSimpleBlock(1, 0, []byte{0x01}))
	lateTags := synTags("ContactId", "late")
	cluster2 := synCluster(1024, synSimpleBlock(1, 0, []byte{0x02}))
	raw := synFragment(tracks, cluster1, lateTags, cluster2)

	// Feed exactly up to the end of the first Cluster, then the rest.
	const segmentHeaderLen = 4 + 8 // Segment ID plus the unknown-size VINT
	cut := len(ebmltest.Encode(synEBMLHeader())) + segmentHeaderLen +
		len(ebmltest.Encode(tracks)) + len(ebmltest.Encode(cluster1))

	a := fragment.New()
	first, err := a.Feed(raw[:cut])
	if err != nil {
		t.Fatalf("Feed: %v", err)
	}
	if len(first) != 0 {
		t.Fatalf("got %d fragments from the first Feed; the Cluster ended but its metadata had not settled", len(first))
	}

	second, err := a.Feed(raw[cut:])
	if err != nil {
		t.Fatalf("Feed: %v", err)
	}
	// Fragment 1 is released by fragment 2's Cluster header, which is the proof that
	// no further Segment-level metadata belongs before it.
	if len(second) != 1 {
		t.Fatalf("got %d fragments from the second Feed, want the 1 released by the next Cluster", len(second))
	}
	if got, ok := fragTag(second[0], "ContactId"); !ok || got != "late" {
		t.Fatalf("fragment 1 ContactId = %q, %v; want the tag that had settled by delivery", got, ok)
	}
	if got := len(second[0].Blocks); got != 1 || second[0].Blocks[0].Frames[0][0] != 0x01 {
		t.Fatalf("released fragment carries %d blocks; want fragment 1's own block", got)
	}

	tail, err := a.Finalize()
	if err != nil {
		t.Fatalf("Finalize: %v", err)
	}
	if len(tail) != 1 {
		t.Fatalf("Finalize released %d fragments, want fragment 2", len(tail))
	}
	if got, ok := fragTag(tail[0], "ContactId"); !ok || got != "late" {
		t.Fatalf("fragment 2 ContactId = %q, %v; the tag is the Segment's, not one fragment's", got, ok)
	}
	// Both Fragments share the one Segment node, as documented -- which is why the
	// tag is the same for both.
	if second[0].Segment != tail[0].Segment {
		t.Fatal("Fragments of one Segment must share its tree")
	}
}

// TestMetadataCompleteReleasesEarly is the escape hatch: a consumer that knows its
// stream's layout releases on the element its producer writes last, and pays no
// wait at all. The knowledge stays in the predicate -- this package never learns a
// tag name.
func TestMetadataCompleteReleasesEarly(t *testing.T) {
	tracks := synTracks(synTrackEntry(1, "AUDIO_FROM_CUSTOMER"))
	cluster := synCluster(0, synSimpleBlock(1, 0, []byte{0x01}))
	trailing := synTags("LAST", "yes")
	raw := synFragment(tracks, cluster, trailing)

	var asked []parser.ElementID
	frags := runWhole(t, raw, fragment.WithMetadataComplete(
		func(pending *fragment.Fragment, completed parser.ElementID) bool {
			asked = append(asked, completed)
			_, done := fragTag(pending, "LAST")
			return done
		},
	))
	if len(frags) != 1 {
		t.Fatalf("got %d fragments, want 1", len(frags))
	}
	if got, ok := fragTag(frags[0], "LAST"); !ok || got != "yes" {
		t.Fatalf("released fragment LAST = %q, %v; want the tag it waited for", got, ok)
	}
	// The predicate is consulted for the Cluster itself first, then for each direct
	// child of the Segment that completes while the fragment is held.
	if len(asked) < 2 || asked[0] != matroska.IDCluster {
		t.Fatalf("predicate saw %v; want the Cluster first, then the trailing metadata", asked)
	}
	if asked[len(asked)-1] != matroska.IDTags {
		t.Fatalf("predicate's last consult was %s, want Tags", asked[len(asked)-1])
	}
}

// TestMetadataCompleteAlwaysTrueIsEagerEmission pins the documented idiom: a
// predicate that always says yes emits at the Cluster's end, which is what a
// real-time analysis consumer wants -- knowingly taking the partial view.
func TestMetadataCompleteAlwaysTrueIsEagerEmission(t *testing.T) {
	tracks := synTracks(synTrackEntry(1, "AUDIO_FROM_CUSTOMER"))
	cluster1 := synCluster(0, synSimpleBlock(1, 0, []byte{0x01}))
	lateTags := synTags("ContactId", "late")
	cluster2 := synCluster(1024, synSimpleBlock(1, 0, []byte{0x02}))
	raw := synFragment(tracks, cluster1, lateTags, cluster2)

	const segmentHeaderLen = 4 + 8
	cut := len(ebmltest.Encode(synEBMLHeader())) + segmentHeaderLen +
		len(ebmltest.Encode(tracks)) + len(ebmltest.Encode(cluster1))

	a := fragment.New(fragment.WithMetadataComplete(
		func(*fragment.Fragment, parser.ElementID) bool { return true },
	))
	first, err := a.Feed(raw[:cut])
	if err != nil {
		t.Fatalf("Feed: %v", err)
	}
	if len(first) != 1 {
		t.Fatalf("got %d fragments from the first Feed, want 1 at the Cluster's end", len(first))
	}
	if got, ok := fragTag(first[0], "ContactId"); ok {
		t.Fatalf("eager fragment carries a tag that had not arrived yet: %q", got)
	}
	if _, err := a.Feed(raw[cut:]); err != nil {
		t.Fatalf("Feed: %v", err)
	}
	if _, err := a.Finalize(); err != nil {
		t.Fatalf("Finalize: %v", err)
	}
}

// TestMetadataCompleteNilIsTheDefault keeps the option from being able to make a
// fragment vanish: a nil predicate is the default rule, not "never release".
func TestMetadataCompleteNilIsTheDefault(t *testing.T) {
	raw := synFragment(
		synTracks(synTrackEntry(1, "AUDIO_FROM_CUSTOMER")),
		synCluster(0, synSimpleBlock(1, 0, []byte{0x01})),
		synTags("trailing", "yes"),
	)
	frags := runWhole(t, raw, fragment.WithMetadataComplete(nil))
	if len(frags) != 1 {
		t.Fatalf("got %d fragments, want 1", len(frags))
	}
	if got, ok := fragTag(frags[0], "trailing"); !ok || got != "yes" {
		t.Fatalf("trailing tag = %q, %v; want the default wait to have settled it", got, ok)
	}
}

func TestFragmentTagIsLastWins(t *testing.T) {
	raw := synFragment(
		synTags("state", "first"),
		synTags("state", "second"),
		synCluster(0, synSimpleBlock(1, 0, []byte{0x01})),
	)
	frags := runWhole(t, raw)
	if len(frags) != 1 {
		t.Fatalf("got %d fragments, want 1", len(frags))
	}
	// RFC 9559 leaves repeated-name precedence undefined; this library chooses
	// last-wins so later stream statements remain visible.
	if got, ok := fragTag(frags[0], "state"); !ok || got != "second" {
		t.Fatalf("Tag(state) = %q, %v; want second (library last-wins choice)", got, ok)
	}
	if got := fragTags(frags[0])["state"]; got != "second" {
		t.Fatalf("Tags()[state] = %q, want second to match Tag", got)
	}
}

// TestFragmentTagNeedsADeclaredValue pins the shape a live stream sends: a
// SimpleTag that names a tag without declaring a TagString states no value, so it
// is not reported as the empty string and does not erase a value stated earlier
// under last-wins.
func TestFragmentTagNeedsADeclaredValue(t *testing.T) {
	valueless := ebmltest.Master(matroska.IDTags,
		ebmltest.Master(matroska.IDTag,
			ebmltest.Master(matroska.IDSimpleTag,
				ebmltest.UTF8(matroska.IDTagName, "state"),
			),
		),
	)
	raw := synFragment(
		synTags("state", "real"),
		valueless,
		synCluster(0, synSimpleBlock(1, 0, []byte{0x01})),
	)
	frags := runWhole(t, raw)
	if len(frags) != 1 {
		t.Fatalf("got %d fragments, want 1", len(frags))
	}
	if got, ok := fragTag(frags[0], "state"); !ok || got != "real" {
		t.Fatalf("Tag(state) = %q, %v; want real: an absent TagString is not an empty value", got, ok)
	}
	if got, present := fragTags(frags[0])["state"]; got != "real" || !present {
		t.Fatalf("Tags()[state] = %q, %v; want real to match Tag", got, present)
	}
}

// TestFragmentTagRequiresATagParent pins what Tags documents: collection walks
// Tag elements, so a SimpleTag sitting directly under Tags has no Targets to be
// scoped by and is not reported.
func TestFragmentTagRequiresATagParent(t *testing.T) {
	orphan := ebmltest.Master(matroska.IDTags,
		ebmltest.Master(matroska.IDSimpleTag,
			ebmltest.UTF8(matroska.IDTagName, "state"),
			ebmltest.UTF8(matroska.IDTagString, "orphan"),
		),
	)
	frags := runWhole(t, synFragment(orphan, synCluster(0, synSimpleBlock(1, 0, []byte{0x01}))))
	if len(frags) != 1 {
		t.Fatalf("got %d fragments, want 1", len(frags))
	}
	if got, ok := fragTag(frags[0], "state"); ok {
		t.Fatalf("Tag(state) = %q, %v; want no value for a SimpleTag outside a Tag", got, ok)
	}
	// The element is still retained and reachable by loose extraction: it is not
	// reported as a TAG, not dropped from the tree.
	if got := len(frags[0].Segment.Descendants(matroska.IDSimpleTag)); got != 1 {
		t.Fatalf("retained SimpleTag count = %d, want 1", got)
	}
}

// TestStructuralErrorIsTerminalWithoutResync checks the default: a structural
// error is reported, the fragments completed before it are still returned, and the
// assembler does not silently carry on.
func TestStructuralErrorIsTerminalWithoutResync(t *testing.T) {
	good := synFragment(
		synTracks(synTrackEntry(1, "AUDIO_FROM_CUSTOMER")),
		synCluster(0, synSimpleBlock(1, 0, []byte{0x01})),
	)
	// A leading 0x00 byte encodes a 9-byte element ID VINT, which exceeds the
	// 4-byte maximum: the cursor cannot locate any element from there on.
	garbage := []byte{0x00, 0x00, 0xFF, 0x13, 0x37}
	raw := ebmltest.Concat(good, garbage, good)

	a := fragment.New()
	frags, err := a.Feed(raw)
	if err == nil {
		t.Fatal("Feed of a stream with spliced garbage must report an error")
	}
	if !errors.Is(err, parser.ErrElementIDTooLong) {
		t.Fatalf("Feed error = %v, want the ID-length diagnosis", err)
	}
	if len(frags) != 1 {
		t.Fatalf("got %d fragments with the error, want the 1 that completed before it", len(frags))
	}
	if _, err2 := a.Feed(nil); !errors.Is(err2, parser.ErrElementIDTooLong) {
		t.Fatalf("a later Feed = %v, want the same terminal error", err2)
	}
}

// truncatedStream is a complete fragment with its last two bytes cut off, so the
// stream ends INSIDE the final SimpleBlock's declared payload: Feed cannot fault it
// -- more bytes would finish the element -- and it is Finalize that diagnoses the
// truncation.
func truncatedStream() []byte {
	raw := synFragment(
		synTracks(synTrackEntry(1, "AUDIO_FROM_CUSTOMER")),
		synCluster(0, synSimpleBlock(1, 0, []byte{0x01, 0x02, 0x03, 0x04})),
	)
	return raw[:len(raw)-2]
}

// TestFailingFinalizeLatchesForLaterFeed pins which of the two "no more input"
// conditions a later Feed is answered with. A Finalize that FAILS both latches the
// terminal failure and marks the assembler finalized, so a Feed after it can be
// answered two ways -- and only one of them is the documented contract: once a
// terminal failure is latched, every later call reports THAT failure. Answering with
// a fresh "already finalized" invalid-use error instead would throw the diagnosis
// away and blame the caller for the stream's defect.
func TestFailingFinalizeLatchesForLaterFeed(t *testing.T) {
	a := fragment.New()
	if _, err := a.Feed(truncatedStream()); err != nil {
		t.Fatalf("Feed of a truncated stream = %v; the bytes so far are readable", err)
	}
	_, err := a.Finalize()
	if !errors.Is(err, parser.ErrTruncated) {
		t.Fatalf("Finalize = %v, want the truncation diagnosis", err)
	}

	if _, err2 := a.Feed(nil); !errors.Is(err2, parser.ErrTruncated) {
		t.Fatalf("Feed after a failing Finalize = %v, want the latched %v", err2, err)
	}
	if _, err3 := a.Finalize(); !errors.Is(err3, parser.ErrTruncated) {
		t.Fatalf("Finalize after a failing Finalize = %v, want the latched %v", err3, err)
	}
}

// TestFeedAfterCleanFinalizeIsInvalidUse is the other order, and the reason the
// "already finalized" error is still worth having: nothing failed, so there is no
// latched error to report, and feeding a stream the caller itself declared over is a
// programmer error rather than a property of the bytes.
func TestFeedAfterCleanFinalizeIsInvalidUse(t *testing.T) {
	raw := synFragment(
		synTracks(synTrackEntry(1, "AUDIO_FROM_CUSTOMER")),
		synCluster(0, synSimpleBlock(1, 0, []byte{0x01, 0x02})),
	)

	a := fragment.New()
	if _, err := a.Feed(raw); err != nil {
		t.Fatalf("Feed: %v", err)
	}
	if _, err := a.Finalize(); err != nil {
		t.Fatalf("Finalize: %v", err)
	}

	_, err := a.Feed(nil)
	var invalid parser.Invalid
	if !errors.As(err, &invalid) {
		t.Fatalf("Feed after a clean Finalize = %v, want a parser.Invalid", err)
	}
	if !strings.Contains(err.Error(), "already finalized") {
		t.Fatalf("Feed after a clean Finalize = %v, want the already-finalized diagnosis", err)
	}
}

// TestWithResyncRecoversFromSplicedGarbage checks opt-in recovery: garbage between
// two Segments costs the bytes it occupies and nothing else -- the fragment after
// it is assembled normally, and the loss is reported with a non-zero skipped count.
func TestWithResyncRecoversFromSplicedGarbage(t *testing.T) {
	first := synFragment(
		synInfo(synSegmentUUID([]byte{0xA1, 0xA2}), synTimestampScale(1_000_000)),
		synTracks(synTrackEntry(1, "AUDIO_FROM_CUSTOMER")),
		synTags("ContactId", "first"),
		synCluster(0, synSimpleBlock(1, 0, []byte{0x01, 0x02})),
	)
	second := synFragment(
		synInfo(synSegmentUUID([]byte{0xB1, 0xB2}), synTimestampScale(2_000_000)),
		synTracks(synTrackEntry(1, "AUDIO_TO_CUSTOMER")),
		synTags("ContactId", "second"),
		synCluster(7, synSimpleBlock(1, 0, []byte{0x03, 0x04})),
	)
	// Garbage whose first byte cannot begin an element: the cursor fails there,
	// inside the first (still open) unknown-size Segment.
	garbage := []byte{0x00, 0x11, 0x22, 0x33, 0x44, 0x55, 0x66, 0x77}
	raw := ebmltest.Concat(first, garbage, second)

	type recovery struct {
		offset  int64
		skipped int64
		cause   error
	}

	for _, sp := range []struct {
		label  string
		chunks [][]byte
	}{
		{"whole", [][]byte{raw}},
		{"one_byte", splitOneByte(raw)},
		{"fibonacci", splitFibonacci(raw)},
	} {
		t.Run(sp.label, func(t *testing.T) {
			var recoveries []recovery
			a := fragment.New(fragment.WithResync(func(offset, skipped int64, cause error) {
				recoveries = append(recoveries, recovery{offset, skipped, cause})
			}))

			var frags []*fragment.Fragment
			for _, ch := range sp.chunks {
				got, err := a.Feed(ch)
				if err != nil {
					t.Fatalf("Feed: %v", err)
				}
				frags = append(frags, got...)
			}
			tail, err := a.Finalize()
			if err != nil {
				t.Fatalf("Finalize: %v", err)
			}
			frags = append(frags, tail...)

			if len(recoveries) != 1 {
				t.Fatalf("got %d recoveries, want exactly 1: %+v", len(recoveries), recoveries)
			}
			rec := recoveries[0]
			if want := int64(len(first) + len(garbage)); rec.offset != want {
				t.Fatalf("resumed at offset %d, want %d (the next fragment's EBML header)", rec.offset, want)
			}
			if rec.skipped != int64(len(garbage)) {
				t.Fatalf("skipped %d bytes, want %d", rec.skipped, len(garbage))
			}
			if rec.skipped == 0 {
				t.Fatal("a recovery that discards bytes must report a non-zero skipped count")
			}
			if !errors.Is(rec.cause, parser.ErrElementIDTooLong) {
				t.Fatalf("cause = %v, want the ID-length diagnosis", rec.cause)
			}
			// Recovery ran because the failure was structural, which is the only
			// class that may trigger it.
			if !parser.IsStructural(rec.cause) {
				t.Fatalf("cause = %v, want a structural classification", rec.cause)
			}
			var ce *parser.ContentError
			if errors.As(rec.cause, &ce) {
				t.Fatalf("cause = %v, want no content origin", rec.cause)
			}

			if len(frags) != 2 {
				t.Fatalf("got %d fragments, want 2 (one per Segment)", len(frags))
			}
			if got, _ := fragTag(frags[0], "ContactId"); got != "first" {
				t.Fatalf("fragment 0 ContactId = %q", got)
			}
			// The fragment after the garbage is complete and uncontaminated, and its
			// offsets still refer to the original stream.
			after := frags[1]
			if got, _ := fragTag(after, "ContactId"); got != "second" {
				t.Fatalf("fragment 1 ContactId = %q", got)
			}
			if got := after.TimestampScale(); got != 2_000_000 {
				t.Fatalf("fragment 1 TimestampScale = %d, want 2000000", got)
			}
			if got := after.ClusterTimestamp(); got != 7 {
				t.Fatalf("fragment 1 ClusterTimestamp = %d, want 7", got)
			}
			if got := after.TrackPCM(1); !reflect.DeepEqual(got, []byte{0x03, 0x04}) {
				t.Fatalf("fragment 1 PCM = %x, want 0304", got)
			}
			if _, ok := after.TrackByName("AUDIO_TO_CUSTOMER"); !ok {
				t.Fatal("fragment 1 lost its own track list")
			}
			if got, want := after.Segment.Offset, int64(len(first)+len(garbage)+len(ebmltest.Encode(synEBMLHeader()))); got != want {
				t.Fatalf("recovered Segment Offset = %d, want %d (offsets stay stream-absolute)", got, want)
			}
		})
	}
}

// TestWithResyncPendingAtEOF checks the honest end of recovery: when the stream
// ends inside garbage there is nothing left to resynchronize on, so Finalize
// reports the error that started it rather than pretending the stream was clean.
func TestWithResyncPendingAtEOF(t *testing.T) {
	good := synFragment(
		synTracks(synTrackEntry(1, "AUDIO_FROM_CUSTOMER")),
		synCluster(0, synSimpleBlock(1, 0, []byte{0x01})),
	)
	raw := ebmltest.Concat(good, []byte{0x00, 0x99, 0x88})

	var recoveries int
	a := fragment.New(fragment.WithResync(func(int64, int64, error) { recoveries++ }))
	frags, err := a.Feed(raw)
	if err != nil {
		t.Fatalf("Feed: %v", err)
	}
	if len(frags) != 1 {
		t.Fatalf("got %d fragments, want 1", len(frags))
	}
	tail, err := a.Finalize()
	if err == nil {
		t.Fatal("Finalize must report the pending failure when the stream ends inside garbage")
	}
	if !errors.Is(err, parser.ErrElementIDTooLong) {
		t.Fatalf("Finalize error = %v, want the ID-length diagnosis", err)
	}
	if len(tail) != 0 {
		t.Fatalf("got %d fragments from Finalize, want 0", len(tail))
	}
	if recoveries != 0 {
		t.Fatalf("notify called %d times, want 0 (no recovery ever happened)", recoveries)
	}
}

// synBadSimpleBlock builds a SimpleBlock that is structurally impeccable -- correct
// ID, correct declared size, so the cursor reads it as one leaf and knows exactly
// where the next element begins -- but whose CONTENT is invalid: it declares track
// number 0, which no SimpleBlock may.
func synBadSimpleBlock() ebmltest.Node {
	return ebmltest.Leaf(matroska.IDSimpleBlock, []byte{0x80, 0x00, 0x00, 0x00, 0x77})
}

// TestContentErrorIsTerminalEvenWithResync checks the boundary of recovery: a
// payload the assembler cannot decode is a verdict about CONTENT, not a structural
// failure of the cursor, so it must surface from Feed unchanged even with
// WithResync enabled -- no byte scanning, no notify, no silent continuation.
func TestContentErrorIsTerminalEvenWithResync(t *testing.T) {
	good := synFragment(
		synTracks(synTrackEntry(1, "AUDIO_FROM_CUSTOMER")),
		synCluster(0, synSimpleBlock(1, 0, []byte{0x01, 0x02})),
	)
	bad := synFragment(
		synTracks(synTrackEntry(1, "AUDIO_FROM_CUSTOMER")),
		synCluster(0, synBadSimpleBlock()),
	)
	// A third, perfectly good fragment follows: recovery would have found its EBML
	// header and carried on, which is exactly what must NOT happen here.
	raw := ebmltest.Concat(good, bad, good)

	for _, sp := range []struct {
		label  string
		chunks [][]byte
	}{
		{"whole", [][]byte{raw}},
		{"one_byte", splitOneByte(raw)},
	} {
		t.Run(sp.label, func(t *testing.T) {
			var recoveries int
			a := fragment.New(fragment.WithResync(func(int64, int64, error) { recoveries++ }))

			var frags []*fragment.Fragment
			var err error
			for _, ch := range sp.chunks {
				var got []*fragment.Fragment
				got, err = a.Feed(ch)
				frags = append(frags, got...)
				if err != nil {
					break
				}
			}
			if err == nil {
				t.Fatal("Feed of a stream with an undecodable SimpleBlock must report an error")
			}
			if recoveries != 0 {
				t.Fatalf("notify called %d times, want 0: a content error must never trigger a scan", recoveries)
			}
			if parser.IsStructural(err) {
				t.Fatalf("error %v is classified structural, but the stream's shape was read correctly", err)
			}
			var ce *parser.ContentError
			if !errors.As(err, &ce) {
				t.Fatalf("error = %v (%T), want a *parser.ContentError for the payload", err, err)
			}
			if ce.ID != matroska.IDSimpleBlock {
				t.Fatalf("ContentError = %+v, want the undecodable SimpleBlock", ce)
			}
			if !strings.Contains(err.Error(), "SimpleBlock") || !strings.Contains(err.Error(), "offset") {
				t.Fatalf("error %q does not say which SimpleBlock failed", err)
			}
			// The fragment that completed before the bad block is still delivered,
			// and the one after it is never reached.
			if len(frags) != 1 {
				t.Fatalf("got %d fragments, want the 1 that completed before the error", len(frags))
			}

			// Terminal: the assembler does not resume, from Feed or from Finalize.
			more, err2 := a.Feed(nil)
			if !errors.As(err2, &ce) {
				t.Fatalf("a later Feed = %v, want the same terminal content error", err2)
			}
			if len(more) != 0 {
				t.Fatalf("a later Feed emitted %d fragments, want 0", len(more))
			}
			tail, err3 := a.Finalize()
			if !errors.As(err3, &ce) {
				t.Fatalf("Finalize = %v, want the same terminal content error", err3)
			}
			if len(tail) != 0 {
				t.Fatalf("Finalize emitted %d fragments, want 0", len(tail))
			}
			if recoveries != 0 {
				t.Fatalf("notify called %d times by the end, want 0", recoveries)
			}
		})
	}
}

// skipped is one call of WithSkipContentErrors' notify.
type skipped struct {
	id     parser.ElementID
	offset int64
	cause  error
}

// TestWithSkipContentErrorsDropsOneElement is the content counterpart of
// TestWithResyncRecoversFromSplicedGarbage: an undecodable SimpleBlock costs that
// ELEMENT and nothing else. The structural position was never in doubt, so the
// Fragment is still emitted -- with the blocks that did decode -- and the stream
// continues into the next fragment.
func TestWithSkipContentErrorsDropsOneElement(t *testing.T) {
	first := synFragment(
		synTracks(synTrackEntry(1, "AUDIO_FROM_CUSTOMER")),
		synTags("ContactId", "first"),
		synCluster(0,
			synSimpleBlock(1, 0, []byte{0x01}),
			synBadSimpleBlock(),
			synSimpleBlock(1, 2, []byte{0x03}),
		),
	)
	second := synFragment(
		synTracks(synTrackEntry(1, "AUDIO_FROM_CUSTOMER")),
		synTags("ContactId", "second"),
		synCluster(7, synSimpleBlock(1, 0, []byte{0x04})),
	)
	raw := ebmltest.Concat(first, second)

	for _, sp := range []struct {
		label  string
		chunks [][]byte
	}{
		{"whole", [][]byte{raw}},
		{"one_byte", splitOneByte(raw)},
		{"fibonacci", splitFibonacci(raw)},
	} {
		t.Run(sp.label, func(t *testing.T) {
			var drops []skipped
			frags := run(t, sp.chunks, fragment.WithSkipContentErrors(
				func(id parser.ElementID, offset int64, cause error) {
					drops = append(drops, skipped{id, offset, cause})
				})).all()

			if len(frags) != 2 {
				t.Fatalf("got %d fragments, want 2 (neither fragment is lost)", len(frags))
			}
			if len(drops) != 1 {
				t.Fatalf("got %d dropped elements, want exactly 1: %+v", len(drops), drops)
			}
			drop := drops[0]
			if drop.id != matroska.IDSimpleBlock {
				t.Fatalf("dropped element = %s, want the SimpleBlock", drop.id)
			}
			// The offset identifies the element, and therefore the fragment it sits
			// in: it is the retained node's own offset in the original stream.
			retained := frags[0].Cluster.ChildrenByID(matroska.IDSimpleBlock)
			if len(retained) != 3 {
				t.Fatalf("Cluster retained %d SimpleBlocks, want 3 (the shape keeps the dropped one)", len(retained))
			}
			if drop.offset != retained[1].Offset {
				t.Fatalf("notified offset %d, want the bad block's offset %d", drop.offset, retained[1].Offset)
			}
			var ce *parser.ContentError
			if !errors.As(drop.cause, &ce) {
				t.Fatalf("cause = %v (%T), want the *parser.ContentError Feed would have returned", drop.cause, drop.cause)
			}
			if ce.ID != matroska.IDSimpleBlock || ce.Offset != drop.offset {
				t.Fatalf("cause = %+v, want it to name the same element", ce)
			}
			if parser.IsStructural(drop.cause) {
				t.Fatalf("cause %v is classified structural; a content verdict never is", drop.cause)
			}
			if !strings.Contains(drop.cause.Error(), "SimpleBlock") {
				t.Fatalf("cause %q does not say what failed", drop.cause)
			}

			// Only the offending block is gone: the two good ones are decoded, in
			// stream order, and the fragment's metadata is untouched.
			if len(frags[0].Blocks) != 2 {
				t.Fatalf("fragment 0 has %d blocks, want the 2 that decoded", len(frags[0].Blocks))
			}
			if got := frags[0].TrackPCM(1); !reflect.DeepEqual(got, []byte{0x01, 0x03}) {
				t.Fatalf("fragment 0 PCM = %x, want 0103", got)
			}
			if got, _ := fragTag(frags[0], "ContactId"); got != "first" {
				t.Fatalf("fragment 0 ContactId = %q", got)
			}
			if got, _ := fragTag(frags[1], "ContactId"); got != "second" {
				t.Fatalf("fragment 1 ContactId = %q", got)
			}
			if got := frags[1].TrackPCM(1); !reflect.DeepEqual(got, []byte{0x04}) {
				t.Fatalf("fragment 1 PCM = %x, want 04", got)
			}
		})
	}
}

// TestWithSkipContentErrorsNilNotifyStaysTerminal checks the option cannot make a
// failure disappear quietly: with a nil notify there is nobody to report a dropped
// element to, so the content error is terminal exactly as by default.
func TestWithSkipContentErrorsNilNotifyStaysTerminal(t *testing.T) {
	raw := synFragment(
		synTracks(synTrackEntry(1, "AUDIO_FROM_CUSTOMER")),
		synCluster(0, synBadSimpleBlock()),
	)

	a := fragment.New(fragment.WithSkipContentErrors(nil))
	frags, err := a.Feed(raw)
	var ce *parser.ContentError
	if !errors.As(err, &ce) {
		t.Fatalf("Feed = %v (%T), want the *parser.ContentError the default reports", err, err)
	}
	if len(frags) != 0 {
		t.Fatalf("got %d fragments, want 0", len(frags))
	}
	if _, err2 := a.Feed(nil); !errors.As(err2, &ce) {
		t.Fatalf("a later Feed = %v, want the same terminal content error", err2)
	}
}

// TestWithSkipContentErrorsLeavesStructuralFailuresAlone checks the division of
// labour from the other side: this option covers the CONTENT class only, so a
// structural failure stays terminal and its notify is never called.
func TestWithSkipContentErrorsLeavesStructuralFailuresAlone(t *testing.T) {
	good := synFragment(
		synTracks(synTrackEntry(1, "AUDIO_FROM_CUSTOMER")),
		synCluster(0, synSimpleBlock(1, 0, []byte{0x01})),
	)
	raw := ebmltest.Concat(good, []byte{0x00, 0x11, 0x22, 0x33, 0x44, 0x55, 0x66, 0x77}, good)

	var drops int
	a := fragment.New(fragment.WithSkipContentErrors(func(parser.ElementID, int64, error) { drops++ }))
	frags, err := a.Feed(raw)
	if err == nil {
		t.Fatal("Feed of a structurally damaged stream must report an error")
	}
	if !parser.IsStructural(err) {
		t.Fatalf("error %v is not classified structural", err)
	}
	if drops != 0 {
		t.Fatalf("notify called %d times, want 0: a structural failure is WithResync's business", drops)
	}
	if len(frags) != 1 {
		t.Fatalf("got %d fragments, want the 1 that completed before the damage", len(frags))
	}
	if _, err2 := a.Feed(nil); !parser.IsStructural(err2) {
		t.Fatalf("a later Feed = %v, want the same terminal structural error", err2)
	}
}

// TestSkipContentErrorsAndResyncCoverOneClassEach sets both options and damages the
// stream both ways: each notify hears about its own class and nothing else, and the
// stream survives both.
func TestSkipContentErrorsAndResyncCoverOneClassEach(t *testing.T) {
	first := synFragment(
		synTracks(synTrackEntry(1, "AUDIO_FROM_CUSTOMER")),
		synCluster(0, synSimpleBlock(1, 0, []byte{0x01}), synBadSimpleBlock()),
	)
	second := synFragment(
		synTracks(synTrackEntry(1, "AUDIO_FROM_CUSTOMER")),
		synCluster(7, synSimpleBlock(1, 0, []byte{0x02})),
	)
	garbage := []byte{0x00, 0x11, 0x22, 0x33}
	raw := ebmltest.Concat(first, garbage, second)

	var drops []skipped
	var resyncs int
	frags := run(t, [][]byte{raw},
		fragment.WithSkipContentErrors(func(id parser.ElementID, offset int64, cause error) {
			drops = append(drops, skipped{id, offset, cause})
		}),
		fragment.WithResync(func(offset, skipped int64, cause error) {
			resyncs++
			if !parser.IsStructural(cause) {
				t.Errorf("resync cause = %v, want a structural failure", cause)
			}
		}),
	).all()

	if len(frags) != 2 {
		t.Fatalf("got %d fragments, want 2", len(frags))
	}
	if len(drops) != 1 {
		t.Fatalf("got %d dropped elements, want 1: %+v", len(drops), drops)
	}
	if resyncs != 1 {
		t.Fatalf("got %d recoveries, want 1", resyncs)
	}
	if parser.IsStructural(drops[0].cause) {
		t.Fatalf("dropped element cause %v must not be structural", drops[0].cause)
	}
	if got := frags[0].TrackPCM(1); !reflect.DeepEqual(got, []byte{0x01}) {
		t.Fatalf("fragment 0 PCM = %x, want 01", got)
	}
	if got := frags[1].TrackPCM(1); !reflect.DeepEqual(got, []byte{0x02}) {
		t.Fatalf("fragment 1 PCM = %x, want 02", got)
	}
}

// TestContentErrorIsTerminalWithoutResync checks the same content error is
// reported identically when recovery was never enabled, so WithResync changes
// nothing at all about this class of failure.
func TestContentErrorIsTerminalWithoutResync(t *testing.T) {
	raw := synFragment(
		synTracks(synTrackEntry(1, "AUDIO_FROM_CUSTOMER")),
		synCluster(0, synBadSimpleBlock()),
	)

	a := fragment.New()
	frags, err := a.Feed(raw)
	if err == nil {
		t.Fatal("Feed must report the undecodable SimpleBlock")
	}
	var ce *parser.ContentError
	if !errors.As(err, &ce) {
		t.Fatalf("error = %v (%T), want a *parser.ContentError", err, err)
	}
	if len(frags) != 0 {
		t.Fatalf("got %d fragments, want 0 (the only Cluster never completed)", len(frags))
	}
}
