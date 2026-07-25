package fragment

import (
	"bytes"
	"fmt"

	"github.com/yacchi/ebml-reader/ext/tree"
	"github.com/yacchi/ebml-reader/matroska"
	"github.com/yacchi/ebml-reader/parser"
)

// DefaultMaxRetainedPayload caps how many bytes a single retained leaf payload
// may hold when WithMaxRetainedPayload says nothing: 1 MiB.
//
// It is generous for the metadata a fragment actually retains -- tags, track
// descriptions, Segment info -- while keeping a hostile or corrupt stream from
// turning one absurd declared size into an allocation of that size. Bulk media is
// not affected: SimpleBlock payloads are decoded into Fragment.Blocks and never
// retained in the tree, so the cap governs metadata only.
const DefaultMaxRetainedPayload = 1 << 20

// Option configures an Assembler.
type Option func(*Assembler)

// WithMaxRetainedPayload caps how many payload bytes a single leaf element may
// retain. A leaf whose declared size exceeds n is retained with its structure
// intact but its payload elided: tree.Element.Truncated is set and Payload is
// nil, while Offset, HeaderLen, Size and End stay accurate, so the bytes can be
// re-read from the original source.
//
// WithMaxRetainedPayload(0) retains the shape of every fragment without copying a
// single metadata byte; a negative n means unlimited. The default is
// DefaultMaxRetainedPayload. The cap does not apply to SimpleBlock payloads,
// which are never retained in the tree -- they are decoded into Fragment.Blocks,
// which is the data the consumer asked for.
func WithMaxRetainedPayload(n int) Option {
	return func(a *Assembler) { a.maxPayload = n }
}

// WithRegistry sets the element registry the assembler classifies and names
// elements through. The default is matroska.Default(), the built-in RFC 9559
// table.
//
// It is the extension path for a stream carrying vendor or private elements:
// registering an element as matroska.TypeMaster in a registry derived with
// matroska.NewRegistry(matroska.Default()) is what makes the cursor DESCEND into
// it, so its children become retained nodes instead of one opaque leaf, and the
// retained tree resolves Name, Describe and Type through the same registry. A nil
// registry is ignored.
func WithRegistry(reg *matroska.Registry) Option {
	return func(a *Assembler) {
		if reg != nil {
			a.reg = reg
		}
	}
}

// WithResync enables last-resort recovery from a STRUCTURAL error and reports
// every recovery to notify.
//
// WITHOUT it -- the default -- a structural error is terminal: Feed returns it and
// every later call returns it again, because a cursor that cannot locate the next
// element cannot honestly continue. WITH it, the assembler treats such an error as
// data loss rather than a verdict: it scans the bytes forward for the next
// top-level element ID (an EBML header or a Segment), drops everything before it,
// resets all Segment-scoped state, and resumes parsing there. Each recovery calls
// notify(offset, skipped, cause) exactly once, with the absolute offset parsing
// resumed at, how many bytes were discarded, and the error that triggered it, so
// the loss is visible to the caller rather than silent.
//
// RECOVERY IS FOR STRUCTURAL FAILURES ONLY -- exactly those for which
// parser.IsStructural reports true, the failures after which the cursor can no
// longer locate the next element. That single predicate is the whole gate: the
// core owns the classification, including the rule that a handler-originated
// failure is never structural. A CONTENT error is not one of them: when this
// assembler cannot decode a SimpleBlock payload, or any other handler error
// occurs, the stream's shape was read correctly and the error is the verdict on
// its content. Such an error is returned from Feed unchanged, no matter what this
// option is set to; it does not scan a single byte, does not reset Segment-scoped
// state, and does not call notify. It is terminal, like every error without this
// option, and later calls report it again.
//
// BYTE SCANNING HAPPENS ONLY HERE, AND ONLY AFTER A HARD FAILURE. The happy path
// never scans for the EBML magic: fragment boundaries are found structurally, from
// the element that opens the next top-level part, which is exactly why PCM
// containing those four magic bytes cannot split a fragment. Recovery has no such
// guarantee -- a magic-looking byte sequence inside media data can be mistaken for
// a fragment start -- so this is a way to keep a long-lived live stream alive after
// corruption, not a parsing strategy.
//
// Enabling it makes the assembler retain the bytes it has fed but not yet
// consumed, so that recovery has something to scan; a nil notify disables
// recovery again.
func WithResync(notify func(offset int64, skipped int64, cause error)) Option {
	return func(a *Assembler) { a.notify = notify }
}

