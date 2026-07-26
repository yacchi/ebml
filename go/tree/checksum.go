package tree

import (
	"errors"
	"fmt"

	"github.com/yacchi/ebml/crc"
	"github.com/yacchi/ebml/matroska"
	"github.com/yacchi/ebml/parser"
)

// ChecksumUnavailableError reports that an element's CRC-32 could NOT BE JUDGED,
// because the bytes it covers are not all in hand: a retention cap
// (WithMaxPayload) elided a payload somewhere in the subtree being summed, or the
// stored checksum's own payload was elided. Err is the reason, so errors.Is
// reaches ErrTruncatedPayload.
//
// It exists as a third answer because the other two would both be lies. Reporting
// a mismatch would accuse a document of damage on evidence the reader threw away
// itself, and reporting success -- silently passing an element nothing was
// verified about -- is the worst outcome available here, since the whole value of
// a checksum is that a pass means something. A caller that wants the verdict
// re-parses the same bytes without the cap.
//
// It deliberately does NOT carry parser.NewContentError's marker: the content
// classes say a consumer JUDGED an element and refused it, and a caller acting on
// that class -- ext/fragment.WithSkipContentErrors drops the offending element --
// would discard an element that may be perfectly sound.
type ChecksumUnavailableError struct {
	ID     parser.ElementID
	Offset int64
	Desc   string
	Err    error
}

func (e *ChecksumUnavailableError) Error() string {
	name := e.Desc
	if name == "" {
		name = e.ID.String()
	}
	return fmt.Sprintf("cannot verify CRC-32 of %s at offset %d: %v", name, e.Offset, e.Err)
}

func (e *ChecksumUnavailableError) Unwrap() error { return e.Err }

// MultipleChecksumsError reports a master carrying more than one CRC-32 child.
// RFC 8794 allows at most one, and there is no defensible way to proceed: the
// covered bytes are defined as the parent's data minus THE CRC-32 element, so two
// of them do not merely disagree about a value, they disagree about what was
// summed. Count is how many were found.
type MultipleChecksumsError struct {
	Count int
}

func (e *MultipleChecksumsError) Error() string {
	return fmt.Sprintf("master carries %d CRC-32 children: RFC 8794 allows at most one", e.Count)
}

// ChecksumPositionError reports a CRC-32 element that is not the first ordered
// child of its parent, which RFC 8794 section 11.3.1 requires it to be. Index is
// its 0-based position among its parent's children.
//
// It is reported only when the checksum itself is CORRECT. Verification does not
// depend on the position -- refusing to check bytes that can be checked helps
// nobody -- so a misplaced checksum is still computed, and a document that is
// merely misordered is distinguished from one that is damaged.
type ChecksumPositionError struct {
	Index int
}

func (e *ChecksumPositionError) Error() string {
	return fmt.Sprintf("CRC-32 element is child %d of its parent: RFC 8794 requires it to be the first", e.Index)
}

// VerifyChecksum verifies THIS element's stored CRC-32 against the bytes it
// covers, and nothing deeper. It returns nil when the checksum is correct, and
// nil when there is nothing to check: a nil receiver, the zero value, a leaf, and
// a master with no CRC-32 child all pass, because absence of a checksum is not a
// failure -- most masters carry none.
//
// Verification is EXPLICIT and never happens on its own. A checksum covers the
// element data AS STORED (see package crc), so only the retained model has the
// bytes to sum at all; the streaming cursor hands out a view valid until the next
// pull and could not answer the question if it wanted to. The bytes summed here
// are this element's OTHER children re-marshalled in stream order, which is exact
// because parse-then-marshal is byte-identical -- the same property Marshal
// documents, used as evidence rather than as a convenience.
//
// This is the only checksum method on Element, deliberately. Recursion is the
// caller's, because only the caller knows whether to stop at the first bad
// element or collect every one, and Walk already expresses both:
//
//	var err error
//	root.Walk(func(el *Element) bool { err = el.VerifyChecksum(); return err == nil })
//	// err is the first failure, or nil when the whole subtree verified.
//
// # Failures
//
// Every verdict about the document's bytes -- a mismatch, a payload whose length
// is not crc.Size, more than one CRC-32 child, a correct checksum in the wrong
// position -- is returned wrapped in parser.NewContentError, so
// parser.IsStructural is false for it while errors.As still reaches
// *crc.MismatchError, *crc.LengthError, *MultipleChecksumsError and
// *ChecksumPositionError. That class is not a technicality: the extents were read
// correctly, so the position of the next element is known and nothing about the
// parse is in doubt. Only this element's bytes are.
//
// When the checksum is wrong AND misplaced, the MISMATCH is reported. Both are
// true, and one answer has to be chosen; RFC 8794 does not say which, so this
// library states its choice: damage outranks disorder, because a caller acting on
// a position error would reorder bytes that cannot be trusted in the first place.
//
// When a covered payload was elided by a retention cap the answer is neither pass
// nor mismatch but *ChecksumUnavailableError; see its documentation for why
// silently passing would be the worst of the three.
func (e *Element) VerifyChecksum() error {
	if e == nil || len(e.Children) == 0 {
		// A leaf has no children and so can hold no CRC-32 element: a checksum
		// element is a CHILD of the master whose data it covers.
		return nil
	}

	var (
		stored  *Element
		index   int
		count   int
		covered []*Element
		seen    int
	)
	for _, child := range e.Children {
		if child == nil {
			// Marshal skips a nil child, so it does not occupy an ordinal here
			// either; the two views of "the children" must not disagree.
			continue
		}
		if child.ID == matroska.IDCRC32 {
			count++
			if stored == nil {
				stored, index = child, seen
			}
		} else {
			covered = append(covered, child)
		}
		seen++
	}

	if stored == nil {
		return nil
	}
	contentErr := func(err error) error {
		return parser.NewContentError(matroska.IDCRC32, stored.Offset, err)
	}
	if count > 1 {
		return contentErr(&MultipleChecksumsError{Count: count})
	}
	if stored.Truncated {
		return stored.unavailableChecksum(ErrTruncatedPayload)
	}
	want, err := crc.Decode(stored.Payload)
	if err != nil {
		return contentErr(err)
	}

	data, err := MarshalBytes(covered...)
	if err != nil {
		if errors.Is(err, ErrTruncatedPayload) {
			return e.unavailableChecksum(err)
		}
		// The tree holds an element no EBML encoding can express, so there is
		// nothing to sum and nothing about the document to report. It is the
		// tree's own defect and is passed through as Marshal stated it.
		return err
	}
	if err := crc.Verify(data, want); err != nil {
		return contentErr(err)
	}
	if index != 0 {
		return contentErr(&ChecksumPositionError{Index: index})
	}
	return nil
}

// unavailableChecksum names this element in a verification that could not reach a
// verdict.
func (e *Element) unavailableChecksum(err error) error {
	return &ChecksumUnavailableError{ID: e.ID, Offset: e.Offset, Desc: e.Describe(), Err: err}
}
