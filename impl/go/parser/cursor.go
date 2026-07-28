package parser

import (
	"fmt"
	"io"
)

// BoundaryFunc decides where an unknown-size master ends. It is asked, on the
// header of every element that would otherwise become a child of the innermost
// open unknown-size master, whether that master ends here instead: open is the
// open master's ID, next the ID of the element about to be reported.
//
// EBML gives an unknown-size master no declared end, so before EOF the only thing
// that can establish one is the knowledge that some element cannot be its child --
// and that is element schema, which this layer deliberately does not have (see
// KindClassifier). The rule is therefore consumer POLICY and belongs to the cursor
// as a whole, not to an individual event: a per-event answer would let the same
// stream be split differently depending on where the consumer happened to look.
//
// Reporting true closes the master -- its EndNode is reported with End at this
// offset, before the element's own event -- and the question is repeated for the
// master enclosing it on the next Next, so several nested unknown-size masters
// close outward one event at a time. Reporting false, and supplying no rule at
// all, keeps the documented default: the master stays open until Finalize.
//
// This is what a stream of concatenated unknown-size masters needs. KVS GetMedia
// is exactly that -- one unknown-size Segment per fragment -- and its rule is that
// a Segment ends where the next top-level element begins:
//
//	parser.WithBoundary(func(open, next parser.ElementID) bool {
//	    return next == matroska.IDEBML || next == matroska.IDSegment
//	})
//
// The decision is driven by element structure, never by scanning the bytes for the
// EBML magic, so payload data that happens to contain that magic cannot cause a
// spurious split.
type BoundaryFunc func(open, next ElementID) bool

// WithBoundary supplies the rule that ends an unknown-size master before EOF; see
// BoundaryFunc. Without it every unknown-size master stays open until Finalize.
//
// Only a Cursor consults it. New accepts the option and ignores it: the low-level
// Parser has no boundary rule of its own, CloseMaster being its explicit form.
func WithBoundary(fn BoundaryFunc) Option {
	return func(s *settings) { s.boundary = fn }
}

// Node is one event of a Cursor: an element header the cursor has just reported,
// or a master that has just ended. It carries the element's identity and extent,
// and the operations that are valid for it -- and only those, so a leaf payload
// cannot be requested from a master and a decision cannot be taken on an end.
//
// The interface is CLOSED: the unexported marker method means *MasterNode,
// *LeafNode and *EndNode are the only implementations, so a type switch over the
// three is exhaustive for all time and a consumer never has to handle a fourth
// case it cannot construct.
//
// A Node carries no element name and no value type: naming and typing are the
// registry's job (package matroska), which keeps this layer free of element
// knowledge. What it does carry is Kind, the classifier's verdict the cursor read
// the element BY -- structure, not interpretation; see Kind for that division.
//
// # Validity
//
// A node is valid only until the next call to Next (or Finalize, which also
// advances the cursor). Feed does not invalidate a node -- that is deliberate, so a
// payload can be retried across chunk boundaries (see LeafNode.Payload). Read what
// you need from a node, or copy it, before calling Next again.
//
// EVERY method below, and every method of the three variants, rejects a node the
// cursor has moved past: the cursor stamps each node it hands out with a generation
// it bumps on every Next, and a method whose stamp is not the current one panics
// instead of answering. The accessors are checked exactly like the decisions, so a
// stale node can never silently report the values of a LATER event.
//
// The guarantee has NO exception. It holds for the pointer the cursor handed out and
// for a node COPY (v := *node) alike, since the copy carries the generation it was
// made in; and it holds when the cursor's current event is of the SAME variant as the
// retained node just as it does for any other. What makes it exceptionless is that
// Next allocates a new node per event rather than refilling one instance per variant:
// a node the cursor has moved past is a distinct object whose stamp stays behind at
// the generation it was issued in, so there is always a stamp left to disagree with.
// Reuse would leave a retained pointer pointing at storage that has since been
// refilled -- the live node itself, which no check can tell from a fresh one. The
// price is one small allocation per event; see Cursor for the measurement and for the
// cheaper surface a consumer unwilling to pay it uses instead.
//
// See MasterNode.Descend for why a programmer error panics here rather than being
// reported as an error.
type Node interface {
	// ID is the element's ID exactly as encoded on the wire.
	ID() ElementID
	// Kind is the verdict the KindClassifier returned for this element -- exactly
	// what the cursor decided the element's STRUCTURE by, so it is KindMaster for a
	// *MasterNode and the classifier's own leaf kind (KindUint, KindBinary,
	// KindUnknown, ...) for a *LeafNode, never flattened to one leaf kind. An
	// *EndNode reports KindEndMaster, the kind of the observation rather than of the
	// element.
	//
	// It is NOT the value type to decode a payload as; see Kind.
	Kind() Kind
	// Depth is the number of enclosing masters; 0 at the top level. For an
	// EndNode it is the closed master's own depth.
	Depth() int
	// Offset is the absolute offset of the element's first HEADER byte.
	Offset() int64
	// HeaderLen is the encoded length of the ID VINT plus the size VINT, so the
	// payload starts at Offset+HeaderLen.
	HeaderLen() int
	// Size is the declared payload length, or UnknownSize.
	Size() int64
	// End is one past the element's last byte (Offset+HeaderLen+Size), or
	// UnknownSize while a master's size is unknown. On an EndNode it is the
	// concrete offset the master closed at, even for an unknown-size master.
	End() int64

	// node keeps this interface closed to the three variants in this package.
	node()
}

