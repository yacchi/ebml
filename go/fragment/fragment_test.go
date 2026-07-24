package fragment_test

import (
	"encoding/hex"
	"math/rand"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/yacchi/ebml-reader/fragment"
)

// loadHex reads a fixtures/kvs/<name>.ebml.hex file, stripping comment lines
// (starting with '#') and decoding the whitespace-separated hex body. This
// intentionally replicates the loader in parser/kvs_fixture_test.go rather than
// importing it (that loader lives in an external test package).
func loadHex(t *testing.T, name string) []byte {
	t.Helper()
	path := filepath.Join("..", "..", "fixtures", "kvs", name+".ebml.hex")
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read fixture %s: %v", name, err)
	}
	var sb strings.Builder
	for _, ln := range strings.Split(string(b), "\n") {
		ln = strings.TrimSpace(ln)
		if ln == "" || strings.HasPrefix(ln, "#") {
			continue
		}
		for _, tok := range strings.Fields(ln) {
			sb.WriteString(tok)
		}
	}
	raw, err := hex.DecodeString(sb.String())
	if err != nil {
		t.Fatalf("decode hex fixture %s: %v", name, err)
	}
	return raw
}

// ---- Split helpers (from tests/split_patterns.json) ----

func splitOneByte(b []byte) [][]byte {
	out := make([][]byte, 0, len(b))
	for i := 0; i < len(b); i++ {
		out = append(out, b[i:i+1])
	}
	return out
}

// splitRandom reproduces the "random" pattern: seed 12345, chunk sizes 1..7.
func splitRandom(b []byte, seed int64, maxChunk int) [][]byte {
	r := rand.New(rand.NewSource(seed))
	var out [][]byte
	for i := 0; i < len(b); {
		n := 1 + r.Intn(maxChunk)
		if i+n > len(b) {
			n = len(b) - i
		}
		out = append(out, b[i:i+n])
		i += n
	}
	return out
}

func splitFibonacci(b []byte) [][]byte {
	var out [][]byte
	a, c := 1, 2
	for i := 0; i < len(b); {
		n := a
		if i+n > len(b) {
			n = len(b) - i
		}
		out = append(out, b[i:i+n])
		i += n
		a, c = c, a+c
	}
	return out
}

// runResult separates fragments delivered during Feed calls from those delivered
// during Finalize, so the tail-fix property (Feed-before-Finalize) is testable.
type runResult struct {
	fromFeed     []*fragment.Fragment
	fromFinalize []*fragment.Fragment
}

func (r runResult) all() []*fragment.Fragment {
	out := make([]*fragment.Fragment, 0, len(r.fromFeed)+len(r.fromFinalize))
	out = append(out, r.fromFeed...)
	out = append(out, r.fromFinalize...)
	return out
}

func run(t *testing.T, chunks [][]byte) runResult {
	t.Helper()
	a := fragment.New()
	var res runResult
	for _, ch := range chunks {
		frags, err := a.Feed(ch)
		if err != nil {
			t.Fatalf("Feed: %v", err)
		}
		res.fromFeed = append(res.fromFeed, frags...)
	}
	frags, err := a.Finalize()
	if err != nil {
		t.Fatalf("Finalize: %v", err)
	}
	res.fromFinalize = append(res.fromFinalize, frags...)
	return res
}

func mustTag(t *testing.T, f *fragment.Fragment, name string) string {
	t.Helper()
	v, ok := f.Tag(name)
	if !ok {
		t.Fatalf("missing tag %s", name)
	}
	return v
}

// allFixtures is every committed KVS fixture.
var allFixtures = []string{
	"topology_basic",
	"multi_cluster",
	"multi_segment",
	"tagless_single",
	"tagless_consecutive",
	"filter_mismatch",
	"gap",
	"false_ebml_magic_in_pcm",
	"tail_last_fragment",
}

// summary is a comparable projection of a Fragment used to assert that different
// chunkings produce identical results.
type summary struct {
	Tags       map[string]string
	Tracks     []fragment.Track
	ClusterTS  uint64
	BlockTrack []uint64
	BlockPCM   []string
}

