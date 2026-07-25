package parser

import "fmt"

// Action is a handler's decision about the element it was just shown.
//
// Master elements are answered with Descend or SkipSubtree, leaf elements with
// ReadPayload or SkipPayload. The zero Action is invalid: a handler must always
// state what it wants, so nothing is retained or discarded by accident.
type Action int

const (
	// Descend enters a master element, so its children are reported as events.
	Descend Action = iota + 1
	// SkipSubtree consumes a master element's whole payload without reporting a
	// single descendant event (and without a Close for the skipped master).
	SkipSubtree
	// ReadPayload delivers a leaf element's payload to Handler.Payload.
	ReadPayload
	// SkipPayload consumes a leaf element's payload without delivering it, so
	// bulk data (e.g. PCM in a SimpleBlock) never has to be held in memory.
	SkipPayload
)

func (a Action) String() string {
	switch a {
	case Descend:
		return "descend"
	case SkipSubtree:
		return "skip_subtree"
	case ReadPayload:
		return "read_payload"
	case SkipPayload:
		return "skip_payload"
	default:
		return fmt.Sprintf("action(%d)", int(a))
	}
}

// Node is the read-only context of one scan event: an element's identity and
// extent plus the masters enclosing it. It is passed by value and holds no
// payload bytes, so a handler can judge an element together with its ancestry
// without retaining anything.
//
// A Node carries no element name: naming is the registry's job (package
// matroska), which keeps this layer free of element knowledge.
type Node struct {
	ID        ElementID
	Kind      Kind
	Depth     int // number of enclosing masters; 0 at the top level
	Offset    int64
	HeaderLen int
	Size      int64 // UnknownSize when the element declares an unknown size
	// End is the absolute offset one past the element's last byte
	// (Offset+HeaderLen+Size), or UnknownSize when Size is unknown. On a Close
	// event End is the concrete offset at which the master actually closed, even
	// for an unknown-size master.
	End int64

	openMasters []ElementID
}

// OpenMasters returns the IDs of the masters enclosing this node, outermost
// first (so its length equals Depth). The returned slice is a copy owned by the
// caller, and the ancestry a Node reports never changes, so a Node kept beyond
// its event still describes the element it was reported for.
func (n Node) OpenMasters() []ElementID {
	if len(n.openMasters) == 0 {
		return nil
	}
	out := make([]ElementID, len(n.openMasters))
	copy(out, n.openMasters)
	return out
}

// Handler receives the scan events of a Scanner.
//
// Master and Leaf are called on the element's HEADER — before any payload byte
// need have arrived — and their Action decides what happens to the element's
// payload. Deciding on the header is what keeps a scan split-invariant: the
// event sequence and every decision point are identical no matter how the input
// was chunked.
//
// Payload is called only for a leaf whose Leaf decision was ReadPayload, with
// the same Node. Close is called for every master that was descended into, once
// its end is reached, with Node.End filled in with the offset where it closed.
type Handler interface {
	Master(Node) (Action, error)
	Leaf(Node) (Action, error)
	// Payload receives a leaf's complete payload. The slice is valid only for
	// the duration of the call; a handler that keeps the bytes must copy them.
	Payload(Node, []byte) error
	Close(Node) error
}

// BoundaryDecider is the optional half of the Handler contract: it decides where
// an unknown-size master ends.
//
// EBML gives an unknown-size master no declared end, so the only thing that can
// establish one before EOF is the knowledge that some element cannot be its
// child -- and that is element schema, which this layer deliberately does not
// have (see KindClassifier). A Handler that also implements BoundaryDecider is
// therefore asked, on the header of every element that would otherwise be
// reported as a child of the innermost open unknown-size master, whether that
// master ends here instead.
//
// Reporting true closes the master -- its Close event fires with End at this
// offset, before the element's own event -- and the question is then repeated for
// the master enclosing it, so several nested unknown-size masters close outward
// in one step. Reporting false (and not implementing the interface at all) keeps
// the documented default: the master stays open until Finalize.
//
// This is what a stream of concatenated unknown-size masters needs. KVS GetMedia
// is exactly that -- one unknown-size Segment per fragment -- and its rule is
// that a Segment ends where the next top-level element begins:
//
//	func (h *fragments) Boundary(open, next parser.Node) bool {
//	    return next.ID == matroska.IDEBML || next.ID == matroska.IDSegment
//	}
//
// The decision is driven by element structure, never by scanning the bytes for
// the EBML magic, so payload data that happens to contain that magic cannot
// cause a spurious split.
type BoundaryDecider interface {
	Boundary(open Node, next Node) bool
}