// nodeExtent is the identity and extent every node variant carries, plus the cursor
// that issued it and the generation it was issued in. It is embedded by value, and
// its methods take a pointer receiver, so only *MasterNode, *LeafNode and *EndNode
// satisfy Node.
type nodeExtent struct {
	c         *Cursor
	gen       uint64
	id        ElementID
	kind      Kind
	depth     int
	offset    int64
	headerLen int
	size      int64
	end       int64
}

func (n *nodeExtent) ID() ElementID {
	n.fresh("ID")
	return n.id
}

func (n *nodeExtent) Kind() Kind {
	n.fresh("Kind")
	return n.kind
}

func (n *nodeExtent) Depth() int {
	n.fresh("Depth")
	return n.depth
}

func (n *nodeExtent) Offset() int64 {
	n.fresh("Offset")
	return n.offset
}

func (n *nodeExtent) HeaderLen() int {
	n.fresh("HeaderLen")
	return n.headerLen
}

func (n *nodeExtent) Size() int64 {
	n.fresh("Size")
	return n.size
}

func (n *nodeExtent) End() int64 {
	n.fresh("End")
	return n.end
}

func (n *nodeExtent) node() {}

// fresh rejects a node the cursor has moved past. Every exported node method calls
// it first -- the extent accessors as much as the decisions -- so a stale node cannot
// answer with the CURRENT event's values, which is the silent corruption the validity
// rule exists to prevent. There is no retention it fails to see: a node belongs to the
// single event it was issued for, so its stamp can never become current again. See
// Node.
//
// The test is one integer comparison against the cursor's generation counter, and
// the node carries its generation by value, so a COPY of a node is rejected once the
// cursor has moved on just like the node it was copied from. Nothing is allocated:
// the message is built only on the failing path.
func (n *nodeExtent) fresh(method string) {
	if n.c == nil || n.c.gen != n.gen {
		panicStale(method)
	}
}

// panicStale is the single stale-node failure, so every node method fails the same
// way with the same message.
func panicStale(method string) {
	panic("parser: " + method + " called on a stale node: a Cursor node is valid only until the next Next call")
}

// decide records a flow-control decision, rejecting a stale node and a second
// decision on the same node.
func (n *nodeExtent) decide(method string, d decision) {
	n.fresh(method)
	c := n.c
	if c.decision != decNone {
		panic(fmt.Sprintf("parser: %s called after %s: a node takes exactly one decision", method, c.decision))
	}
	c.decision = d
}

// set fills the extent of a newly allocated node, stamping the cursor's current
// generation so that this node -- and only this node -- is fresh.
func (n *nodeExtent) set(c *Cursor, id ElementID, kind Kind, depth int, offset int64, headerLen int, size, end int64) {
	n.c = c
	n.gen = c.gen
	n.id = id
	n.kind = kind
	n.depth = depth
	n.offset = offset
	n.headerLen = headerLen
	n.size = size
	n.end = end
}

// MasterNode is a master element's header. Its payload is a sequence of child
// elements, so the decision it offers is structural: Descend to have those
// children reported, or Skip to step over the whole subtree.
//
// Taking no decision at all descends. See Cursor for why that default, and the
// leaf's opposite one, are the pay-for-what-you-use pair.
type MasterNode struct{ nodeExtent }

