package parser

import (
	"errors"
	"fmt"
)

// ErrStructural is the class marker every failure the CURSOR itself raises about
// the shape of the stream unwraps to: an over-long VINT, truncated input at EOF, a
// child overflowing its known-size parent, an unknown-size leaf, a premature
// explicit close, and the generic Invalid. It exists so the class is implemented
// once, without matching on messages or listing every concrete type, and so
// errors.Is(err, ErrStructural) on an error a cursor operation returned directly
// keeps working.
//
// It is NOT the classification test. Ask IsStructural instead: the class has a
// boundary at the consumer, and a sentinel cannot express one.
//
// Two things are deliberately NOT structural:
//
//   - NeedMoreData is not a failure at all. It is the cursor asking for the rest
//     of an element.
//   - An error a consumer raised about an element's CONTENT -- marked as such with
//     ContentError -- is not about the stream's shape, whatever error value the
//     consumer chose.
//
// The distinction is what a consumer with a recovery strategy needs: scanning
// bytes forward for the next plausible element can only be justified after a
// structural failure, because only then has the cursor lost the ability to
// locate the next element. A content error (a payload that will not decode) is
// terminal and belongs to the caller -- see ext/fragment.WithResync.
var ErrStructural = errors.New("structural EBML error")

// IsStructural reports whether err is a structural failure OF THE CURSOR: the
// bytes could not be read as EBML, so the position of the next element is
// unknown. It is the canonical way to ask that question, and the only test that
// may gate a recovery strategy which scans bytes forward for a resume point.
//
// It is a predicate, not a sentinel comparison, because the class has a BOUNDARY
// that errors.Is cannot express. errors.Is traverses the whole Unwrap chain, so it
// can never stop anywhere: a consumer's verdict about an element's content is free
// to carry ErrStructural itself, or Invalid, or TruncatedError, or anything
// wrapping them, and errors.Is(err, ErrStructural) would then answer true even
// though the cursor read the stream's shape perfectly and the refusal was the
// consumer's own. IsStructural owns the rule instead: it walks the chain and
// answers false the moment it crosses a *ContentError, whatever that error
// carries. NeedMoreData is not a failure at all, so it is false as well.
//
// A consumer classifying an error that may have come from a layer above the cursor
// -- anything out of ext/fragment.Assembler.Feed, say -- must use this predicate.
func IsStructural(err error) bool {
	for err != nil {
		if err == ErrStructural {
			return true
		}
		switch err.(type) {
		case *ContentError:
			// The consumer boundary: a verdict about content is never structural,
			// so the chain below it is not consulted at all.
			return false
		case NeedMoreData:
			return false
		}
		switch u := err.(type) {
		case interface{ Unwrap() error }:
			err = u.Unwrap()
		case interface{ Unwrap() []error }:
			for _, sub := range u.Unwrap() {
				if IsStructural(sub) {
					return true
				}
			}
			return false
		default:
			return false
		}
	}
	return false
}

// ContentError marks an error as a CONSUMER's verdict about an element's content
// rather than a failure of the cursor. The bytes were read correctly; what they
// mean was refused -- a SimpleBlock payload that will not decode, a value outside
// the range the consumer accepts.
//
// It is the other half of the error classification IsStructural implements, and the
// reason the core offers it at all: the cursor cannot produce this error, since a
// pull loop never runs consumer code, but IsStructural has to be able to answer
// "is the stream broken?" for an error a consumer built and passed on. Wrapping the
// verdict in this type is what makes the answer no.
//
// An error carrying this wrapper is NEVER structural, whatever value it carries:
// IsStructural stops at this boundary, so a consumer that reports ErrStructural,
// Invalid or TruncatedError verbatim is still classified as content-originated.
// Unwrap reports the consumer's own error, so errors.Is and errors.As still reach
// its sentinels and types through the wrapper unchanged -- that reachability is
// deliberate, and only the classification question stops here.
//
// The distinction is what a consumer with a recovery strategy needs: scanning bytes
// forward for the next plausible element can only be justified after a structural
// failure, because only then has the cursor lost the ability to locate the next
// element. A content error is terminal -- see ext/fragment.WithResync, whose whole
// gate is IsStructural.
//
// ID and Offset locate the element the verdict is about, so the failure is
// findable in the stream without the consumer restating it.
type ContentError struct {
	ID     ElementID
	Offset int64
	Err    error
}