// Assembler drives the streaming EBML cursor over a continuous Matroska byte
// stream and assembles one Fragment per completed Cluster.
//
// Usage is push-based: feed arbitrary []byte chunks with Feed, which returns the
// Fragments that completed within that chunk, then call Finalize once at EOF to
// close the trailing unknown-size Segment and surface any structural error. The
// result is split-invariant: the sequence of Fragments, and their contents, is
// identical however the input bytes are chunked across Feed calls.
//
// It is a parser.Handler driven by a parser.Scanner, and nothing more: every
// decision it makes -- descend into this master, decline that subtree, deliver
// this payload, skip that one -- is a decision the core event API offers any
// consumer, which is why this package needs no parsing of its own. An Assembler
// is not safe for concurrent use.
type Assembler struct {
	reg        *matroska.Registry
	maxPayload int
	notify     func(offset int64, skipped int64, cause error)

	// sc is the cursor being driven; nil only while a resync is pending.
	sc *parser.Scanner

	// open holds one entry per master the scanner descended into, outermost
	// first: the retained element, or nil for a master whose subtree is not
	// retained (anything outside a Segment). It mirrors the scanner's own open
	// masters, so a Close event always pops exactly one entry.
	open []*tree.Element

	// Segment-scoped state, reset whenever a Segment is entered or closed.
	segment *tree.Element
	cluster *tree.Element
	blocks  []*parser.SimpleBlock

	// pending is the retained leaf awaiting its payload, set on the leaf's header
	// and consumed by the payload event that follows it.
	pending *tree.Element

	// emitted collects the Fragments completed during the current call.
	emitted []*Fragment

	// Resync state; only lastResync is set before the first failure.
	resyncing  bool
	cause      error
	failOffset int64 // offset the failing cursor came to rest at
	lastResync int64 // last offset parsing resumed at (-1: none), so recovery must advance
	tail       []byte
	tailStart  int64 // absolute offset of tail[0]

	finalized bool
}

// New returns an Assembler for a continuous Matroska stream. Without options it
// classifies elements through the built-in RFC 9559 registry, retains leaf
// payloads up to DefaultMaxRetainedPayload, and treats a structural error as
// terminal.
func New(opts ...Option) *Assembler {
	a := &Assembler{
		reg:        matroska.Default(),
		maxPayload: DefaultMaxRetainedPayload,
		// No offset has been resumed at yet, and offset 0 is a legitimate one.
		lastResync: -1,
	}
	for _, opt := range opts {
		if opt != nil {
			opt(a)
		}
	}
	a.sc = a.newScanner(0)
	return a
}

// newScanner builds a cursor over this assembler that reports offsets counted
// from startOffset, which is 0 for the initial scan and the resume point after a
// recovery.
func (a *Assembler) newScanner(startOffset int64) *parser.Scanner {
	return parser.NewScanner(handler{a},
		a.reg.KindForElementID,
		parser.WithStartOffset(startOffset),
	)
}

// Feed pushes a chunk of the stream into the assembler and returns every Fragment
// whose Cluster completed while consuming it. The returned slice is freshly
// allocated per call (nil when nothing completed) and owned by the caller.
//
// An error is returned together with the Fragments that had completed before it,
// so a malformed tail never discards a good prefix. Every error is terminal and
// reported again by every later call, with one exception: with WithResync set, a
// structural error (parser.IsStructural is true) is recovered from instead of
// returned. A content error -- a SimpleBlock that will not decode -- is returned
// even then; see WithResync.
func (a *Assembler) Feed(chunk []byte) ([]*Fragment, error) {
	a.emitted = nil
	if a.finalized {
		return nil, parser.Invalid{Msg: "assembler already finalized"}
	}
	if a.resyncing {
		a.tail = append(a.tail, chunk...)
		return a.emitted, a.resume()
	}
	if err := a.sc.Feed(chunk); err != nil {
		if a.notify == nil || !parser.IsStructural(err) {
			return a.emitted, err
		}
		a.recordFailure(err)
		if err := a.resume(); err != nil {
			return a.emitted, err
		}
	}
	return a.emitted, nil
}

// Finalize closes the stream at EOF: it flushes what the buffered bytes complete,
// then closes the trailing unknown-size Segment. It returns any Fragments that
// completed during finalization -- normally none, since a known-size Cluster emits
// as soon as its bytes are in -- and a structural error if EOF arrived inside an
// element.
//
// Recovery does not apply at EOF, there being no further bytes to resynchronize
// on: when a resync was still pending, the trailing bytes are discarded and
// Finalize reports the error that started it.
func (a *Assembler) Finalize() ([]*Fragment, error) {
	a.emitted = nil
	if a.finalized {
		return nil, nil
	}
	a.finalized = true
	if a.resyncing {
		cause := a.cause
		a.tail, a.cause, a.resyncing = nil, nil, false
		return a.emitted, cause
	}
	if err := a.sc.Finalize(); err != nil {
		return a.emitted, err
	}
	return a.emitted, nil
}