// LeafNode is a leaf element's header. Its payload is opaque bytes to the cursor,
// so the decision it offers is about materialising them: Payload delivers them,
// Skip steps over them, and doing nothing skips them.
type LeafNode struct{ nodeExtent }

// EndNode reports that a master just ended. It carries no decision: the master is
// already closed and its extent is settled.
//
// For a known-size master the end is COMPUTED, so it is observed the moment the
// declared payload has been consumed -- regardless of any enclosing unknown-size
// master that is still open. That is the property this library exists for: a KVS
// Cluster's end is observable without waiting for its Segment, so the last
// fragment of a live stream need not wait for the connection to close.
//
// WHY it ended is a separate question from WHERE, and Reason answers it.
type EndNode struct {
	nodeExtent
	reason CloseReason
}

// CloseReason names why a master ended. The extent alone cannot say: an
// unknown-size master closed by the boundary rule and one closed by the end of the
// input produce the same EndNode otherwise, and they mean different things to a
// consumer -- the first is an ordinary structural close, the second is the stream
// running out with that master still open.
//
// The three values are exhaustive because the cursor has exactly three ways to
// close a master, and a fourth would be a change to Cursor rather than a value a
// stream can produce.
type CloseReason uint8

const (
	// ClosedByDeclaredEnd is a known-size master whose declared payload has been
	// consumed. It is the ordinary close and the zero value.
	ClosedByDeclaredEnd CloseReason = iota
	// ClosedByBoundary is an unknown-size master ended by the consumer's boundary
	// rule, because the header of an element that cannot be its child arrived.
	// That element is not consumed by the close: the next Next reports it, at the
	// enclosing depth. See Cursor.WithBoundary.
	ClosedByBoundary
	// ClosedByEndOfInput is a master still open when Finalize declared the input
	// over. Every master open at that point closes this way, outermost last. It
	// says nothing about whether the tail was truncated -- an unknown-size Segment
	// closing here is the normal end of a complete live stream -- so a consumer
	// asking whether bytes were lost reads the error, not this.
	ClosedByEndOfInput
)

func (r CloseReason) String() string {
	switch r {
	case ClosedByDeclaredEnd:
		return "declared end"
	case ClosedByBoundary:
		return "boundary"
	case ClosedByEndOfInput:
		return "end of input"
	}
	return fmt.Sprintf("CloseReason(%d)", uint8(r))
}

// Reason reports why the master ended: its declared end was reached, the boundary
// rule closed it, or the input ended with it still open.
//
// It is the one thing about a close that a consumer cannot derive for itself. Which
// of the two unknown-size closes happened is decided by the boundary rule, so
// re-deriving it means restating that rule in the caller -- the duplication
// matroska.StreamBoundary exists to prevent.
//
// It panics on a stale node, like every other node method; see Node.
func (n *EndNode) Reason() CloseReason {
	n.fresh("Reason")
	return n.reason
}

// Descend enters the master, so its children are reported by the following Next
// calls. It is the DEFAULT: a MasterNode nobody decided on is descended into, and
// calling Descend only states that intent explicitly.
//
// It records the decision; the cursor acts on it during the next Next.
//
// It panics when the node is stale -- when a Next call has already happened, so
// the node no longer describes the element the caller is looking at -- and when a
// decision was already taken on this node. Both are programmer errors that no
// stream condition can produce and no amount of further input can repair, exactly
// like the nil classifier New rejects, so failing loudly beats acting on the wrong
// element silently.
func (n *MasterNode) Descend() {
	n.decide("Descend", decDescend)
}

// Skip steps over the master's ENTIRE subtree: no descendant event is reported and
// no EndNode is reported for the master itself, exactly as if the element had not
// been there.
//
// The subtree's size must be known. An unknown-size master has no locatable end,
// so there is nothing to skip over; that attempt is reported as a structural error
// by the next Next, not here, because whether a master declares a size is a
// property of the stream. Descend into it instead and let the boundary rule close
// it (see WithBoundary).
//
// It panics on a stale node or a second decision; see Descend.
func (n *MasterNode) Skip() {
	n.decide("Skip", decSkipSubtree)
}

