package parser

import (
	"errors"
	"fmt"
	"io"
	"reflect"
	"sort"
	"strings"
	"testing"
)

// The node lifetime rule -- a node is valid only until the next Next -- is enforced
// by a generation the cursor stamps into every node it hands out. These tests hold
// the whole rule to that standard: EVERY exported method of EVERY variant rejects a
// node the cursor has moved past, the extent accessors included, so a stale node can
// never silently report the values of a LATER event of the same variant. The method
// list comes from the types themselves, so a method added later is covered without
// anyone remembering to extend the table.
//
// The rule has NO exception, and that is what the cursor pays a per-event allocation
// for: every retention is caught, the pointer the cursor handed out exactly as much
// as a copy of it, whether the live event is of the same variant or another one. The
// price is measured here too, so the trade is on the record rather than asserted.

// allocsPerEvent is the measured cost of that guarantee: Next allocates exactly one
// object per event, the node it hands out, and nothing else -- reading every extent
// accessor adds none and delivering a payload adds none. Every allocation assertion
// below is written against this one number, so a change in the cursor's allocation
// behaviour shows up as one obvious diff; BenchmarkCursorScan and BenchmarkParserScan
// price it against driving Parser directly, which hands out no node at all.
const allocsPerEvent = 1

// nodeVariants are the three node variants, with the values used to reach one of
// each from a cursor.
var nodeVariants = []struct {
	name string
	// next advances a fully fed cursor to the next event of this variant.
	next func(t *testing.T, c *Cursor) Node
}{
	{"MasterNode", func(t *testing.T, c *Cursor) Node { return advanceTo[*MasterNode](t, c) }},
	{"LeafNode", func(t *testing.T, c *Cursor) Node { return advanceTo[*LeafNode](t, c) }},
	{"EndNode", func(t *testing.T, c *Cursor) Node { return advanceTo[*EndNode](t, c) }},
}

// wantNodeMethods is the exported method set of each variant. It exists so that a
// method added to a node type fails HERE, in one obvious place, instead of quietly
// entering the API without a staleness test: the tests below drive whatever the type
// reports, and this guard states what that is expected to be.
var wantNodeMethods = map[string][]string{
	"MasterNode": {"Depth", "Descend", "End", "HeaderLen", "ID", "Kind", "Offset", "Size", "Skip"},
	"LeafNode":   {"Depth", "End", "HeaderLen", "ID", "Kind", "Offset", "Payload", "Size", "Skip"},
	"EndNode":    {"Depth", "End", "HeaderLen", "ID", "Kind", "Offset", "Size", "Start"},
}

// advanceTo pulls events from a fully fed cursor until it reports one of variant T.
func advanceTo[T Node](t *testing.T, c *Cursor) T {
	t.Helper()
	for {
		n, err := c.Next()
		if err != nil {
			t.Fatalf("Next while looking for a %T: %v", *new(T), err)
		}
		if got, ok := n.(T); ok {
			return got
		}
	}
}

// nodeMethodNames reports the exported methods of a node handle, from the type
// itself, and requires each to be callable with no arguments -- which is what lets
// these tests drive every method generically.
func nodeMethodNames(t *testing.T, handle Node) []string {
	t.Helper()
	typ := reflect.TypeOf(handle)
	var names []string
	for i := 0; i < typ.NumMethod(); i++ {
		m := typ.Method(i)
		if m.Type.NumIn() != 1 { // the receiver only
			t.Fatalf("%s.%s takes arguments; extend these tests to call it", typ, m.Name)
		}
		names = append(names, m.Name)
	}
	sort.Strings(names)
	return names
}

// callNodeMethod invokes an exported node method by name, with no arguments.
func callNodeMethod(handle Node, method string) {
	reflect.ValueOf(handle).MethodByName(method).Call(nil)
}

func freshCursor(t *testing.T, raw []byte) *Cursor {
	t.Helper()
	c := NewCursor(testKindClassifier)
	c.Feed(raw)
	return c
}

