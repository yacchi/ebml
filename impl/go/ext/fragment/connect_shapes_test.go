package fragment_test

import (
	"testing"

	"github.com/yacchi/ebml/impl/go/ext/fragment"
	"github.com/yacchi/ebml/impl/go/matroska"
)

// The corpus states nine field-confirmed shapes in prose, in fixtures/kvs/README.json
// and in each fixture's hex header. The tests below are the compiler-checked half:
// each one reads a committed fixture and asserts the shape it exists to carry, so a
// regenerated corpus that quietly loses one of them fails here rather than in a
// consumer.

// TestConnectProfileTrackShape pins the two track facts of the Amazon Connect
// profile: a TrackEntry carries NO Audio master, and its codec declaration
// contradicts its own payload. A consumer that reads sampling frequency, channel
// count or bit depth from the track finds nothing to read, and one that dispatches
// a decoder on CodecID decodes noise; the MimeType tag is where the capture states
// the truth.
func TestConnectProfileTrackShape(t *testing.T) {
	for _, name := range []string{"connect_real_shape", "track_order_swapped", "short_block_mid_track"} {
		t.Run(name, func(t *testing.T) {
			f := runWhole(t, loadHex(t, name))[0]
			for _, tr := range f.Tracks() {
				if audio := tr.Find(matroska.IDAudio); audio.Exists() {
					t.Errorf("track %q carries an Audio master; Connect sends none",
						tr.Find(matroska.IDName).AsString())
				}
				if got := tr.Find(matroska.IDCodecID).AsString(); got != "A_AAC" {
					t.Errorf("CodecID = %q, want the misdeclared A_AAC", got)
				}
				if got := tr.Find(matroska.IDCodecPrivate).Bytes(); len(got) != 2 || got[0] != 0x11 || got[1] != 0x90 {
					t.Errorf("CodecPrivate = %x, want 1190", got)
				}
			}
			if got := fragTags(f)["MimeType"]; got != "audio/L16;rate=8000;channels=1" {
				t.Errorf("MimeType tag = %q; it is the only correct statement of the payload format", got)
			}
		})
	}
}

// TestConnectProfileInfoShape pins the Info the profile carries: a SegmentUUID, a
// Title, and MuxingApp equal to WritingApp.
func TestConnectProfileInfoShape(t *testing.T) {
	f := runWhole(t, loadHex(t, "connect_real_shape"))[0]
	if f.Value(matroska.IDSegmentUUID) == nil {
		t.Fatal("Info carries no SegmentUUID")
	}
	// The SHAPE is the modelled fact, never the vendor string: Title is PRESENT on
	// a live stream, which a consumer that expects it only on a file gets wrong.
	// Asserting the value would pin somebody's build string into a test and invite
	// exactly the matching this library says not to do.
	if got := f.Value(matroska.IDTitle).AsString(); got == "" {
		t.Error("Info carries no Title; the profile sends one even on a live stream")
	}
	muxing := f.Value(matroska.IDMuxingApp).AsString()
	writing := f.Value(matroska.IDWritingApp).AsString()
	if muxing == "" || muxing != writing {
		t.Errorf("MuxingApp = %q, WritingApp = %q; the profile sends one string in both", muxing, writing)
	}
}

// TestTrackOrderIsNotFixed is the reason track_order_swapped exists. The two
// Connect-shaped fixtures number the SAME two channel names the opposite way while
// carrying the SAME constant AUDIO_*_CUSTOMER tag values, so those tag values agree
// with one fixture's mapping and contradict the other's. Only Name resolves a
// direction, and this test fails the moment the corpus goes back to one fixed order.
func TestTrackOrderIsNotFixed(t *testing.T) {
	firstTrackName := func(name string) string {
		f := runWhole(t, loadHex(t, name))[0]
		tags := fragTags(f)
		if tags["AUDIO_TO_CUSTOMER"] != "1" || tags["AUDIO_FROM_CUSTOMER"] != "2" {
			t.Fatalf("%s: AUDIO_*_CUSTOMER tag values = %q/%q, want the constants 1 and 2",
				name, tags["AUDIO_TO_CUSTOMER"], tags["AUDIO_FROM_CUSTOMER"])
		}
		track, ok := f.Track(1)
		if !ok {
			t.Fatalf("%s: no track 1", name)
		}
		// The UID travels with the NAME, so it must not follow the numbering.
		uid, err := track.Find(matroska.IDTrackUID).AsUint()
		if err != nil {
			t.Fatalf("%s: TrackUID: %v", name, err)
		}
		byName, ok := f.TrackByName(track.Find(matroska.IDName).AsString())
		if !ok || byName != track {
			t.Fatalf("%s: TrackByName did not resolve track 1", name)
		}
		if want := map[string]uint64{"AUDIO_FROM_CUSTOMER": 0xA001, "AUDIO_TO_CUSTOMER": 0xA002}[byName.Find(matroska.IDName).AsString()]; uid != want {
			t.Fatalf("%s: TrackUID %#x does not follow the name (want %#x)", name, uid, want)
		}
		return track.Find(matroska.IDName).AsString()
	}
	if a, b := firstTrackName("connect_real_shape"), firstTrackName("track_order_swapped"); a == b {
		t.Fatalf("both fixtures put %q on track 1; the corpus no longer models either capture order", a)
	}
}

