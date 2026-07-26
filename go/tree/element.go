package tree

import (
	"errors"
	"fmt"
	"time"

	"github.com/yacchi/ebml/matroska"
	"github.com/yacchi/ebml/parser"
)

// ebmlEpoch is the EBML date epoch: 2001-01-01T00:00:00 UTC. An EBML date
// payload is a signed 64-bit count of nanoseconds relative to it.
var ebmlEpoch = time.Date(2001, time.January, 1, 0, 0, 0, 0, time.UTC)

var (
	// ErrNoElement is reported by the value accessors when there is no element
	// to read: the receiver is nil, which is what every navigation method
	// returns for a miss. Navigation chains are therefore safe to write without
	// intermediate nil checks and the error surfaces once, at the accessor.
	ErrNoElement = errors.New("element is absent")

	// ErrTruncatedPayload is reported by the value accessors when the element's
	// payload was elided by a retention cap (see WithMaxPayload), so its bytes
	// were never retained. Offset/HeaderLen/Size and End are still accurate, so
	// the payload can be re-read from the original byte source.
	ErrTruncatedPayload = errors.New("element payload was elided by a retention cap")

	// ErrValueLength is reported when a payload's length cannot represent the
	// requested value type (for example a 9-byte integer, or a date that is
	// neither empty nor 8 bytes).
	ErrValueLength = errors.New("payload length cannot represent this value type")
)

// Registry is the element knowledge that Name, Describe and Type report. It is
// exactly the subset of package matroska's registry API that a tree needs, and
// matroska stays the single source of that knowledge: DefaultRegistry delegates
// to it.
//
// A caller that knows elements matroska does not normally has no implementation
// to write: a *matroska.Registry derived with matroska.NewRegistry satisfies this
// interface, so registering the vendor elements there and passing that registry to
// both WithRegistry and WithClassifier is the whole extension path. A hand-written
// implementation stays possible for element knowledge that lives elsewhere
// entirely. A Registry never gates retention and never gates decoding, so an
// element no registry knows is still retained by Parse and still readable with
// every value accessor.
type Registry interface {
	NameForID(id parser.ElementID) string
	Describe(id parser.ElementID) string
	TypeFor(id parser.ElementID) (matroska.ValueType, bool)
}

// matroskaRegistry resolves element knowledge through package matroska.
type matroskaRegistry struct{}

func (matroskaRegistry) NameForID(id parser.ElementID) string { return matroska.NameForID(id) }

func (matroskaRegistry) Describe(id parser.ElementID) string { return matroska.Describe(id) }

func (matroskaRegistry) TypeFor(id parser.ElementID) (matroska.ValueType, bool) {
	return matroska.TypeFor(id)
}

// DefaultRegistry is the registry used by a tree parsed without WithRegistry and
// by any Element carrying none, including a hand-built one and the zero value.
// It is package matroska, the standard RFC 9559 element registry.
var DefaultRegistry Registry = matroskaRegistry{}

// Element is one node of a retained EBML element tree.
//
// Every element that occurs in the stream is retained by ID -- including
// elements the registry has never heard of -- so any element is reachable and
// readable with no library change. The registry only informs Name, Describe and
// Type; it never gates retention and never gates decoding.
//
// Nodes are linked both ways: Children/Find walk down, Parent/Ancestor walk up.
// Walking up is what makes it possible to judge a node together with its
// enclosing context -- a SimpleTag by its enclosing Tag's Targets, a block by
// its Segment's Info -- in one expression:
//
//	uid, err := tag.Find(matroska.IDTargets, matroska.IDTagTrackUID).AsUint()
//
// The tree offers two deliberately distinguishable access modes, strict and
// loose. The package documentation explains how they differ and compose.
//
// Every method is safe on a nil receiver and on the zero value: navigation
// yields nil for a miss, and the value accessors yield zero values or errors.
// No method panics, so chains never need intermediate nil checks.
type Element struct {
	ID        parser.ElementID
	Offset    int64  // absolute stream offset of the element header
	HeaderLen int    // encoded length of the ID VINT plus the size VINT
	Size      int64  // declared payload size; parser.UnknownSize when unknown
	Payload   []byte // leaf payload; nil for masters. Owned by the Element, never aliasing a parser buffer
	Truncated bool   // payload elided by a retention cap
	Children  []*Element

	// parent is the enclosing master, nil at the root of the retained tree.
	// It is maintained by AppendChild so it can never disagree with Children.
	parent *Element

	// registry resolves Name, Describe and Type. It is nil on a hand-built
	// Element unless SetRegistry set it, in which case the nearest ancestor that
	// carries one applies, and DefaultRegistry when none does.
	registry Registry
}