// TestNodeMethodSetIsCovered guards the tables below: the staleness tests drive every
// exported method the node types report, and this states which methods those are, so
// adding one to a variant cannot slip past the freshness contract unnoticed.
func TestNodeMethodSetIsCovered(t *testing.T) {
	raw := loadHexFixture(t, "fixtures/kvs/topology_basic.ebml.hex")
	for _, v := range nodeVariants {
		t.Run(v.name, func(t *testing.T) {
			handle := v.next(t, freshCursor(t, raw))
			got := nodeMethodNames(t, handle)
			want := wantNodeMethods[v.name]
			if fmt.Sprint(got) != fmt.Sprint(want) {
				t.Fatalf("exported methods of %s = %v, want %v (a new method needs a staleness test)", v.name, got, want)
			}
		})
	}
}

// TestStaleNodeRejectsEveryMethod is the core of the lifetime rule: a handle the
// cursor has moved past fails on every method, and it fails the same way -- the
// stale-node panic -- whether the method decides flow control or merely reads an
// extent field.
//
// The first case is the one a variant check alone cannot catch, and the reason the
// stamp is a generation and not a "which kind did I report last": the cursor's
// CURRENT event is of the very same variant as the stale handle, so answering it
// would report the later element's ID, offset and size without a word.
func TestStaleNodeRejectsEveryMethod(t *testing.T) {
	raw := loadHexFixture(t, "fixtures/kvs/topology_basic.ebml.hex")

	cases := []struct {
		name    string
		variant string // which method table applies
		// build returns a handle the cursor has moved past.
		build func(t *testing.T) Node
	}{
		{"leaf_copy_while_same_variant_is_live", "LeafNode", func(t *testing.T) Node {
			// A copy of an earlier leaf while another LEAF is the live event.
			c := freshCursor(t, raw)
			stale := *advanceTo[*LeafNode](t, c)
			advanceTo[*LeafNode](t, c)
			return &stale
		}},
		{"master_copy_while_same_variant_is_live", "MasterNode", func(t *testing.T) Node {
			c := freshCursor(t, raw)
			stale := *advanceTo[*MasterNode](t, c)
			advanceTo[*MasterNode](t, c)
			return &stale
		}},
		{"end_copy_while_same_variant_is_live", "EndNode", func(t *testing.T) Node {
			c := freshCursor(t, raw)
			stale := *advanceTo[*EndNode](t, c)
			advanceTo[*EndNode](t, c)
			return &stale
		}},
		{"pointer_while_other_variant_is_live", "MasterNode", func(t *testing.T) Node {
			// The pointer the cursor handed out, kept across Next.
			c := freshCursor(t, raw)
			stale := advanceTo[*MasterNode](t, c)
			advanceTo[*LeafNode](t, c)
			return stale
		}},
		// The three cases below are the retention that had NO check while the cursor
		// reused one instance per variant: the pointer it handed out pointed at that
		// instance, so once the instance was refilled for a later event of the SAME
		// variant the pointer was the live node -- same struct, live values, no stamp
		// left to disagree with. Allocating a node per event is what turned them into
		// ordinary stale nodes, and these cases are what keep them so.
		{"master_pointer_while_same_variant_is_live", "MasterNode", func(t *testing.T) Node {
			c := freshCursor(t, raw)
			stale := advanceTo[*MasterNode](t, c)
			advanceTo[*MasterNode](t, c)
			return stale
		}},
		{"leaf_pointer_while_same_variant_is_live", "LeafNode", func(t *testing.T) Node {
			c := freshCursor(t, raw)
			stale := advanceTo[*LeafNode](t, c)
			advanceTo[*LeafNode](t, c)
			return stale
		}},
		{"end_pointer_while_same_variant_is_live", "EndNode", func(t *testing.T) Node {
			c := freshCursor(t, raw)
			stale := advanceTo[*EndNode](t, c)
			advanceTo[*EndNode](t, c)
			return stale
		}},
		{"copy_after_finalize", "LeafNode", func(t *testing.T) Node {
			// Finalize advances the cursor too, so it invalidates nodes as well.
			c := freshCursor(t, raw)
			stale := *advanceTo[*LeafNode](t, c)
			for {
				_, err := c.Next()
				if isNeedMore(err) {
					break
				}
				if err != nil {
					t.Fatalf("Next: %v", err)
				}
			}
			if err := c.Finalize(); err != nil {
				t.Fatalf("Finalize: %v", err)
			}
			return &stale
		}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			for _, method := range wantNodeMethods[tc.variant] {
				t.Run(method, func(t *testing.T) {
					// A fresh build per method: a decision method that did NOT
					// panic would otherwise change the cursor's state for the
					// methods after it.
					handle := tc.build(t)
					wantStalePanic(t, fmt.Sprintf("%s.%s on a stale node", tc.variant, method), func() {
						callNodeMethod(handle, method)
					})
				})
			}
		})
	}
}