func summarize(frags []*fragment.Fragment) []summary {
	out := make([]summary, 0, len(frags))
	for _, f := range frags {
		s := summary{
			Tags:      f.Tags,
			Tracks:    f.Tracks,
			ClusterTS: f.ClusterTimestamp,
		}
		for _, b := range f.Blocks {
			s.BlockTrack = append(s.BlockTrack, b.TrackNumber)
			var pcm []byte
			for _, fr := range b.Frames {
				pcm = append(pcm, fr...)
			}
			s.BlockPCM = append(s.BlockPCM, hex.EncodeToString(pcm))
		}
		out = append(out, s)
	}
	return out
}

// TestSplitInvariance asserts every fixture yields identical Fragments under
// one_byte, random, and fibonacci chunking (the core streaming guarantee).
func TestSplitInvariance(t *testing.T) {
	for _, name := range allFixtures {
		name := name
		t.Run(name, func(t *testing.T) {
			raw := loadHex(t, name)
			want := summarize(run(t, splitOneByte(raw)).all())
			for _, sp := range []struct {
				label  string
				chunks [][]byte
			}{
				{"random", splitRandom(raw, 12345, 7)},
				{"fibonacci", splitFibonacci(raw)},
			} {
				got := summarize(run(t, sp.chunks).all())
				if !reflect.DeepEqual(got, want) {
					t.Fatalf("[%s] split %s differs from one_byte\n got=%v\nwant=%v", name, sp.label, got, want)
				}
			}
		})
	}
}

func TestTopologyBasic(t *testing.T) {
	raw := loadHex(t, "topology_basic")
	res := run(t, splitRandom(raw, 12345, 7))
	frags := res.all()
	if len(frags) != 1 {
		t.Fatalf("want 1 fragment, got %d", len(frags))
	}
	f := frags[0]

	if got := mustTag(t, f, "ContactId"); got != "00000000-0000-4000-8000-000000000001" {
		t.Fatalf("ContactId = %q", got)
	}
	if got := mustTag(t, f, "InstanceId"); got == "" {
		t.Fatalf("InstanceId empty")
	}
	fn, ok := f.FragmentNumber()
	if !ok || fn != "91343852333181000000000000000000000000000000001" {
		t.Fatalf("FragmentNumber = %q ok=%v", fn, ok)
	}
	if _, ok := f.ContinuationToken(); !ok {
		t.Fatalf("ContinuationToken missing")
	}
	ts, ok := f.ProducerTimestamp()
	if !ok || ts.Unix() != 1000000000 || ts.Nanosecond() != 0 {
		t.Fatalf("ProducerTimestamp = %v ok=%v", ts, ok)
	}

	if len(f.Tracks) != 1 {
		t.Fatalf("want 1 track, got %d", len(f.Tracks))
	}
	tr := f.Tracks[0]
	if tr.Number != 1 || tr.Type != 2 || tr.CodecID != "A_PCM/INT/LIT" {
		t.Fatalf("track = %+v", tr)
	}

	if f.ClusterTimestamp != 0 {
		t.Fatalf("ClusterTimestamp = %d", f.ClusterTimestamp)
	}
	if len(f.Blocks) != 3 {
		t.Fatalf("want 3 blocks, got %d", len(f.Blocks))
	}
	// 3 SimpleBlocks of 32 PCM bytes each on track 1.
	if got := len(f.TrackPCM(1)); got != 96 {
		t.Fatalf("TrackPCM(1) len = %d, want 96", got)
	}
	if got := f.TrackPCM(99); got != nil {
		t.Fatalf("TrackPCM(99) = %v, want nil", got)
	}
}

// TestTailLastFragment asserts the tail-fix property: the final fragment's
// Cluster is delivered during a Feed call, strictly before Finalize is invoked.
func TestTailLastFragment(t *testing.T) {
	raw := loadHex(t, "tail_last_fragment")
	res := run(t, splitOneByte(raw))

	if len(res.fromFinalize) != 0 {
		t.Fatalf("expected no fragments from Finalize (tail must emit before EOF), got %d", len(res.fromFinalize))
	}
	if len(res.fromFeed) != 2 {
		t.Fatalf("want 2 fragments from Feed, got %d", len(res.fromFeed))
	}
	// Second fragment is the tail: 2 SimpleBlocks, ClusterTimestamp 1024.
	tail := res.fromFeed[1]
	if tail.ClusterTimestamp != 1024 {
		t.Fatalf("tail ClusterTimestamp = %d", tail.ClusterTimestamp)
	}
	if len(tail.Blocks) != 2 {
		t.Fatalf("tail blocks = %d", len(tail.Blocks))
	}
	if fn, _ := tail.FragmentNumber(); fn != "tail-1" {
		t.Fatalf("tail FragmentNumber = %q", fn)
	}
}