// ---- Recovery (only reached when WithResync is set) ----

// recordFailure abandons the failed cursor, keeping the bytes it had not consumed
// so recovery has something to scan forward through.
func (a *Assembler) recordFailure(cause error) {
	a.failOffset = a.sc.Offset()
	a.tail = a.sc.Unconsumed()
	a.tailStart = a.failOffset
	a.cause = cause
	a.resyncing = true
	a.sc = nil
}

// resume looks for the next top-level element in the retained bytes and restarts
// the scan there, repeating if the restarted scan fails structurally again. It
// returns with a resync still pending, and no error, when no boundary is in the
// bytes seen so far.
//
// It returns an error only when the restarted scan failed in a way recovery must
// not act on -- a handler/content error -- which the caller then reports as it
// stands.
func (a *Assembler) resume() error {
	for a.resyncing {
		at, ok := a.findBoundary()
		if !ok {
			a.compactTail()
			return nil
		}
		cause, skipped := a.cause, at-a.failOffset
		pending := append([]byte(nil), a.tail[at-a.tailStart:]...)

		a.resetSegment()
		a.open = nil
		a.pending = nil
		a.tail, a.tailStart = nil, 0
		a.cause, a.resyncing = nil, false
		a.lastResync = at
		a.sc = a.newScanner(at)

		a.notify(at, skipped, cause)
		if err := a.sc.Feed(pending); err != nil {
			if !parser.IsStructural(err) {
				return err
			}
			a.recordFailure(err)
		}
	}
	return nil
}

// topLevelIDBytes are the encoded IDs that begin a new top-level document part.
// They are the only byte patterns recovery ever searches for.
var topLevelIDBytes = [][]byte{
	{0x1A, 0x45, 0xDF, 0xA3}, // EBML header
	{0x18, 0x53, 0x80, 0x67}, // Segment
}

// findBoundary returns the absolute offset of the first top-level element ID in
// the retained bytes at or after the point recovery may resume from, which is
// past the last resume point so that every recovery makes progress.
func (a *Assembler) findBoundary() (int64, bool) {
	from := a.failOffset
	if from <= a.lastResync {
		from = a.lastResync + 1
	}
	start := from - a.tailStart
	if start < 0 {
		start = 0
	}
	if start > int64(len(a.tail)) {
		return 0, false
	}
	found := int64(-1)
	for _, id := range topLevelIDBytes {
		if i := bytes.Index(a.tail[start:], id); i >= 0 {
			if at := start + int64(i); found < 0 || at < found {
				found = at
			}
		}
	}
	if found < 0 {
		return 0, false
	}
	return a.tailStart + found, true
}

// compactTail drops the retained bytes that can no longer begin a top-level
// element ID, keeping the last three in case one straddles the next Feed.
func (a *Assembler) compactTail() {
	const keep = 3 // longest prefix of a 4-byte ID that could still complete
	if len(a.tail) <= keep {
		return
	}
	drop := len(a.tail) - keep
	a.tail = append([]byte(nil), a.tail[drop:]...)
	a.tailStart += int64(drop)
}

// ---- parser.Handler ----

// handler adapts an Assembler to parser.Handler and parser.BoundaryDecider. It is
// a separate type so that the event methods -- which only the Scanner may call --
// stay off the Assembler's own API, where Feed and Finalize are the whole
// interface.
type handler struct{ a *Assembler }

func (h handler) Master(n parser.Node) (parser.Action, error) { return h.a.master(n) }

func (h handler) Leaf(n parser.Node) (parser.Action, error) { return h.a.leaf(n) }

func (h handler) Payload(n parser.Node, payload []byte) error { return h.a.payload(n, payload) }

func (h handler) Close(n parser.Node) error { return h.a.closeMaster(n) }

func (h handler) Boundary(open parser.Node, next parser.Node) bool { return h.a.boundary(open, next) }

// boundary implements parser.BoundaryDecider: an unknown-size Segment ends where
// the next top-level element begins. This is the whole fragment-boundary rule,
// and it is structural -- it fires on an element header the cursor parsed, never
// on a byte pattern found by scanning -- so media data containing the EBML magic
// cannot split a fragment.
func (a *Assembler) boundary(open parser.Node, next parser.Node) bool {
	return next.ID == matroska.IDEBML || next.ID == matroska.IDSegment
}