func (e *ContentError) Error() string {
	return fmt.Sprintf("content of element %s at offset %d: %v", e.ID, e.Offset, e.Err)
}

func (e *ContentError) Unwrap() error { return e.Err }

// NewContentError marks err as a consumer's verdict about the content of the
// element with the given ID at the given absolute offset; see ContentError. A nil
// err stays nil, so it can wrap a call's result directly.
func NewContentError(id ElementID, offset int64, err error) error {
	if err == nil {
		return nil
	}
	return &ContentError{ID: id, Offset: offset, Err: err}
}

// structuralSentinel is a sentinel that reports itself as ErrStructural. Every
// typed error below already unwraps to one of these sentinels, so the class is
// implemented once here instead of on each type.
type structuralSentinel struct{ msg string }

func (e *structuralSentinel) Error() string { return e.msg }

// Unwrap makes errors.Is(err, ErrStructural) true for this sentinel and for
// every error that unwraps to it.
func (e *structuralSentinel) Unwrap() error { return ErrStructural }

var (
	ErrElementIDTooLong       error = &structuralSentinel{"element ID VINT exceeds maximum length"}
	ErrElementSizeTooLong     error = &structuralSentinel{"element size VINT exceeds maximum length"}
	ErrTruncated              error = &structuralSentinel{"truncated input"}
	ErrElementOverflowsParent error = &structuralSentinel{"element overflows parent"}
	ErrUnknownSizeLeaf        error = &structuralSentinel{"unknown-size element is not a master"}
	ErrPrematureClose         error = &structuralSentinel{"known-size master closed before its declared end"}
)

// PrematureCloseError reports a CloseMaster call on a known-size master whose
// declared end has not been reached yet. Explicit closure exists for a master
// with no declared end; a known-size master already carries its boundary in the
// stream, and honouring the caller here would silently reparent the master's
// remaining payload into the enclosing master and corrupt every subsequent depth
// and offset. The cursor is left unchanged, so the caller can keep reading the
// master's remaining children and reach its end normally.
//
// Offset is the absolute offset at which the close was attempted and DeclaredEnd
// the offset one past the master's declared extent, so DeclaredEnd-Offset is how
// many payload bytes were still outstanding.
type PrematureCloseError struct {
	ID          ElementID
	Offset      int64
	DeclaredEnd int64
}

func (e PrematureCloseError) Error() string {
	return fmt.Sprintf("cannot close known-size master %s at offset %d: %d payload bytes remain before its declared end offset %d",
		e.ID, e.Offset, e.DeclaredEnd-e.Offset, e.DeclaredEnd)
}

func (e PrematureCloseError) Unwrap() error {
	return ErrPrematureClose
}

// TruncatedError reports that the input ended in the middle of an element: the
// stream is structurally incomplete, whether or not that is a fault.
//
// Whether a truncated tail is expected is the CONSUMER's call and cannot be made
// here -- a live connection that ends at an arbitrary byte is normal, a finite
// body that ends mid-element is a transfer fault, and the cursor cannot tell the
// two apart. What it can do is supply the evidence, which is what the fields
// beyond Msg are for; the classification stays with the caller.
//
// EndOffset is the ABSOLUTE offset one past the last byte the cursor was fed, so
// it is where the input ended, not where the cut element began. It is measured on
// the same axis as Node.Offset and Parser.Offset, including any WithStartOffset.
//
// ID is the INNERMOST ELEMENT STILL OPEN at EndOffset, one meaning in every case:
// an element whose declared payload was cut is itself open, and an element header
// cut part-way through is inside whichever master encloses it. HasID says whether
// ID names anything at all -- false means nothing was open, which the zero ID
// cannot state on its own, and it is reachable, since input may end mid-header at
// the top level.
//
// InHeader distinguishes the two cuts WITHOUT reading Msg, because the cut element
// is ID only when it is false:
//
//   - InHeader false: the input ended inside a declared PAYLOAD, so ID is the
//     element that was cut and its header had been read in full.
//   - InHeader true: the input ended inside an element HEADER, so the cut element
//     has NO ID yet -- the ID VINT is part of what was lost -- and none is invented
//     for it. ID names its enclosing master, or nothing when HasID is false.
//
// Msg says the same thing in prose for a human reader, and is never the way to
// tell: classifying by message text is what these fields exist to replace.
// Error() is exactly "truncated input" or "truncated input: <Msg>" and carries
// none of the evidence, so a message-matching consumer keeps working and a
// diagnosing one reads fields.
//
// NOT EVERY SHORT INPUT ARRIVES HERE. A truncated tail that happens to end on an
// element boundary INSIDE a known-size master is not cut mid-element at all; the
// master's declared end is simply never reached, which the cursor reports as an
// Invalid, not as a TruncatedError. Such an input carries no evidence of this
// shape, so a consumer that classifies a short tail handles both errors.
type TruncatedError struct {
	Msg string
	// EndOffset is the absolute offset at which the input ended.
	EndOffset int64
	// ID is the innermost element open at EndOffset; consult HasID first.
	ID ElementID
	// HasID reports whether ID names an element. False means no element was open.
	HasID bool
	// InHeader reports whether the cut fell inside an element header rather than
	// inside a declared payload, which is what decides whether ID is the cut
	// element or merely its enclosing master.
	InHeader bool
}

