package tags_test

import (
	"bytes"
	"reflect"
	"testing"

	"github.com/yacchi/ebml/ext/scope"
	"github.com/yacchi/ebml/ext/tags"
	"github.com/yacchi/ebml/internal/ebmltest"
	"github.com/yacchi/ebml/matroska"
	"github.com/yacchi/ebml/parser"
	"github.com/yacchi/ebml/stream"
	"github.com/yacchi/ebml/tree"
)

// A doc comment naming *scope.Scope as what satisfies tags.Source would be a
// dependency the compiler cannot check, and this package deliberately names no
// producer. The relationship is asserted here instead, in the one place an ext
// package may import a sibling: if Scope.GetAll ever changes shape, this stops
// compiling rather than leaving a sentence somewhere that has quietly become
// false.
var _ tags.Source = (*scope.Scope)(nil)

func simpleTag(name, value string, nested ...ebmltest.Node) ebmltest.Node {
	children := []ebmltest.Node{
		ebmltest.UTF8(matroska.IDTagName, name),
		ebmltest.UTF8(matroska.IDTagString, value),
	}
	children = append(children, nested...)
	return ebmltest.Master(matroska.IDSimpleTag, children...)
}

// valuelessTag is a SimpleTag that declares a TagName and no TagString at all,
// which is what a tag carrying a TagBinary value, or an incomplete one, looks
// like. It is not the same shape as simpleTag(name, "").
func valuelessTag(name string, nested ...ebmltest.Node) ebmltest.Node {
	children := []ebmltest.Node{ebmltest.UTF8(matroska.IDTagName, name)}
	children = append(children, nested...)
	return ebmltest.Master(matroska.IDSimpleTag, children...)
}

func tag(target *tags.Target, entries ...ebmltest.Node) ebmltest.Node {
	children := make([]ebmltest.Node, 0, len(entries)+1)
	if target != nil {
		var targetChildren []ebmltest.Node
		if target.TypeValue != 0 {
			targetChildren = append(targetChildren, ebmltest.Uint(matroska.IDTargetTypeValue, target.TypeValue))
		}
		if target.TrackUID != 0 {
			targetChildren = append(targetChildren, ebmltest.Uint(matroska.IDTagTrackUID, target.TrackUID))
		}
		if target.EditionUID != 0 {
			targetChildren = append(targetChildren, ebmltest.Uint(matroska.IDTagEditionUID, target.EditionUID))
		}
		if target.ChapterUID != 0 {
			targetChildren = append(targetChildren, ebmltest.Uint(matroska.IDTagChapterUID, target.ChapterUID))
		}
		if target.AttachmentUID != 0 {
			targetChildren = append(targetChildren, ebmltest.Uint(matroska.IDTagAttachmentUID, target.AttachmentUID))
		}
		children = append(children, ebmltest.Master(matroska.IDTargets, targetChildren...))
	}
	children = append(children, entries...)
	return ebmltest.Master(matroska.IDTag, children...)
}

func tagsElement(entries ...ebmltest.Node) ebmltest.Node {
	return ebmltest.Master(matroska.IDTags, entries...)
}

// readTags reads through the scope path, which is ReadFrom: the caller hands
// over the Scope and never names an element ID. Most tests here are about what
// the view computes, not where the roots came from, so they go through this one
// helper; TestRootShapesAgree is what pins the shapes to the same answer.
func readTags(t *testing.T, raw []byte) *tags.Set {
	t.Helper()
	return tags.ReadFrom(readScope(t, raw))
}

// segmentRoot reads through the tree path: one retained Segment is the whole
// argument, which is what a consumer holding an ext/fragment Fragment has.
func segmentRoot(t *testing.T, raw []byte) *tree.Element {
	t.Helper()
	roots, err := tree.Parse(raw, tree.WithClassifier(matroska.KindForElementID))
	if err != nil {
		t.Fatal(err)
	}
	if len(roots) != 1 || roots[0].ID != matroska.IDSegment {
		t.Fatalf("parsed %d roots; want one Segment", len(roots))
	}
	return roots[0]
}

