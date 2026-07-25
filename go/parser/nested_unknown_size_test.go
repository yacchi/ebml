package parser_test

import (
	"errors"
	"io"
	"reflect"
	"strings"
	"testing"

	"github.com/yacchi/ebml/internal/ebmltest"
	"github.com/yacchi/ebml/internal/ebmltrace"
	. "github.com/yacchi/ebml/parser"
)

const (
	nestedOuter ElementID = 0x18538067
	nestedInner ElementID = 0x1549A966
	nestedKnown ElementID = 0x1654AE6B
	nestedLeaf  ElementID = 0x4487
	nestedNext  ElementID = 0x4286
)

func nestedClassifier(id ElementID) Kind {
	switch id {
	case nestedOuter, nestedInner, nestedKnown:
		return KindMaster
	default:
		return KindBinary
	}
}

func isNestedNeedMore(err error) bool {
	var need NeedMoreData
	return errors.As(err, &need)
}

func wantNestedStalePanic(t *testing.T, f func()) {
	t.Helper()
	defer func() {
		r := recover()
		msg, ok := r.(string)
		if !ok || !strings.Contains(msg, "called on a stale node: a Cursor node is valid only until the next Next call") {
			t.Fatalf("panic = %v, want the stale-node panic", r)
		}
	}()
	f()
}

func nestedUnknownInput() []byte {
	return ebmltest.Encode(
		ebmltest.UnknownMaster(nestedOuter,
			ebmltest.UnknownMaster(nestedInner,
				ebmltest.Leaf(nestedLeaf, []byte{0x01}),
			),
		),
		ebmltest.Leaf(nestedNext, []byte{0x02}),
	)
}

func nextNested(t *testing.T, c *Cursor) Node {
	t.Helper()
	n, err := c.Next()
	if err != nil {
		t.Fatalf("Next: %v", err)
	}
	return n
}

func finalizeNested(t *testing.T, c *Cursor) {
	t.Helper()
	for {
		_, err := c.Next()
		if err == nil {
			continue
		}
		if !isNestedNeedMore(err) {
			t.Fatalf("Next before Finalize: %v", err)
		}
		break
	}
	if err := c.Finalize(); err != nil {
		t.Fatalf("Finalize: %v", err)
	}
}

func TestNestedUnknownSizeClosesOutwardOneLevelPerEvent(t *testing.T) {
	raw := nestedUnknownInput()
	c := NewCursor(nestedClassifier, WithBoundary(func(open, next ElementID) bool {
		return next == nestedNext && (open == nestedOuter || open == nestedInner)
	}))
	c.Feed(raw)

	if n := nextNested(t, c); n.ID() != nestedOuter || n.Depth() != 0 {
		t.Fatalf("outer master = %s at depth %d", n.ID(), n.Depth())
	}
	if n := nextNested(t, c); n.ID() != nestedInner || n.Depth() != 1 {
		t.Fatalf("inner master = %s at depth %d", n.ID(), n.Depth())
	}
	if n := nextNested(t, c); n.ID() != nestedLeaf || n.Depth() != 2 {
		t.Fatalf("inner leaf = %s at depth %d", n.ID(), n.Depth())
	}
	bEnd, ok := nextNested(t, c).(*EndNode)
	if !ok || bEnd.ID() != nestedInner || bEnd.Depth() != 1 {
		t.Fatalf("inner end = %#v, want %s at depth 1", bEnd, nestedInner)
	}
	bDepth := bEnd.Depth()
	aEnd, ok := nextNested(t, c).(*EndNode)
	if !ok || aEnd.ID() != nestedOuter || aEnd.Depth() != 0 || aEnd.Depth() >= bDepth {
		t.Fatalf("outer end = %#v, want %s at depth 0 below inner depth", aEnd, nestedOuter)
	}
	x, ok := nextNested(t, c).(*LeafNode)
	if !ok || x.ID() != nestedNext || x.Depth() != 0 {
		t.Fatalf("following element = %#v, want %s at depth 0", x, nestedNext)
	}
	if n, err := c.Next(); !isNestedNeedMore(err) || n != nil {
		t.Fatalf("Next after X = (%v, %v), want NeedMoreData", n, err)
	}
}