// Skip steps over the leaf's payload without materialising it. It is the DEFAULT:
// a LeafNode nobody decided on is skipped, and calling Skip only states that
// intent explicitly. It is how bulk data -- PCM in a SimpleBlock, say -- never has
// to be held in memory.
//
// It panics on a stale node or a second decision; see MasterNode.Descend.
func (n *LeafNode) Skip() {
	n.decide("Skip", decSkipPayload)
}

// Payload returns the leaf's complete payload WITHOUT copying it: the bytes are a
// view of the cursor's own buffer, which is what keeps bulk data -- PCM in a
// SimpleBlock, say -- from being copied merely to be looked at.
//
// The view comes with requirements, and they are the node's own validity rule
// (see Node) applied to bytes:
//
//   - The bytes are valid only until the next Next (or Finalize). They survive
//     Feed, like the node itself.
//   - They must NOT be modified. They are the cursor's buffer, not the caller's.
//   - A caller that needs them afterwards, or needs to change them, copies them
//     (bytes.Clone) -- and pays for that copy only where it is really needed.
//
// It may report NeedMoreData, and that is not a failure: a leaf is reported on its
// HEADER, before its payload bytes need have arrived, because deciding on the
// header is what makes a scan split-invariant. The node stays valid across Feed,
// so the answer to NeedMoreData is simply the next chunk:
//
//	for {
//	    payload, err := leaf.Payload()
//	    if err == nil { use(payload); break }
//	    var needMore parser.NeedMoreData
//	    if !errors.As(err, &needMore) { return err }
//	    cursor.Feed(next(chunk))   // the node survives this
//	}
//
// It is idempotent within the node's lifetime: once the payload has been read,
// every further call returns those same bytes rather than reading the stream again.
// The cursor remembers only WHERE they are, never the slice it handed out, so a
// caller that breaks the rule above and writes to the bytes cannot alter the
// cursor's own state.
//
// It panics on a stale node, and when the payload was already skipped -- Skip and
// Payload are contradictory decisions about the same element; see
// MasterNode.Descend.
func (n *LeafNode) Payload() ([]byte, error) {
	n.fresh("Payload")
	c := n.c
	switch c.decision {
	case decPayloadDone:
		return c.p.view(c.payloadStart, c.payloadLen), nil
	case decSkipPayload:
		panic("parser: LeafNode.Payload called after Skip: a leaf's payload cannot be both skipped and delivered")
	}
	start, size, err := c.p.consumePayload()
	if err != nil {
		return nil, err
	}
	c.payloadStart, c.payloadLen = start, size
	c.decision = decPayloadDone
	return c.p.view(start, size), nil
}

// Start is the absolute offset of the closed master's first header byte, so its
// full extent is [Start, End). It is the same value Offset reports, named for the
// question an EndNode answers: which range of the stream was this master?
//
// It panics on a stale node, like every other node method; see Node.
func (n *EndNode) Start() int64 {
	n.fresh("Start")
	return n.offset
}

// pending is the element operation the cursor still owes the stream: a header has
// been consumed and its payload has not been dealt with yet. It survives a Next
// that reported NeedMoreData, so the decision taken on the header is carried out
// once the bytes arrive and is never asked for twice.
type pending uint8

const (
	pendNone pending = iota
	pendMaster
	pendLeaf
)

// decision is what the consumer asked for on the pending element, or decNone when
// it asked for nothing and the lazy default applies.
type decision uint8

const (
	decNone decision = iota
	decDescend
	decSkipSubtree
	decSkipPayload
	// decPayloadDone means LeafNode.Payload already consumed the payload, so the
	// cursor has nothing left to do for that element.
	decPayloadDone
)

func (d decision) String() string {
	switch d {
	case decDescend:
		return "Descend"
	case decSkipSubtree:
		return "Skip"
	case decSkipPayload:
		return "Skip"
	case decPayloadDone:
		return "Payload"
	default:
		return "none"
	}
}

// openMaster is a master the cursor descended into: what an EndNode needs in order
// to report the master's full extent when it closes.
type openMaster struct {
	id        ElementID
	offset    int64
	headerLen int
	size      int64
}

