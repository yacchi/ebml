package tree

import (
	"fmt"

	"github.com/yacchi/ebml/matroska"
	"github.com/yacchi/ebml/parser"
)

// ParseError reports that Parse could not continue because the input is not
// well-formed EBML. Offset is the absolute offset within the parsed buffer where
// the failure was detected, and the wrapped error is the parser's own diagnosis
// (parser.TruncatedError, parser.ElementOverflowError, parser.VINTLengthError,
// parser.UnknownSizeLeafError, parser.Invalid, ...), so errors.Is and errors.As
// reach it. Parse drives the cursor itself and has no handler, so every error it
// reports is a structural one: parser.IsStructural(err) is true.
//
// Parse returns a ParseError together with every element it had already parsed
// completely, so a malformed tail never discards the good prefix.
type ParseError struct {
	Offset int64
	Err    error
}

func (e ParseError) Error() string {
	return fmt.Sprintf("parse tree failed at offset %d: %v", e.Offset, e.Err)
}

func (e ParseError) Unwrap() error { return e.Err }

// config holds the resolved Parse options.
type config struct {
	classifier parser.KindClassifier
	registry   Registry
	// maxPayload caps retained leaf payloads; negative means unlimited.
	maxPayload int
}

// Option configures Parse.
type Option func(*config)

// WithClassifier overrides the element classifier used to decide which elements
// are masters. The default is matroska.KindForElementID, the registry-driven
// classifier of the standard element set.
//
// A classifier is the one place where "what is a master" is decided, so passing
// a custom one is how a caller parses a document whose masters the standard
// registry does not know: wrap matroska.KindForElementID and return
// parser.KindMaster for the extra IDs. A nil classifier is ignored.
func WithClassifier(f parser.KindClassifier) Option {
	return func(c *config) {
		if f != nil {
			c.classifier = f
		}
	}
}

// WithRegistry overrides the registry that the returned elements resolve Name,
// Describe and Type through. The default is DefaultRegistry, which is package
// matroska. A nil registry is ignored.
//
// It is orthogonal to WithClassifier: a classifier decides structure (master or
// leaf) while a registry only names and types what was parsed, so a caller
// teaching this package about extra elements normally passes both.
func WithRegistry(r Registry) Option {
	return func(c *config) {
		if r != nil {
			c.registry = r
		}
	}
}

// WithMaxPayload caps how many payload bytes a single leaf element may retain.
// A leaf whose declared size exceeds n is retained with Truncated set and a nil
// Payload; its Offset, HeaderLen, Size and End stay accurate, so the caller can
// re-read those bytes from the original source. WithMaxPayload(0) therefore
// retains the structure of a document without copying any leaf bytes, and a
// negative n means unlimited, which is the default.
func WithMaxPayload(n int) Option {
	return func(c *config) { c.maxPayload = n }
}

// Parse parses a complete in-memory EBML buffer into its top-level elements,
// each with its children, and returns them in stream order.
//
// Offsets in the result are relative to the start of data. Masters have a nil
// Payload and their children in Children; leaves carry a copy of their payload,
// owned by the Element.
//
// Parse is the escape hatch for bytes that were read as one opaque blob: an
// element whose ID the classifier did not know as a master arrives as a binary
// leaf, and re-parsing that leaf's Bytes with Parse (optionally with
// WithClassifier) recovers its internal structure. The same applies to payloads
// that legitimately nest an EBML document, such as CodecPrivate.
//
// An unknown-size master is closed when a new top-level element (an EBML header
// or a Segment) begins, and otherwise extends to the end of the buffer -- so a
// buffer holding several concatenated unknown-size Segments yields one top-level
// element per Segment rather than nesting them. That rule is a consumer policy,
// which is why it lives here: the core cursor deliberately has no element schema
// and so cannot decide that an element cannot be a master's child.
//
// On malformed input Parse returns a ParseError together with the complete
// prefix it had already parsed; an element whose header or payload is cut off at
// the end of the buffer is not part of that prefix. It never panics, whatever
// the input bytes are.
func Parse(data []byte, opts ...Option) ([]*Element, error) {
	cfg := config{
		classifier: matroska.KindForElementID,
		registry:   nil, // nil means DefaultRegistry, resolved per element
		maxPayload: -1,
	}
	for _, opt := range opts {
		if opt != nil {
			opt(&cfg)
		}
	}

	p := parser.New(cfg.classifier)
	p.Feed(data)

	var (
		roots []*Element
		stack []*Element
	)
	attach := func(el *Element) {
		if len(stack) == 0 {
			roots = append(roots, el)
			return
		}
		stack[len(stack)-1].AppendChild(el)
	}

	for {
		offset := p.Offset()
		h, err := p.Peek()
		if err != nil {
			if isNeedMoreData(err) {
				break
			}
			return roots, ParseError{Offset: offset, Err: err}
		}

		// A known-size master reached its declared end.
		if h.Kind == parser.KindEndMaster {
			if len(stack) == 0 {
				return roots, ParseError{Offset: offset, Err: parser.Invalid{Msg: "end of master with no open master"}}
			}
			if err := p.LeaveMaster(); err != nil {
				return roots, ParseError{Offset: offset, Err: err}
			}
			stack = stack[:len(stack)-1]
			continue
		}

		// An unknown-size master ends structurally where the next top-level
		// element begins. This cascades, so nested unknown-size masters close
		// outward on subsequent iterations.
		if len(stack) > 0 && stack[len(stack)-1].Size == parser.UnknownSize && isTopLevelElementID(h.ID) {
			if err := p.CloseMaster(); err != nil {
				return roots, ParseError{Offset: offset, Err: err}
			}
			stack = stack[:len(stack)-1]
			continue
		}

		if _, err := p.ConsumeHeader(); err != nil {
			if isNeedMoreData(err) {
				break
			}
			return roots, ParseError{Offset: offset, Err: err}
		}
		el := newElement(h, offset, cfg.registry)

		if h.Kind == parser.KindMaster {
			if err := p.EnterMaster(); err != nil {
				return roots, ParseError{Offset: offset, Err: err}
			}
			attach(el)
			stack = append(stack, el)
			continue
		}

		if cfg.maxPayload >= 0 && h.Size > int64(cfg.maxPayload) {
			if err := p.SkipPayload(); err != nil {
				if isNeedMoreData(err) {
					break
				}
				return roots, ParseError{Offset: offset, Err: err}
			}
			el.Truncated = true
			attach(el)
			continue
		}

		payload, err := p.ReadPayload()
		if err != nil {
			if isNeedMoreData(err) {
				break
			}
			return roots, ParseError{Offset: offset, Err: err}
		}
		el.Payload = payload
		attach(el)
	}

	// FinalizeEOF turns "the buffer simply ended" into a verdict: it closes the
	// masters that legitimately end at EOF and reports a cut-off header or
	// payload as parser.TruncatedError.
	if _, err := p.FinalizeEOF(); err != nil {
		return roots, ParseError{Offset: p.Offset(), Err: err}
	}
	return roots, nil
}

// isTopLevelElementID reports whether id begins a new top-level document part
// (an EBML header or a Segment), which structurally closes any open
// unknown-size master.
func isTopLevelElementID(id parser.ElementID) bool {
	return id == matroska.IDEBML || id == matroska.IDSegment
}

func isNeedMoreData(err error) bool {
	_, ok := err.(parser.NeedMoreData)
	return ok
}
