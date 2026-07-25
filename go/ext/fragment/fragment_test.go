package fragment_test

import (
	"encoding/hex"
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/yacchi/ebml/ext/fragment"
	"github.com/yacchi/ebml/ext/tree"
	"github.com/yacchi/ebml/matroska"
	"github.com/yacchi/ebml/parser"
)

// Well-known AWS KVS tag names used by the fixtures under fixtures/kvs/. These
// are consumer conventions, not part of the generic fragment package API; see
// examples/kvs-getmedia for how a real consumer reads them through Fragment.Tag.
const (
	tagFragmentNumber    = "AWS_KINESISVIDEO_FRAGMENT_NUMBER"
	tagContinuationToken = "AWS_KINESISVIDEO_CONTINUATION_TOKEN"
	tagProducerTimestamp = "AWS_KINESISVIDEO_PRODUCER_TIMESTAMP"
)

// Element IDs the fixtures carry that no registry knows: unknown_elements plants
// an unregistered leaf and an unregistered master-shaped element.
const (
	idUnregisteredLeaf   parser.ElementID = 0xEE
	idUnregisteredMaster parser.ElementID = 0x4FFF
)

// loadHex reads a fixtures/kvs/<name>.ebml.hex file, stripping comment lines
// (starting with '#') and decoding the whitespace-separated hex body.
func loadHex(t *testing.T, name string) []byte {
	t.Helper()
	path := filepath.Join("..", "..", "..", "fixtures", "kvs", name+".ebml.hex")
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

func run(t *testing.T, chunks [][]byte, opts ...fragment.Option) runResult {
	t.Helper()
	a := fragment.New(opts...)
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

// parseDecimalSeconds converts a decimal seconds-since-epoch string (as used by
// the tagProducerTimestamp fixture values) into a UTC time.Time. Up to 9
// fractional digits are honored as nanoseconds; extra digits are truncated.
func parseDecimalSeconds(t *testing.T, s string) time.Time {
	t.Helper()
	intPart, fracPart := s, ""
	if i := strings.IndexByte(s, '.'); i >= 0 {
		intPart, fracPart = s[:i], s[i+1:]
	}
	sec, err := strconv.ParseInt(intPart, 10, 64)
	if err != nil {
		t.Fatalf("parse producer timestamp %q: %v", s, err)
	}
	var nsec int64
	if fracPart != "" {
		if len(fracPart) > 9 {
			fracPart = fracPart[:9]
		}
		f, err := strconv.ParseInt(fracPart, 10, 64)
		if err != nil {
			t.Fatalf("parse producer timestamp %q: %v", s, err)
		}
		for i := len(fracPart); i < 9; i++ {
			f *= 10
		}
		nsec = f
	}
	return time.Unix(sec, nsec).UTC()
}

// allFixtures is every committed KVS fixture.
var allFixtures = []string{
	"topology_basic",
	"two_tracks",
	"multi_cluster",
	"multi_segment",
	"tagless_single",
	"tagless_consecutive",
	"partial_tags",
	"filter_mismatch",
	"gap",
	"false_ebml_magic_in_pcm",
	"tail_last_fragment",
	"scaled_timestamps",
	"unknown_elements",
}

// project renders a Fragment as text: both retained trees node by node (identity,
// extent, retention state and payload) plus the decoded blocks and the derived
// values. Comparing projections is how two runs are asserted to have produced the
// same fragments, since the trees themselves are pointer graphs.
func project(f *fragment.Fragment) string {
	var b strings.Builder
	render := func(label string, root *tree.Element) {
		if root == nil {
			fmt.Fprintf(&b, "%s: absent\n", label)
			return
		}
		root.Walk(func(e *tree.Element) bool {
			fmt.Fprintf(&b, "%s %s off=%d hlen=%d size=%d trunc=%v payload=%s\n",
				label, formatPath(e.Path()), e.Offset, e.HeaderLen, e.Size, e.Truncated,
				hex.EncodeToString(e.Bytes()))
			return true
		})
	}
	render("segment", f.Segment)
	render("cluster", f.Cluster)
	for i, blk := range f.Blocks {
		var pcm []byte
		for _, frame := range blk.Frames {
			pcm = append(pcm, frame...)
		}
		fmt.Fprintf(&b, "block %d track=%d timecode=%d time=%s pcm=%s\n",
			i, blk.TrackNumber, blk.Timecode, f.BlockTime(blk), hex.EncodeToString(pcm))
	}
	tags := f.Tags()
	names := make([]string, 0, len(tags))
	for name := range tags {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		fmt.Fprintf(&b, "tag %s=%s\n", name, tags[name])
	}
	fmt.Fprintf(&b, "scale=%d cluster_ts=%d start=%s end=%s tracks=%d\n",
		f.TimestampScale(), f.ClusterTimestamp(), f.StartTime(), f.EndTime(), len(f.Tracks()))
	return b.String()
}

func formatPath(path []parser.ElementID) string {
	parts := make([]string, 0, len(path))
	for _, id := range path {
		parts = append(parts, id.String())
	}
	return strings.Join(parts, ">")
}

func projectAll(frags []*fragment.Fragment) []string {
	out := make([]string, 0, len(frags))
	for _, f := range frags {
		out = append(out, project(f))
	}
	return out
}

// TestSplitInvariance asserts every fixture yields identical Fragments under
// one_byte, random and fibonacci chunking (the core streaming guarantee).
func TestSplitInvariance(t *testing.T) {
	for _, name := range allFixtures {
		name := name
		t.Run(name, func(t *testing.T) {
			raw := loadHex(t, name)
			want := projectAll(run(t, splitOneByte(raw)).all())
			if len(want) == 0 {
				t.Fatalf("fixture %s produced no fragments", name)
			}
			for _, sp := range []struct {
				label  string
				chunks [][]byte
			}{
				{"random", splitRandom(raw, 12345, 7)},
				{"fibonacci", splitFibonacci(raw)},
				{"whole", [][]byte{raw}},
			} {
				got := projectAll(run(t, sp.chunks).all())
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
	fn, ok := f.Tag(tagFragmentNumber)
	if !ok || fn != "91343852333181000000000000000000000000000000001" {
		t.Fatalf("FragmentNumber = %q ok=%v", fn, ok)
	}
	if _, ok := f.Tag(tagContinuationToken); !ok {
		t.Fatalf("ContinuationToken missing")
	}
	ts := parseDecimalSeconds(t, mustTag(t, f, tagProducerTimestamp))
	if ts.Unix() != 1000000000 || ts.Nanosecond() != 0 {
		t.Fatalf("ProducerTimestamp = %v", ts)
	}
	if got, want := len(f.Tags()), 5; got != want {
		t.Fatalf("len(Tags()) = %d, want %d", got, want)
	}

	// Tracks are retained nodes, read through the registry's element IDs.
	tracks := f.Tracks()
	if len(tracks) != 2 {
		t.Fatalf("want 2 tracks, got %d", len(tracks))
	}
	for i, want := range []struct {
		number uint64
		name   string
	}{{1, "AUDIO_FROM_CUSTOMER"}, {2, "AUDIO_TO_CUSTOMER"}} {
		number, err := tracks[i].Find(matroska.IDTrackNumber).AsUint()
		if err != nil || number != want.number {
			t.Fatalf("track[%d] TrackNumber = %d (err %v), want %d", i, number, err, want.number)
		}
		if got := tracks[i].Find(matroska.IDName).AsString(); got != want.name {
			t.Fatalf("track[%d] Name = %q, want %q", i, got, want.name)
		}
		if got := tracks[i].Find(matroska.IDCodecID).AsString(); got != "A_PCM/INT/LIT" {
			t.Fatalf("track[%d] CodecID = %q", i, got)
		}
		entry, ok := f.Track(want.number)
		if !ok || entry != tracks[i] {
			t.Fatalf("Track(%d) did not return track[%d]", want.number, i)
		}
		if entry, ok := f.TrackByName(want.name); !ok || entry != tracks[i] {
			t.Fatalf("TrackByName(%q) did not return track[%d]", want.name, i)
		}
	}
	if _, ok := f.Track(99); ok {
		t.Fatal("Track(99) reported found")
	}
	if _, ok := f.TrackByName("nope"); ok {
		t.Fatal("TrackByName(\"nope\") reported found")
	}

	// Segment Info: synthetic SegmentUUID (seed 0x10 => 0x10..0x1f) and the
	// Matroska-default scale, which the Segment declares explicitly here.
	wantUID := make([]byte, 16)
	for i := range wantUID {
		wantUID[i] = 0x10 + byte(i)
	}
	if got := f.Segment.Find(matroska.IDInfo, matroska.IDSegmentUUID).Bytes(); !reflect.DeepEqual(got, wantUID) {
		t.Fatalf("SegmentUUID = %x, want %x", got, wantUID)
	}
	if got := f.TimestampScale(); got != 1_000_000 {
		t.Fatalf("TimestampScale = %d, want 1000000", got)
	}
	if got := f.ClusterTimestamp(); got != 0 {
		t.Fatalf("ClusterTimestamp = %d", got)
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

	// The Cluster is a root of its own and holds no accumulated siblings.
	if f.Cluster.Parent() != nil {
		t.Fatal("Cluster must be a root, not a child of the Segment tree")
	}
	if got := len(f.Segment.ChildrenByID(matroska.IDCluster)); got != 0 {
		t.Fatalf("Segment retained %d Cluster children, want 0", got)
	}
	// Block payloads live in Blocks, not twice in the tree.
	for _, blk := range f.Cluster.ChildrenByID(matroska.IDSimpleBlock) {
		if !blk.Truncated || blk.Bytes() != nil {
			t.Fatalf("SimpleBlock at %d retained its payload; want it elided", blk.Offset)
		}
		if blk.Size != 36 {
			t.Fatalf("SimpleBlock at %d Size = %d, want 36 (extent still accurate)", blk.Offset, blk.Size)
		}
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
	if got := tail.ClusterTimestamp(); got != 1024 {
		t.Fatalf("tail ClusterTimestamp = %d", got)
	}
	if len(tail.Blocks) != 2 {
		t.Fatalf("tail blocks = %d", len(tail.Blocks))
	}
	if fn, _ := tail.Tag(tagFragmentNumber); fn != "tail-1" {
		t.Fatalf("tail FragmentNumber = %q", fn)
	}
}

func TestMultiSegment(t *testing.T) {
	raw := loadHex(t, "multi_segment")
	frags := run(t, splitRandom(raw, 12345, 7)).all()
	if len(frags) != 4 {
		t.Fatalf("want 4 fragments, got %d", len(frags))
	}
	wantTS := []int64{1000000000, 1000000001, 1000000002, 1000000003}
	for i, f := range frags {
		if ts := parseDecimalSeconds(t, mustTag(t, f, tagProducerTimestamp)); ts.Unix() != wantTS[i] {
			t.Fatalf("fragment %d ProducerTimestamp = %v", i, ts)
		}
		if got := f.ClusterTimestamp(); got != uint64(i)*1024 {
			t.Fatalf("fragment %d ClusterTimestamp = %d", i, got)
		}
	}
	// Each fragment carries its own Segment tree: no two share one, and only the
	// first Segment declares Info.
	for i := 1; i < len(frags); i++ {
		if frags[i].Segment == frags[i-1].Segment {
			t.Fatalf("fragments %d and %d share a Segment tree", i-1, i)
		}
		if info := frags[i].Segment.Find(matroska.IDInfo); info.Exists() {
			t.Fatalf("fragment %d has an Info element leaked from an earlier Segment", i)
		}
		if got := frags[i].TimestampScale(); got != 1_000_000 {
			t.Fatalf("fragment %d TimestampScale = %d, want the 1000000 default", i, got)
		}
	}
	if !frags[0].Segment.Find(matroska.IDInfo).Exists() {
		t.Fatal("fragment 0 should carry an Info element")
	}
}

func TestTaglessSingle(t *testing.T) {
	raw := loadHex(t, "tagless_single")
	frags := run(t, splitRandom(raw, 12345, 7)).all()
	if len(frags) != 3 {
		t.Fatalf("want 3 fragments, got %d", len(frags))
	}
	// Middle fragment's Segment omits the entire Tags element.
	tags := frags[1].Tags()
	if tags == nil {
		t.Fatal("Tags() must never be nil, even for a tagless Segment")
	}
	if len(tags) != 0 {
		t.Fatalf("middle fragment should be tagless, got %v", tags)
	}
	if _, ok := frags[1].Tag("ContactId"); ok {
		t.Fatalf("tagless fragment must not have ContactId")
	}
	// It still parses structurally: two declared tracks + a block.
	if len(frags[1].Tracks()) != 2 || len(frags[1].Blocks) != 1 {
		t.Fatalf("tagless fragment structure: tracks=%d blocks=%d", len(frags[1].Tracks()), len(frags[1].Blocks))
	}
	for _, i := range []int{0, 2} {
		if _, ok := frags[i].Tag("ContactId"); !ok {
			t.Fatalf("fragment %d should have ContactId", i)
		}
	}
}

func TestTaglessConsecutive(t *testing.T) {
	raw := loadHex(t, "tagless_consecutive")
	frags := run(t, splitFibonacci(raw)).all()
	if len(frags) != 4 {
		t.Fatalf("want 4 fragments, got %d", len(frags))
	}

	for i, wantTagged := range []bool{true, false, false, true} {
		_, ok := frags[i].Tag("ContactId")
		if ok != wantTagged {
			t.Fatalf("fragment %d ContactId present = %v, want %v", i, ok, wantTagged)
		}
	}
}

func TestPartialTags(t *testing.T) {
	raw := loadHex(t, "partial_tags")
	frags := run(t, splitRandom(raw, 12345, 7)).all()
	if len(frags) != 2 {
		t.Fatalf("want 2 fragments, got %d", len(frags))
	}
	if got := len(frags[1].Tags()); got != 3 {
		t.Fatalf("partial fragment Tags() length = %d, want 3", got)
	}
	if _, ok := frags[1].Tag("ContactId"); ok {
		t.Fatal("partial fragment must omit ContactId in the source Tags")
	}
	if _, ok := frags[1].Tag("InstanceId"); ok {
		t.Fatal("partial fragment must omit InstanceId in the source Tags")
	}
	if got := frags[1].Value(matroska.IDSegmentUUID).Bytes(); !reflect.DeepEqual(got, frags[0].Value(matroska.IDSegmentUUID).Bytes()) {
		t.Fatalf("partial fragment UUID = %x, want %x", got, frags[0].Value(matroska.IDSegmentUUID).Bytes())
	}
}

func TestTwoTracks(t *testing.T) {
	raw := loadHex(t, "two_tracks")
	frags := run(t, splitFibonacci(raw)).all()
	if len(frags) != 1 {
		t.Fatalf("want 1 fragment, got %d", len(frags))
	}
	f := frags[0]
	if len(f.Blocks) != 3 {
		t.Fatalf("want 3 blocks, got %d", len(f.Blocks))
	}
	wantTrack1 := make([]byte, 0, 40)
	for i := byte(0); i < 24; i++ {
		wantTrack1 = append(wantTrack1, 0x11+i)
	}
	for i := byte(0); i < 16; i++ {
		wantTrack1 = append(wantTrack1, 0x31+i)
	}
	wantTrack2 := make([]byte, 0, 32)
	for i := byte(0); i < 32; i++ {
		wantTrack2 = append(wantTrack2, 0x21+i)
	}
	if got := f.TrackPCM(1); !reflect.DeepEqual(got, wantTrack1) {
		t.Fatalf("TrackPCM(1) = %x, want %x", got, wantTrack1)
	}
	if got := f.TrackPCM(2); !reflect.DeepEqual(got, wantTrack2) {
		t.Fatalf("TrackPCM(2) = %x, want %x", got, wantTrack2)
	}
	if len(f.TrackPCM(1)) == len(f.TrackPCM(2)) {
		t.Fatal("track PCM lengths must differ")
	}
	for _, name := range []string{"AUDIO_FROM_CUSTOMER", "AUDIO_TO_CUSTOMER"} {
		if _, ok := f.TrackByName(name); !ok {
			t.Fatalf("TrackByName(%q) did not resolve", name)
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
	wantTS := []uint64{0, 1024, 4096}
	for i, f := range frags {
		if fn, _ := f.Tag(tagFragmentNumber); fn != want[i] {
			t.Fatalf("fragment %d FragmentNumber = %q, want %q", i, fn, want[i])
		}
		if got := f.ClusterTimestamp(); got != wantTS[i] {
			t.Fatalf("fragment %d ClusterTimestamp = %d, want %d", i, got, wantTS[i])
		}
	}
}

// TestFilterMismatch asserts a mid-stream metadata change is just data: the
// ContactId switches from A to B at fragment 2 and every fragment parses the same.
func TestFilterMismatch(t *testing.T) {
	raw := loadHex(t, "filter_mismatch")
	frags := run(t, splitRandom(raw, 12345, 7)).all()
	if len(frags) != 4 {
		t.Fatalf("want 4 fragments, got %d", len(frags))
	}
	want := []string{
		"00000000-0000-4000-8000-000000000001",
		"00000000-0000-4000-8000-000000000001",
		"00000000-0000-4000-8000-000000000002",
		"00000000-0000-4000-8000-000000000002",
	}
	for i, f := range frags {
		if got := mustTag(t, f, "ContactId"); got != want[i] {
			t.Fatalf("fragment %d ContactId = %q, want %q", i, got, want[i])
		}
		if len(f.Blocks) != 1 {
			t.Fatalf("fragment %d blocks = %d, want 1", i, len(f.Blocks))
		}
	}
}

// TestMultiCluster asserts two Clusters in one Segment yield two Fragments that
// SHARE the Segment tree, both delivered before EOF, with distinct Clusters.
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
	if frags[0].Segment != frags[1].Segment {
		t.Fatal("Clusters of one Segment must share that Segment's tree")
	}
	if frags[0].Cluster == frags[1].Cluster {
		t.Fatal("each Cluster must be its own tree")
	}
	if !reflect.DeepEqual(frags[0].Tags(), frags[1].Tags()) {
		t.Fatalf("clusters should share tags: %v vs %v", frags[0].Tags(), frags[1].Tags())
	}
	if fn, _ := frags[0].Tag(tagFragmentNumber); fn != "multi-cluster" {
		t.Fatalf("FragmentNumber = %q", fn)
	}
	if frags[0].ClusterTimestamp() != 0 || frags[1].ClusterTimestamp() != 1024 {
		t.Fatalf("cluster timestamps = %d, %d", frags[0].ClusterTimestamp(), frags[1].ClusterTimestamp())
	}
	for i, f := range frags {
		if len(f.Blocks) != 2 {
			t.Fatalf("cluster %d blocks = %d, want 2", i, len(f.Blocks))
		}
	}
	// The shared Segment never accumulates the Clusters that pass through it.
	if got := len(frags[1].Segment.ChildrenByID(matroska.IDCluster)); got != 0 {
		t.Fatalf("shared Segment accumulated %d Clusters", got)
	}
}

// TestStrictAndLooseAgree checks the two access modes reach the same value: the
// SegmentUUID found by its exact path and the one found by ID alone are the same
// node.
func TestStrictAndLooseAgree(t *testing.T) {
	frags := run(t, splitFibonacci(loadHex(t, "topology_basic"))).all()
	f := frags[0]

	strict := f.Segment.Find(matroska.IDInfo, matroska.IDSegmentUUID)
	loose := f.Value(matroska.IDSegmentUUID)
	if !strict.Exists() {
		t.Fatal("strict lookup found no SegmentUUID")
	}
	if strict != loose {
		t.Fatalf("strict and loose lookups returned different nodes (%v vs %v)", strict, loose)
	}
	if !reflect.DeepEqual(strict.Bytes(), loose.Bytes()) {
		t.Fatalf("strict %x != loose %x", strict.Bytes(), loose.Bytes())
	}
	// A loose miss is nil and still safe to read through.
	if got := f.Value(0x4242); got != nil {
		t.Fatalf("Value of an absent ID = %v, want nil", got)
	}
	if got := f.Value(0x4242).Bytes(); got != nil {
		t.Fatalf("Bytes() of a nil result = %x, want nil", got)
	}
	if _, err := f.Value(0x4242).AsUint(); err == nil {
		t.Fatal("AsUint() of a nil result should report an error, not panic")
	}
}

// TestValuesReturnsEveryOccurrenceInStreamOrder checks the loose lookup is
// exhaustive and ordered: SimpleTag repeats inside one Tags element, and every
// occurrence comes back in the order the stream carried it.
func TestValuesReturnsEveryOccurrenceInStreamOrder(t *testing.T) {
	f := run(t, splitFibonacci(loadHex(t, "topology_basic"))).all()[0]

	tags := f.Values(matroska.IDSimpleTag)
	if len(tags) != 5 {
		t.Fatalf("Values(SimpleTag) returned %d elements, want 5", len(tags))
	}
	var names []string
	for i, st := range tags {
		if i > 0 && st.Offset <= tags[i-1].Offset {
			t.Fatalf("Values is not in stream order: element %d at %d follows %d", i, st.Offset, tags[i-1].Offset)
		}
		names = append(names, st.Find(matroska.IDTagName).AsString())
	}
	want := []string{
		tagProducerTimestamp, tagFragmentNumber, tagContinuationToken, "ContactId", "InstanceId",
	}
	if !reflect.DeepEqual(names, want) {
		t.Fatalf("SimpleTag names = %v, want %v", names, want)
	}

	// Scope: Segment metadata first, then Cluster contents.
	blocks := f.Values(matroska.IDSimpleBlock)
	if len(blocks) != 3 {
		t.Fatalf("Values(SimpleBlock) returned %d, want 3", len(blocks))
	}
	if blocks[0].Offset < tags[len(tags)-1].Offset {
		t.Fatal("Cluster contents must follow Segment metadata in Values order")
	}
	// The roots themselves are in scope.
	if got := f.Value(matroska.IDSegment); got != f.Segment {
		t.Fatalf("Value(Segment) = %v, want the Segment root", got)
	}
	if got := f.Value(matroska.IDCluster); got != f.Cluster {
		t.Fatalf("Value(Cluster) = %v, want the Cluster root", got)
	}
}

// TestLooseResultDisambiguatedByItsOwnPath checks the bridge between the modes: a
// loose lookup for an ID that occurs under two different parents returns nodes
// that still know where they are, so the caller tightens afterwards instead of
// committing to a path up front.
func TestLooseResultDisambiguatedByItsOwnPath(t *testing.T) {
	reg := matroska.NewRegistry(matroska.Default())
	if err := reg.Register(matroska.ElementInfo{ID: idUnregisteredMaster, Name: "VendorBox", Type: matroska.TypeMaster}); err != nil {
		t.Fatalf("Register: %v", err)
	}
	f := run(t, splitFibonacci(loadHex(t, "unknown_elements")), fragment.WithRegistry(reg)).all()[0]

	// Name occurs under each TrackEntry and once inside the vendor box.
	names := f.Values(matroska.IDName)
	if len(names) != 3 {
		t.Fatalf("Values(Name) returned %d elements, want 3", len(names))
	}
	var trackNames, otherNames []string
	for _, name := range names {
		if name.Ancestor(matroska.IDTrackEntry).Exists() {
			trackNames = append(trackNames, name.AsString())
			continue
		}
		otherNames = append(otherNames, name.AsString())
		if got := formatPath(name.Path()); got != "0x18538067>0x4FFF>0x536E" {
			t.Fatalf("vendor Name path = %s", got)
		}
		if box := name.Parent(); box.ID != idUnregisteredMaster || box.Name() != "VendorBox" {
			t.Fatalf("vendor Name parent = %s (%q)", box.ID, box.Name())
		}
	}
	if !reflect.DeepEqual(trackNames, []string{"AUDIO_FROM_CUSTOMER", "AUDIO_TO_CUSTOMER"}) {
		t.Fatalf("track names = %v", trackNames)
	}
	if !reflect.DeepEqual(otherNames, []string{"vendor-box"}) {
		t.Fatalf("non-track names = %v", otherNames)
	}
	// Strict access is unaffected by the extra Name: only real tracks are tracks.
	if got := len(f.Tracks()); got != 2 {
		t.Fatalf("Tracks() = %d, want 2", got)
	}
	if _, ok := f.Track(7); ok {
		t.Fatal("the vendor box's TrackNumber must not read as a track")
	}
}

// TestUnregisteredElementIsRetainedAndReadable checks retention has no allowlist:
// the unregistered leaf 0xEE is retained like any other element and its payload
// decodes, while the registry simply has no name for it.
func TestUnregisteredElementIsRetainedAndReadable(t *testing.T) {
	f := run(t, splitOneByte(loadHex(t, "unknown_elements"))).all()[0]

	el := f.Value(idUnregisteredLeaf)
	if el == nil {
		t.Fatal("the unregistered leaf 0xEE was not retained")
	}
	if el.Parent() != f.Segment {
		t.Fatalf("0xEE parent = %v, want the Segment root", el.Parent())
	}
	v, err := el.AsUint()
	if err != nil {
		t.Fatalf("AsUint: %v", err)
	}
	if v != 42 {
		t.Fatalf("0xEE = %d, want 42", v)
	}
	if got := el.Name(); got != "" {
		t.Fatalf("Name() = %q, want \"\" for an unregistered ID", got)
	}
	if got := el.Describe(); got != "0xEE" {
		t.Fatalf("Describe() = %q, want the bare hex ID", got)
	}
	if got, ok := matroska.TypeFor(el.ID); ok {
		t.Fatalf("TypeFor(0xEE) = %v, %v; want unknown", got, ok)
	}

	// Without a registry entry the master-shaped 0x4FFF is one opaque leaf whose
	// bytes are complete, so its structure is recoverable afterwards.
	box := f.Value(idUnregisteredMaster)
	if box == nil {
		t.Fatal("0x4FFF was not retained")
	}
	if len(box.Children) != 0 {
		t.Fatalf("0x4FFF has %d children without a registry entry, want 0", len(box.Children))
	}
	roots, err := tree.Parse(box.Bytes())
	if err != nil {
		t.Fatalf("re-parsing the opaque payload: %v", err)
	}
	if len(roots) != 2 || roots[0].AsString() != "vendor-box" {
		t.Fatalf("re-parsed payload = %v", roots)
	}
}

// TestWithRegistryMakesUnknownMasterNest checks the extension path: registering
// 0x4FFF as a master is what makes the cursor descend into it, so its children
// become retained nodes at the right depth, under its registered name.
func TestWithRegistryMakesUnknownMasterNest(t *testing.T) {
	reg := matroska.NewRegistry(matroska.Default())
	if err := reg.Register(matroska.ElementInfo{ID: idUnregisteredMaster, Name: "VendorBox", Type: matroska.TypeMaster}); err != nil {
		t.Fatalf("Register: %v", err)
	}
	raw := loadHex(t, "unknown_elements")

	plain := run(t, splitFibonacci(raw)).all()[0]
	if got := len(plain.Value(idUnregisteredMaster).Children); got != 0 {
		t.Fatalf("without the registry 0x4FFF has %d children, want 0", got)
	}

	f := run(t, splitFibonacci(raw), fragment.WithRegistry(reg)).all()[0]
	box := f.Value(idUnregisteredMaster)
	if box == nil {
		t.Fatal("0x4FFF was not retained")
	}
	if got := box.Name(); got != "VendorBox" {
		t.Fatalf("Name() = %q, want VendorBox (the tree must resolve through the given registry)", got)
	}
	if got := box.Type(); got != matroska.TypeMaster {
		t.Fatalf("Type() = %v, want master", got)
	}
	if len(box.Children) != 2 {
		t.Fatalf("0x4FFF has %d children, want 2", len(box.Children))
	}
	if box.Payload != nil {
		t.Fatal("a descended master must retain no payload")
	}
	if got := box.Find(matroska.IDName).AsString(); got != "vendor-box" {
		t.Fatalf("VendorBox>Name = %q", got)
	}
	number, err := box.Find(matroska.IDTrackNumber).AsUint()
	if err != nil || number != 7 {
		t.Fatalf("VendorBox>TrackNumber = %d (err %v), want 7", number, err)
	}
	if box.Children[0].Depth() != 2 {
		t.Fatalf("child Depth() = %d, want 2 (Segment>VendorBox>child)", box.Children[0].Depth())
	}
}

// TestScaledTimestamps checks the timestamp semantics: both operands are in
// TimestampScale units, and a negative relative timecode places a block before
// its Cluster's timestamp. Every expectation is recomputed here from the fixture's
// declared values rather than read back from the accessor under test.
func TestScaledTimestamps(t *testing.T) {
	f := run(t, splitOneByte(loadHex(t, "scaled_timestamps"))).all()[0]

	const (
		wantScale     = uint64(100000)
		wantClusterTS = uint64(1000)
	)
	if got := f.TimestampScale(); got != wantScale {
		t.Fatalf("TimestampScale = %d, want %d (the Segment declares a non-default scale)", got, wantScale)
	}
	if got := f.ClusterTimestamp(); got != wantClusterTS {
		t.Fatalf("ClusterTimestamp = %d, want %d", got, wantClusterTS)
	}
	if len(f.Blocks) != 3 {
		t.Fatalf("want 3 blocks, got %d", len(f.Blocks))
	}
	wantTimecodes := []int16{-20, 0, 20}
	for i, blk := range f.Blocks {
		if blk.Timecode != wantTimecodes[i] {
			t.Fatalf("block %d Timecode = %d, want %d", i, blk.Timecode, wantTimecodes[i])
		}
		// Recomputed independently: ticks are summed first, then scaled once.
		ticks := int64(wantClusterTS) + int64(wantTimecodes[i])
		want := time.Duration(ticks) * time.Duration(wantScale)
		if got := f.BlockTime(blk); got != want {
			t.Fatalf("BlockTime(block %d) = %s, want %s", i, got, want)
		}
	}
	// 980 * 100000 ns = 98 ms; the negative timecode really does move it earlier
	// than the Cluster timestamp's own 100 ms.
	if got, want := f.BlockTime(f.Blocks[0]), 98*time.Millisecond; got != want {
		t.Fatalf("first block time = %s, want %s", got, want)
	}
	if got := f.BlockTime(f.Blocks[0]); got >= time.Duration(wantClusterTS)*time.Duration(wantScale) {
		t.Fatalf("negative timecode did not move the block before the Cluster timestamp (%s)", got)
	}
	if got, want := f.StartTime(), 98*time.Millisecond; got != want {
		t.Fatalf("StartTime = %s, want %s", got, want)
	}
	if got, want := f.EndTime(), 102*time.Millisecond; got != want {
		t.Fatalf("EndTime = %s, want %s", got, want)
	}
	if f.BlockTime(nil) != 0 {
		t.Fatal("BlockTime(nil) must be 0")
	}
}

// TestWithMaxRetainedPayload checks the retention cap: an oversized leaf keeps its
// structure and extent while its payload is elided, and the cap does not disturb
// block decoding.
func TestWithMaxRetainedPayload(t *testing.T) {
	raw := loadHex(t, "topology_basic")

	full := run(t, splitFibonacci(raw)).all()[0]
	uid := full.Value(matroska.IDSegmentUUID)
	if len(uid.Bytes()) != 16 {
		t.Fatalf("uncapped SegmentUUID payload = %d bytes, want 16", len(uid.Bytes()))
	}

	// A cap below the 16-byte SegmentUUID but above the small scalars.
	f := run(t, splitFibonacci(raw), fragment.WithMaxRetainedPayload(8)).all()[0]
	capped := f.Value(matroska.IDSegmentUUID)
	if capped == nil {
		t.Fatal("an over-cap leaf must still be retained")
	}
	if !capped.Truncated {
		t.Fatal("an over-cap leaf must be marked Truncated")
	}
	if capped.Bytes() != nil {
		t.Fatalf("an over-cap leaf must retain no payload, got %x", capped.Bytes())
	}
	if capped.Offset != uid.Offset || capped.Size != uid.Size || capped.End() != uid.End() {
		t.Fatalf("truncation changed the extent: %+v vs %+v", capped, uid)
	}
	if _, err := capped.AsUint(); err == nil {
		t.Fatal("reading an elided payload must report an error")
	}
	// Under-cap leaves are untouched, and blocks are decoded regardless of the cap.
	if got := f.TimestampScale(); got != 1_000_000 {
		t.Fatalf("TimestampScale under a cap = %d, want 1000000", got)
	}
	if got := len(f.TrackPCM(1)); got != 96 {
		t.Fatalf("TrackPCM(1) under a cap = %d bytes, want 96", got)
	}

	// Zero retains structure without copying a single metadata byte.
	structureOnly := run(t, splitFibonacci(raw), fragment.WithMaxRetainedPayload(0)).all()[0]
	structureOnly.Segment.Walk(func(e *tree.Element) bool {
		if e.Bytes() != nil {
			t.Fatalf("%s retained %d payload bytes under a zero cap", e.Describe(), len(e.Bytes()))
		}
		return true
	})
	if got := len(structureOnly.Values(matroska.IDSimpleTag)); got != 5 {
		t.Fatalf("structure under a zero cap: %d SimpleTags, want 5", got)
	}
	if got := len(structureOnly.TrackPCM(1)); got != 96 {
		t.Fatalf("blocks must still decode under a zero cap, got %d PCM bytes", got)
	}
}
