package parser

import (
	"encoding/hex"
	"fmt"
	"math"
)

type Kind string

const (
	KindUnknown   Kind = "unknown"
	KindMaster    Kind = "master"
	KindUint      Kind = "uint"
	KindBinary    Kind = "binary"
	KindEndMaster Kind = "end_master"
)

type ElementHeader struct {
	ID   ElementID
	Size int64 // -1 means unknown-size
	Kind Kind
	// HeaderLen is the encoded length of the ID VINT plus the size VINT, so the
	// payload starts at Offset+HeaderLen.
	HeaderLen int
	// Offset is the absolute stream offset of the element's first header byte, so
	// callers never have to reconstruct an element's position from event order.
	// For the KindEndMaster pseudo-header it is the offset at which the master's
	// end was reached.
	Offset int64
}

type elementCtx struct {
	id     ElementID
	size   int64
	kind   Kind
	offset int64 // absolute offset of the element's header
}

type masterFrame struct {
	id        ElementID
	start     int64 // absolute offset of the master's header
	endOffset int64 // -1 means unknown-size
}

// KindClassifier maps an element ID to the kind the cursor should treat it as.
// It is the cursor's only source of element knowledge and therefore a required
// argument of New and NewScanner; matroska.KindForElementID covers the standard
// Matroska element set.
type KindClassifier func(id ElementID) Kind

type Option func(*Parser)

// WithStartOffset makes the cursor count absolute offsets from off instead of 0,
// for a cursor that resumes a stream another cursor was reading.
//
// It exists for last-resort recovery: a structural error leaves a cursor unable
// to continue, and the only way forward is to abandon it and start a fresh one
// further along the same stream (the bytes it had buffered but not consumed are
// available from Unconsumed). Without this option every offset the new cursor
// reported would restart at 0 and no longer refer to the stream the caller is
// reading. A negative off is ignored.
func WithStartOffset(off int64) Option {
	return func(p *Parser) {
		if off >= 0 {
			p.absOffset = off
		}
	}
}

type Parser struct {
	buf       []byte
	pos       int
	absOffset int64

	stack   []masterFrame
	current *elementCtx

	kindClassifier KindClassifier
}

// New returns a cursor that classifies elements with classify.
//
// The classifier is required, not optional: the cursor holds no element table of
// its own and knows no element ID, so without one it could not tell a master from
// a leaf at all. There is deliberately no fallback -- a built-in default would
// silently read an unlisted master (a Cluster, say) as one opaque binary blob,
// which is a structural misreading that no error would report. Pass
// matroska.KindForElementID for standard Matroska, or Registry.KindForElementID
// of a registry extended with vendor elements.
//
// New panics when classify is nil. That is a programmer error at construction
// time, not a stream condition: a nil classifier cannot be repaired by feeding
// different bytes, so failing loudly here is better than every element reporting
// KindUnknown later.
func New(classify KindClassifier, opts ...Option) *Parser {
	if classify == nil {
		panic("parser.New: classify must not be nil; the cursor has no built-in element knowledge (pass matroska.KindForElementID)")
	}
	p := &Parser{
		buf:            make([]byte, 0, 4096),
		kindClassifier: classify,
	}
	for _, opt := range opts {
		opt(p)
	}
	return p
}

func (p *Parser) Offset() int64 { return p.absOffset }
func (p *Parser) Depth() int    { return len(p.stack) }

func (p *Parser) Feed(b []byte) {
	if len(b) == 0 {
		return
	}
	p.buf = append(p.buf, b...)
}

func (p *Parser) available() int {
	return len(p.buf) - p.pos
}

// Unconsumed returns a copy of the bytes that were fed but not yet consumed, the
// first of which sits at absolute offset Offset().
//
// It is the counterpart of WithStartOffset and exists for the same reason: after
// a structural error a cursor cannot continue, and a consumer that wants to
// resynchronize needs the bytes it had already fed. Handing them back means the
// consumer does not have to keep a second copy of the whole stream just in case.
// It is not part of normal reading: a healthy scan never needs it.
func (p *Parser) Unconsumed() []byte {
	if p.available() == 0 {
		return nil
	}
	out := make([]byte, p.available())
	copy(out, p.buf[p.pos:])
	return out
}

func (p *Parser) trimBuffer() {
	if p.pos == 0 {
		return
	}
	if p.pos < 4096 {
		return
	}
	copy(p.buf, p.buf[p.pos:])
	p.buf = p.buf[:len(p.buf)-p.pos]
	p.pos = 0
}