// master decides what happens to a master element. Everything inside a Segment is
// descended into and retained; everything outside one is declined, since no
// Fragment could hold it.
func (a *Assembler) master(n parser.Node) (parser.Action, error) {
	if n.ID == matroska.IDSegment && a.segment == nil {
		a.segment = a.newElement(n)
		a.segment.SetRegistry(a.reg)
		a.push(a.segment)
		return parser.Descend, nil
	}
	if a.segment == nil {
		// Outside a Segment there is nothing to retain: the EBML header and any
		// other top-level master are declined whole. An unknown-size one cannot be
		// skipped by size, so it is entered and its children are ignored.
		if n.Size == parser.UnknownSize {
			a.push(nil)
			return parser.Descend, nil
		}
		return parser.SkipSubtree, nil
	}

	el := a.newElement(n)
	if parent := a.top(); n.ID == matroska.IDCluster && parent == a.segment {
		a.cluster, a.blocks = el, nil
		el.SetRegistry(a.reg)
	} else {
		parent.AppendChild(el)
	}
	a.push(el)
	return parser.Descend, nil
}

// leaf decides what happens to a leaf element. Every leaf inside a Segment is
// retained by ID -- there is no allowlist, so an element no registry knows is
// retained like any other -- while its PAYLOAD is delivered only when it is worth
// holding: a SimpleBlock's bytes are decoded into Blocks, and a payload over the
// retention cap is elided.
func (a *Assembler) leaf(n parser.Node) (parser.Action, error) {
	if a.segment == nil {
		return parser.SkipPayload, nil
	}
	parent := a.top()
	if parent == nil {
		return parser.SkipPayload, nil
	}
	el := a.newElement(n)
	parent.AppendChild(el)

	if n.ID == matroska.IDSimpleBlock && a.cluster != nil {
		// The payload is needed to decode the block, but not to retain: the
		// decoded frames are the fragment's copy of it.
		el.Truncated = true
		return parser.ReadPayload, nil
	}
	if a.maxPayload >= 0 && n.Size > int64(a.maxPayload) {
		el.Truncated = true
		return parser.SkipPayload, nil
	}
	a.pending = el
	return parser.ReadPayload, nil
}

// payload receives a leaf payload the handler asked for: a SimpleBlock is decoded
// into Blocks, anything else is copied into the retained element.
func (a *Assembler) payload(n parser.Node, payload []byte) error {
	el := a.pending
	a.pending = nil

	// The slice is valid only for this call, so the bytes are copied before
	// anything keeps them -- decoded frames alias the payload they came from.
	buf := append([]byte(nil), payload...)

	if n.ID == matroska.IDSimpleBlock && a.cluster != nil {
		block, err := parser.ParseSimpleBlock(buf)
		if err != nil {
			return fmt.Errorf("SimpleBlock at offset %d: %w", n.Offset, err)
		}
		a.blocks = append(a.blocks, block)
		return nil
	}
	if el != nil {
		el.Payload = buf
	}
	return nil
}

// closeMaster reacts to a master's end. A Cluster's end is where a Fragment is emitted
// -- the early-emission point, reached as soon as the Cluster's declared size is
// consumed -- and a Segment's end is where all Segment-scoped state is dropped, so
// nothing leaks into the next Segment.
func (a *Assembler) closeMaster(n parser.Node) error {
	el, err := a.pop(n)
	if err != nil {
		return err
	}
	switch {
	case el != nil && el == a.cluster:
		a.emitted = append(a.emitted, &Fragment{
			Segment: a.segment,
			Cluster: a.cluster,
			Blocks:  a.blocks,
		})
		a.cluster, a.blocks = nil, nil
	case el != nil && el == a.segment:
		a.resetSegment()
	}
	return nil
}

// ---- retention bookkeeping ----

func (a *Assembler) newElement(n parser.Node) *tree.Element {
	return &tree.Element{
		ID:        n.ID,
		Offset:    n.Offset,
		HeaderLen: n.HeaderLen,
		Size:      n.Size,
	}
}

func (a *Assembler) push(el *tree.Element) {
	a.open = append(a.open, el)
}

// pop removes the entry for the master that just closed, checking it against the
// closing element so a bookkeeping slip is a clear error and not a silently
// misplaced subtree.
func (a *Assembler) pop(n parser.Node) (*tree.Element, error) {
	if len(a.open) == 0 {
		return nil, fmt.Errorf("close of %s with no open master", n.ID)
	}
	el := a.open[len(a.open)-1]
	a.open = a.open[:len(a.open)-1]
	if el != nil && el.ID != n.ID {
		return nil, fmt.Errorf("close of %s does not match retained master %s", n.ID, el.ID)
	}
	return el, nil
}

// top returns the retained element the next child belongs to, or nil when the
// enclosing master is not retained.
func (a *Assembler) top() *tree.Element {
	if len(a.open) == 0 {
		return nil
	}
	return a.open[len(a.open)-1]
}

// resetSegment drops everything scoped to one Segment. Fragments already emitted
// keep their own references, so dropping these cannot affect them.
func (a *Assembler) resetSegment() {
	a.segment, a.cluster, a.blocks = nil, nil, nil
}
