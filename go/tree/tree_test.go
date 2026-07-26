package tree_test

import (
	"encoding/binary"
	"errors"
	"math"
	"testing"
	"time"

	"github.com/yacchi/ebml/internal/ebmltest"
	"github.com/yacchi/ebml/internal/kvsgen"
	"github.com/yacchi/ebml/matroska"
	"github.com/yacchi/ebml/parser"
	"github.com/yacchi/ebml/tree"
	"github.com/yacchi/ebml/writer"
)

// ebmlEpoch mirrors the EBML date origin the accessors decode against.
var ebmlEpoch = time.Date(2001, time.January, 1, 0, 0, 0, 0, time.UTC)

func fixture(t *testing.T, name string) kvsgen.Fixture {
	t.Helper()
	for _, f := range kvsgen.BuildAll() {
		if f.Name == name {
			return f
		}
	}
	t.Fatalf("fixture %q not found", name)
	return kvsgen.Fixture{}
}

func countDescendants(roots []*tree.Element, id parser.ElementID) int {
	n := 0
	for _, root := range roots {
		if root.ID == id {
			n++
		}
		n += len(root.Descendants(id))
	}
	return n
}

// TestParseMatchesFixtureFacts parses every synthetic KVS fixture and checks the
// retained tree against the structural facts the generator recorded. It covers
// the unknown-size Segment rule (a Segment is closed by the next top-level
// element or by the end of the buffer) and, through false_ebml_magic_in_pcm,
// that a SimpleBlock containing the EBML magic bytes is not mis-split.
func TestParseMatchesFixtureFacts(t *testing.T) {
	for _, f := range kvsgen.BuildAll() {
		t.Run(f.Name, func(t *testing.T) {
			roots, err := tree.Parse(f.Data)
			if err != nil {
				t.Fatalf("Parse() error = %v", err)
			}

			headers, segments := 0, 0
			for _, root := range roots {
				switch root.ID {
				case matroska.IDEBML:
					headers++
				case matroska.IDSegment:
					segments++
				default:
					t.Errorf("unexpected top-level element %s", root.Describe())
				}
				if root.Parent() != nil {
					t.Errorf("%s: top-level element has a parent", root.Describe())
				}
				if root.Depth() != 0 {
					t.Errorf("%s: top-level Depth() = %d, want 0", root.Describe(), root.Depth())
				}
			}
			if headers != f.Facts.EBMLHeaders {
				t.Errorf("EBML headers = %d, want %d", headers, f.Facts.EBMLHeaders)
			}
			if segments != f.Facts.Segments {
				t.Errorf("Segments = %d, want %d", segments, f.Facts.Segments)
			}
			if got := countDescendants(roots, matroska.IDCluster); got != f.Facts.Clusters {
				t.Errorf("Clusters = %d, want %d", got, f.Facts.Clusters)
			}
			if got := countDescendants(roots, matroska.IDSimpleBlock); got != f.Facts.SimpleBlocks {
				t.Errorf("SimpleBlocks = %d, want %d", got, f.Facts.SimpleBlocks)
			}
		})
	}
}

// TestParseSizesAndExtents checks that a retained element reports the extent the
// stream declared: a known-size Cluster ends where its size says, and an
// unknown-size Segment reports no end at all.
func TestParseSizesAndExtents(t *testing.T) {
	f := fixture(t, "known_size_cluster")
	roots, err := tree.Parse(f.Data)
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	if len(roots) != 2 {
		t.Fatalf("len(roots) = %d, want 2", len(roots))
	}

	segment := roots[1]
	if segment.Size != parser.UnknownSize {
		t.Errorf("Segment Size = %d, want UnknownSize", segment.Size)
	}
	if segment.End() != parser.UnknownSize {
		t.Errorf("Segment End() = %d, want UnknownSize", segment.End())
	}

	cluster := segment.Find(matroska.IDCluster)
	if !cluster.Exists() {
		t.Fatal("Cluster not found under Segment")
	}
	if cluster.Size < 0 {
		t.Fatalf("Cluster Size = %d, want a known size", cluster.Size)
	}
	if want := cluster.Offset + int64(cluster.HeaderLen) + cluster.Size; cluster.End() != want {
		t.Errorf("Cluster End() = %d, want %d", cluster.End(), want)
	}
	if last := cluster.Children[len(cluster.Children)-1]; last.End() != cluster.End() {
		t.Errorf("last child End() = %d, want the Cluster end %d", last.End(), cluster.End())
	}
	if cluster.Payload != nil {
		t.Error("master element retained a payload")
	}
}