// Cursor is the reading core: a TOKEN PULL loop over a continuous EBML stream.
// Input is pushed in chunks with Feed, and events are pulled one at a time with
// Next, so the consumer owns the read loop and keeps its state in local variables
// instead of hoisting it into the fields of a callback object.
//
//	c := parser.NewCursor(matroska.KindForElementID)
//	for {
//	    node, err := c.Next()
//	    if err != nil {
//	        var needMore parser.NeedMoreData
//	        switch {
//	        case errors.As(err, &needMore):
//	            if chunk, ok := src.next(); ok {
//	                c.Feed(chunk)
//	                continue
//	            }
//	            if err := c.Finalize(); err != nil { return err }
//	            continue
//	        case errors.Is(err, io.EOF):
//	            return nil
//	        default:
//	            return err // structural: IsStructural(err) is true
//	        }
//	    }
//	    switch node.(type) {
//	    case *parser.MasterNode: // Descend (the default) or Skip
//	    case *parser.LeafNode:   // Payload, or Skip (the default)
//	    case *parser.EndNode:    // a master's extent is settled
//	    }
//	}
//
// # Pay for what you use
//
// Every event is reported on the element's HEADER, before its payload bytes need
// have arrived, and the defaults are the cheap ones: a MasterNode nobody touches
// is descended into, and a LeafNode nobody touches has its payload SKIPPED, never
// materialised. So a consumer materialises exactly the payloads it asks for, and it
// does not pay to look at even those: LeafNode.Payload hands out a view of the
// cursor's own buffer, so a scan that reads EVERY payload copies nothing.
//
// Deciding on the header is also what keeps a scan SPLIT-INVARIANT: the event
// sequence and every decision point are identical no matter how the input was
// chunked, because no decision can depend on how much of a payload happened to
// have arrived.
//
// # What the freshness guarantee costs
//
// Next allocates ONE node per event -- one small struct, and nothing else: reading
// every extent accessor adds nothing, and delivering a payload adds nothing, both
// measured in the parser tests. That allocation is what buys the exceptionless node
// validity rule of Node: a node the cursor has moved past is a distinct object, so
// every retention of it is caught. Reusing one instance per variant would save the
// allocation and give a retained pointer back the ability to report a later event
// silently, which is the failure the rule exists to prevent.
//
// A consumer for which that is too much drives Parser instead: Peek reports an
// ElementHeader BY VALUE, so the low-level engine hands out no node and the whole
// per-event cost above disappears -- at the price of doing the flow control and the
// open-master bookkeeping by hand. BenchmarkCursorScan and BenchmarkParserScan run
// the same scan of the same fixture both ways, so the difference is on the record
// rather than argued about. Cursor is the surface that is safe by default; Parser is
// the one that is cheap.
//
// # Errors
//
// Next reports three kinds of outcome, and a consumer must tell them apart:
//
//   - NeedMoreData -- not a failure. Nothing further can be reported from the
//     bytes fed so far; the answer is the next Feed, or Finalize when the input is
//     over. Iteration resumes exactly where it stopped.
//   - io.EOF -- the stream is over: Finalize ran and every remaining master has
//     been reported. Repeated calls keep reporting it.
//   - a STRUCTURAL failure, for which IsStructural is true: these bytes cannot be
//     read as EBML, so the position of the next element is unknown. The cursor is
//     failed and stays failed; every later call reports the same error.
//
// A Cursor is not safe for concurrent use.
type Cursor struct {
	p        *Parser
	boundary BoundaryFunc

	// open are the masters descended into, outermost first, so len(open) is the
	// current depth.
	open []openMaster

	// gen counts the events handed out. Every node is stamped with it and every
	// node method compares against it, which is how a node the cursor has moved
	// past -- or a copy of one -- is caught instead of answering with this event's
	// values. It is bumped by Next and Finalize, the operations that advance the
	// cursor, and NEVER by Feed: a node has to survive a Feed for the payload retry
	// after NeedMoreData to work at all (see LeafNode.Payload).
	gen uint64

	pending  pending
	decision decision
	// pendOpen is the pending master's extent, kept here so descending can push
	// it after the parser has cleared its own current element.
	pendOpen openMaster
	// payloadStart and payloadLen locate the payload LeafNode.Payload delivered
	// inside the parser's buffer, so a second call within the node's lifetime
	// re-derives those same bytes instead of reading the stream twice. The cache is
	// deliberately the EXTENT and not the slice handed out: it is unreachable
	// integers, so a caller that modifies the bytes it was given cannot change what
	// the cursor holds.
	payloadStart int
	payloadLen   int

	// closes are the masters Finalize closed at EOF, awaiting delivery as
	// EndNodes, inner to outer.
	closes    []ClosedMaster
	finalized bool
	eofRun    bool

	err     error // latched terminal failure
	lastErr error // what the last Next reported, for Err
}