func readScope(t *testing.T, raw []byte) *scope.Scope {
	t.Helper()
	src := stream.New(bytes.NewReader(raw), matroska.KindForElementID,
		parser.WithBoundary(matroska.StreamBoundary))
	tracker := scope.NewTracker(matroska.IDSegment, src)
	var closed *scope.Scope
	for node, err := range src.Nodes() {
		if err != nil {
			t.Fatal(err)
		}
		done, err := tracker.Observe(node)
		if err != nil {
			t.Fatal(err)
		}
		if done != nil {
			closed = done
			break
		}
		if master, ok := node.(*parser.MasterNode); ok {
			master.Descend()
		}
	}
	if closed != nil {
		return closed
	}
	if done := tracker.Finish(); done != nil {
		return done
	}
	t.Fatal("stream ended without a Segment scope")
	return nil
}

func segment(children ...ebmltest.Node) []byte {
	return ebmltest.Encode(ebmltest.Master(matroska.IDSegment, children...))
}

func TestGetIsLastWins(t *testing.T) {
	raw := segment(tagsElement(
		tag(nil, simpleTag("state", "first")),
		tag(nil, simpleTag("state", "second")),
	))
	got, ok := readTags(t, raw).Get(tags.Target{}, "state")
	if !ok || got != "second" {
		t.Fatalf("Get(state) = %q, %v; want second (last-wins is the library choice)", got, ok)
	}
	if got == "first" {
		t.Fatal("Get(state) used first-wins; repeated-name precedence is intentionally last-wins")
	}
}

func TestValuesKeepsStreamOrder(t *testing.T) {
	raw := segment(tagsElement(
		tag(nil, simpleTag("state", "first")),
		tag(nil, simpleTag("state", "second")),
	))
	got := readTags(t, raw).Values(tags.Target{}, "state")
	if want := []string{"first", "second"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("Values(state) = %v, want %v", got, want)
	}
}

func TestTargetsSeparateNamespaces(t *testing.T) {
	raw := segment(tagsElement(
		tag(&tags.Target{TrackUID: 1}, simpleTag("state", "one")),
		tag(&tags.Target{TrackUID: 2}, simpleTag("state", "two")),
	))
	set := readTags(t, raw)
	for target, want := range map[tags.Target]string{
		{TrackUID: 1}: "one",
		{TrackUID: 2}: "two",
	} {
		if got, ok := set.Get(target, "state"); !ok || got != want {
			t.Fatalf("Get(%+v) = %q, %v; want %q", target, got, ok, want)
		}
	}
	if _, ok := set.Get(tags.Target{}, "state"); ok {
		t.Fatal("track-targeted tag leaked into the Segment namespace")
	}
}

func TestAbsentTargetsIsTheSegment(t *testing.T) {
	set := readTags(t, segment(tagsElement(tag(nil, simpleTag("state", "segment")))))
	if got, ok := set.Get(tags.Target{}, "state"); !ok || got != "segment" {
		t.Fatalf("Get(state) = %q, %v; want the Segment-scoped value", got, ok)
	}
}

func TestTagsBeforeAndAfterACluster(t *testing.T) {
	raw := segment(
		tagsElement(tag(nil, simpleTag("before", "yes"))),
		ebmltest.UnknownMaster(matroska.IDCluster),
		tagsElement(tag(nil, simpleTag("after", "yes"))),
	)
	set := readTags(t, raw)
	if got, ok := set.Get(tags.Target{}, "after"); !ok || got != "yes" {
		t.Fatalf("trailing tag = %q, %v; want readable after an unknown-size Cluster", got, ok)
	}
}

