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
)

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
	got, ok := tags.Read(readScope(t, raw)).Get(tags.Target{}, "state")
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
	got := tags.Read(readScope(t, raw)).Values(tags.Target{}, "state")
	if want := []string{"first", "second"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("Values(state) = %v, want %v", got, want)
	}
}

func TestTargetsSeparateNamespaces(t *testing.T) {
	raw := segment(tagsElement(
		tag(&tags.Target{TrackUID: 1}, simpleTag("state", "one")),
		tag(&tags.Target{TrackUID: 2}, simpleTag("state", "two")),
	))
	set := tags.Read(readScope(t, raw))
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
	set := tags.Read(readScope(t, segment(tagsElement(tag(nil, simpleTag("state", "segment"))))))
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
	set := tags.Read(readScope(t, raw))
	if got, ok := set.Get(tags.Target{}, "after"); !ok || got != "yes" {
		t.Fatalf("trailing tag = %q, %v; want readable after an unknown-size Cluster", got, ok)
	}
}

func TestNestedSimpleTagCounts(t *testing.T) {
	raw := segment(tagsElement(tag(nil,
		simpleTag("outer", "one", simpleTag("inner", "two")),
	)))
	set := tags.Read(readScope(t, raw))
	if got, ok := set.Get(tags.Target{}, "inner"); !ok || got != "two" {
		t.Fatalf("nested tag = %q, %v; want two", got, ok)
	}
}

func TestEmptyTagNameIsSkipped(t *testing.T) {
	set := tags.Read(readScope(t, segment(tagsElement(tag(nil, simpleTag("", "ignored"))))))
	if _, ok := set.Get(tags.Target{}, ""); ok {
		t.Fatal("empty TagName was not skipped")
	}
}

func TestAbsentTagStringIsNotAValue(t *testing.T) {
	set := tags.Read(readScope(t, segment(tagsElement(tag(nil, valuelessTag("state"))))))
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
	set := tags.Read(readScope(t, segment(tagsElement(tag(nil, simpleTag("state", ""))))))
	if got, ok := set.Get(tags.Target{}, "state"); !ok || got != "" {
		t.Fatalf("Get(state) = %q, %v; want the declared empty value", got, ok)
	}
}

func TestAbsentTagStringDoesNotOverwriteAValue(t *testing.T) {
	raw := segment(tagsElement(
		tag(nil, simpleTag("state", "real")),
		tag(nil, valuelessTag("state")),
	))
	set := tags.Read(readScope(t, raw))
	if got, ok := set.Get(tags.Target{}, "state"); !ok || got != "real" {
		t.Fatalf("Get(state) = %q, %v; want real: a valueless SimpleTag must not erase a value under last-wins", got, ok)
	}
}

func TestValuelessSimpleTagStillCarriesItsChildren(t *testing.T) {
	raw := segment(tagsElement(tag(nil,
		valuelessTag("outer", simpleTag("inner", "two")),
	)))
	set := tags.Read(readScope(t, raw))
	if got, ok := set.Get(tags.Target{}, "inner"); !ok || got != "two" {
		t.Fatalf("nested tag = %q, %v; want two: skipping a value must not skip the subtree", got, ok)
	}
}

func TestNilSafety(t *testing.T) {
	var nilSet *tags.Set
	for _, set := range []*tags.Set{tags.Read(nil), nilSet} {
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