// NewCursor returns a token pull cursor that classifies elements with classify.
//
// The classifier is required, not optional, for the reasons given on New, which
// this constructor calls: NewCursor panics when classify is nil. Options are the
// Parser's own plus WithBoundary, which is the cursor's unknown-size master rule.
func NewCursor(classify KindClassifier, opts ...Option) *Cursor {
	set := apply(opts)
	return &Cursor{
		p:        New(classify, opts...),
		boundary: set.boundary,
		// Sized for the depth of real documents so a steady-state scan does not
		// grow this slice, which would allocate mid-scan.
		open: make([]openMaster, 0, 8),
	}
}

// Offset reports the absolute offset the cursor has reached.
func (c *Cursor) Offset() int64 { return c.p.Offset() }

// Depth reports how many masters are currently open. It is 0 before the first
// event and 0 again once Finalize has closed everything.
func (c *Cursor) Depth() int { return len(c.open) }

// Unconsumed returns a copy of the bytes fed but not yet consumed, the first of
// which sits at absolute offset Offset(). See Parser.Unconsumed: it exists for a
// consumer that wants to resynchronize after a structural failure, not for normal
// reading.
func (c *Cursor) Unconsumed() []byte { return c.p.Unconsumed() }

// Feed appends the next chunk of the stream. It never advances the cursor and
// never reports an event: nothing is parsed until Next asks for it, so a node
// already in the consumer's hands stays valid across a Feed -- which is what lets
// a payload be retried after NeedMoreData.
//
// Feeding after Finalize is a programmer error: the input was declared over. It is
// recorded as a structural failure and reported by the next Next.
func (c *Cursor) Feed(chunk []byte) {
	if c.err != nil {
		return
	}
	if c.finalized {
		// The latched error is what the next Next reports; fail's return value
		// has no one to go to, since Feed reports nothing itself.
		_ = c.fail(Invalid{Msg: "cannot feed a finalized cursor"})
		return
	}
	c.p.Feed(chunk)
}

// Next reports the next event: a *MasterNode or *LeafNode for an element header, or
// an *EndNode for a master that just ended. The returned node is valid only until
// the following Next call.
//
// It first carries out the decision left on the previous node -- descending,
// skipping a subtree, or skipping a payload -- so a consumer that wants the
// default need do nothing at all. See Cursor for how to classify the errors it
// reports.
func (c *Cursor) Next() (Node, error) {
	n, err := c.next()
	c.lastErr = err
	return n, err
}

func (c *Cursor) next() (Node, error) {
	// Every node handed out so far is now stale -- the new generation is what says
	// so -- and the payload extent belongs to the leaf that is being left behind.
	c.gen++
	c.payloadStart, c.payloadLen = 0, 0

	if c.err != nil {
		return nil, c.err
	}

	if err := c.resolve(); err != nil {
		if _, ok := err.(NeedMoreData); ok {
			// The decision stands and the operation is retried on the next call;
			// this is flow control, not a failure.
			return nil, err
		}
		return nil, c.fail(err)
	}
	return c.advance()
}

// resolve carries out the operation the previously reported node left pending. A
// NeedMoreData return leaves the pending state untouched, so the same operation is
// retried once more bytes arrive: the consumer is never asked about the element
// twice.
func (c *Cursor) resolve() error {
	switch c.pending {
	case pendMaster:
		if c.decision == decSkipSubtree {
			if c.pendOpen.size == UnknownSize {
				return Invalid{Msg: fmt.Sprintf("cannot skip subtree of unknown-size master %s: it has no locatable end; descend into it instead", c.pendOpen.id)}
			}
			if err := c.p.SkipCurrentPayload(); err != nil {
				return err
			}
		} else {
			// decNone (the lazy default) and decDescend both descend.
			if err := c.p.EnterMaster(); err != nil {
				return err
			}
			c.open = append(c.open, c.pendOpen)
		}
	case pendLeaf:
		// decNone (the lazy default) and decSkipPayload both skip; decPayloadDone
		// means Payload already consumed the bytes.
		if c.decision != decPayloadDone {
			if err := c.p.SkipPayload(); err != nil {
				return err
			}
		}
	}
	c.pending, c.decision, c.pendOpen = pendNone, decNone, openMaster{}
	return nil
}