// wantStalePanic requires the STALE-node panic specifically, not merely some panic:
// every node method must fail the one uniform way, so a decision-conflict message
// (or any other) does not count as detecting a stale node.
func wantStalePanic(t *testing.T, what string, f func()) {
	t.Helper()
	defer func() {
		r := recover()
		if r == nil {
			t.Fatalf("%s did not panic", what)
		}
		msg, ok := r.(string)
		if !ok {
			t.Fatalf("%s panicked with %T, want a string message", what, r)
		}
		if !strings.Contains(msg, "called on a stale node: a Cursor node is valid only until the next Next call") {
			t.Fatalf("%s panic message = %q, want the uniform stale-node message", what, msg)
		}
		if !strings.HasPrefix(msg, "parser: ") {
			t.Fatalf("%s panic message = %q, want a parser-prefixed message", what, msg)
		}
	}()
	f()
}

// TestFreshNodeAcceptsEveryMethod is the other half: the freshness check must reject
// only stale nodes. Every method of the live node works, and so does every method of
// a COPY taken in the same generation -- the copy carries that generation, so it is
// as valid as the node it came from until the next Next.
func TestFreshNodeAcceptsEveryMethod(t *testing.T) {
	raw := loadHexFixture(t, "fixtures/kvs/topology_basic.ebml.hex")

	for _, v := range nodeVariants {
		t.Run(v.name, func(t *testing.T) {
			for _, method := range wantNodeMethods[v.name] {
				t.Run(method, func(t *testing.T) {
					// One method per cursor: several decisions on one node are a
					// programmer error of their own.
					live := v.next(t, freshCursor(t, raw))
					callNodeMethod(live, method)

					copied := copyOf(t, v.next(t, freshCursor(t, raw)))
					callNodeMethod(copied, method)
				})
			}
		})
	}
}

// copyOf copies a node the way a consumer would (v := *node), keeping the variant.
func copyOf(t *testing.T, n Node) Node {
	t.Helper()
	switch v := n.(type) {
	case *MasterNode:
		c := *v
		return &c
	case *LeafNode:
		c := *v
		return &c
	case *EndNode:
		c := *v
		return &c
	default:
		t.Fatalf("unknown node variant %T", n)
		return nil
	}
}

