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
// boundary at the handler, and a sentinel cannot express one.
//
// Two things are deliberately NOT structural:
//
//   - NeedMoreData is not a failure at all. It is the cursor asking for the rest
//     of an element and never reaches a Scanner caller.
//   - An error a Handler returned is the consumer's own verdict about CONTENT,
//     not about the stream's shape, whatever error value the handler chose.
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
// can never stop anywhere: a Handler is free to return ErrStructural itself, or
// Invalid, or TruncatedError, or anything wrapping them, and
// errors.Is(scannerErr, ErrStructural) would then answer true even though the
// cursor read the stream's shape perfectly and the refusal was the consumer's own.
// IsStructural owns the rule instead: it walks the chain and answers false the
// moment it crosses a *HandlerError, whatever that handler error carries.
// NeedMoreData is not a failure at all, so it is false as well.
//
// A consumer classifying an error that may have passed through a Handler --
// anything out of Scanner.Feed or Scanner.Finalize -- must use this predicate.
func IsStructural(err error) bool {
	for err != nil {
		if err == ErrStructural {
			return true
		}
		switch err.(type) {
		case *HandlerError:
			// The handler boundary: a handler-originated failure is never
			// structural, so the chain below it is not consulted at all.
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

type TruncatedError struct {
	Msg string
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
// it and a Scanner absorbs it rather than returning it, since more Feed calls are
// the answer.
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
