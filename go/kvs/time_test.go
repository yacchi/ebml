package kvs

import (
	"bytes"
	"testing"
	"time"

	"github.com/yacchi/ebml/ext/fragment"
)

// scaled_timestamps is the fixture built for exactly this arithmetic: it declares
// TimestampScale=100000 ns, a Cluster Timestamp of 1000 ticks, and three blocks at
// relative timecodes -20, 0 and +20 -- so a wrong scale, a wrong origin or a lost
// sign all produce a visibly wrong instant rather than a plausible one.
const (
	scaledTick  = 100_000
	clusterTick = 1000
)

func firstFragment(t *testing.T, name string) *fragment.Fragment {
	t.Helper()
	r := NewReader(bytes.NewReader(fixture(t, name)))
	f, _, err := r.Next()
	if err != nil {
		t.Fatalf("Next: %v", err)
	}
	return f
}

func TestClusterTimeIsEpochBased(t *testing.T) {
	f := firstFragment(t, "scaled_timestamps")
	want := time.Unix(0, clusterTick*scaledTick).UTC()
	if got := ClusterTime(f); !got.Equal(want) {
		t.Fatalf("ClusterTime = %v, want %v", got, want)
	}
	// The point of the whole file: ext/fragment states the same instant as a
	// duration from the Segment's own origin, and this package reads that origin as
	// the epoch.
	if got, want := ClusterTime(f).UnixNano(), int64(f.ClusterTimestamp())*int64(f.TimestampScale()); got != want {
		t.Fatalf("ClusterTime nanos = %d, want %d", got, want)
	}
}

func TestStartAndEndTimeSpanTheBlocks(t *testing.T) {
	f := firstFragment(t, "scaled_timestamps")
	if len(f.Blocks) != 3 {
		t.Fatalf("fixture has %d blocks, want 3", len(f.Blocks))
	}
	// The first block's timecode is NEGATIVE, so the fragment starts before its own
	// Cluster time -- a sign lost anywhere in the chain shows up right here.
	wantStart := time.Unix(0, (clusterTick-20)*scaledTick).UTC()
	wantEnd := time.Unix(0, (clusterTick+20)*scaledTick).UTC()
	if got := StartTime(f); !got.Equal(wantStart) {
		t.Fatalf("StartTime = %v, want %v", got, wantStart)
	}
	if got := EndTime(f); !got.Equal(wantEnd) {
		t.Fatalf("EndTime = %v, want %v", got, wantEnd)
	}
	if !StartTime(f).Before(ClusterTime(f)) {
		t.Error("StartTime is not before ClusterTime; the negative timecode was dropped")
	}
	if got, want := EndTime(f).Sub(StartTime(f)), 40*scaledTick*time.Nanosecond; got != want {
		t.Fatalf("EndTime - StartTime = %v, want %v (block starts only)", got, want)
	}
}

// TestConnectShapeAgreesWithProducerTimestamp is the field-shape case these
// functions exist for: connect_real_shape carries an EPOCH-BASED Cluster Timestamp,
// as Amazon Connect sends, so the fragment's own time and the producer's tag name
// the same instant. Reading the Cluster Timestamp as an elapsed media time -- which
// is what ext/fragment's duration accessors return, correctly, for a timeline whose
// origin it cannot know -- puts the fragment 31 years from its tag.
func TestConnectShapeAgreesWithProducerTimestamp(t *testing.T) {
	r := NewReader(bytes.NewReader(fixture(t, "connect_real_shape")))
	f, m, err := r.Next()
	if err != nil {
		t.Fatalf("Next: %v", err)
	}
	if m.ProducerTimestamp.IsZero() {
		t.Fatal("fixture has no producer timestamp to compare against")
	}
	if got := ClusterTime(f); !got.Equal(m.ProducerTimestamp) {
		t.Fatalf("ClusterTime = %v, want the producer timestamp %v", got, m.ProducerTimestamp)
	}
	// The first block sits at the Cluster's own instant, the last 10 ms later.
	if got := StartTime(f); !got.Equal(m.ProducerTimestamp) {
		t.Fatalf("StartTime = %v, want %v", got, m.ProducerTimestamp)
	}
	if got, want := EndTime(f).Sub(StartTime(f)), 10*time.Millisecond; got != want {
		t.Fatalf("EndTime - StartTime = %v, want %v", got, want)
	}
	// A duration read as an instant is the failure this fixture now makes visible.
	if asDuration := f.StartTime(); asDuration < 24*time.Hour {
		t.Fatalf("core StartTime = %v; the fixture no longer models an epoch-based timeline", asDuration)
	}
}

func TestBlockTimeMatchesTheCoreDuration(t *testing.T) {
	f := firstFragment(t, "scaled_timestamps")
	for i, b := range f.Blocks {
		if got, want := BlockTime(f, b).UnixNano(), int64(f.BlockTime(b)); got != want {
			t.Errorf("block %d: BlockTime nanos = %d, want %d", i, got, want)
		}
	}
}

func TestTimesAreUTC(t *testing.T) {
	f := firstFragment(t, "scaled_timestamps")
	// Metadata's timestamps are UTC, and these must agree with them so a consumer
	// can compare a fragment's own time against ProducerTimestamp without
	// converting.
	for name, got := range map[string]time.Time{
		"ClusterTime": ClusterTime(f),
		"StartTime":   StartTime(f),
		"EndTime":     EndTime(f),
		"BlockTime":   BlockTime(f, f.Blocks[0]),
	} {
		if got.Location() != time.UTC {
			t.Errorf("%s location = %v, want UTC", name, got.Location())
		}
	}
}

func TestTimesOfNothingAreZero(t *testing.T) {
	blockless := &fragment.Fragment{}
	for name, got := range map[string]time.Time{
		"StartTime(nil)":       StartTime(nil),
		"EndTime(nil)":         EndTime(nil),
		"ClusterTime(nil)":     ClusterTime(nil),
		"BlockTime(nil,nil)":   BlockTime(nil, nil),
		"StartTime(blockless)": StartTime(blockless),
		"EndTime(blockless)":   EndTime(blockless),
	} {
		if !got.IsZero() {
			t.Errorf("%s = %v, want the zero Time so IsZero can tell it apart from the epoch", name, got)
		}
	}
	// A Cluster that declared no Timestamp IS at zero ticks, which is the epoch and
	// not an absent time: Matroska gives an absent Timestamp and a zero one the same
	// meaning, so reporting the zero Time here would invent a distinction.
	if got := ClusterTime(blockless); got.IsZero() {
		t.Error("ClusterTime of a Timestamp-less Cluster reported the zero Time, not the epoch")
	}
}