// TestNodeIsAllocatedPerEvent pins the mechanism the exceptionless validity rule rests
// on: the cursor hands out a NEW node for every event, never a refilled instance, so a
// node it has moved past is a distinct object whose generation stamp stays behind at
// the generation it was issued in. That is what leaves something for the check to
// disagree with -- while one instance per variant was reused, a retained pointer WAS
// the live node once the instance had been refilled, and the rule had to carve that
// retention out of the guarantee.
//
// Should the cursor ever start reusing instances again, it fails here, and the
// carve-out would have to come back into Node's doc, README.md and spec/SPEC.md
// section 3 with it.
func TestNodeIsAllocatedPerEvent(t *testing.T) {
	raw := loadHexFixture(t, "fixtures/kvs/topology_basic.ebml.hex")

	for _, v := range nodeVariants {
		t.Run(v.name, func(t *testing.T) {
			c := freshCursor(t, raw)
			retained := v.next(t, c)
			firstOffset := retained.Offset()
			// A copy is an independent value carrying the generation it was made in,
			// so it is invalidated with the event it was copied from -- and so, now,
			// is the pointer it was copied from.
			copied := copyOf(t, retained)

			live := v.next(t, c)
			if retained == live {
				t.Fatalf("cursor reported two %s events through one instance (%p): a pointer retained across Next would denote the live node and no check could catch it", v.name, retained)
			}
			if got := live.Offset(); got == firstOffset {
				t.Fatalf("%s at offset %d was reported twice; the test needs two distinct events", v.name, got)
			}
			// Same variant live, and both the retained pointer and the copy fail the
			// one uniform way.
			for _, held := range []struct {
				what string
				node Node
			}{
				{"retained pointer", retained},
				{"copy", copied},
			} {
				wantStalePanic(t, fmt.Sprintf("%s.ID on the %s to the earlier event", v.name, held.what), func() {
					held.node.ID()
				})
			}
			if got := copyOf(t, live).ID(); got != live.ID() {
				t.Fatalf("copy of the live %s reports %s, want the live %s", v.name, got, live.ID())
			}
		})
	}
}

// TestGenerationAdvancesOnNextAndFinalizeNotFeed pins the freshness bookkeeping
// directly on the counter, independently of any node method: only the operations that
// ADVANCE the cursor may invalidate a node.
//
// Feed must not, and that is not a detail: the documented answer to a payload that has
// not arrived is to feed the next chunk and ask the SAME node again, so a Feed that
// bumped the generation would turn the retry into a stale-node panic. Next must, even
// when it reports NeedMoreData, since it hands the consumer nothing to keep; and
// Finalize must, since it closes masters and so leaves any node already handed out
// behind.
func TestGenerationAdvancesOnNextAndFinalizeNotFeed(t *testing.T) {
	raw := loadHexFixture(t, "fixtures/kvs/topology_basic.ebml.hex")

	c := NewCursor(testKindClassifier)
	if got := c.gen; got != 0 {
		t.Fatalf("generation before the first event = %d, want 0", got)
	}
	c.Feed(raw[:1])
	if got := c.gen; got != 0 {
		t.Fatalf("Feed advanced the generation to %d", got)
	}
	gen := c.gen
	for i := 0; i < 4; i++ { // one byte fed, so every one of these is NeedMoreData
		if _, err := c.Next(); !isNeedMore(err) {
			t.Fatalf("Next %d = %v, want NeedMoreData", i, err)
		}
		if c.gen != gen+1 {
			t.Fatalf("Next %d moved the generation from %d to %d, want exactly one step", i, gen, c.gen)
		}
		gen = c.gen
	}

	// The payload retry, byte by byte: the node must stay usable across every Feed.
	c = NewCursor(testKindClassifier)
	pos := 0
	feed := func() bool {
		if pos >= len(raw) {
			return false
		}
		c.Feed(raw[pos : pos+1])
		pos++
		return true
	}
	var leaf *LeafNode
	for leaf == nil {
		n, err := c.Next()
		if err != nil {
			if !isNeedMore(err) {
				t.Fatalf("Next: %v", err)
			}
			if !feed() {
				t.Fatal("the stream ended before a non-empty leaf was reported")
			}
			continue
		}
		if l, ok := n.(*LeafNode); ok && l.Size() > 0 {
			leaf = l
		}
	}
	gen = c.gen
	id, size := leaf.ID(), leaf.Size()
	retries := 0
	for {
		payload, err := leaf.Payload()
		if err == nil {
			if int64(len(payload)) != size {
				t.Fatalf("Payload of %s = %d bytes, want its declared %d", id, len(payload), size)
			}
			break
		}
		if !isNeedMore(err) {
			t.Fatalf("Payload of %s: %v", id, err)
		}
		if !feed() {
			t.Fatalf("the stream ended before the payload of %s arrived", id)
		}
		retries++
		if c.gen != gen {
			t.Fatalf("Feed during the payload retry moved the generation from %d to %d", gen, c.gen)
		}
		if leaf.ID() != id || leaf.Size() != size {
			t.Fatalf("across Feed the node reports %s size %d, want %s size %d", leaf.ID(), leaf.Size(), id, size)
		}
	}
	if retries == 0 {
		t.Fatal("the payload arrived without a retry, so the Feed invariant was not exercised")
	}

	// Finalize advances it, like Next.
	for {
		_, err := c.Next()
		if err == nil {
			continue
		}
		if !isNeedMore(err) {
			t.Fatalf("Next: %v", err)
		}
		if !feed() {
			break
		}
	}
	gen = c.gen
	if err := c.Finalize(); err != nil {
		t.Fatalf("Finalize: %v", err)
	}
	if c.gen <= gen {
		t.Fatalf("Finalize left the generation at %d, so a node handed out before it would still answer", c.gen)
	}
}