// TestStrictAccess exercises the structural mode: exact downward paths plus the
// upward navigation that describes where a node sits.
func TestStrictAccess(t *testing.T) {
	f := fixture(t, "topology_basic")
	roots, err := tree.Parse(f.Data)
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	segment := roots[1]

	scale, err := segment.Find(matroska.IDInfo, matroska.IDTimestampScale).AsUint()
	if err != nil {
		t.Fatalf("TimestampScale AsUint() error = %v", err)
	}
	if scale != 1000000 {
		t.Errorf("TimestampScale = %d, want 1000000", scale)
	}

	entries := segment.FindAll(matroska.IDTracks, matroska.IDTrackEntry)
	if len(entries) != 2 {
		t.Fatalf("FindAll(Tracks, TrackEntry) = %d entries, want 2", len(entries))
	}
	for i, entry := range entries {
		if entry.Index() != i {
			t.Errorf("TrackEntry %d Index() = %d", i, entry.Index())
		}
		if entry.Depth() != 2 {
			t.Errorf("TrackEntry %d Depth() = %d, want 2", i, entry.Depth())
		}
		if entry.Parent().ID != matroska.IDTracks {
			t.Errorf("TrackEntry %d Parent() = %s, want Tracks", i, entry.Parent().Describe())
		}
		if entry.Root() != segment {
			t.Errorf("TrackEntry %d Root() is not the Segment", i)
		}
	}

	// A miss anywhere along the path yields nil, and the chain still resolves.
	if segment.Find(matroska.IDTracks, matroska.IDCluster, matroska.IDName).Exists() {
		t.Error("Find() through a missing level returned an element")
	}
	if got := segment.FindAll(matroska.IDCues); got != nil {
		t.Errorf("FindAll() with no match = %v, want nil", got)
	}
	if segment.Find() != segment {
		t.Error("Find() with an empty path did not return the receiver")
	}
	if all := segment.FindAll(); len(all) != 1 || all[0] != segment {
		t.Error("FindAll() with an empty path did not return just the receiver")
	}

	// ChildrenByID is the single-level form of the same strict access.
	if got := len(segment.Find(matroska.IDTracks).ChildrenByID(matroska.IDTrackEntry)); got != 2 {
		t.Errorf("ChildrenByID(TrackEntry) = %d, want 2", got)
	}

	// Ancestry: outermost first, and Path is Depth+1 IDs from the retained root.
	tagString := segment.Find(matroska.IDTags, matroska.IDTag, matroska.IDSimpleTag, matroska.IDTagString)
	if !tagString.Exists() {
		t.Fatal("TagString not found")
	}
	wantPath := []parser.ElementID{
		matroska.IDSegment, matroska.IDTags, matroska.IDTag, matroska.IDSimpleTag, matroska.IDTagString,
	}
	gotPath := tagString.Path()
	if len(gotPath) != len(wantPath) {
		t.Fatalf("Path() = %v, want %v", gotPath, wantPath)
	}
	for i := range wantPath {
		if gotPath[i] != wantPath[i] {
			t.Fatalf("Path() = %v, want %v", gotPath, wantPath)
		}
	}
	if len(gotPath) != tagString.Depth()+1 {
		t.Errorf("len(Path()) = %d, want Depth()+1 = %d", len(gotPath), tagString.Depth()+1)
	}
	ancestors := tagString.Ancestors()
	if len(ancestors) != 4 || ancestors[0] != segment || ancestors[3].ID != matroska.IDSimpleTag {
		t.Errorf("Ancestors() = %v, want outermost-first Segment..SimpleTag", ancestors)
	}
	if got := tagString.Ancestor(matroska.IDTag); got == nil || got.ID != matroska.IDTag {
		t.Errorf("Ancestor(Tag) = %v, want the enclosing Tag", got)
	}
	if got := tagString.Ancestor(matroska.IDTagString); got != nil {
		t.Error("Ancestor() matched the element's own ID")
	}
	if got := tagString.Ancestor(matroska.IDCluster); got != nil {
		t.Error("Ancestor() matched an element that does not enclose the receiver")
	}
}