// HandlerFuncs adapts plain functions to Handler. Every field is optional:
// a nil MasterFunc descends, a nil LeafFunc skips the payload, nil
// PayloadFunc/CloseFunc ignore the event, and a nil BoundaryFunc keeps every
// unknown-size master open until Finalize.
type HandlerFuncs struct {
	MasterFunc   func(Node) (Action, error)
	LeafFunc     func(Node) (Action, error)
	PayloadFunc  func(Node, []byte) error
	CloseFunc    func(Node) error
	BoundaryFunc func(open Node, next Node) bool
}

func (h HandlerFuncs) Master(n Node) (Action, error) {
	if h.MasterFunc == nil {
		return Descend, nil
	}
	return h.MasterFunc(n)
}

func (h HandlerFuncs) Leaf(n Node) (Action, error) {
	if h.LeafFunc == nil {
		return SkipPayload, nil
	}
	return h.LeafFunc(n)
}

func (h HandlerFuncs) Payload(n Node, payload []byte) error {
	if h.PayloadFunc == nil {
		return nil
	}
	return h.PayloadFunc(n, payload)
}

func (h HandlerFuncs) Close(n Node) error {
	if h.CloseFunc == nil {
		return nil
	}
	return h.CloseFunc(n)
}

// Boundary implements BoundaryDecider. A nil BoundaryFunc never ends an
// unknown-size master early.
func (h HandlerFuncs) Boundary(open Node, next Node) bool {
	if h.BoundaryFunc == nil {
		return false
	}
	return h.BoundaryFunc(open, next)
}

// Handler operation names, as reported by HandlerError.Op.
const (
	OpMaster  = "master"
	OpLeaf    = "leaf"
	OpPayload = "payload"
	OpClose   = "close"
)

// HandlerError reports that a Handler method returned an error, so the scan was
// aborted by the CONSUMER rather than by the bytes.
//
// It exists to make the origin of a failure decidable: Feed and Finalize return
// either a structural failure of the cursor or a handler error wrapped in this
// type. A caller tells them apart with IsStructural(err) for the first and
// errors.As(err, &handlerErr) for the second. That matters because the two call
// for opposite responses: a structural failure means the cursor can no longer
// locate elements, which is the only situation in which scanning bytes forward
// for a resume point is defensible, while a handler error is the consumer's own
// verdict about content (a payload that will not decode, say) and is terminal.
//
// An error carrying this wrapper is NEVER structural, whatever value the handler
// returned: IsStructural stops at this boundary, so a handler that returns
// ErrStructural, Invalid or TruncatedError verbatim is still reported as
// handler-originated. Unwrap reports the handler's own error, so errors.Is and
// errors.As still reach the handler's sentinels and types through the wrapper
// unchanged -- that reachability is deliberate, and only the classification
// question stops here.
//
// Op names the handler method that failed (OpMaster, OpLeaf, OpPayload, OpClose)
// and Node is the element it was called with, so the failure is locatable in the
// stream without the handler having to add that itself.
type HandlerError struct {
	Op   string
	Node Node
	Err  error
}

func (e *HandlerError) Error() string {
	return fmt.Sprintf("handler %s for element %s at offset %d: %v", e.Op, e.Node.ID, e.Node.Offset, e.Err)
}