// TestPayloadViewCannotAlterCursorState pins the payload half of the rule. Payload
// hands out a VIEW of the cursor's buffer -- bulk PCM is never copied merely to be
// looked at -- and its idempotence cache is the payload's EXTENT, not the slice the
// caller was given. So a caller that breaks the documented rule and writes to those
// bytes cannot reach the cursor's state: what the node reports, where the cursor
// stands, and the whole rest of the scan are unchanged.
func TestPayloadViewCannotAlterCursorState(t *testing.T) {
	raw := loadHexFixture(t, "fixtures/kvs/topology_basic.ebml.hex")
	want := cursorWantLines(cursorTopologyBasicEvents)

	c := NewCursor(testKindClassifier)
	c.Feed(raw)

	var lines []string
	mutated := 0
	finalized := false
	for {
		n, err := c.Next()
		if err != nil {
			if isNeedMore(err) {
				if finalized {
					t.Fatal("NeedMoreData after Finalize")
				}
				finalized = true
				if err := c.Finalize(); err != nil {
					t.Fatalf("Finalize: %v", err)
				}
				continue
			}
			if errors.Is(err, io.EOF) {
				break
			}
			t.Fatalf("Next: %v", err)
		}
		lines = append(lines, cursorLine(n))

		leaf, ok := n.(*LeafNode)
		if !ok {
			continue
		}
		payload, err := leaf.Payload()
		if err != nil {
			t.Fatalf("Payload of %s at %d: %v", leaf.ID(), leaf.Offset(), err)
		}
		if len(payload) == 0 {
			continue
		}
		// The view's capacity is clamped to its length, so not even an append by
		// the caller can reach bytes the cursor has not consumed.
		if cap(payload) != len(payload) {
			t.Fatalf("Payload cap = %d, want it clamped to len %d", cap(payload), len(payload))
		}
		// The cache is the extent: plain integers, nothing the caller holds.
		if c.payloadLen != len(payload) {
			t.Fatalf("cursor payload extent = %d bytes, want the delivered %d", c.payloadLen, len(payload))
		}

		id, offset, size, end := leaf.ID(), leaf.Offset(), leaf.Size(), leaf.End()
		at := c.Offset()
		for i := range payload { // a caller breaking the "must not be modified" rule
			payload[i] ^= 0xFF
		}
		if leaf.ID() != id || leaf.Offset() != offset || leaf.Size() != size || leaf.End() != end {
			t.Fatalf("mutating the payload changed what the node reports: %s @%d s%d e%d",
				leaf.ID(), leaf.Offset(), leaf.Size(), leaf.End())
		}
		if c.Offset() != at || c.payloadLen != len(payload) {
			t.Fatalf("mutating the payload changed cursor state: offset %d, extent %d bytes", c.Offset(), c.payloadLen)
		}
		again, err := leaf.Payload()
		if err != nil {
			t.Fatalf("second Payload of %s: %v", leaf.ID(), err)
		}
		if len(again) != len(payload) || &again[0] != &payload[0] {
			t.Fatal("second Payload did not re-derive the delivered extent from the buffer")
		}
		mutated++
	}

	// The scan itself is untouched: every event, in order, exactly as a scan that
	// asked for no payload at all.
	assertCursorLines(t, lines, want)
	if mutated == 0 {
		t.Fatal("no payload was mutated, so nothing was proven")
	}
}