// advance reports the next event from the bytes available.
func (c *Cursor) advance() (Node, error) {
	if n, err, ok := c.dequeueClose(); ok {
		return n, err
	}

	h, err := c.p.Peek()
	if err != nil {
		if _, ok := err.(NeedMoreData); !ok {
			return nil, c.fail(err)
		}
		if !c.finalized {
			return nil, err
		}
		// The input is over and the buffered bytes are exhausted: close what is
		// still open and report those masters, then the stream itself is done.
		if !c.eofRun {
			if err := c.runEOF(); err != nil {
				return nil, c.fail(err)
			}
			if n, err, ok := c.dequeueClose(); ok {
				return n, err
			}
		}
		return nil, io.EOF
	}

	if h.Kind == KindEndMaster {
		if h.ID != 0 {
			return nil, c.fail(Invalid{Msg: fmt.Sprintf("classifier reported element %s as %s", h.ID, KindEndMaster)})
		}
		// A known-size master reached its declared end.
		if err := c.p.LeaveMaster(); err != nil {
			return nil, c.fail(err)
		}
		return c.endEvent(ClosedByDeclaredEnd)
	}

	// An unknown-size master has no declared end, so only the consumer's boundary
	// rule can end it before EOF. Asked on this element's header, it may report
	// that the master ends here; the element itself is then reported by the next
	// call, at the enclosing depth.
	if c.boundary != nil && len(c.open) > 0 {
		if open := c.open[len(c.open)-1]; open.size == UnknownSize && c.boundary(open.id, h.ID) {
			if err := c.p.CloseMaster(); err != nil {
				return nil, c.fail(err)
			}
			return c.endEvent(ClosedByBoundary)
		}
	}

	// Peek parsed the whole header, so its bytes are buffered and consuming it
	// cannot need more data.
	if _, err := c.p.ConsumeHeader(); err != nil {
		return nil, c.fail(err)
	}
	if h.Kind == KindMaster {
		return c.issueMaster(h), nil
	}
	return c.issueLeaf(h), nil
}

// endOf is the element's end offset, or UnknownSize while its size is unknown.
func endOf(h ElementHeader) int64 {
	if h.Size == UnknownSize {
		return UnknownSize
	}
	return h.Offset + int64(h.HeaderLen) + h.Size
}

// issueMaster, issueLeaf and issueEnd each allocate the node they report. A node is
// never reused for a second event, which is what leaves an abandoned node's
// generation stamp behind for fresh to catch; see Node.
func (c *Cursor) issueMaster(h ElementHeader) Node {
	c.pending, c.decision = pendMaster, decNone
	c.pendOpen = openMaster{id: h.ID, offset: h.Offset, headerLen: h.HeaderLen, size: h.Size}
	n := &MasterNode{}
	n.set(c, h.ID, h.Kind, len(c.open), h.Offset, h.HeaderLen, h.Size, endOf(h))
	return n
}

func (c *Cursor) issueLeaf(h ElementHeader) Node {
	c.pending, c.decision = pendLeaf, decNone
	n := &LeafNode{}
	n.set(c, h.ID, h.Kind, len(c.open), h.Offset, h.HeaderLen, h.Size, endOf(h))
	return n
}

// issueEnd reports the innermost open master as closed at the current offset. The
// parser has already popped it; this pops the cursor's own record of it. It returns
// the concrete *EndNode so that the EOF path can correct the end offset it computed.
func (c *Cursor) issueEnd(reason CloseReason) (*EndNode, error) {
	if len(c.open) == 0 {
		return nil, c.fail(Invalid{Msg: "master end reached with no open master"})
	}
	om := c.open[len(c.open)-1]
	c.open = c.open[:len(c.open)-1]
	n := &EndNode{reason: reason}
	// The kind of the OBSERVATION, not of the element: the master itself was
	// classified KindMaster, and what an EndNode reports is that it just ended.
	n.set(c, om.id, KindEndMaster, len(c.open), om.offset, om.headerLen, om.size, c.p.Offset())
	return n, nil
}