// TestShortBlockMidTrack pins the block cadence: 1024-byte blocks alternating
// between the tracks, with one 192-byte block in the MIDDLE of track 1's run. A
// consumer deriving a block's duration from a constant size, or reframing a track's
// PCM into fixed-size chunks, breaks on exactly this.
func TestShortBlockMidTrack(t *testing.T) {
	f := runWhole(t, loadHex(t, "short_block_mid_track"))[0]
	var track1 []int
	for _, b := range f.Blocks {
		if b.TrackNumber != 1 {
			continue
		}
		size := 0
		for _, frame := range b.Frames {
			size += len(frame)
		}
		track1 = append(track1, size)
	}
	want := []int{1024, 192, 1024}
	if len(track1) != len(want) {
		t.Fatalf("track 1 has %d blocks, want %d", len(track1), len(want))
	}
	for i := range want {
		if track1[i] != want[i] {
			t.Fatalf("track 1 block sizes = %v, want %v", track1, want)
		}
	}
	if len(f.TrackPCM(1)) == len(f.TrackPCM(2)) {
		t.Fatal("the two tracks carry equal byte counts; the mid-track outlier is gone")
	}
}

// TestTaglessTailHasNoFollowingFragment pins where the tagless fragment sits. The
// other two tagless fixtures keep theirs mid-stream, where the next tagged fragment
// settles attribution; here the tagless fragment is LAST, so a consumer that defers
// attribution until the next tagged fragment drops it.
func TestTaglessTailHasNoFollowingFragment(t *testing.T) {
	frags := runWhole(t, loadHex(t, "tagless_tail"))
	if len(frags) != 3 {
		t.Fatalf("got %d fragments, want 3", len(frags))
	}
	for i, f := range frags[:2] {
		if got := mustTag(t, f, "ContactId"); got == "" {
			t.Fatalf("fragment %d has no ContactId", i)
		}
	}
	if tags := fragTags(frags[2]); len(tags) != 0 {
		t.Fatalf("the final fragment carries tags %v; it must be the tagless one", tags)
	}
}

// TestStreamReuseCarriesSeveralContacts pins the stream-reuse shape: one stream,
// several contacts, and a first fragment that already belongs to a contact other
// than the one the stream is named for. A consumer that trusts the stream name, or
// the first fragment it reads, transcribes another contact's audio.
func TestStreamReuseCarriesSeveralContacts(t *testing.T) {
	const streamNameContact = "00000000-0000-4000-8000-000000000001"
	frags := runWhole(t, loadHex(t, "stream_reuse"))
	if len(frags) != 4 {
		t.Fatalf("got %d fragments, want 4", len(frags))
	}
	seen := map[string]bool{}
	var order []string
	for i, f := range frags {
		id := mustTag(t, f, "ContactId")
		if id == streamNameContact {
			t.Fatalf("fragment %d belongs to the contact the stream is named for; the fixture no longer models reuse", i)
		}
		if !seen[id] {
			seen[id] = true
			order = append(order, id)
		}
	}
	if len(order) < 2 {
		t.Fatalf("the stream holds %d contact(s), want at least 2", len(order))
	}
	// The runs are separated in time, not merely in tags: a reused stream holds
	// fragments days apart, so a consumer cannot infer one contact from continuity.
	first, last := clusterMillis(t, frags[0]), clusterMillis(t, frags[len(frags)-1])
	if last-first < 24*60*60*1000 {
		t.Fatalf("first and last Cluster timestamps are %d ms apart; the reuse gap is gone", last-first)
	}
}

func clusterMillis(t *testing.T, f *fragment.Fragment) int64 {
	t.Helper()
	if f.TimestampScale() != 1_000_000 {
		t.Fatalf("TimestampScale = %d ns, want the 1 ms default", f.TimestampScale())
	}
	return int64(f.ClusterTimestamp())
}