func TestNestedSimpleTagCounts(t *testing.T) {
	raw := segment(tagsElement(tag(nil,
		simpleTag("outer", "one", simpleTag("inner", "two")),
	)))
	set := readTags(t, raw)
	if got, ok := set.Get(tags.Target{}, "inner"); !ok || got != "two" {
		t.Fatalf("nested tag = %q, %v; want two", got, ok)
	}
}

func TestEmptyTagNameIsSkipped(t *testing.T) {
	set := readTags(t, segment(tagsElement(tag(nil, simpleTag("", "ignored")))))
	if _, ok := set.Get(tags.Target{}, ""); ok {
		t.Fatal("empty TagName was not skipped")
	}
}

func TestAbsentTagStringIsNotAValue(t *testing.T) {
	set := readTags(t, segment(tagsElement(tag(nil, valuelessTag("state")))))
	if got, ok := set.Get(tags.Target{}, "state"); ok {
		t.Fatalf("Get(state) = %q, %v; want no value: an absent TagString is not an empty one", got, ok)
	}
	if got := set.Values(tags.Target{}, "state"); got != nil {
		t.Fatalf("Values(state) = %v, want nil", got)
	}
	if _, present := set.All(tags.Target{})["state"]; present {
		t.Fatal("All included a name whose SimpleTag declared no TagString")
	}
}

func TestEmptyTagStringIsAValue(t *testing.T) {
	set := readTags(t, segment(tagsElement(tag(nil, simpleTag("state", "")))))
	if got, ok := set.Get(tags.Target{}, "state"); !ok || got != "" {
		t.Fatalf("Get(state) = %q, %v; want the declared empty value", got, ok)
	}
}

func TestAbsentTagStringDoesNotOverwriteAValue(t *testing.T) {
	raw := segment(tagsElement(
		tag(nil, simpleTag("state", "real")),
		tag(nil, valuelessTag("state")),
	))
	set := readTags(t, raw)
	if got, ok := set.Get(tags.Target{}, "state"); !ok || got != "real" {
		t.Fatalf("Get(state) = %q, %v; want real: a valueless SimpleTag must not erase a value under last-wins", got, ok)
	}
}

func TestValuelessSimpleTagStillCarriesItsChildren(t *testing.T) {
	raw := segment(tagsElement(tag(nil,
		valuelessTag("outer", simpleTag("inner", "two")),
	)))
	set := readTags(t, raw)
	if got, ok := set.Get(tags.Target{}, "inner"); !ok || got != "two" {
		t.Fatalf("nested tag = %q, %v; want two: skipping a value must not skip the subtree", got, ok)
	}
}

// TestRootShapesAgree pins the three ways in to one answer. A caller may hold
// the Tags elements (ext/scope), the whole Segment (ext/fragment), or the Scope
// itself, and all three must compute the same view down to the order Values
// reports -- otherwise the entry points are one name with several meanings,
// which is the thing this package was reshaped to stop being.
func TestRootShapesAgree(t *testing.T) {
	raw := segment(
		tagsElement(tag(nil, simpleTag("state", "first"))),
		ebmltest.UnknownMaster(matroska.IDCluster),
		tagsElement(
			tag(nil, simpleTag("state", "second")),
			tag(&tags.Target{TrackUID: 7}, simpleTag("state", "track")),
		),
	)
	shapes := map[string]*tags.Set{
		"ReadFrom(scope)":  tags.ReadFrom(readScope(t, raw)),
		"Read(segment)":    tags.Read(segmentRoot(t, raw)),
		"Read(tags roots)": tags.Read(readScope(t, raw).GetAll(matroska.IDTags)...),
	}
	want := shapes["ReadFrom(scope)"]
	for name, got := range shapes {
		if !reflect.DeepEqual(got.Targets(), want.Targets()) {
			t.Fatalf("%s Targets = %v, want %v", name, got.Targets(), want.Targets())
		}
		for _, target := range want.Targets() {
			if !reflect.DeepEqual(got.Values(target, "state"), want.Values(target, "state")) {
				t.Fatalf("%s Values(%+v) = %v, want %v",
					name, target, got.Values(target, "state"), want.Values(target, "state"))
			}
		}
		if v, ok := got.Get(tags.Target{}, "state"); !ok || v != "second" {
			t.Fatalf("%s Get(state) = %q, %v; want second", name, v, ok)
		}
		if v, ok := got.Get(tags.Target{TrackUID: 7}, "state"); !ok || v != "track" {
			t.Fatalf("%s Get(track state) = %q, %v; want track", name, v, ok)
		}
	}
}