// newElement builds an unlinked tree node from a header that was just consumed,
// the absolute stream offset that header started at, and the registry the tree
// resolves element knowledge through.
func newElement(h parser.ElementHeader, offset int64, reg Registry) *Element {
	return &Element{
		ID:        h.ID,
		Offset:    offset,
		HeaderLen: h.HeaderLen,
		Size:      h.Size,
		registry:  reg,
	}
}

// AppendChild links child as the last child of e, keeping the parent pointer
// consistent with Children. It is the only way a parent link is ever set, so the
// two directions can never disagree, and it is what a caller assembling a tree of
// its own -- from cursor events, say -- links nodes with. It is a no-op when
// either side is nil.
func (e *Element) AppendChild(child *Element) {
	if e == nil || child == nil {
		return
	}
	child.parent = e
	e.Children = append(e.Children, child)
}

// SetRegistry sets the registry e resolves Name, Describe and Type through.
// Descendants that carry no registry of their own resolve through their nearest
// ancestor that does, so setting it on the root of a hand-built tree is enough
// for the whole tree. A nil r clears it, restoring the DefaultRegistry fallback.
func (e *Element) SetRegistry(r Registry) {
	if e == nil {
		return
	}
	e.registry = r
}

// reg returns the registry this element resolves element knowledge through: its
// own, or the nearest enclosing one, falling back to DefaultRegistry for a nil
// receiver or a tree that carries none, and to package matroska if
// DefaultRegistry itself was cleared.
func (e *Element) reg() Registry {
	for node := e; node != nil; node = node.parent {
		if node.registry != nil {
			return node.registry
		}
	}
	if DefaultRegistry != nil {
		return DefaultRegistry
	}
	return matroskaRegistry{}
}

// Exists reports whether e refers to an element at all. It is false only for a
// nil Element, so it is the explicit form of the nil check that the navigation
// methods otherwise make unnecessary.
func (e *Element) Exists() bool { return e != nil }

// Name returns the element's registered name, or an empty string when the ID is
// not registered (and for a nil Element).
func (e *Element) Name() string {
	if e == nil {
		return ""
	}
	return e.reg().NameForID(e.ID)
}

// Describe returns "Name (0xID)" for a registered element and the bare EBML hex
// ID form for an unregistered one. It returns an empty string for a nil Element.
func (e *Element) Describe() string {
	if e == nil {
		return ""
	}
	return e.reg().Describe(e.ID)
}

// Type returns the registry's value type for the element's ID, or
// matroska.TypeUnknown when the ID is not registered (and for a nil Element).
//
// Type is informational only. It does not restrict the value accessors: an
// element whose registered type is binary can still be read with AsUint, and an
// element with an unregistered ID can be read with every accessor.
func (e *Element) Type() matroska.ValueType {
	if e == nil {
		return matroska.TypeUnknown
	}
	if t, ok := e.reg().TypeFor(e.ID); ok {
		return t
	}
	return matroska.TypeUnknown
}

// End returns the absolute stream offset one byte past the element, i.e.
// Offset + HeaderLen + Size.
//
// It returns parser.UnknownSize when the size is not known -- for an
// unknown-size master, whose end is only established when the master closes --
// and for a nil Element. Callers must check for parser.UnknownSize before using
// the result as an offset.
func (e *Element) End() int64 {
	if e == nil || e.Size < 0 {
		return parser.UnknownSize
	}
	return e.Offset + int64(e.HeaderLen) + e.Size
}

// payload returns the bytes the value accessors decode from, or an error when
// there is nothing retained to decode.
func (e *Element) payload() ([]byte, error) {
	if e == nil {
		return nil, ErrNoElement
	}
	if e.Truncated {
		return nil, fmt.Errorf("%s: %w", e.Describe(), ErrTruncatedPayload)
	}
	return e.Payload, nil
}

// AsUint decodes the raw payload as an EBML unsigned integer.
//
// Decoding works from the payload bytes alone, so an element the registry has
// never heard of is readable too. A mismatch between the registered value type
// and the accessor used is deliberately not an error -- real streams carry
// out-of-spec types, and the caller is the authority on how to read the bytes.
// Errors are reported only when there is no element (ErrNoElement), the payload
// was elided by a retention cap (ErrTruncatedPayload), or the payload length
// cannot represent the value (ErrValueLength). A master element (payload nil)
// decodes as the zero value.
func (e *Element) AsUint() (uint64, error) {
	b, err := e.payload()
	if err != nil {
		return 0, err
	}
	v, err := parser.DecodeUint(b)
	if err != nil {
		return 0, fmt.Errorf("%s: %w: %v", e.Describe(), ErrValueLength, err)
	}
	return v, nil
}

