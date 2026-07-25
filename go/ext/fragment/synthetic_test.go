package fragment_test

import (
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/yacchi/ebml-reader/ext/fragment"
	"github.com/yacchi/ebml-reader/matroska"
	"github.com/yacchi/ebml-reader/parser"
)

// ---- Minimal, self-contained EBML byte encoders. ----
//
// These are deliberately NOT shared with internal/kvsgen: streams built here are
// independent of the generator, so a bug shared between the generator and the
// assembler cannot hide behind green tests. They also let one test state exactly
// which elements are present and which are absent -- the cases a fixture corpus
// would need a fixture each for.

func synID(id parser.ElementID) []byte {
	b := []byte{byte(id >> 24), byte(id >> 16), byte(id >> 8), byte(id)}
	start := 0
	for start < 3 && b[start] == 0 {
		start++
	}
	return append([]byte(nil), b[start:]...)
}

func synSize(n uint64) []byte {
	for l := 1; l <= 8; l++ {
		max := uint64(1)<<(7*uint(l)) - 1
		if n < max {
			v := n | (uint64(1) << (7 * uint(l)))
			out := make([]byte, l)
			for i := l - 1; i >= 0; i-- {
				out[i] = byte(v)
				v >>= 8
			}
			return out
		}
	}
	panic("synthetic: size too large to encode")
}

func synUint(v uint64) []byte {
	if v == 0 {
		return []byte{0x00}
	}
	n := 0
	for x := v; x > 0; x >>= 8 {
		n++
	}
	out := make([]byte, n)
	for i := n - 1; i >= 0; i-- {
		out[i] = byte(v)
		v >>= 8
	}
	return out
}

func synElem(id parser.ElementID, payload []byte) []byte {
	out := synID(id)
	out = append(out, synSize(uint64(len(payload)))...)
	return append(out, payload...)
}

// synUnknownElem encodes an element with the 8-byte unknown-size VINT, which is
// what a real KVS Segment uses.
func synUnknownElem(id parser.ElementID, payload []byte) []byte {
	out := synID(id)
	out = append(out, 0x01, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF)
	return append(out, payload...)
}

func synConcat(parts ...[]byte) []byte {
	var out []byte
	for _, p := range parts {
		out = append(out, p...)
	}
	return out
}

func synEBMLHeader() []byte {
	return synElem(matroska.IDEBML, synConcat(
		synElem(matroska.IDEBMLVersion, synUint(1)),
		synElem(matroska.IDEBMLReadVersion, synUint(1)),
		synElem(matroska.IDDocType, []byte("matroska")),
	))
}

// synTrackEntry builds one TrackEntry. Passing name == "" OMITS the Name element
// entirely, as opposed to emitting an empty one, so the element-absent path is
// testable distinctly from an empty-but-present element.
func synTrackEntry(number uint64, name string) []byte {
	payload := synConcat(
		synElem(matroska.IDTrackNumber, synUint(number)),
		synElem(matroska.IDTrackType, synUint(2)),
		synElem(matroska.IDCodecID, []byte("A_PCM/INT/LIT")),
	)
	if name != "" {
		payload = append(payload, synElem(matroska.IDName, []byte(name))...)
	}
	return synElem(matroska.IDTrackEntry, payload)
}

func synTracks(entries ...[]byte) []byte {
	return synElem(matroska.IDTracks, synConcat(entries...))
}

func synSimpleBlock(trackNumber byte, timecode int16, audio []byte) []byte {
	content := []byte{0x80 | trackNumber}
	content = append(content, byte(uint16(timecode)>>8), byte(uint16(timecode)))
	content = append(content, 0x00) // flags
	content = append(content, audio...)
	return synElem(matroska.IDSimpleBlock, content)
}

func synCluster(clusterTS uint64, blocks ...[]byte) []byte {
	payload := synElem(matroska.IDTimestamp, synUint(clusterTS))
	for _, b := range blocks {
		payload = append(payload, b...)
	}
	return synElem(matroska.IDCluster, payload)
}

// synInfo builds an Info element from arbitrary pre-built children, so a test
// selects exactly which of SegmentUUID/TimestampScale are present.
func synInfo(children ...[]byte) []byte {
	return synElem(matroska.IDInfo, synConcat(children...))
}