// TestAncestorReturnsNearestMatch pins the "nearest enclosing match" rule using a
// re-parsed nested document, where the same ID encloses a node twice.
func TestAncestorReturnsNearestMatch(t *testing.T) {
	inner := ebmltest.Encode(ebmltest.Master(matroska.IDTag,
		ebmltest.Master(matroska.IDSimpleTag,
			ebmltest.Master(matroska.IDSimpleTag,
				ebmltest.UTF8(matroska.IDTagString, "v"),
			),
		),
	))
	roots, err := tree.Parse(inner)
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	leaf := roots[0].Find(matroska.IDSimpleTag, matroska.IDSimpleTag, matroska.IDTagString)
	if !leaf.Exists() {
		t.Fatal("TagString not found")
	}
	nearest := leaf.Ancestor(matroska.IDSimpleTag)
	if nearest.Depth() != 2 {
		t.Errorf("Ancestor(SimpleTag).Depth() = %d, want the inner one at 2", nearest.Depth())
	}
}

// TestLooseAccess exercises the depth-agnostic mode and the bridge back to strict
// access: a loose lookup returns nodes, so the caller can tighten afterwards.
func TestLooseAccess(t *testing.T) {
	f := fixture(t, "topology_basic")
	roots, err := tree.Parse(f.Data)
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	segment := roots[1]

	// Loose: every occurrence at any depth, in stream order, receiver excluded.
	names := segment.Descendants(matroska.IDName)
	if len(names) != 2 {
		t.Fatalf("Descendants(Name) = %d, want 2", len(names))
	}
	if names[0].AsString() != "AUDIO_FROM_CUSTOMER" || names[1].AsString() != "AUDIO_TO_CUSTOMER" {
		t.Errorf("Descendants(Name) values = %q, %q; want stream order",
			names[0].AsString(), names[1].AsString())
	}
	for _, name := range names {
		if !name.Ancestor(matroska.IDTrackEntry).Exists() {
			t.Error("a loosely found Name could not be tightened to its TrackEntry")
		}
	}
	if got := segment.Descendants(matroska.IDSegment); got != nil {
		t.Error("Descendants() returned the receiver itself")
	}

	// The bridge: pull the AWS fragment number without naming its exact path,
	// then use the node's structure to read its sibling value.
	var fragmentNumber string
	for _, tagName := range segment.Descendants(matroska.IDTagName) {
		if tagName.AsString() != "AWS_KINESISVIDEO_FRAGMENT_NUMBER" {
			continue
		}
		fragmentNumber = tagName.Parent().Find(matroska.IDTagString).AsString()
	}
	if len(f.Facts.FragmentNumbers) == 0 {
		t.Fatal("fixture records no fragment numbers")
	}
	if fragmentNumber != f.Facts.FragmentNumbers[0] {
		t.Errorf("fragment number = %q, want %q", fragmentNumber, f.Facts.FragmentNumbers[0])
	}

	// Walk visits pre-order and stops as soon as the callback declines.
	visited := 0
	segment.Walk(func(*tree.Element) bool {
		visited++
		return visited < 3
	})
	if visited != 3 {
		t.Errorf("Walk() visited %d elements after declining at 3", visited)
	}
	var order []parser.ElementID
	segment.Walk(func(e *tree.Element) bool {
		order = append(order, e.ID)
		return true
	})
	if len(order) < 3 || order[0] != matroska.IDSegment || order[1] != matroska.IDInfo {
		t.Errorf("Walk() order = %v, want pre-order from the receiver", order[:min(3, len(order))])
	}
}