func TestNestedUnknownSizeInnerClosesOuterStaysOpen(t *testing.T) {
	c := NewCursor(nestedClassifier, WithBoundary(func(open, next ElementID) bool {
		return open == nestedInner && next == nestedNext
	}))
	c.Feed(nestedUnknownInput())
	nextNested(t, c)
	nextNested(t, c)
	nextNested(t, c)
	if n := nextNested(t, c); n.ID() != nestedInner {
		t.Fatalf("first end ID = %s, want %s", n.ID(), nestedInner)
	}
	x, ok := nextNested(t, c).(*LeafNode)
	if !ok || x.ID() != nestedNext || x.Depth() != 1 {
		t.Fatalf("following element = %#v, want %s as child at depth 1", x, nestedNext)
	}
	finalizeNested(t, c)
	aEnd, ok := nextNested(t, c).(*EndNode)
	if !ok || aEnd.ID() != nestedOuter || aEnd.Depth() != 0 {
		t.Fatalf("final outer end = %#v, want %s at depth 0", aEnd, nestedOuter)
	}
	if _, err := c.Next(); !errors.Is(err, io.EOF) {
		t.Fatalf("Next after final outer end = %v, want io.EOF", err)
	}
}

func TestBoundaryRuleSeesOnlyTheInnermostOpenMaster(t *testing.T) {
	type pair struct{ open, next ElementID }
	var got []pair
	c := NewCursor(nestedClassifier, WithBoundary(func(open, next ElementID) bool {
		got = append(got, pair{open, next})
		return next == nestedNext
	}))
	c.Feed(nestedUnknownInput())
	for i := 0; i < 5; i++ {
		nextNested(t, c)
	}
	want := []pair{
		{nestedOuter, nestedInner},
		{nestedInner, nestedLeaf},
		{nestedInner, nestedNext},
		{nestedOuter, nestedNext},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("boundary calls = %v, want %v", got, want)
	}
}

func TestThreeLevelUnknownSizeAllCloseAtFinalize(t *testing.T) {
	raw := ebmltest.Encode(
		ebmltest.UnknownMaster(nestedOuter,
			ebmltest.UnknownMaster(nestedInner,
				ebmltest.UnknownMaster(nestedKnown,
					ebmltest.Leaf(nestedLeaf, []byte{0x01}),
				),
			),
		),
	)
	c := NewCursor(nestedClassifier)
	c.Feed(raw)
	for i := 0; i < 4; i++ {
		nextNested(t, c)
	}
	finalizeNested(t, c)
	want := []struct {
		id    ElementID
		depth int
	}{{nestedKnown, 2}, {nestedInner, 1}, {nestedOuter, 0}}
	for i, w := range want {
		n, ok := nextNested(t, c).(*EndNode)
		if !ok || n.ID() != w.id || n.Depth() != w.depth {
			t.Fatalf("end %d = %#v, want %s at depth %d", i, n, w.id, w.depth)
		}
	}
	if _, err := c.Next(); !errors.Is(err, io.EOF) {
		t.Fatalf("Next after finalize cascade = %v, want io.EOF", err)
	}
}

func TestUnknownSizeMasterInsideKnownSizeMasterClosesAtParentEnd(t *testing.T) {
	raw := ebmltest.Encode(
		ebmltest.Master(nestedOuter,
			ebmltest.UnknownMaster(nestedInner,
				ebmltest.Leaf(nestedLeaf, []byte{0x01}),
			),
		),
	)
	c := NewCursor(nestedClassifier)
	c.Feed(raw)
	nextNested(t, c)
	nextNested(t, c)
	nextNested(t, c)
	finalizeNested(t, c)
	q, ok := nextNested(t, c).(*EndNode)
	if !ok || q.ID() != nestedInner || q.End() != int64(len(raw)) {
		t.Fatalf("unknown child end = %#v, want %s ending at %d", q, nestedInner, len(raw))
	}
	p, ok := nextNested(t, c).(*EndNode)
	if !ok || p.ID() != nestedOuter || p.End() != int64(len(raw)) {
		t.Fatalf("known parent end = %#v, want %s ending at declared end %d", p, nestedOuter, len(raw))
	}
	if _, err := c.Next(); !errors.Is(err, io.EOF) {
		t.Fatalf("Next after known-parent close = %v, want io.EOF", err)
	}
}