// endEvent is issueEnd as an event, keeping the failure a nil Node rather than a
// non-nil interface holding a nil *EndNode.
func (c *Cursor) endEvent(reason CloseReason) (Node, error) {
	n, err := c.issueEnd(reason)
	if err != nil {
		return nil, err
	}
	return n, nil
}

// dequeueClose reports the next master Finalize closed at EOF. ok is false when
// none is waiting, so the caller carries on reading the stream.
func (c *Cursor) dequeueClose() (n Node, err error, ok bool) {
	if len(c.closes) == 0 {
		return nil, nil, false
	}
	cm := c.closes[0]
	c.closes = c.closes[1:]
	if len(c.open) == 0 {
		return nil, c.fail(Invalid{Msg: fmt.Sprintf("master %s closed at EOF was never entered", cm.ID)}), true
	}
	if got := c.open[len(c.open)-1].id; got != cm.ID {
		return nil, c.fail(Invalid{Msg: fmt.Sprintf("master stack mismatch at EOF: closing %s, cursor holds %s", cm.ID, got)}), true
	}
	end, err := c.issueEnd(ClosedByEndOfInput)
	if err != nil {
		return nil, err, true
	}
	end.end = cm.End
	return end, nil, true
}

// Finalize declares that no more bytes will arrive. It closes the masters still
// open -- an unknown-size master such as a KVS Segment is closed here -- and
// reports TruncatedError when the stream ends inside a header or inside a declared
// payload. That error carries the evidence a consumer classifies a truncated tail
// by -- the absolute offset at which the input ended, and the element open there --
// because whether the tail is expected depends on the caller's transport, not on
// the stream; see TruncatedError.
//
// Call it after Next has reported NeedMoreData, i.e. once the bytes fed have been
// drained. Those masters are reported as EndNodes by the following Next calls,
// which then report io.EOF; that is deliberate, since a master's extent is an
// event like any other and a consumer assembling per master must see the last one.
// Should complete events still be unreported when Finalize is called, they are
// reported first and the closing happens when Next reaches the end of the bytes:
// closing early would silently discard events the consumer has not seen.
//
// Finalize is idempotent, and Feed after it is a programmer error. Like Next it
// advances the cursor, so it invalidates any node in the consumer's hands.
func (c *Cursor) Finalize() error {
	if c.err != nil {
		return c.err
	}
	if c.finalized {
		return nil
	}
	c.finalized = true
	// Finalize advances the cursor, so it invalidates every node handed out.
	c.gen++
	c.payloadStart, c.payloadLen = 0, 0

	// The last reported node may still owe the stream an operation. At EOF its
	// payload has either arrived in full or the stream is truncated, and
	// FinalizeEOF is what reports the latter.
	pendingIncomplete := false
	if err := c.resolve(); err != nil {
		if _, ok := err.(NeedMoreData); !ok {
			return c.fail(err)
		}
		pendingIncomplete = true
	}
	if !pendingIncomplete {
		_, err := c.p.Peek()
		if err == nil {
			// Events the buffered bytes still complete: Next reports them first,
			// and the closing happens once it reaches the end of the bytes.
			return nil
		}
		if _, ok := err.(NeedMoreData); !ok {
			return c.fail(err)
		}
	}
	if err := c.runEOF(); err != nil {
		return c.fail(err)
	}
	return nil
}

// runEOF closes the remaining masters and queues them for delivery.
func (c *Cursor) runEOF() error {
	c.eofRun = true
	closed, err := c.p.FinalizeEOF()
	if err != nil {
		return err
	}
	c.closes = closed
	return nil
}

// Err restates why the cursor last stopped, for a consumer that has moved past
// the error Next returned:
//
//   - nil before the first event.
//   - NeedMoreData when the bytes fed are exhausted: feed more, or Finalize, and
//     call Next again to resume exactly where it stopped.
//   - io.EOF when the stream ended cleanly after Finalize.
//   - a structural failure otherwise, for which IsStructural is true.
//
// It reports; it never advances. Next remains the only way to acquire an event,
// and that NeedMoreData and io.EOF are distinct values of this error is the
// distinction the whole reading surface is shaped around: a reader whose input
// arrives in chunks must tell "the next chunk is due" from "the input is over".
func (c *Cursor) Err() error { return c.lastErr }

func (c *Cursor) fail(err error) error {
	c.err = err
	return err
}