// warmCursor returns a cursor fed with the whole fixture and advanced past the
// deepest nesting, so the open-master stack has reached its capacity: a slice growing
// mid-measurement is not a per-event cost.
func warmCursor(t *testing.T, raw []byte) *Cursor {
	t.Helper()
	c := NewCursor(testKindClassifier)
	c.Feed(raw)
	for i := 0; i < 20; i++ {
		if _, err := c.Next(); err != nil {
			t.Fatalf("warm-up Next: %v", err)
		}
	}
	return c
}

// TestPayloadDeliveryAddsNoAllocation is the zero-COPY claim measured, now that a
// per-event node has a price of its own: delivering a leaf payload hands out a view of
// bytes already in the cursor's buffer, so a scan that materialises EVERY payload
// allocates exactly what a scan that materialises none does -- the node, and nothing
// else. It is why a consumer may look at bulk PCM without paying for a copy of it.
func TestPayloadDeliveryAddsNoAllocation(t *testing.T) {
	raw := loadHexFixture(t, "fixtures/kvs/topology_basic.ebml.hex")

	measure := func(readPayload bool) (float64, int) {
		c := warmCursor(t, raw)
		delivered := 0
		allocs := testing.AllocsPerRun(30, func() {
			n, err := c.Next()
			if err != nil {
				t.Fatalf("Next: %v", err)
			}
			leaf, ok := n.(*LeafNode)
			if !ok || !readPayload {
				return
			}
			payload, err := leaf.Payload()
			if err != nil {
				t.Fatalf("Payload of %s: %v", leaf.ID(), err)
			}
			delivered += len(payload)
		})
		return allocs, delivered
	}

	reading, delivered := measure(true)
	if delivered == 0 {
		t.Fatal("no payload was delivered")
	}
	skipping, _ := measure(false)
	if reading != allocsPerEvent || skipping != allocsPerEvent {
		t.Fatalf("allocations per event = %v reading every payload, %v skipping every payload, want %d for both (the node alone)",
			reading, skipping, allocsPerEvent)
	}
}

// TestNodeAccessorsAddNoAllocation keeps the freshness check honest about cost: it is
// one integer comparison against the cursor's generation, so reading every extent
// accessor of an event adds nothing to the one node the event allocated.
func TestNodeAccessorsAddNoAllocation(t *testing.T) {
	raw := loadHexFixture(t, "fixtures/kvs/topology_basic.ebml.hex")
	c := warmCursor(t, raw)

	var sink int64
	allocs := testing.AllocsPerRun(30, func() {
		n, err := c.Next()
		if err != nil {
			t.Fatalf("Next: %v", err)
		}
		sink += int64(n.ID()) + int64(len(n.Kind())) + int64(n.Depth()) + n.Offset() + int64(n.HeaderLen()) + n.Size() + n.End()
	})
	if sink == 0 {
		t.Fatal("no event was read")
	}
	if allocs != allocsPerEvent {
		t.Fatalf("allocations per event with every accessor read = %v, want %d (the node alone)", allocs, allocsPerEvent)
	}
}