// AsInt decodes the raw payload as an EBML signed integer. The error and
// type-mismatch semantics are those documented on AsUint.
func (e *Element) AsInt() (int64, error) {
	b, err := e.payload()
	if err != nil {
		return 0, err
	}
	v, err := parser.DecodeInt(b)
	if err != nil {
		return 0, fmt.Errorf("%s: %w: %v", e.Describe(), ErrValueLength, err)
	}
	return v, nil
}

// AsFloat decodes the raw payload as an EBML float: 4 bytes as IEEE 754 binary32
// and 8 bytes as binary64, with an empty payload decoding as 0. The error and
// type-mismatch semantics are those documented on AsUint.
func (e *Element) AsFloat() (float64, error) {
	b, err := e.payload()
	if err != nil {
		return 0, err
	}
	v, err := parser.DecodeFloat(b)
	if err != nil {
		return 0, fmt.Errorf("%s: %w: %v", e.Describe(), ErrValueLength, err)
	}
	return v, nil
}

// AsString decodes the raw payload as an EBML string, stopping at the first NUL
// byte as EBML zero-padding requires. It cannot fail: a missing element, an
// elided payload and an empty payload all yield an empty string. Use Bytes when
// the difference matters. The type-mismatch semantics are those documented on
// AsUint.
func (e *Element) AsString() string {
	b, err := e.payload()
	if err != nil {
		return ""
	}
	return parser.DecodeString(b)
}

// AsTime decodes the raw payload as an EBML date: a signed 64-bit count of
// nanoseconds relative to 2001-01-01T00:00:00 UTC, which negative values place
// before that epoch. An empty payload is exactly the epoch, as EBML defines.
//
// A payload that is neither empty nor 8 bytes cannot represent a date and is
// reported as ErrValueLength; the other errors are those documented on AsUint.
func (e *Element) AsTime() (time.Time, error) {
	b, err := e.payload()
	if err != nil {
		return time.Time{}, err
	}
	switch len(b) {
	case 0:
		return ebmlEpoch, nil
	case 8:
		ns, err := parser.DecodeInt(b)
		if err != nil {
			return time.Time{}, fmt.Errorf("%s: %w: %v", e.Describe(), ErrValueLength, err)
		}
		return ebmlEpoch.Add(time.Duration(ns)), nil
	default:
		return time.Time{}, fmt.Errorf("%s: %w: date payload is %d bytes; expected 0 or 8",
			e.Describe(), ErrValueLength, len(b))
	}
}

// Bytes returns the element's raw payload, nil for a master, for a nil Element,
// and for a payload elided by a retention cap. The result aliases Payload and
// must not be modified.
func (e *Element) Bytes() []byte {
	if e == nil || e.Truncated {
		return nil
	}
	return e.Payload
}

// ChildrenByID returns every direct child with the given ID, in stream order,
// and nil when there is none.
func (e *Element) ChildrenByID(id parser.ElementID) []*Element {
	if e == nil {
		return nil
	}
	var out []*Element
	for _, child := range e.Children {
		if child != nil && child.ID == id {
			out = append(out, child)
		}
	}
	return out
}

// firstChild returns the first direct child with the given ID, or nil.
func (e *Element) firstChild(id parser.ElementID) *Element {
	if e == nil {
		return nil
	}
	for _, child := range e.Children {
		if child != nil && child.ID == id {
			return child
		}
	}
	return nil
}

// Find walks the path of element IDs down from e, taking the first match at
// every level, and returns nil as soon as a level has no match (so a chain of
// Find calls never needs a nil check). An empty path returns e itself.
//
// Find is strict, structural access: it states WHERE the value must live. Use
// FindAll when repeated children mean "first match" is not enough, and
// Descendants when containment is irrelevant.
func (e *Element) Find(path ...parser.ElementID) *Element {
	current := e
	for _, id := range path {
		current = current.firstChild(id)
		if current == nil {
			return nil
		}
	}
	return current
}

// FindAll returns every element reachable from e by exactly the given path of
// element IDs, across all matching branches: with a path of Tracks, TrackEntry
// it returns the TrackEntry children of every Tracks child, in stream order. It
// returns nil when no branch matches. An empty path returns just e.
func (e *Element) FindAll(path ...parser.ElementID) []*Element {
	if e == nil {
		return nil
	}
	level := []*Element{e}
	for _, id := range path {
		var next []*Element
		for _, node := range level {
			next = append(next, node.ChildrenByID(id)...)
		}
		if len(next) == 0 {
			return nil
		}
		level = next
	}
	return level
}

