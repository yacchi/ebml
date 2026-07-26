package scope_test

import (
	"bytes"
	"errors"
	"io"
	"testing"

	"github.com/yacchi/ebml/ext/scope"
	"github.com/yacchi/ebml/ext/stream"
	"github.com/yacchi/ebml/internal/ebmltest"
	"github.com/yacchi/ebml/matroska"
	"github.com/yacchi/ebml/parser"
)

func TestScopeCollectsDirectChildren(t *testing.T) {
	raw := ebmltest.Encode(ebmltest.Master(matroska.IDSegment,
		ebmltest.Master(matroska.IDInfo),
		ebmltest.Master(matroska.IDTracks),
		ebmltest.Master(matroska.IDTags),
		ebmltest.Master(matroska.IDTags),
	))
	s := scopeFor(raw, matroska.IDSegment)
	if got := len(s.GetAll(matroska.IDTags)); got != 2 {
		t.Fatalf("Tags count = %d, want 2", got)
	}
	if !s.Get(matroska.IDInfo).Exists() {
		t.Fatal("Info was not retained")
	}
	if s.Get(ebmltest.UnassignedLeafID).Exists() {
		t.Fatal("unobserved ID unexpectedly exists")
	}
}

func TestNestedElementIsNotADirectChild(t *testing.T) {
	raw := ebmltest.Encode(ebmltest.Master(matroska.IDSegment,
		ebmltest.Master(matroska.IDTags,
			ebmltest.Master(matroska.IDTag),
			ebmltest.Master(matroska.IDTag),
		),
	))
	s := scopeFor(raw, matroska.IDSegment)
	if got := s.GetAll(matroska.IDTag); got != nil {
		t.Fatalf("direct Tag lookup = %d, want none", len(got))
	}
	if got := len(s.GetAll(matroska.IDTags)[0].Descendants(matroska.IDTag)); got != 2 {
		t.Fatalf("descendant Tags = %d, want 2", got)
	}
}

func TestSkippedMasterDoesNotBreakTheStack(t *testing.T) {
	raw := ebmltest.Encode(ebmltest.Master(matroska.IDSegment,
		ebmltest.Master(matroska.IDInfo),
		ebmltest.Master(matroska.IDCluster, ebmltest.Uint(matroska.IDTimestamp, 1)),
		ebmltest.Master(matroska.IDTracks),
	))
	var later *parser.MasterNode
	s := run(raw, matroska.IDSegment, func(n parser.Node) {
		if m, ok := n.(*parser.MasterNode); ok {
			if m.ID() == matroska.IDCluster {
				m.Skip()
			} else {
				m.Descend()
			}
			if m.ID() == matroska.IDTracks {
				later = m
			}
		}
	})
	if later == nil || s.Get(matroska.IDTracks) == nil {
		t.Fatal("later sibling was not retained after skipped master")
	}
}

func TestUnobservedNodeIsNotInScope(t *testing.T) {
	raw := ebmltest.Encode(ebmltest.Master(matroska.IDSegment,
		ebmltest.Master(matroska.IDInfo),
	))
	s := stream.New(bytes.NewReader(raw), matroska.KindForElementID)
	tkr := scope.NewTracker(matroska.IDSegment, s)
	n, _ := s.Next()
	tkr.Observe(n)
	n.(*parser.MasterNode).Descend()
	n, _ = s.Next()
	n.(*parser.MasterNode).Skip()
	if got := tkr.Finish().Get(matroska.IDInfo); got.Exists() {
		t.Fatal("node was retained despite not being observed")
	}
}

func TestScopeClosesAtNextMasterOfTheSameID(t *testing.T) {
	raw := ebmltest.Concat(
		ebmltest.Encode(ebmltest.Master(matroska.IDSegment, ebmltest.Master(matroska.IDInfo))),
		ebmltest.Encode(ebmltest.Master(matroska.IDSegment, ebmltest.Master(matroska.IDTracks))),
	)
	s := stream.New(bytes.NewReader(raw), matroska.KindForElementID)
	tkr := scope.NewTracker(matroska.IDSegment, s)
	var done *scope.Scope
	var second *scope.Scope
	var secondIDs []parser.ElementID
	var closedCount int
	for {
		n, err := s.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		closed, err := tkr.Observe(n)
		if err != nil {
			t.Fatal(err)
		}
		if closed != nil {
			closedCount++
			if done == nil {
				done = closed
			}
		}
		if m, ok := n.(*parser.MasterNode); ok {
			m.Descend()
			if m.ID() == matroska.IDSegment && done != nil && second == nil {
				second = tkr.Current()
				secondIDs = second.IDs()
			}
		}
	}
	if closedCount != 2 || done == nil || !done.Get(matroska.IDInfo).Exists() {
		t.Fatal("first scope did not close with Info")
	}
	if second == nil || len(secondIDs) != 0 {
		t.Fatal("second scope did not start empty")
	}
}