func TestKnownSizeMasterInsideUnknownSizeMaster(t *testing.T) {
	type pair struct{ open, next ElementID }
	var calls []pair
	c := NewCursor(nestedClassifier, WithBoundary(func(open, next ElementID) bool {
		calls = append(calls, pair{open, next})
		return open == nestedOuter && next == nestedNext
	}))
	raw := ebmltest.Encode(
		ebmltest.UnknownMaster(nestedOuter,
			ebmltest.Master(nestedKnown, ebmltest.Leaf(nestedLeaf, []byte{0x01})),
			ebmltest.Leaf(nestedNext, []byte{0x02}),
		),
	)
	c.Feed(raw)
	nextNested(t, c)
	if n := nextNested(t, c); n.ID() != nestedKnown {
		t.Fatalf("known child = %s, want %s", n.ID(), nestedKnown)
	}
	if n := nextNested(t, c); n.ID() != nestedLeaf {
		t.Fatalf("known child leaf = %s, want %s", n.ID(), nestedLeaf)
	}
	if n := nextNested(t, c); n.ID() != nestedKnown {
		t.Fatalf("known child end = %s, want %s", n.ID(), nestedKnown)
	}
	for _, call := range calls {
		if call.open == nestedKnown {
			t.Fatalf("boundary called with known child open: %v", calls)
		}
	}
	if n := nextNested(t, c); n.ID() != nestedOuter {
		t.Fatalf("unknown parent end = %s, want %s", n.ID(), nestedOuter)
	}
	if want := (pair{nestedOuter, nestedNext}); len(calls) == 0 || calls[len(calls)-1] != want {
		t.Fatalf("boundary calls after known child = %v, want final call %v", calls, want)
	}
}

type nestedEvent struct {
	id          ElementID
	kind        Kind
	depth       int
	offset, end int64
}

func runNestedSplit(t *testing.T, chunks [][]byte) []nestedEvent {
	t.Helper()
	c := NewCursor(nestedClassifier, WithBoundary(func(open, next ElementID) bool {
		return next == nestedNext && (open == nestedOuter || open == nestedInner)
	}))
	var events []nestedEvent
	for _, chunk := range chunks {
		c.Feed(chunk)
		for {
			n, err := c.Next()
			if isNestedNeedMore(err) {
				break
			}
			if err != nil {
				t.Fatalf("Next: %v", err)
			}
			events = append(events, nestedEvent{n.ID(), n.Kind(), n.Depth(), n.Offset(), n.End()})
		}
	}
	for {
		n, err := c.Next()
		if err == nil {
			events = append(events, nestedEvent{n.ID(), n.Kind(), n.Depth(), n.Offset(), n.End()})
			continue
		}
		if !isNestedNeedMore(err) {
			t.Fatalf("Next before Finalize: %v", err)
		}
		break
	}
	if err := c.Finalize(); err != nil {
		t.Fatalf("Finalize: %v", err)
	}
	for {
		n, err := c.Next()
		if errors.Is(err, io.EOF) {
			return events
		}
		if err != nil {
			t.Fatalf("Next after Finalize: %v", err)
		}
		events = append(events, nestedEvent{n.ID(), n.Kind(), n.Depth(), n.Offset(), n.End()})
	}
}

func TestNestedUnknownSizeIsSplitInvariant(t *testing.T) {
	raw := nestedUnknownInput()
	want := runNestedSplit(t, ebmltrace.Whole(raw))
	splits := []struct {
		name   string
		chunks [][]byte
	}{
		{"one_byte", ebmltrace.SplitOneByte(raw)},
		{"fibonacci", ebmltrace.SplitFibonacci(raw)},
		{"random", ebmltrace.SplitRandom(raw, 12345, 7)},
	}
	for _, split := range splits {
		t.Run(split.name, func(t *testing.T) {
			if got := runNestedSplit(t, split.chunks); !reflect.DeepEqual(got, want) {
				t.Fatalf("events differ from whole-input run:\n got: %v\nwant: %v", got, want)
			}
		})
	}
}

func TestStaleEndNodePanicsAcrossTheCascade(t *testing.T) {
	c := NewCursor(nestedClassifier, WithBoundary(func(open, next ElementID) bool {
		return next == nestedNext && (open == nestedOuter || open == nestedInner)
	}))
	c.Feed(nestedUnknownInput())
	nextNested(t, c)
	nextNested(t, c)
	nextNested(t, c)
	bEnd, ok := nextNested(t, c).(*EndNode)
	if !ok {
		t.Fatalf("inner close = %T, want *EndNode", bEnd)
	}
	if _, ok := nextNested(t, c).(*EndNode); !ok {
		t.Fatal("outer close was not an EndNode")
	}
	wantNestedStalePanic(t, func() {
		bEnd.End()
	})
}
