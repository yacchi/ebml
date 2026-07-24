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
	ID        uint32
	Size      int64 // -1 means unknown-size
	Kind      Kind
	HeaderLen int
}

type elementCtx struct {
	id   uint32
	size int64
	kind Kind
}

type masterFrame struct {
	id        uint32
	endOffset int64 // -1 means unknown-size
}

type KindClassifier func(id uint32) Kind

type Option func(*Parser)

func WithKindClassifier(f KindClassifier) Option {
	return func(p *Parser) {
		p.kindClassifier = f
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

func New(opts ...Option) *Parser {
	p := &Parser{
		buf: make([]byte, 0, 4096),
	}
	p.kindClassifier = KindForElementID
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
			return ElementHeader{Kind: KindEndMaster}, nil
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

	kind := KindUnknown
	if p.kindClassifier != nil {
		kind = p.kindClassifier(id)
	}
	h := ElementHeader{
		ID:        id,
		Size:      size,
		Kind:      kind,
		HeaderLen: idLen + sizeLen,
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

	p.current = &elementCtx{id: h.ID, size: h.Size, kind: h.Kind}
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
	p.stack = append(p.stack, masterFrame{id: p.current.id, endOffset: end})
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

// CloseMaster pops one master from the stack without validating offsets.
// This is intended for "explicit close" (e.g. unknown-size boundary detection).
func (p *Parser) CloseMaster() error {
	if len(p.stack) == 0 {
		return Invalid{Msg: "master stack is empty"}
	}
	p.stack = p.stack[:len(p.stack)-1]
	return nil
}

type ClosedMaster struct {
	ID    uint32
	Depth int // depth before pop
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
		closed = append(closed, ClosedMaster{ID: top.id, Depth: len(p.stack)})
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
		closed = append(closed, ClosedMaster{ID: top.id, Depth: len(p.stack)})
		p.stack = p.stack[:len(p.stack)-1]
	}
	return closed, nil
}

func (p *Parser) SkipPayload() error {
	if p.current == nil {
		return Invalid{Msg: "no current element; call consume_header first"}
	}
	if p.current.kind == KindMaster {
		return Invalid{Msg: "cannot skip payload of master; use enter_master"}
	}
	if p.current.size < 0 {
		return Invalid{Msg: "cannot skip unknown-size payload"}
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
		return Invalid{Msg: "cannot skip unknown-size payload"}
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
		return nil, Invalid{Msg: "cannot read unknown-size payload"}
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

func FormatID(id uint32) string {
	// Minimal formatting for tests/logging: lowercase hex without "0x".
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