func TestFinishClosesAnOpenScope(t *testing.T) {
	raw := ebmltest.Encode(ebmltest.UnknownMaster(matroska.IDSegment))
	s := stream.New(bytes.NewReader(raw), matroska.KindForElementID)
	tkr := scope.NewTracker(matroska.IDSegment, s)
	n, err := s.Next()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tkr.Observe(n); err != nil {
		t.Fatal(err)
	}
	n.(*parser.MasterNode).Descend()
	if got := tkr.Finish(); got == nil || got.End() != -1 {
		t.Fatalf("Finish result = %#v, want open-end sentinel", got)
	}
	if tkr.Finish() != nil {
		t.Fatal("second Finish returned a scope")
	}
}

func TestObserveIsAtomicOnError(t *testing.T) {
	raw := ebmltest.Encode(ebmltest.Master(matroska.IDSegment,
		ebmltest.Uint(matroska.IDTimestamp, 1),
		ebmltest.Uint(matroska.IDTimestampScale, 2),
	))
	src := &failingPayload{fail: matroska.IDTimestamp}
	s := stream.New(bytes.NewReader(raw), matroska.KindForElementID)
	tkr := scope.NewTracker(matroska.IDSegment, src)
	var done *scope.Scope
	for {
		n, err := s.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		if leaf, ok := n.(*parser.LeafNode); ok {
			src.stream = s
			src.leaf = leaf
		}
		closed, err := tkr.Observe(n)
		if closed != nil {
			done = closed
		}
		if err != nil && !errors.Is(err, parser.ErrStructural) {
			t.Fatal(err)
		}
		if m, ok := n.(*parser.MasterNode); ok {
			m.Descend()
		}
	}
	if current := tkr.Finish(); current != nil {
		done = current
	}
	got := done
	if got.Get(matroska.IDTimestamp).Exists() {
		t.Fatal("failed leaf was retained")
	}
	if !got.Get(matroska.IDTimestampScale).Exists() {
		t.Fatal("next leaf was not retained")
	}
}

func TestRetainedBytesAreCopied(t *testing.T) {
	buf := []byte{1, 2, 3}
	raw := ebmltest.Encode(ebmltest.Master(matroska.IDSegment,
		ebmltest.Leaf(matroska.IDCodecPrivate, buf),
	))
	src := &mutablePayload{bytes: append([]byte(nil), buf...)}
	s := stream.New(bytes.NewReader(raw), matroska.KindForElementID)
	tkr := scope.NewTracker(matroska.IDSegment, src)
	var done *scope.Scope
	for {
		n, err := s.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		src.stream = s
		closed, err := tkr.Observe(n)
		if closed != nil {
			done = closed
		}
		if err != nil {
			t.Fatal(err)
		}
		if m, ok := n.(*parser.MasterNode); ok {
			m.Descend()
		}
	}
	src.bytes[0] = 9
	if current := tkr.Finish(); current != nil {
		done = current
	}
	if got := done.Get(matroska.IDCodecPrivate).Bytes(); !bytes.Equal(got, buf) {
		t.Fatalf("retained bytes = %v, want %v", got, buf)
	}
}

func TestRevChangesOnEveryRecord(t *testing.T) {
	raw := ebmltest.Encode(ebmltest.Master(matroska.IDSegment,
		ebmltest.Master(matroska.IDInfo),
	))
	s := stream.New(bytes.NewReader(raw), matroska.KindForElementID)
	tkr := scope.NewTracker(matroska.IDSegment, s)
	var before uint64
	for {
		n, err := s.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		if _, err := tkr.Observe(n); err != nil {
			t.Fatal(err)
		}
		if cur := tkr.Current(); cur != nil {
			if cur.Rev() <= before {
				t.Fatalf("Rev = %d did not increase from %d", cur.Rev(), before)
			}
			before = cur.Rev()
		}
		if m, ok := n.(*parser.MasterNode); ok {
			m.Descend()
		}
	}
}

func TestNilScopeQueriesAreSafe(t *testing.T) {
	var s *scope.Scope
	if s.Get(1) != nil || s.GetAll(1) != nil || s.IDs() != nil || s.Rev() != 0 ||
		s.Master() != 0 || s.Start() != 0 || s.End() != -1 {
		t.Fatal("nil Scope query was not safe")
	}
}