func (e *HandlerError) Unwrap() error { return e.Err }

// handlerErr wraps a handler's error so its origin stays visible; a nil error
// stays nil.
func handlerErr(op string, n Node, err error) error {
	if err == nil {
		return nil
	}
	return &HandlerError{Op: op, Node: n, Err: err}
}

// pendingOp is the payload operation a decided element is waiting on. It exists
// because a decision is taken on the header, while the payload bytes may arrive
// in a later Feed: the handler is never consulted twice about one element.
type pendingOp int

const (
	opNone pendingOp = iota
	opSkipSubtree
	opReadPayload
	opSkipPayload
)

// Scanner drives the cursor over a byte stream and reports each element to a
// Handler, which decides on every element whether to descend/read it or to skip
// it. Retention is therefore entirely the consumer's policy: the Scanner itself
// keeps only the chain of open masters, never element payloads.
//
// A Scanner is not safe for concurrent use. A handler error aborts the scan and
// propagates out of Feed or Finalize; every later call returns that same error.
//
// Feed and Finalize return exactly two classes of error, and a caller is meant to
// treat them differently:
//
//   - A STRUCTURAL failure of the cursor, for which IsStructural is true: these
//     bytes cannot be read as EBML, so the next element's position is unknown.
//   - A HANDLER error, wrapped in *HandlerError and never structural: the stream
//     was readable and the consumer refused what it was shown.
//
// So IsStructural(err) asks "is the stream broken?" and
// errors.As(err, &he) with he a *HandlerError asks "did my handler stop this?".
// NeedMoreData is neither: the Scanner absorbs it, because the answer to it is
// simply the next Feed.
//
// An unknown-size master stays open until Finalize unless the Handler also
// implements BoundaryDecider: deciding that such a master ended because an
// element appeared that cannot be its child requires the element schema, which
// this layer deliberately does not have (see KindClassifier), so a consumer of a
// stream of concatenated unknown-size masters -- e.g. KVS GetMedia, whose
// fragments are consecutive unknown-size Segments -- supplies that rule as a
// BoundaryDecider.
type Scanner struct {
	p *Parser
	h Handler
	// boundary is h itself when it implements BoundaryDecider, nil otherwise.
	boundary BoundaryDecider

	open []Node // masters descended into, outermost first
	// path holds the IDs of the open masters, outermost first. It is never
	// modified once built: descending allocates a new slice, so the ancestry a
	// Node carries stays valid even if the handler keeps the Node.
	path []ElementID

	pending  pendingOp
	pendNode Node

	err  error
	done bool
}

// NewScanner returns a Scanner that reports events to h and classifies elements
// with classify, so a real Matroska stream is scanned with
// NewScanner(h, matroska.KindForElementID). Options are the Parser's own.
//
// The classifier is required and must not be nil, for the reasons given on New,
// which this constructor calls: NewScanner panics on a nil classify.
//
// When h also implements BoundaryDecider it is consulted about where an
// unknown-size master ends; otherwise such a master stays open until Finalize.
func NewScanner(h Handler, classify KindClassifier, opts ...Option) *Scanner {
	s := &Scanner{p: New(classify, opts...), h: h}
	if b, ok := h.(BoundaryDecider); ok {
		s.boundary = b
	}
	return s
}

// Offset reports the absolute offset the scan has reached.
func (s *Scanner) Offset() int64 { return s.p.Offset() }

// Unconsumed returns a copy of the bytes fed but not yet consumed, the first of
// which sits at absolute offset Offset(). See Parser.Unconsumed: it is there for
// a consumer that wants to resynchronize after a structural error, not for
// normal reading.
func (s *Scanner) Unconsumed() []byte { return s.p.Unconsumed() }

// Depth reports how many masters are currently open.
func (s *Scanner) Depth() int { return s.p.Depth() }