func synSegmentUUID(uid []byte) []byte {
	return synElem(matroska.IDSegmentUUID, uid)
}

func synTimestampScale(scale uint64) []byte {
	return synElem(matroska.IDTimestampScale, synUint(scale))
}

// synFragment wraps a Segment body as a full EBML-header + unknown-size Segment
// unit, matching a real KVS fragment.
func synFragment(segmentBody []byte) []byte {
	return synConcat(synEBMLHeader(), synUnknownElem(matroska.IDSegment, segmentBody))
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
	seg := synConcat(
		synTracks(synTrackEntry(1, "AUDIO_FROM_CUSTOMER")),
		synCluster(0, synSimpleBlock(1, 0, []byte{0x01, 0x02})),
	)
	frags := runWhole(t, synFragment(seg))
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
	seg := synConcat(
		synInfo(synSegmentUUID(uid)), // no TimestampScale child
		synTracks(synTrackEntry(1, "AUDIO_FROM_CUSTOMER")),
		synCluster(0, synSimpleBlock(1, 0, []byte{0x01, 0x02})),
	)
	f := runWhole(t, synFragment(seg))[0]
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

	seg1 := synConcat(
		synInfo(synSegmentUUID(uidA), synTimestampScale(1_000_000)),
		synTracks(synTrackEntry(1, "AUDIO_FROM_CUSTOMER")),
		synCluster(0, synSimpleBlock(1, 0, []byte{0x01})),
	)
	seg2 := synConcat( // deliberately NO Info element
		synTracks(synTrackEntry(1, "AUDIO_FROM_CUSTOMER")),
		synCluster(0, synSimpleBlock(1, 0, []byte{0x02})),
	)
	seg3 := synConcat(
		synInfo(synSegmentUUID(uidC), synTimestampScale(2_000_000)),
		synTracks(synTrackEntry(1, "AUDIO_FROM_CUSTOMER")),
		synCluster(0, synSimpleBlock(1, 0, []byte{0x03})),
	)
	raw := synConcat(synFragment(seg1), synFragment(seg2), synFragment(seg3))

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
	seg := synConcat(
		synTracks(synTrackEntry(1, "")), // "" => Name element omitted entirely
		synCluster(0, synSimpleBlock(1, 0, []byte{0x01})),
	)
	f := runWhole(t, synFragment(seg))[0]
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
	seg := synConcat(
		synInfo(synSegmentUUID(uid), synTimestampScale(1_000_000)),
		synTracks(synTrackEntry(1, "AUDIO_FROM_CUSTOMER")),
		synCluster(0, synSimpleBlock(1, 0, []byte{0x01})),
	)
	f := runWhole(t, synFragment(seg))[0]
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
func TestLateMetadataIsNotAttributedToAnEarlierFragment(t *testing.T) {
	tracks := synTracks(synTrackEntry(1, "AUDIO_FROM_CUSTOMER"))
	cluster1 := synCluster(0, synSimpleBlock(1, 0, []byte{0x01}))
	lateTags := synElem(matroska.IDTags, synElem(matroska.IDTag, synElem(matroska.IDSimpleTag, synConcat(
		synElem(matroska.IDTagName, []byte("ContactId")),
		synElem(matroska.IDTagString, []byte("late")),
	))))
	cluster2 := synCluster(1024, synSimpleBlock(1, 0, []byte{0x02}))
	raw := synFragment(synConcat(tracks, cluster1, lateTags, cluster2))

	// Feed exactly up to the end of the first Cluster, then the rest.
	const segmentHeaderLen = 4 + 8 // Segment ID plus the unknown-size VINT
	cut := len(synEBMLHeader()) + segmentHeaderLen + len(tracks) + len(cluster1)

	a := fragment.New()
	first, err := a.Feed(raw[:cut])
	if err != nil {
		t.Fatalf("Feed: %v", err)
	}
	if len(first) != 1 {
		t.Fatalf("got %d fragments from the first Feed, want 1", len(first))
	}
	if got, ok := first[0].Tag("ContactId"); ok {
		t.Fatalf("fragment 1 carries a tag that had not arrived yet: %q", got)
	}
	if got := len(first[0].Values(matroska.IDSimpleTag)); got != 0 {
		t.Fatalf("fragment 1 sees %d SimpleTags, want 0 at the moment it is delivered", got)
	}

	second, err := a.Feed(raw[cut:])
	if err != nil {
		t.Fatalf("Feed: %v", err)
	}
	if len(second) != 1 {
		t.Fatalf("got %d fragments from the second Feed, want 1", len(second))
	}
	if got, ok := second[0].Tag("ContactId"); !ok || got != "late" {
		t.Fatalf("fragment 2 ContactId = %q, %v; want the metadata that arrived before its Cluster", got, ok)
	}
	if _, err := a.Finalize(); err != nil {
		t.Fatalf("Finalize: %v", err)
	}
	// Both Fragments share the one Segment node, as documented.
	if first[0].Segment != second[0].Segment {
		t.Fatal("Fragments of one Segment must share its tree")
	}
}

// TestStructuralErrorIsTerminalWithoutResync checks the default: a structural
// error is reported, the fragments completed before it are still returned, and the
// assembler does not silently carry on.
func TestStructuralErrorIsTerminalWithoutResync(t *testing.T) {
	good := synFragment(synConcat(
		synTracks(synTrackEntry(1, "AUDIO_FROM_CUSTOMER")),
		synCluster(0, synSimpleBlock(1, 0, []byte{0x01})),
	))
	// A leading 0x00 byte encodes a 9-byte element ID VINT, which exceeds the
	// 4-byte maximum: the cursor cannot locate any element from there on.
	garbage := []byte{0x00, 0x00, 0xFF, 0x13, 0x37}
	raw := synConcat(good, garbage, good)

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

// TestWithResyncRecoversFromSplicedGarbage checks opt-in recovery: garbage between
// two Segments costs the bytes it occupies and nothing else -- the fragment after
// it is assembled normally, and the loss is reported with a non-zero skipped count.
func TestWithResyncRecoversFromSplicedGarbage(t *testing.T) {
	first := synFragment(synConcat(
		synInfo(synSegmentUUID([]byte{0xA1, 0xA2}), synTimestampScale(1_000_000)),
		synTracks(synTrackEntry(1, "AUDIO_FROM_CUSTOMER")),
		synElem(matroska.IDTags, synElem(matroska.IDTag, synElem(matroska.IDSimpleTag, synConcat(
			synElem(matroska.IDTagName, []byte("ContactId")),
			synElem(matroska.IDTagString, []byte("first")),
		)))),
		synCluster(0, synSimpleBlock(1, 0, []byte{0x01, 0x02})),
	))
	second := synFragment(synConcat(
		synInfo(synSegmentUUID([]byte{0xB1, 0xB2}), synTimestampScale(2_000_000)),
		synTracks(synTrackEntry(1, "AUDIO_TO_CUSTOMER")),
		synElem(matroska.IDTags, synElem(matroska.IDTag, synElem(matroska.IDSimpleTag, synConcat(
			synElem(matroska.IDTagName, []byte("ContactId")),
			synElem(matroska.IDTagString, []byte("second")),
		)))),
		synCluster(7, synSimpleBlock(1, 0, []byte{0x03, 0x04})),
	))
	// Garbage whose first byte cannot begin an element: the cursor fails there,
	// inside the first (still open) unknown-size Segment.
	garbage := []byte{0x00, 0x11, 0x22, 0x33, 0x44, 0x55, 0x66, 0x77}
	raw := synConcat(first, garbage, second)

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
			var he *parser.HandlerError
			if errors.As(rec.cause, &he) {
				t.Fatalf("cause = %v, want no handler origin", rec.cause)
			}

			if len(frags) != 2 {
				t.Fatalf("got %d fragments, want 2 (one per Segment)", len(frags))
			}
			if got, _ := frags[0].Tag("ContactId"); got != "first" {
				t.Fatalf("fragment 0 ContactId = %q", got)
			}
			// The fragment after the garbage is complete and uncontaminated, and its
			// offsets still refer to the original stream.
			after := frags[1]
			if got, _ := after.Tag("ContactId"); got != "second" {
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
			if got, want := after.Segment.Offset, int64(len(first)+len(garbage)+len(synEBMLHeader())); got != want {
				t.Fatalf("recovered Segment Offset = %d, want %d (offsets stay stream-absolute)", got, want)
			}
		})
	}
}