func TestUnknownElementIsRetained(t *testing.T) {
	raw := ebmltest.Encode(ebmltest.Master(matroska.IDSegment,
		ebmltest.Leaf(ebmltest.UnassignedLeafID, []byte{7}),
	))
	s := scopeFor(raw, matroska.IDSegment)
	if !s.Get(ebmltest.UnassignedLeafID).Exists() {
		t.Fatal("unknown element was not retained")
	}
}

func scopeFor(raw []byte, id parser.ElementID) *scope.Scope {
	return run(raw, id, func(n parser.Node) {
		if m, ok := n.(*parser.MasterNode); ok {
			m.Descend()
		}
	})
}

func run(raw []byte, id parser.ElementID, decide func(parser.Node)) *scope.Scope {
	s := stream.New(bytes.NewReader(raw), matroska.KindForElementID)
	tkr := scope.NewTracker(id, s)
	var last *scope.Scope
	for {
		n, err := s.Next()
		if errors.Is(err, io.EOF) {
			if current := tkr.Finish(); current != nil {
				last = current
			}
			return last
		}
		if err != nil {
			panic(err)
		}
		if done, err := tkr.Observe(n); err != nil {
			panic(err)
		} else if done != nil {
			last = done
		}
		decide(n)
	}
}

type failingPayload struct {
	stream *stream.Stream
	leaf   *parser.LeafNode
	fail   parser.ElementID
}

func (f *failingPayload) Payload(leaf *parser.LeafNode) ([]byte, error) {
	if leaf.ID() == f.fail {
		return nil, parser.ErrStructural
	}
	return f.stream.Payload(leaf)
}

type mutablePayload struct {
	stream *stream.Stream
	bytes  []byte
}

func (m *mutablePayload) Payload(leaf *parser.LeafNode) ([]byte, error) {
	if leaf.ID() == matroska.IDCodecPrivate {
		return m.bytes, nil
	}
	return m.stream.Payload(leaf)
}

// TestGetReturnsTheMostRecentChild pins the last-wins choice. RFC 9559 gives no
// precedence for an element a schema allows to repeat, and their order carries no
// meaning, so the library picks the later statement -- the same choice ext/tags
// makes for a repeated TagName, and for the same reason.
func TestGetReturnsTheMostRecentChild(t *testing.T) {
	raw := ebmltest.Encode(ebmltest.Master(matroska.IDSegment,
		ebmltest.Master(matroska.IDTags, ebmltest.UTF8(matroska.IDTagString, "first")),
		ebmltest.Master(matroska.IDTags, ebmltest.UTF8(matroska.IDTagString, "second")),
	))
	s := scopeFor(raw, matroska.IDSegment)

	all := s.GetAll(matroska.IDTags)
	if len(all) != 2 {
		t.Fatalf("Tags count = %d, want 2", len(all))
	}
	if got := all[0].Find(matroska.IDTagString).AsString(); got != "first" {
		t.Errorf("GetAll[0] = %q, want %q: GetAll must stay in stream order", got, "first")
	}
	if got := s.Get(matroska.IDTags).Find(matroska.IDTagString).AsString(); got != "second" {
		t.Errorf("Get = %q, want %q: Get is the most recent, not the first", got, "second")
	}
}

// TestGetAllReturnsACallerOwnedSlice pins that a scope never hands out the state
// it is still accumulating into: a caller that rewrites the returned slice must
// not be able to change what the scope reports next.
func TestGetAllReturnsACallerOwnedSlice(t *testing.T) {
	raw := ebmltest.Encode(ebmltest.Master(matroska.IDSegment,
		ebmltest.Master(matroska.IDTags, ebmltest.UTF8(matroska.IDTagString, "first")),
		ebmltest.Master(matroska.IDTags, ebmltest.UTF8(matroska.IDTagString, "second")),
	))
	s := scopeFor(raw, matroska.IDSegment)

	got := s.GetAll(matroska.IDTags)
	if len(got) != 2 {
		t.Fatalf("Tags count = %d, want 2", len(got))
	}
	got[0] = nil

	again := s.GetAll(matroska.IDTags)
	if len(again) != 2 || again[0] == nil {
		t.Fatal("mutating a returned slice changed what the scope reports")
	}
	if v := again[0].Find(matroska.IDTagString).AsString(); v != "first" {
		t.Fatalf("first Tags = %q, want %q", v, "first")
	}
}