func (e TruncatedError) Error() string {
	if e.Msg == "" {
		return ErrTruncated.Error()
	}
	return fmt.Sprintf("%s: %s", ErrTruncated, e.Msg)
}

func (e TruncatedError) Unwrap() error {
	return ErrTruncated
}

type ElementOverflowError struct {
	ChildID   ElementID
	ChildEnd  int64
	ParentID  ElementID
	ParentEnd int64
}

func (e ElementOverflowError) Error() string {
	return fmt.Sprintf("element %s ends at offset %d beyond parent %s end offset %d",
		e.ChildID, e.ChildEnd, e.ParentID, e.ParentEnd)
}

func (e ElementOverflowError) Unwrap() error {
	return ErrElementOverflowsParent
}

// UnknownSizeLeafError reports an element that declares an unknown size while
// the classifier does not treat it as a master. EBML reserves the unknown size
// for master elements: a non-master element of unknown size has no end, so
// neither its payload nor the element after it can be located, and the stream
// cannot be read past it.
//
// It is a recoverable diagnosis rather than a corrupt-stream verdict: the most
// common cause is a master element the classifier does not know (a vendor or
// private element), which is read as a binary leaf. Kind reports what the
// classifier said, and registering that ID as a master -- see
// matroska.Registry.Register -- turns the element into one the cursor descends
// into. Offset locates the element's first header byte in the stream, so a
// consumer can resynchronize or re-read from there.
type UnknownSizeLeafError struct {
	ID     ElementID
	Offset int64 // absolute offset of the element's first header byte
	Kind   Kind  // kind the classifier reported for ID
}

func (e UnknownSizeLeafError) Error() string {
	return fmt.Sprintf("element %s at offset %d declares unknown size but is classified as %s, not master",
		e.ID, e.Offset, e.Kind)
}

func (e UnknownSizeLeafError) Unwrap() error {
	return ErrUnknownSizeLeaf
}

type VINTLengthError struct {
	What   string
	Length int
	Max    int
	Cause  error
}

func (e VINTLengthError) Error() string {
	return fmt.Sprintf("%s VINT length %d exceeds maximum %d", e.What, e.Length, e.Max)
}

// Unwrap reports Cause, which is ErrElementIDTooLong or ErrElementSizeTooLong.
// An over-long VINT is structural whatever the length concerned, so a value
// constructed without a Cause still matches ErrStructural.
func (e VINTLengthError) Unwrap() error {
	if e.Cause == nil {
		return ErrStructural
	}
	return e.Cause
}

// NeedMoreData reports that the operation needs at least MinBytes more input
// bytes to decide. It is flow control, not corruption: IsStructural is false for
// it, and the answer to it is the next Feed -- or Finalize, once the input is over.
type NeedMoreData struct {
	MinBytes int
}

func (e NeedMoreData) Error() string {
	return fmt.Sprintf("need more data: min_bytes=%d", e.MinBytes)
}

// Invalid reports a cursor operation that does not fit the current state, or a
// stream shape no other typed error covers. It is structural: it unwraps to
// ErrStructural and IsStructural is true for it.
type Invalid struct {
	Msg string
}

func (e Invalid) Error() string {
	if e.Msg == "" {
		return "invalid"
	}
	return "invalid: " + e.Msg
}

// Unwrap reports ErrStructural: Invalid has no sentinel of its own, so it joins
// the structural class directly.
func (e Invalid) Unwrap() error { return ErrStructural }