// TestWithResyncPendingAtEOF checks the honest end of recovery: when the stream
// ends inside garbage there is nothing left to resynchronize on, so Finalize
// reports the error that started it rather than pretending the stream was clean.
func TestWithResyncPendingAtEOF(t *testing.T) {
	good := synFragment(synConcat(
		synTracks(synTrackEntry(1, "AUDIO_FROM_CUSTOMER")),
		synCluster(0, synSimpleBlock(1, 0, []byte{0x01})),
	))
	raw := synConcat(good, []byte{0x00, 0x99, 0x88})

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
func synBadSimpleBlock() []byte {
	return synElem(matroska.IDSimpleBlock, []byte{0x80, 0x00, 0x00, 0x00, 0x77})
}

// TestContentErrorIsTerminalEvenWithResync checks the boundary of recovery: a
// payload the assembler cannot decode is a verdict about CONTENT, not a structural
// failure of the cursor, so it must surface from Feed unchanged even with
// WithResync enabled -- no byte scanning, no notify, no silent continuation.
func TestContentErrorIsTerminalEvenWithResync(t *testing.T) {
	good := synFragment(synConcat(
		synTracks(synTrackEntry(1, "AUDIO_FROM_CUSTOMER")),
		synCluster(0, synSimpleBlock(1, 0, []byte{0x01, 0x02})),
	))
	bad := synFragment(synConcat(
		synTracks(synTrackEntry(1, "AUDIO_FROM_CUSTOMER")),
		synElem(matroska.IDCluster, synConcat(
			synElem(matroska.IDTimestamp, synUint(0)),
			synBadSimpleBlock(),
		)),
	))
	// A third, perfectly good fragment follows: recovery would have found its EBML
	// header and carried on, which is exactly what must NOT happen here.
	raw := synConcat(good, bad, good)

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
			var he *parser.HandlerError
			if !errors.As(err, &he) {
				t.Fatalf("error = %v (%T), want a *parser.HandlerError from the payload event", err, err)
			}
			if he.Op != parser.OpPayload || he.Node.ID != matroska.IDSimpleBlock {
				t.Fatalf("HandlerError = %+v, want the %s event of a SimpleBlock", he, parser.OpPayload)
			}
			if !strings.Contains(err.Error(), "SimpleBlock at offset") {
				t.Fatalf("error %q does not say which SimpleBlock failed", err)
			}
			// The fragment that completed before the bad block is still delivered,
			// and the one after it is never reached.
			if len(frags) != 1 {
				t.Fatalf("got %d fragments, want the 1 that completed before the error", len(frags))
			}

			// Terminal: the assembler does not resume, from Feed or from Finalize.
			more, err2 := a.Feed(nil)
			if !errors.As(err2, &he) {
				t.Fatalf("a later Feed = %v, want the same terminal handler error", err2)
			}
			if len(more) != 0 {
				t.Fatalf("a later Feed emitted %d fragments, want 0", len(more))
			}
			tail, err3 := a.Finalize()
			if !errors.As(err3, &he) {
				t.Fatalf("Finalize = %v, want the same terminal handler error", err3)
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

// TestContentErrorIsTerminalWithoutResync checks the same content error is
// reported identically when recovery was never enabled, so WithResync changes
// nothing at all about this class of failure.
func TestContentErrorIsTerminalWithoutResync(t *testing.T) {
	raw := synFragment(synConcat(
		synTracks(synTrackEntry(1, "AUDIO_FROM_CUSTOMER")),
		synElem(matroska.IDCluster, synConcat(
			synElem(matroska.IDTimestamp, synUint(0)),
			synBadSimpleBlock(),
		)),
	))

	a := fragment.New()
	frags, err := a.Feed(raw)
	if err == nil {
		t.Fatal("Feed must report the undecodable SimpleBlock")
	}
	var he *parser.HandlerError
	if !errors.As(err, &he) {
		t.Fatalf("error = %v (%T), want a *parser.HandlerError", err, err)
	}
	if len(frags) != 0 {
		t.Fatalf("got %d fragments, want 0 (the only Cluster never completed)", len(frags))
	}
}
