package fragment

import (
	"bytes"
	"errors"
	"fmt"
	"io"

	"github.com/yacchi/ebml/ext/tree"
	"github.com/yacchi/ebml/matroska"
	"github.com/yacchi/ebml/parser"
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

// WithSkipContentErrors makes a CONTENT error survivable: the offending element is
// dropped, notify is told which one it was, and assembly carries on.
//
// WITHOUT it -- the default -- a content error is terminal: Feed returns it and
// every later call returns it again. WITH it, the assembler treats such an error as
// one unusable ELEMENT rather than a verdict on the stream: it discards what that
// element would have contributed -- an undecodable SimpleBlock does not become one
// of Fragment.Blocks -- calls notify(id, offset, err) exactly once with the
// element's ID, its absolute offset in the stream and the *parser.ContentError that
// Feed would otherwise have returned, and keeps going. The enclosing Fragment is
// NOT lost: it is emitted at its Cluster's end with the blocks that did decode. A
// consumer that would rather discard the whole fragment still can, because notify
// says which fragment it was -- the offset falls inside it -- so nothing is decided
// here that the caller cannot undo. The element also stays in the retained tree,
// with Truncated set like every SimpleBlock, so the fragment's SHAPE still accounts
// for the bytes its blocks no longer cover.
//
// NOTHING IS SCANNED AND NOTHING IS RESET. A content error means the stream's shape
// was read correctly and only the payload's MEANING was refused, so the cursor's
// position is exactly as good afterwards as before: the next element's header is
// where it always was. That is the whole reason the offending element can be dropped
// on its own -- no byte scanning, no lost fragment, no Segment-scoped state thrown
// away.
//
// DIVISION OF LABOUR WITH WithResync. The two options cover the two error classes,
// and neither touches the other's:
//
//   - WithResync answers a STRUCTURAL failure (parser.IsStructural is true), where
//     the cursor can no longer locate the next element, by scanning forward for the
//     next top-level element ID and losing everything up to it.
//   - WithSkipContentErrors answers a CONTENT failure (*parser.ContentError), where
//     the structure is intact, by dropping one element and losing nothing else.
//
// Setting this option changes NOTHING about a structural error: it is still
// terminal unless WithResync is also set, and it is WithResync's notify that hears
// about it, never this one. Setting WithResync changes nothing about a content
// error either.
//
// A nil notify disables skipping again, exactly as it does for WithResync, so this
// option can never make a failure disappear quietly: either notify hears about every
// dropped element, or the content error is returned as it is by default. There is no
// configuration in which the assembler swallows one.
func WithSkipContentErrors(notify func(id parser.ElementID, offset int64, cause error)) Option {
	return func(a *Assembler) { a.skipContent = notify }
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
// core owns the classification, including the rule that a content verdict is never
// structural. A CONTENT error is not one of them: when this assembler cannot decode
// a SimpleBlock payload it reports a parser.ContentError, because the stream's
// shape was read correctly and the error is the verdict on what the bytes MEAN.
// Such an error is returned from Feed unchanged, no matter what this option is set
// to; it does not scan a single byte, does not reset Segment-scoped state, and does
// not call THIS notify.
//
// That class has an option of its own, WithSkipContentErrors, and the two divide the
// work by error class: this one recovers from structural damage by scanning forward
// and losing the bytes in between, while that one drops the single offending element
// and keeps the fragment, precisely because a content error leaves the structural
// position intact. Without WithSkipContentErrors a content error stays terminal,
// like every error without this option, and later calls report it again.
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
// Inside, it is a plain pull loop over a parser.Cursor and nothing more: every
// decision it makes -- descend into this master, decline that subtree, take this
// payload, leave that one unread -- is a decision the core offers any consumer on
// the node it just pulled, which is why this package needs no parsing of its own.
// An Assembler is not safe for concurrent use.
type Assembler struct {
	reg         *matroska.Registry
	maxPayload  int
	notify      func(offset int64, skipped int64, cause error)
	skipContent func(id parser.ElementID, offset int64, cause error)

	// c is the cursor being pulled; nil only while a resync is pending.
	c *parser.Cursor

	// open holds one entry per master the cursor descended into, outermost
	// first: the retained element, or nil for a master whose subtree is not
	// retained (anything outside a Segment). It mirrors the cursor's own open
	// masters, so an EndNode always pops exactly one entry.
	open []*tree.Element

	// Segment-scoped state, reset whenever a Segment is entered or closed.
	segment *tree.Element
	cluster *tree.Element
	blocks  []*parser.SimpleBlock

	// leaf is the node whose payload the assembler asked for and whose bytes have
	// not all arrived, with leafEl the element that will retain them (nil for a
	// SimpleBlock, which is decoded rather than retained). A cursor node stays
	// valid across Feed, so the read is simply retried when the next chunk lands
	// -- the leaf is never re-decided.
	leaf   *parser.LeafNode
	leafEl *tree.Element

	// emitted collects the Fragments completed during the current call.
	emitted []*Fragment

	// Resync state; only lastResync is set before the first failure.
	resyncing  bool
	cause      error
	failOffset int64 // offset the failing cursor came to rest at
	lastResync int64 // last offset parsing resumed at (-1: none), so recovery must advance
	tail       []byte
	tailStart  int64 // absolute offset of tail[0]

	err       error // latched terminal failure
	finalized bool
}

// New returns an Assembler for a continuous Matroska stream. Without options it
// classifies elements through the built-in RFC 9559 registry, retains leaf
// payloads up to DefaultMaxRetainedPayload, and treats EVERY error as terminal --
// structural (see WithResync) and content (see WithSkipContentErrors) alike.
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
	a.c = a.newCursor(0)
	return a
}

// newCursor builds a cursor over this stream that reports offsets counted from
// startOffset, which is 0 for the initial scan and the resume point after a
// recovery.
func (a *Assembler) newCursor(startOffset int64) *parser.Cursor {
	return parser.NewCursor(a.reg.KindForElementID,
		parser.WithStartOffset(startOffset),
		parser.WithBoundary(segmentBoundary),
	)
}

// segmentBoundary is the whole fragment-boundary rule: a new top-level document
// ends an unknown-size Segment, and the RFC 9559 containment rule ends an
// unknown-size Cluster at the first element that cannot be its child. It is
// structural -- it answers about an element header the cursor parsed, never
// about a byte pattern found by scanning -- so media data containing the EBML
// magic cannot split a fragment.
func segmentBoundary(open, next parser.ElementID) bool {
	if next == matroska.IDEBML || next == matroska.IDSegment {
		return true
	}
	return matroska.EndsUnknownSizeMaster(open, next)
}

// Feed pushes a chunk of the stream into the assembler and returns every Fragment
// whose Cluster completed while consuming it. The returned slice is freshly
// allocated per call (nil when nothing completed) and owned by the caller.
//
// An error is returned together with the Fragments that had completed before it,
// so a malformed tail never discards a good prefix. Every error is terminal and
// reported again by every later call, unless the option for its class says
// otherwise: with WithResync set, a structural error (parser.IsStructural is true)
// is recovered from instead of returned, and with WithSkipContentErrors set, a
// content error -- a SimpleBlock that will not decode -- drops that element and is
// reported to its notify instead of returned. Each option covers only its own
// class; see WithResync.
func (a *Assembler) Feed(chunk []byte) ([]*Fragment, error) {
	a.emitted = nil
	// The latched failure is consulted FIRST, and in every entry point. A Finalize
	// that failed latches the failure and marks the assembler finalized, so testing
	// the finalized flag first would answer that Feed with a fresh invalid-use error
	// and lose the diagnosis the caller is owed: once a terminal failure is latched,
	// every later call reports THAT failure. "Already finalized" is reserved for the
	// other case -- Finalize succeeded and the caller fed again -- which is genuine
	// invalid use and not a latched failure.
	if a.err != nil {
		return nil, a.err
	}
	if a.finalized {
		return nil, parser.Invalid{Msg: "assembler already finalized"}
	}
	if a.resyncing {
		a.tail = append(a.tail, chunk...)
		return a.emitted, a.resume()
	}
	a.c.Feed(chunk)
	if err := a.drain(); err != nil {
		if a.notify == nil || !parser.IsStructural(err) {
			return a.emitted, a.fail(err)
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
	if a.err != nil {
		return nil, a.err
	}
	if a.finalized {
		return nil, nil
	}
	a.finalized = true
	if a.resyncing {
		cause := a.cause
		a.tail, a.cause, a.resyncing = nil, nil, false
		return a.emitted, cause
	}
	// A payload still outstanding here is a stream that ended inside an element,
	// which the cursor's own Finalize is what diagnoses; the node must not be
	// retried afterwards, since Finalize invalidates it.
	a.leaf, a.leafEl = nil, nil
	if err := a.c.Finalize(); err != nil {
		return a.emitted, a.fail(err)
	}
	// Finalize keeps whatever the buffered bytes still complete, so the masters it
	// closed -- the trailing Segment above all -- are reported by these pulls.
	if err := a.drain(); err != nil {
		return a.emitted, a.fail(err)
	}
	return a.emitted, nil
}

// fail latches a terminal error so every later call reports it again.
func (a *Assembler) fail(err error) error {
	a.err = err
	return err
}

// ---- the pull loop ----

// drain pulls events until the bytes fed are exhausted (or the stream is over),
// which is what makes assembly split-invariant: the loop is driven by the events
// the cursor can report, never by how much of a chunk happens to be left.
func (a *Assembler) drain() error {
	for {
		// A payload whose bytes had not all arrived is retried first: the node
		// survived the Feed, so the decision taken on its header still stands.
		if a.leaf != nil {
			done, err := a.readPayload()
			if err != nil {
				return err
			}
			if !done {
				return nil
			}
		}

		node, err := a.c.Next()
		if err != nil {
			switch {
			case isNeedMoreData(err):
				return nil // the answer is the next Feed
			case errors.Is(err, io.EOF):
				return nil // the stream is over and everything has been reported
			default:
				return err // structural: parser.IsStructural(err) is true
			}
		}

		switch n := node.(type) {
		case *parser.MasterNode:
			a.master(n)
		case *parser.LeafNode:
			a.leafHeader(n)
		case *parser.EndNode:
			if err := a.end(n); err != nil {
				return err
			}
		}
	}
}

// master decides what happens to a master element. Everything inside a Segment is
// descended into and retained; everything outside one is declined, since no
// Fragment could hold it.
func (a *Assembler) master(n *parser.MasterNode) {
	if n.ID() == matroska.IDSegment && a.segment == nil {
		a.segment = newElement(n)
		a.segment.SetRegistry(a.reg)
		a.push(a.segment)
		n.Descend()
		return
	}
	if a.segment == nil {
		// Outside a Segment there is nothing to retain: the EBML header and any
		// other top-level master are declined whole. An unknown-size one has no
		// locatable end, so it is entered and its children are ignored.
		if n.Size() == parser.UnknownSize {
			a.push(nil)
			n.Descend()
			return
		}
		n.Skip()
		return
	}

	el := newElement(n)
	if parent := a.top(); n.ID() == matroska.IDCluster && parent == a.segment {
		a.cluster, a.blocks = el, nil
		el.SetRegistry(a.reg)
	} else {
		parent.AppendChild(el)
	}
	a.push(el)
	n.Descend()
}

// leafHeader decides what happens to a leaf element. Every leaf inside a Segment
// is retained by ID -- there is no allowlist, so an element no registry knows is
// retained like any other -- while its PAYLOAD is asked for only when it is worth
// holding: a SimpleBlock's bytes are decoded into Blocks, and a payload over the
// retention cap is left unread. Anything the assembler does ask for is recorded in
// a.leaf and taken by readPayload, which may have to wait for the bytes.
func (a *Assembler) leafHeader(n *parser.LeafNode) {
	if a.segment == nil {
		n.Skip()
		return
	}
	parent := a.top()
	if parent == nil {
		n.Skip()
		return
	}
	el := newElement(n)
	parent.AppendChild(el)

	if n.ID() == matroska.IDSimpleBlock && a.cluster != nil {
		// The payload is needed to decode the block, but not to retain: the
		// decoded frames are the fragment's copy of it.
		el.Truncated = true
		a.leaf, a.leafEl = n, nil
		return
	}
	if a.maxPayload >= 0 && n.Size() > int64(a.maxPayload) {
		el.Truncated = true
		n.Skip()
		return
	}
	a.leaf, a.leafEl = n, el
}

// readPayload takes the payload of the leaf the assembler asked for: a SimpleBlock
// is decoded into Blocks, anything else becomes the retained element's bytes. It
// reports whether the payload arrived; false means the bytes are not all in yet and
// the next Feed must retry.
func (a *Assembler) readPayload() (bool, error) {
	node := a.leaf
	payload, err := node.Payload()
	if err != nil {
		if isNeedMoreData(err) {
			return false, nil
		}
		return false, err
	}
	el := a.leafEl
	a.leaf, a.leafEl = nil, nil

	// Payload hands over a VIEW of the cursor's buffer, valid only until the next
	// Next, and the assembler retains what it takes -- in the decoded frames or in
	// the retained element -- so this is where it becomes the assembler's own copy.
	// Exactly one copy is made, and only for a payload that was asked for.
	payload = bytes.Clone(payload)
	if node.ID() == matroska.IDSimpleBlock && a.cluster != nil {
		block, err := parser.ParseSimpleBlock(payload)
		if err != nil {
			// The shape of the stream was read correctly and these bytes will not
			// decode, which is a verdict about CONTENT: never structural, so
			// resynchronization must not act on it.
			cause := parser.NewContentError(node.ID(), node.Offset(),
				fmt.Errorf("SimpleBlock: %w", err))
			if a.skipContent == nil {
				return false, cause
			}
			// The payload was delivered in full, so the cursor still stands exactly
			// where the next element begins: dropping this one block costs nothing
			// but the block, and the Fragment is emitted with the rest.
			a.skipContent(node.ID(), node.Offset(), cause)
			return true, nil
		}
		a.blocks = append(a.blocks, block)
		return true, nil
	}
	if el != nil {
		el.Payload = payload
	}
	return true, nil
}

// end reacts to a master's end. A Cluster's end is where a Fragment is emitted --
// the early-emission point, reached as soon as the Cluster's declared size is
// consumed -- and a Segment's end is where all Segment-scoped state is dropped, so
// nothing leaks into the next Segment.
func (a *Assembler) end(n *parser.EndNode) error {
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

func isNeedMoreData(err error) bool {
	var needMore parser.NeedMoreData
	return errors.As(err, &needMore)
}

// ---- Recovery (only reached when WithResync is set) ----

// recordFailure abandons the failed cursor, keeping the bytes it had not consumed
// so recovery has something to scan forward through.
func (a *Assembler) recordFailure(cause error) {
	a.failOffset = a.c.Offset()
	a.tail = a.c.Unconsumed()
	a.tailStart = a.failOffset
	a.cause = cause
	a.resyncing = true
	a.c = nil
}

// resume looks for the next top-level element in the retained bytes and restarts
// the scan there, repeating if the restarted scan fails structurally again. It
// returns with a resync still pending, and no error, when no boundary is in the
// bytes seen so far.
//
// It returns an error only when the restarted scan failed in a way recovery must
// not act on -- a content error -- which the caller then reports as it stands.
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
		a.leaf, a.leafEl = nil, nil
		a.tail, a.tailStart = nil, 0
		a.cause, a.resyncing = nil, false
		a.lastResync = at
		a.c = a.newCursor(at)

		a.notify(at, skipped, cause)
		a.c.Feed(pending)
		if err := a.drain(); err != nil {
			if !parser.IsStructural(err) {
				return a.fail(err)
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

// ---- retention bookkeeping ----

// newElement retains the identity and extent of the node the cursor just reported.
// Only these four fields are copied, because a node is valid solely until the next
// pull.
func newElement(n parser.Node) *tree.Element {
	return &tree.Element{
		ID:        n.ID(),
		Offset:    n.Offset(),
		HeaderLen: n.HeaderLen(),
		Size:      n.Size(),
	}
}

func (a *Assembler) push(el *tree.Element) {
	a.open = append(a.open, el)
}

// pop removes the entry for the master that just closed, checking it against the
// closing element so a bookkeeping slip is a clear error and not a silently
// misplaced subtree.
func (a *Assembler) pop(n *parser.EndNode) (*tree.Element, error) {
	if len(a.open) == 0 {
		return nil, fmt.Errorf("close of %s with no open master", n.ID())
	}
	el := a.open[len(a.open)-1]
	a.open = a.open[:len(a.open)-1]
	if el != nil && el.ID != n.ID() {
		return nil, fmt.Errorf("close of %s does not match retained master %s", n.ID(), el.ID)
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