// TestReadFromRemovesTheSilentMistakes is why ReadFrom exists. The call it
// replaces made the caller name the ID, and both ways of getting that wrong
// fail silently: the wrong ID yields nothing, and Get instead of GetAll keeps
// only the last Tags element -- which on a stream that writes tags before and
// after its Cluster is exactly the shape ext/fragment waits a whole fragment to
// deliver. Neither is reachable through ReadFrom, and this test states what each
// mistake actually costs so the doc is not the only record of it.
func TestReadFromRemovesTheSilentMistakes(t *testing.T) {
	raw := segment(
		tagsElement(tag(nil, simpleTag("before", "yes"))),
		ebmltest.UnknownMaster(matroska.IDCluster),
		tagsElement(tag(nil, simpleTag("after", "yes"))),
	)
	sc := readScope(t, raw)

	correct := tags.ReadFrom(sc)
	for _, name := range []string{"before", "after"} {
		if _, ok := correct.Get(tags.Target{}, name); !ok {
			t.Fatalf("ReadFrom lost %q", name)
		}
	}

	// IDTag instead of IDTags: a root is searched, not counted, so a Tag root
	// contributes nothing and the view is empty rather than merely wrong.
	if got := tags.Read(sc.GetAll(matroska.IDTag)...).Targets(); got != nil {
		t.Fatalf("Tag roots produced %v; want an empty view", got)
	}

	// Get instead of GetAll: the last Tags element only, so everything the
	// stream wrote before its Cluster disappears without a word.
	lastOnly := tags.Read(sc.Get(matroska.IDTags))
	if _, ok := lastOnly.Get(tags.Target{}, "after"); !ok {
		t.Fatal("Get(IDTags) lost the last Tags element too; the fixture is wrong")
	}
	if _, ok := lastOnly.Get(tags.Target{}, "before"); ok {
		t.Fatal("Get(IDTags) kept a pre-Cluster tag; this test no longer shows what ReadFrom prevents")
	}
}

// TestOverlappingRootsDoubleCount pins the one thing a caller must not do, so
// that Read's warning about it is a statement about behavior and not a hedge.
func TestOverlappingRootsDoubleCount(t *testing.T) {
	raw := segment(tagsElement(tag(nil, simpleTag("state", "only"))))
	root := segmentRoot(t, raw)
	set := tags.Read(root, root.Find(matroska.IDTags))
	if got := set.Values(tags.Target{}, "state"); len(got) != 2 {
		t.Fatalf("Values(state) = %v; want the value twice: overlapping roots double-count", got)
	}
	if got, ok := set.Get(tags.Target{}, "state"); !ok || got != "only" {
		t.Fatalf("Get(state) = %q, %v; want only: last-wins hides the duplicate", got, ok)
	}
}

func TestNilSafety(t *testing.T) {
	var nilSet *tags.Set
	for _, set := range []*tags.Set{tags.Read(), tags.Read(nil), nilSet} {
		if _, ok := set.Get(tags.Target{}, "name"); ok {
			t.Fatal("nil set returned a tag")
		}
		if got := set.Values(tags.Target{}, "name"); got != nil {
			t.Fatalf("Values on nil set = %v, want nil", got)
		}
		if got := set.All(tags.Target{}); got == nil || len(got) != 0 {
			t.Fatalf("All on nil set = %v, want non-nil empty map", got)
		}
		if got := set.Targets(); got != nil {
			t.Fatalf("Targets on nil set = %v, want nil", got)
		}
	}
}