// TestRegistryResolution checks that element knowledge comes from a registry,
// that package matroska is the default, and that WithRegistry overrides it
// without affecting retention or decoding.
func TestRegistryResolution(t *testing.T) {
	data := ebmltest.Encode(ebmltest.Uint(matroska.IDTimestampScale, 1000000))
	roots, err := tree.Parse(data)
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	el := roots[0]
	if el.Name() != "TimestampScale" {
		t.Errorf("Name() = %q, want TimestampScale", el.Name())
	}
	if el.Describe() != matroska.Describe(matroska.IDTimestampScale) {
		t.Errorf("Describe() = %q, want the matroska form", el.Describe())
	}
	if el.Type() != matroska.TypeUint {
		t.Errorf("Type() = %v, want TypeUint", el.Type())
	}

	overridden, err := tree.Parse(data, tree.WithRegistry(fakeRegistry{}))
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	el = overridden[0]
	if el.Name() != "OVERRIDDEN" {
		t.Errorf("Name() = %q, want OVERRIDDEN", el.Name())
	}
	if el.Describe() != "OVERRIDDEN!" {
		t.Errorf("Describe() = %q, want OVERRIDDEN!", el.Describe())
	}
	if el.Type() != matroska.TypeBinary {
		t.Errorf("Type() = %v, want TypeBinary", el.Type())
	}
	if v, err := el.AsUint(); err != nil || v != 1000000 {
		t.Errorf("AsUint() = %d, %v; want 1000000, nil (a registry must not gate decoding)", v, err)
	}
	if tree.WithRegistry(nil) == nil {
		t.Error("WithRegistry(nil) returned a nil Option")
	}

	// A hand-built Element carries no registry and falls back to the default.
	hand := &tree.Element{ID: matroska.IDTimestampScale}
	if hand.Name() != "TimestampScale" {
		t.Errorf("hand-built Name() = %q, want the DefaultRegistry answer", hand.Name())
	}
	if tree.DefaultRegistry == nil {
		t.Error("DefaultRegistry is nil")
	}
}

// TestUnregisteredElementIsRetainedAndReadable is the point of decoding from the
// raw payload: an ID no registry knows still parses, still nests, still reads.
func TestUnregisteredElementIsRetainedAndReadable(t *testing.T) {
	const unknownID parser.ElementID = 0x81 // a 1-byte VINT ID that is not registered
	data := ebmltest.Encode(ebmltest.Leaf(unknownID, []byte{0x02, 0x9A}))
	roots, err := tree.Parse(data)
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	el := roots[0]
	if el.ID != unknownID {
		t.Fatalf("ID = %s, want %s", el.ID, unknownID)
	}
	if el.Name() != "" {
		t.Errorf("Name() = %q, want empty for an unregistered ID", el.Name())
	}
	if el.Type() != matroska.TypeUnknown {
		t.Errorf("Type() = %v, want TypeUnknown", el.Type())
	}
	if el.Describe() == "" {
		t.Error("Describe() is empty for an unregistered ID")
	}
	if v, err := el.AsUint(); err != nil || v != 666 {
		t.Errorf("AsUint() = %d, %v; want 666, nil", v, err)
	}
}