// Descendants returns every element with the given ID anywhere below e -- at any
// depth, under any parent, in stream (document) order -- and nil when there is
// none. It never returns e itself.
//
// This is the loose, extractive counterpart of Find, and containment is
// deliberately ignored: an ID that legitimately occurs under several different
// parents yields all of its occurrences in one slice. Asking a Segment for
// matroska.IDName returns the track names under every TrackEntry and the names
// under every ChapterDisplay alike. That is what extracting live-stream metadata
// usually wants -- "give me the fragment number", not
// "give me Tags>Tag>SimpleTag>TagString of the Tag whose TagName is ...".
//
// Looseness costs no precision, because the result is nodes and not bare
// scalars: every returned Element still carries its own structure, so Path,
// Parent and Ancestor can disambiguate afterwards instead of the caller having
// to commit to an exact path up front.
//
//	for _, name := range segment.Descendants(matroska.IDName) {
//	    if entry := name.Ancestor(matroska.IDTrackEntry); entry.Exists() {
//	        // a track name, not a chapter name
//	    }
//	}
//
// Loose and strict access therefore compose rather than compete: start loose,
// and tighten only where the document shape actually carries meaning.
func (e *Element) Descendants(id parser.ElementID) []*Element {
	if e == nil {
		return nil
	}
	var out []*Element
	e.Walk(func(node *Element) bool {
		if node != e && node.ID == id {
			out = append(out, node)
		}
		return true
	})
	return out
}

// Walk visits e and its descendants depth-first in pre-order (an element before
// its children, children in stream order). It stops the whole traversal as soon
// as fn returns false.
func (e *Element) Walk(fn func(*Element) bool) {
	if e == nil || fn == nil {
		return
	}
	e.walk(fn)
}

// walk reports whether the traversal should continue.
func (e *Element) walk(fn func(*Element) bool) bool {
	if !fn(e) {
		return false
	}
	for _, child := range e.Children {
		if child == nil {
			continue
		}
		if !child.walk(fn) {
			return false
		}
	}
	return true
}

// Parent returns the enclosing master, or nil at the root of the retained tree.
func (e *Element) Parent() *Element {
	if e == nil {
		return nil
	}
	return e.parent
}

// Root returns the outermost element of the retained tree containing e, which is
// e itself when e has no parent. It returns nil for a nil Element.
func (e *Element) Root() *Element {
	if e == nil {
		return nil
	}
	root := e
	for root.parent != nil {
		root = root.parent
	}
	return root
}

// Depth returns how many masters enclose e within the retained tree: 0 at its
// root, 1 for that root's children, and so on. It returns 0 for a nil Element.
// It is the depth within what was retained, which equals the stream depth only
// when the tree was retained from the top of the stream.
func (e *Element) Depth() int {
	if e == nil {
		return 0
	}
	depth := 0
	for parent := e.parent; parent != nil; parent = parent.parent {
		depth++
	}
	return depth
}

// Index returns e's position among its parent's children, or -1 when e has no
// parent (including a nil Element).
func (e *Element) Index() int {
	if e == nil || e.parent == nil {
		return -1
	}
	for i, sibling := range e.parent.Children {
		if sibling == e {
			return i
		}
	}
	return -1
}

// Ancestors returns the masters enclosing e, outermost first and excluding e
// itself. It returns nil when e has no parent.
func (e *Element) Ancestors() []*Element {
	if e == nil || e.parent == nil {
		return nil
	}
	var reversed []*Element
	for parent := e.parent; parent != nil; parent = parent.parent {
		reversed = append(reversed, parent)
	}
	out := make([]*Element, len(reversed))
	for i, node := range reversed {
		out[len(reversed)-1-i] = node
	}
	return out
}

// Path returns the element IDs from the root of the retained tree down to e
// inclusive, so it always has Depth()+1 entries. It returns nil for a nil
// Element.
func (e *Element) Path() []parser.ElementID {
	if e == nil {
		return nil
	}
	ancestors := e.Ancestors()
	out := make([]parser.ElementID, 0, len(ancestors)+1)
	for _, ancestor := range ancestors {
		out = append(out, ancestor.ID)
	}
	return append(out, e.ID)
}

// Ancestor returns the nearest master enclosing e that has the given ID, or nil
// when no enclosing element does. It never returns e itself, so an element does
// not match its own ID.
//
// Ancestor is what makes "judge this node together with its enclosing context" a
// single expression, for example reading the enclosing Tag's target while
// visiting a SimpleTag:
//
//	uid, err := simpleTag.Ancestor(matroska.IDTag).
//	    Find(matroska.IDTargets, matroska.IDTagTrackUID).AsUint()
func (e *Element) Ancestor(id parser.ElementID) *Element {
	if e == nil {
		return nil
	}
	for parent := e.parent; parent != nil; parent = parent.parent {
		if parent.ID == id {
			return parent
		}
	}
	return nil
}