// Feed appends the next chunk of the stream and reports every event that the
// bytes now available complete. Chunk boundaries never affect the events.
//
// A returned error is either a structural cursor failure (IsStructural is true)
// or a *HandlerError from one of the handler's own methods; see Scanner.
func (s *Scanner) Feed(chunk []byte) error {
	if s.err != nil {
		return s.err
	}
	if s.done {
		return s.fail(Invalid{Msg: "scanner already finalized"})
	}
	s.p.Feed(chunk)
	return s.drain()
}

// Finalize reports end of input: it flushes what the buffered bytes complete,
// then closes the masters still open (an unknown-size master such as a KVS
// Segment is closed here). It reports TruncatedError if the stream ends inside
// an element.
//
// Its errors are classified exactly as Feed's: a structural cursor failure, for
// which IsStructural is true, or a *HandlerError raised by a Close event.
func (s *Scanner) Finalize() error {
	if s.err != nil {
		return s.err
	}
	if s.done {
		return nil
	}
	if err := s.drain(); err != nil {
		return err
	}
	closed, err := s.p.FinalizeEOF()
	if err != nil {
		return s.fail(err)
	}
	s.done = true
	for _, cm := range closed {
		if len(s.open) == 0 {
			return s.fail(Invalid{Msg: fmt.Sprintf("master %s closed at EOF was never entered", cm.ID)})
		}
		node := s.popMaster()
		if node.ID != cm.ID {
			return s.fail(Invalid{Msg: fmt.Sprintf("master stack mismatch at EOF: closing %s, scanner holds %s", cm.ID, node.ID)})
		}
		node.End = cm.End
		if err := s.h.Close(node); err != nil {
			return s.fail(handlerErr(OpClose, node, err))
		}
	}
	if len(s.open) != 0 {
		return s.fail(Invalid{Msg: "masters still open after finalize"})
	}
	return nil
}

func (s *Scanner) fail(err error) error {
	s.err = err
	return err
}

func (s *Scanner) drain() error {
	for {
		if s.pending != opNone {
			done, err := s.completePending()
			if err != nil {
				return s.fail(err)
			}
			if !done {
				return nil // waiting for payload bytes
			}
			continue
		}

		h, err := s.p.Peek()
		if err != nil {
			if _, ok := err.(NeedMoreData); ok {
				return nil
			}
			return s.fail(err)
		}

		if h.ID == 0 && h.Kind == KindEndMaster {
			if err := s.closeTop(); err != nil {
				return s.fail(err)
			}
			continue
		}
		if h.Kind == KindEndMaster {
			return s.fail(Invalid{Msg: fmt.Sprintf("classifier reported element %s as %s", h.ID, KindEndMaster)})
		}

		node := s.nodeFor(h)

		// An unknown-size master has no declared end, so only the handler's
		// element knowledge can end it before EOF. Asked on this element's
		// header, it may report that the master ends here; the element is then
		// re-examined at the outer depth, which also lets nested unknown-size
		// masters close outward one loop iteration at a time.
		if s.boundary != nil && len(s.open) > 0 {
			if open := s.open[len(s.open)-1]; open.Size == UnknownSize && s.boundary.Boundary(open, node) {
				if err := s.closeUnknownSizeTop(); err != nil {
					return s.fail(err)
				}
				continue
			}
		}

		var action Action
		op := OpLeaf
		if h.Kind == KindMaster {
			op = OpMaster
			action, err = s.h.Master(node)
		} else {
			action, err = s.h.Leaf(node)
		}
		if err != nil {
			return s.fail(handlerErr(op, node, err))
		}
		if err := checkAction(h.Kind, action); err != nil {
			return s.fail(err)
		}

		if _, err := s.p.ConsumeHeader(); err != nil {
			return s.fail(err)
		}

		switch action {
		case Descend:
			if err := s.p.EnterMaster(); err != nil {
				return s.fail(err)
			}
			s.pushMaster(node)
		case SkipSubtree:
			if node.Size == UnknownSize {
				return s.fail(Invalid{Msg: fmt.Sprintf("cannot skip subtree of unknown-size master %s", node.ID)})
			}
			s.pending, s.pendNode = opSkipSubtree, node
		case ReadPayload:
			s.pending, s.pendNode = opReadPayload, node
		case SkipPayload:
			s.pending, s.pendNode = opSkipPayload, node
		}
	}
}