// TestValueAccessors covers each accessor including the length rules, using
// hand-built elements so the zero-value path is exercised too.
func TestValueAccessors(t *testing.T) {
	if v, err := (&tree.Element{Payload: []byte{0x01, 0x00}}).AsUint(); err != nil || v != 256 {
		t.Errorf("AsUint() = %d, %v; want 256, nil", v, err)
	}
	if v, err := (&tree.Element{Payload: []byte{0xFF}}).AsInt(); err != nil || v != -1 {
		t.Errorf("AsInt() = %d, %v; want -1, nil", v, err)
	}

	var f32 [4]byte
	binary.BigEndian.PutUint32(f32[:], math.Float32bits(0.5))
	if v, err := (&tree.Element{Payload: f32[:]}).AsFloat(); err != nil || v != 0.5 {
		t.Errorf("AsFloat() = %v, %v; want 0.5, nil", v, err)
	}
	var f64 [8]byte
	binary.BigEndian.PutUint64(f64[:], math.Float64bits(-2.25))
	if v, err := (&tree.Element{Payload: f64[:]}).AsFloat(); err != nil || v != -2.25 {
		t.Errorf("AsFloat() = %v, %v; want -2.25, nil", v, err)
	}

	// EBML zero-padding: a string stops at the first NUL.
	if got := (&tree.Element{Payload: []byte("abc\x00\x00")}).AsString(); got != "abc" {
		t.Errorf("AsString() = %q, want abc", got)
	}

	// A date is 0 or 8 bytes; empty is exactly the epoch.
	if got, err := (&tree.Element{ID: matroska.IDDateUTC}).AsTime(); err != nil || !got.Equal(ebmlEpoch) {
		t.Errorf("AsTime() on an empty payload = %v, %v; want the EBML epoch", got, err)
	}
	var date [8]byte
	ns := int64(-time.Hour)
	binary.BigEndian.PutUint64(date[:], uint64(ns))
	got, err := (&tree.Element{ID: matroska.IDDateUTC, Payload: date[:]}).AsTime()
	if err != nil {
		t.Fatalf("AsTime() error = %v", err)
	}
	if want := ebmlEpoch.Add(-time.Hour); !got.Equal(want) {
		t.Errorf("AsTime() = %v, want %v (negative offsets predate the epoch)", got, want)
	}
	if _, err := (&tree.Element{ID: matroska.IDDateUTC, Payload: []byte{1, 2, 3}}).AsTime(); !errors.Is(err, tree.ErrValueLength) {
		t.Errorf("AsTime() on a 3-byte payload error = %v, want ErrValueLength", err)
	}
	if _, err := (&tree.Element{Payload: make([]byte, 9)}).AsUint(); !errors.Is(err, tree.ErrValueLength) {
		t.Errorf("AsUint() on a 9-byte payload error = %v, want ErrValueLength", err)
	}

	// Bytes aliases the payload; a master (nil payload) has none.
	payload := []byte{1, 2, 3}
	if got := (&tree.Element{Payload: payload}).Bytes(); len(got) != 3 || &got[0] != &payload[0] {
		t.Error("Bytes() did not alias Payload")
	}
	if got := (&tree.Element{ID: matroska.IDSegment}).Bytes(); got != nil {
		t.Errorf("Bytes() on a master = %v, want nil", got)
	}
}

// TestNilElementIsSafe pins the promise that no method panics on a miss.
func TestNilElementIsSafe(t *testing.T) {
	var e *tree.Element

	if e.Exists() {
		t.Error("Exists() on nil = true")
	}
	if e.Name() != "" || e.Describe() != "" {
		t.Error("Name()/Describe() on nil are not empty")
	}
	if e.Type() != matroska.TypeUnknown {
		t.Errorf("Type() on nil = %v, want TypeUnknown", e.Type())
	}
	if e.End() != parser.UnknownSize {
		t.Errorf("End() on nil = %d, want UnknownSize", e.End())
	}
	if _, err := e.AsUint(); !errors.Is(err, tree.ErrNoElement) {
		t.Errorf("AsUint() on nil error = %v, want ErrNoElement", err)
	}
	if _, err := e.AsInt(); !errors.Is(err, tree.ErrNoElement) {
		t.Errorf("AsInt() on nil error = %v, want ErrNoElement", err)
	}
	if _, err := e.AsFloat(); !errors.Is(err, tree.ErrNoElement) {
		t.Errorf("AsFloat() on nil error = %v, want ErrNoElement", err)
	}
	if _, err := e.AsTime(); !errors.Is(err, tree.ErrNoElement) {
		t.Errorf("AsTime() on nil error = %v, want ErrNoElement", err)
	}
	if e.AsString() != "" || e.Bytes() != nil {
		t.Error("AsString()/Bytes() on nil are not empty")
	}

	// A long navigation chain through a miss must resolve without a nil check.
	if e.Find(matroska.IDSegment).Parent().Root().Ancestor(matroska.IDTags).Exists() {
		t.Error("a chain through nil produced an element")
	}
	if e.ChildrenByID(matroska.IDSegment) != nil || e.FindAll(matroska.IDSegment) != nil ||
		e.Descendants(matroska.IDSegment) != nil || e.Ancestors() != nil || e.Path() != nil {
		t.Error("a slice accessor on nil did not return nil")
	}
	if e.Depth() != 0 || e.Index() != -1 {
		t.Errorf("Depth()/Index() on nil = %d/%d, want 0/-1", e.Depth(), e.Index())
	}
	e.Walk(func(*tree.Element) bool { t.Error("Walk() on nil called fn"); return true })

	// The zero value behaves like a detached element, not a panic.
	var zero tree.Element
	if !zero.Exists() || zero.Depth() != 0 || zero.Index() != -1 || zero.Root() != &zero {
		t.Error("the zero Element does not behave as a detached root")
	}
	(&zero).Walk(nil) // a nil callback is ignored, not dereferenced
}