func (p *Parser) Peek() (ElementHeader, error) {
	if len(p.stack) > 0 {
		top := p.stack[len(p.stack)-1]
		if top.endOffset >= 0 && p.absOffset >= top.endOffset {
			return ElementHeader{Kind: KindEndMaster, Offset: p.absOffset}, nil
		}
	}
	if p.current != nil {
		return ElementHeader{}, Invalid{Msg: "cannot peek with a pending current element; call enter_master/skip_payload first"}
	}

	s := p.buf[p.pos:]

	id, idLen, err := parseElementID(s)
	if err != nil {
		return ElementHeader{}, err
	}
	size, sizeLen, err := parseSize(s[idLen:])
	if err != nil {
		if nmd, ok := err.(NeedMoreData); ok {
			nmd.MinBytes += idLen
			return ElementHeader{}, nmd
		}
		return ElementHeader{}, err
	}

	kind := p.kindClassifier(id)
	h := ElementHeader{
		ID:        id,
		Size:      size,
		Kind:      kind,
		HeaderLen: idLen + sizeLen,
		Offset:    p.absOffset,
	}
	// An unknown size is reserved for masters: for anything else the element has
	// no locatable end, so the stream cannot be read past it. Reporting it here,
	// on the header, is what lets a consumer detect the case before it commits to
	// a payload operation.
	if h.Size == UnknownSize && h.Kind != KindMaster {
		return ElementHeader{}, UnknownSizeLeafError{ID: h.ID, Offset: h.Offset, Kind: h.Kind}
	}
	if err := p.validateChildExtent(h); err != nil {
		return ElementHeader{}, err
	}
	return h, nil
}

func (p *Parser) validateChildExtent(h ElementHeader) error {
	if h.Size < 0 || len(p.stack) == 0 {
		return nil
	}
	parent := p.stack[len(p.stack)-1]
	if parent.endOffset < 0 {
		return nil
	}
	childEnd := p.absOffset + int64(h.HeaderLen) + h.Size
	if childEnd > parent.endOffset {
		return ElementOverflowError{
			ChildID: h.ID, ChildEnd: childEnd,
			ParentID: parent.id, ParentEnd: parent.endOffset,
		}
	}
	return nil
}

func (p *Parser) ConsumeHeader() (ElementHeader, error) {
	h, err := p.Peek()
	if err != nil {
		return ElementHeader{}, err
	}
	if h.Kind == KindEndMaster {
		return ElementHeader{}, Invalid{Msg: "cannot consume EndMaster"}
	}

	if p.available() < h.HeaderLen {
		return ElementHeader{}, NeedMoreData{MinBytes: h.HeaderLen - p.available()}
	}

	p.pos += h.HeaderLen
	p.absOffset += int64(h.HeaderLen)
	p.trimBuffer()

	p.current = &elementCtx{id: h.ID, size: h.Size, kind: h.Kind, offset: h.Offset}
	return h, nil
}

func (p *Parser) EnterMaster() error {
	if p.current == nil {
		return Invalid{Msg: "no current element; call consume_header first"}
	}
	if p.current.kind != KindMaster {
		return Invalid{Msg: "current element is not master"}
	}

	var end int64 = UnknownSize
	if p.current.size >= 0 {
		end = p.absOffset + p.current.size
	}
	p.stack = append(p.stack, masterFrame{id: p.current.id, start: p.current.offset, endOffset: end})
	p.current = nil
	return nil
}

func (p *Parser) LeaveMaster() error {
	if len(p.stack) == 0 {
		return Invalid{Msg: "master stack is empty"}
	}
	top := p.stack[len(p.stack)-1]
	if top.endOffset < 0 {
		return Invalid{Msg: "cannot leave unknown-size master (use finalize_eof)"}
	}
	if p.absOffset < top.endOffset {
		return Invalid{Msg: "cannot leave master before end offset"}
	}
	p.stack = p.stack[:len(p.stack)-1]
	return nil
}

// CloseMaster closes the innermost open master at the current offset, without
// waiting for a declared end. It is the explicit close a consumer performs when
// its own boundary rule decides that an unknown-size master (a KVS Segment, say)
// has ended, which is the case LeaveMaster refuses because there is no declared
// end to reach.
//
// Precondition: the innermost master must either declare an unknown size, or be a
// known-size master whose declared end offset has already been reached. Closing a
// known-size master while its declared payload still has bytes left would discard
// a boundary the stream itself declares: the remaining payload would then be read
// as elements of the enclosing master, so every following depth and every
// enclosing-master chain would be wrong, and the parent's own end offset would be
// overshot without any error. CloseMaster reports that attempt as
// PrematureCloseError (matching ErrPrematureClose) and leaves the cursor
// untouched, so the caller can carry on reading the master's remaining children.
func (p *Parser) CloseMaster() error {
	if len(p.stack) == 0 {
		return Invalid{Msg: "master stack is empty"}
	}
	top := p.stack[len(p.stack)-1]
	if top.endOffset >= 0 && p.absOffset < top.endOffset {
		return PrematureCloseError{ID: top.id, Offset: p.absOffset, DeclaredEnd: top.endOffset}
	}
	p.stack = p.stack[:len(p.stack)-1]
	return nil
}

type ClosedMaster struct {
	ID    ElementID
	Depth int // depth before pop
	// Start is the absolute offset of the master's header, End the absolute
	// offset at which it closed, so a master's full extent is [Start, End).
	// For an unknown-size master End is the offset where the close was forced
	// (here: EOF); it is never UnknownSize.
	Start int64
	End   int64
}