// checkAction rejects a decision that does not fit the element it answers, so a
// mismatch is a clear error instead of undefined behaviour.
func checkAction(kind Kind, a Action) error {
	master := kind == KindMaster
	switch a {
	case Descend, SkipSubtree:
		if !master {
			return Invalid{Msg: fmt.Sprintf("action %s is only valid for a master element, got kind %s", a, kind)}
		}
	case ReadPayload, SkipPayload:
		if master {
			return Invalid{Msg: fmt.Sprintf("action %s is only valid for a leaf element, got kind %s", a, kind)}
		}
	default:
		return Invalid{Msg: fmt.Sprintf("handler returned %s", a)}
	}
	return nil
}

// completePending performs the decided payload operation. It reports whether the
// operation finished; false means the payload bytes have not all arrived yet.
func (s *Scanner) completePending() (bool, error) {
	switch s.pending {
	case opReadPayload:
		payload, err := s.p.ReadPayload()
		if err != nil {
			if _, ok := err.(NeedMoreData); ok {
				return false, nil
			}
			return false, err
		}
		node := s.pendNode
		s.pending, s.pendNode = opNone, Node{}
		if err := s.h.Payload(node, payload); err != nil {
			return false, handlerErr(OpPayload, node, err)
		}
	case opSkipPayload:
		if err := s.p.SkipPayload(); err != nil {
			if _, ok := err.(NeedMoreData); ok {
				return false, nil
			}
			return false, err
		}
		s.pending, s.pendNode = opNone, Node{}
	case opSkipSubtree:
		if err := s.p.SkipCurrentPayload(); err != nil {
			if _, ok := err.(NeedMoreData); ok {
				return false, nil
			}
			return false, err
		}
		s.pending, s.pendNode = opNone, Node{}
	}
	return true, nil
}

func (s *Scanner) closeTop() error {
	if len(s.open) == 0 {
		return Invalid{Msg: "master end reached with no open master"}
	}
	if err := s.p.LeaveMaster(); err != nil {
		return err
	}
	node := s.popMaster()
	node.End = s.p.Offset()
	return handlerErr(OpClose, node, s.h.Close(node))
}

// closeUnknownSizeTop closes the innermost open master at the current offset
// because the handler reported a boundary there. The master declares no end, so
// this is the explicit close LeaveMaster refuses to perform.
func (s *Scanner) closeUnknownSizeTop() error {
	if err := s.p.CloseMaster(); err != nil {
		return err
	}
	node := s.popMaster()
	node.End = s.p.Offset()
	return handlerErr(OpClose, node, s.h.Close(node))
}

func (s *Scanner) pushMaster(node Node) {
	s.open = append(s.open, node)
	// Build the deeper ancestry as a fresh slice instead of appending in place,
	// so every Node already reported keeps the ancestry it was given.
	path := make([]ElementID, len(s.path)+1)
	copy(path, s.path)
	path[len(s.path)] = node.ID
	s.path = path
}

func (s *Scanner) popMaster() Node {
	node := s.open[len(s.open)-1]
	s.open = s.open[:len(s.open)-1]
	// node's own ancestry is the path without it, i.e. the path to restore.
	s.path = node.openMasters
	return node
}

func (s *Scanner) nodeFor(h ElementHeader) Node {
	end := UnknownSize
	if h.Size != UnknownSize {
		end = h.Offset + int64(h.HeaderLen) + h.Size
	}
	return Node{
		ID:          h.ID,
		Kind:        h.Kind,
		Depth:       len(s.path),
		Offset:      h.Offset,
		HeaderLen:   h.HeaderLen,
		Size:        h.Size,
		End:         end,
		openMasters: s.path,
	}
}