// TestWithMaxPayload checks the retention cap: structure without leaf bytes, and
// an accessor that says so instead of returning a wrong value.
func TestWithMaxPayload(t *testing.T) {
	f := fixture(t, "topology_basic")

	full, err := tree.Parse(f.Data)
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	capped, err := tree.Parse(f.Data, tree.WithMaxPayload(0))
	if err != nil {
		t.Fatalf("Parse(WithMaxPayload(0)) error = %v", err)
	}
	if len(capped) != len(full) {
		t.Fatalf("capped roots = %d, want %d", len(capped), len(full))
	}

	leaves := 0
	for _, root := range capped {
		root.Walk(func(e *tree.Element) bool {
			if len(e.Children) > 0 || e.ID == matroska.IDTargets {
				return true // a master: nothing to cap
			}
			leaves++
			if e.Size > 0 && !e.Truncated {
				t.Errorf("%s: leaf of %d bytes was retained under a 0-byte cap", e.Describe(), e.Size)
			}
			if e.Truncated {
				if e.Bytes() != nil {
					t.Errorf("%s: Truncated leaf still reports bytes", e.Describe())
				}
				if _, err := e.AsUint(); !errors.Is(err, tree.ErrTruncatedPayload) {
					t.Errorf("%s: AsUint() error = %v, want ErrTruncatedPayload", e.Describe(), err)
				}
				if e.AsString() != "" {
					t.Errorf("%s: AsString() on a Truncated leaf is not empty", e.Describe())
				}
			}
			return true
		})
	}
	if leaves == 0 {
		t.Fatal("no leaves were inspected")
	}

	// Extents stay accurate, so the caller can re-read the elided bytes.
	fullBlock := full[1].Find(matroska.IDCluster).Find(matroska.IDSimpleBlock)
	cappedBlock := capped[1].Find(matroska.IDCluster).Find(matroska.IDSimpleBlock)
	if !cappedBlock.Exists() || !fullBlock.Exists() {
		t.Fatal("SimpleBlock not found")
	}
	if cappedBlock.Offset != fullBlock.Offset || cappedBlock.Size != fullBlock.Size ||
		cappedBlock.HeaderLen != fullBlock.HeaderLen || cappedBlock.End() != fullBlock.End() {
		t.Error("a Truncated leaf lost its extent")
	}
	start := cappedBlock.Offset + int64(cappedBlock.HeaderLen)
	if got := f.Data[start : start+cappedBlock.Size]; len(got) != len(fullBlock.Bytes()) {
		t.Error("the extent of a Truncated leaf does not address its payload")
	}

	// A cap above the largest payload retains everything.
	generous, err := tree.Parse(f.Data, tree.WithMaxPayload(1<<20))
	if err != nil {
		t.Fatalf("Parse(WithMaxPayload(1<<20)) error = %v", err)
	}
	if b := generous[1].Find(matroska.IDCluster).Find(matroska.IDSimpleBlock); b.Truncated {
		t.Error("a leaf below the cap was marked Truncated")
	}
}