// FinalizeEOF closes all remaining masters at input EOF.
// It pops unknown-size masters, and also pops known-size masters whose end has been reached.
// It returns masters (inner to outer) with their IDs and depths.
func (p *Parser) FinalizeEOF() ([]ClosedMaster, error) {
	var closed []ClosedMaster
	if p.current != nil && p.current.size >= 0 && p.current.size > int64(p.available()) {
		return closed, TruncatedError{Msg: "element payload"}
	}
	for len(p.stack) > 0 {
		top := p.stack[len(p.stack)-1]
		if top.endOffset < 0 || p.absOffset < top.endOffset {
			break
		}
		closed = append(closed, ClosedMaster{ID: top.id, Depth: len(p.stack), Start: top.start, End: p.absOffset})
		p.stack = p.stack[:len(p.stack)-1]
	}
	if p.current == nil && p.available() > 0 {
		if _, err := p.Peek(); err != nil {
			if _, ok := err.(NeedMoreData); ok {
				return closed, TruncatedError{Msg: "element header"}
			}
			return closed, err
		}
	}
	for len(p.stack) > 0 {
		top := p.stack[len(p.stack)-1]
		if top.endOffset >= 0 && p.absOffset < top.endOffset {
			return closed, Invalid{Msg: "eof before reaching known-size master end"}
		}
		closed = append(closed, ClosedMaster{ID: top.id, Depth: len(p.stack), Start: top.start, End: p.absOffset})
		p.stack = p.stack[:len(p.stack)-1]
	}
	return closed, nil
}

// unknownSizePayloadError diagnoses a payload operation on a current element
// that declares an unknown size. For a master that is a legitimate element the
// caller must enter instead of skipping past; for anything else it is the EBML
// violation UnknownSizeLeafError, reported by the same type Peek uses so a
// consumer has one case to detect. verb names the attempted operation.
func (p *Parser) unknownSizePayloadError(verb string) error {
	if p.current.kind != KindMaster {
		return UnknownSizeLeafError{ID: p.current.id, Offset: p.current.offset, Kind: p.current.kind}
	}
	return Invalid{Msg: fmt.Sprintf("cannot %s unknown-size master payload; use enter_master", verb)}
}

func (p *Parser) SkipPayload() error {
	if p.current == nil {
		return Invalid{Msg: "no current element; call consume_header first"}
	}
	if p.current.kind == KindMaster {
		return Invalid{Msg: "cannot skip payload of master; use enter_master"}
	}
	if p.current.size < 0 {
		return p.unknownSizePayloadError("skip")
	}

	if p.current.size > math.MaxInt {
		return Invalid{Msg: "payload too large to skip on this platform"}
	}
	need := int(p.current.size)
	if p.available() < need {
		return NeedMoreData{MinBytes: need - p.available()}
	}

	p.pos += need
	p.absOffset += int64(need)
	p.current = nil
	p.trimBuffer()
	return nil
}

// SkipCurrentPayload skips the payload of the current element, master or non-master.
// For master elements, this skips all child bytes without emitting any master events.
func (p *Parser) SkipCurrentPayload() error {
	if p.current == nil {
		return Invalid{Msg: "no current element; call consume_header first"}
	}
	if p.current.size < 0 {
		return p.unknownSizePayloadError("skip")
	}
	if p.current.size > math.MaxInt {
		return Invalid{Msg: "payload too large to skip on this platform"}
	}
	need := int(p.current.size)
	if p.available() < need {
		return NeedMoreData{MinBytes: need - p.available()}
	}
	p.pos += need
	p.absOffset += int64(need)
	p.current = nil
	p.trimBuffer()
	return nil
}

// ReadPayload reads and consumes the full payload of the current element.
// It returns a copy of the payload bytes.
func (p *Parser) ReadPayload() ([]byte, error) {
	if p.current == nil {
		return nil, Invalid{Msg: "no current element; call consume_header first"}
	}
	if p.current.size < 0 {
		return nil, p.unknownSizePayloadError("read")
	}
	if p.current.size > math.MaxInt {
		return nil, Invalid{Msg: "payload too large to read on this platform"}
	}
	need := int(p.current.size)
	if p.available() < need {
		return nil, NeedMoreData{MinBytes: need - p.available()}
	}
	out := make([]byte, need)
	copy(out, p.buf[p.pos:p.pos+need])
	p.pos += need
	p.absOffset += int64(need)
	p.current = nil
	p.trimBuffer()
	return out, nil
}

// FormatID renders an element ID as bare lowercase hex without a "0x" prefix
// (the form used by the golden event logs). For the conventional EBML notation
// use ElementID.String.
func FormatID(id ElementID) string {
	// Preserve full bytes for the ID length by left-trimming zero bytes.
	var b [4]byte
	b[0] = byte(id >> 24)
	b[1] = byte(id >> 16)
	b[2] = byte(id >> 8)
	b[3] = byte(id)

	start := 0
	for start < 3 && b[start] == 0 {
		start++
	}
	return hex.EncodeToString(b[start:])
}

func (h ElementHeader) String() string {
	return fmt.Sprintf("id=%s size=%d kind=%s header_len=%d", FormatID(h.ID), h.Size, h.Kind, h.HeaderLen)
}