func TestMultiSegment(t *testing.T) {
	raw := loadHex(t, "multi_segment")
	frags := run(t, splitRandom(raw, 12345, 7)).all()
	// The committed fixture concatenates 4 fragments (see kvsgen.multiSegment);
	// the task brief's "2 fragments" is a mis-statement of this fixture.
	if len(frags) != 4 {
		t.Fatalf("want 4 fragments, got %d", len(frags))
	}
	wantTS := []int64{1000000000, 1000000001, 1000000002, 1000000003}
	for i, f := range frags {
		ts, ok := f.ProducerTimestamp()
		if !ok || ts.Unix() != wantTS[i] {
			t.Fatalf("fragment %d ProducerTimestamp = %v ok=%v", i, ts, ok)
		}
		if f.ClusterTimestamp != uint64(i)*1024 {
			t.Fatalf("fragment %d ClusterTimestamp = %d", i, f.ClusterTimestamp)
		}
	}
}

func TestTaglessSingle(t *testing.T) {
	raw := loadHex(t, "tagless_single")
	frags := run(t, splitRandom(raw, 12345, 7)).all()
	if len(frags) != 3 {
		t.Fatalf("want 3 fragments, got %d", len(frags))
	}
	// Middle fragment's Segment omits the entire Tags element.
	if len(frags[1].Tags) != 0 {
		t.Fatalf("middle fragment should be tagless, got %v", frags[1].Tags)
	}
	if _, ok := frags[1].Tag("ContactId"); ok {
		t.Fatalf("tagless fragment must not have ContactId")
	}
	// It still parses structurally: track + a block.
	if len(frags[1].Tracks) != 1 || len(frags[1].Blocks) != 1 {
		t.Fatalf("tagless fragment structure: tracks=%d blocks=%d", len(frags[1].Tracks), len(frags[1].Blocks))
	}
	for _, i := range []int{0, 2} {
		if _, ok := frags[i].Tag("ContactId"); !ok {
			t.Fatalf("fragment %d should have ContactId", i)
		}
	}
}

func TestGap(t *testing.T) {
	raw := loadHex(t, "gap")
	frags := run(t, splitRandom(raw, 12345, 7)).all()
	if len(frags) != 3 {
		t.Fatalf("want 3 fragments, got %d", len(frags))
	}
	want := []string{"gap-0", "gap-1", "gap-4"}
	for i, f := range frags {
		fn, _ := f.FragmentNumber()
		if fn != want[i] {
			t.Fatalf("fragment %d FragmentNumber = %q, want %q", i, fn, want[i])
		}
	}
}

// TestMultiCluster asserts two Clusters in one Segment yield two Fragments that
// share the Segment's Tags/Tracks, both delivered before EOF.
func TestMultiCluster(t *testing.T) {
	raw := loadHex(t, "multi_cluster")
	res := run(t, splitRandom(raw, 12345, 7))
	if len(res.fromFinalize) != 0 {
		t.Fatalf("both Clusters must emit before EOF, got %d from Finalize", len(res.fromFinalize))
	}
	frags := res.fromFeed
	if len(frags) != 2 {
		t.Fatalf("want 2 fragments, got %d", len(frags))
	}
	// Shared Segment metadata.
	if !reflect.DeepEqual(frags[0].Tags, frags[1].Tags) {
		t.Fatalf("clusters should share tags: %v vs %v", frags[0].Tags, frags[1].Tags)
	}
	if !reflect.DeepEqual(frags[0].Tracks, frags[1].Tracks) {
		t.Fatalf("clusters should share tracks")
	}
	if fn, _ := frags[0].FragmentNumber(); fn != "multi-cluster" {
		t.Fatalf("FragmentNumber = %q", fn)
	}
	// Distinct Cluster timestamps and blocks.
	if frags[0].ClusterTimestamp != 0 || frags[1].ClusterTimestamp != 1024 {
		t.Fatalf("cluster timestamps = %d, %d", frags[0].ClusterTimestamp, frags[1].ClusterTimestamp)
	}
	for i, f := range frags {
		if len(f.Blocks) != 2 {
			t.Fatalf("cluster %d blocks = %d, want 2", i, len(f.Blocks))
		}
	}
}