// TestWithClassifier checks that structure is the classifier's decision: an ID the
// standard registry calls a leaf can be descended into with a custom classifier,
// which is also how a blob read as one opaque leaf is re-parsed.
func TestWithClassifier(t *testing.T) {
	const customMaster parser.ElementID = 0x81
	innerNode := ebmltest.UTF8(matroska.IDTagString, "nested")
	inner := ebmltest.Encode(innerNode)
	data := ebmltest.Encode(ebmltest.Master(customMaster, innerNode))

	roots, err := tree.Parse(data)
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	if len(roots[0].Children) != 0 {
		t.Error("an unclassified ID was descended into by default")
	}
	blob := roots[0].Bytes()
	if len(blob) != len(inner) {
		t.Fatalf("opaque leaf payload = %d bytes, want %d", len(blob), len(inner))
	}

	// Re-parse the blob: the escape hatch for bytes read as one leaf.
	nested, err := tree.Parse(blob)
	if err != nil {
		t.Fatalf("Parse(blob) error = %v", err)
	}
	if len(nested) != 1 || nested[0].AsString() != "nested" {
		t.Fatalf("re-parsed blob = %v", nested)
	}

	// Or classify it as a master up front.
	classified, err := tree.Parse(data, tree.WithClassifier(func(id parser.ElementID) parser.Kind {
		if id == customMaster {
			return parser.KindMaster
		}
		return matroska.KindForElementID(id)
	}))
	if err != nil {
		t.Fatalf("Parse(WithClassifier) error = %v", err)
	}
	if got := classified[0].Find(matroska.IDTagString).AsString(); got != "nested" {
		t.Errorf("classified child value = %q, want nested", got)
	}
	if tree.WithClassifier(nil) == nil {
		t.Error("WithClassifier(nil) returned a nil Option")
	}
}

// TestParseErrorKeepsGoodPrefix checks that malformed input is reported as a
// ParseError that still unwraps to the parser's diagnosis, without discarding the
// elements already parsed.
func TestParseErrorKeepsGoodPrefix(t *testing.T) {
	f := fixture(t, "topology_basic")
	cut := f.Data[:len(f.Data)-5] // ends inside the last SimpleBlock payload

	roots, err := tree.Parse(cut)
	if err == nil {
		t.Fatal("Parse() of a truncated buffer returned no error")
	}
	var perr tree.ParseError
	if !errors.As(err, &perr) {
		t.Fatalf("error = %T, want tree.ParseError", err)
	}
	var truncated parser.TruncatedError
	if !errors.As(err, &truncated) {
		t.Errorf("error does not unwrap to parser.TruncatedError: %v", err)
	}
	// Parse only reads, so its failures are structural by construction.
	if !parser.IsStructural(err) {
		t.Errorf("error is not classified structural: %v", err)
	}
	var ce *parser.ContentError
	if errors.As(err, &ce) {
		t.Errorf("error claims a content origin Parse cannot have: %v", err)
	}
	if len(roots) == 0 || roots[0].ID != matroska.IDEBML {
		t.Fatal("the good prefix was discarded")
	}
	if !roots[0].Find(matroska.IDDocType).Exists() {
		t.Error("the prefix lost its children")
	}
	if perr.Error() == "" {
		t.Error("ParseError.Error() is empty")
	}

	// A non-master with unknown size is not recoverable structure.
	badSize := append(writer.EncodeID(matroska.IDTagString), 0xFF)
	if _, err := tree.Parse(badSize); err == nil {
		t.Error("Parse() accepted a non-master with unknown size")
	}
}

// ---- test helpers ----
//
// Hand-shaped inputs keep these tests independent of the fixture generator, and they
// are built through internal/ebmltest, whose ENCODING is package writer's, the
// library's only EBML encoder, so no test carries an encoder of its own.

// fakeRegistry answers for every ID, to prove Name/Describe/Type come from the
// registry and that a registry never gates retention or decoding.
type fakeRegistry struct{}

func (fakeRegistry) NameForID(parser.ElementID) string { return "OVERRIDDEN" }

func (fakeRegistry) Describe(parser.ElementID) string { return "OVERRIDDEN!" }

func (fakeRegistry) TypeFor(parser.ElementID) (matroska.ValueType, bool) {
	return matroska.TypeBinary, true
}
