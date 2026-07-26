package tree

import (
	"bytes"
	"fmt"
	"io"

	"github.com/yacchi/ebml/impl/go/parser"
	"github.com/yacchi/ebml/impl/go/writer"
)

// MarshalError reports an element that cannot be written back to bytes. ID and
// Offset identify it as the tree retained it, Desc is its Describe text, and the
// wrapped error is the reason, so errors.Is reaches ErrTruncatedPayload for an
// elided payload and writer.ErrInvalidID or writer.ErrSizeRange for an element that
// no EBML encoding can express.
type MarshalError struct {
	ID     parser.ElementID
	Offset int64
	Desc   string
	Err    error
}

func (e *MarshalError) Error() string {
	name := e.Desc
	if name == "" {
		name = e.ID.String()
	}
	return fmt.Sprintf("marshal %s at offset %d: %v", name, e.Offset, e.Err)
}

func (e *MarshalError) Unwrap() error { return e.Err }

// Marshal writes roots and their subtrees to w, in the order given, as one EBML
// byte stream.
//
// # Byte-exactness
//
// Parse followed by Marshal reproduces the parsed bytes EXACTLY, with one
// precondition: nothing was truncated, i.e. the tree was parsed without a payload
// cap (see WithMaxPayload), so every leaf still holds its bytes. That is what makes
// the retained model provably lossless.
//
// Two details carry the guarantee. A leaf payload is written verbatim, never
// re-encoded from a decoded value, so a non-canonical payload survives. And each
// header is rebuilt at its ORIGINAL width: HeaderLen minus the length of the
// element's ID VINT is the width the size VINT occupied, which is reproduced as
// such -- EBML permits a size VINT wider than the size needs, and the unknown-size
// marker exists in every width, so neither is normalised away.
//
// # A modified or hand-built tree
//
// Marshal writes what the tree now holds rather than what it declared: the size in
// each header is measured from the payload actually written, so appending a child
// or replacing a payload still produces a valid document. Only the WIDTH is taken
// from HeaderLen, and it is widened when the new size no longer fits, or when
// HeaderLen is absent -- as on a hand-built Element, whose headers therefore come
// out minimal. An element is written as a master over its Children when it has any,
// and as a leaf carrying Payload otherwise; a nil element, in roots or among
// Children, is skipped.
//
// It reports a *MarshalError for an element it cannot write -- one whose payload a
// retention cap elided, one whose ID is not a well-formed EBML element ID, or one
// too large for any EBML size VINT -- and the sink's own error for a failed write.
func Marshal(w io.Writer, roots ...*Element) error {
	for _, root := range roots {
		if root == nil {
			continue
		}
		b, err := encodeElement(root)
		if err != nil {
			return err
		}
		if _, err := w.Write(b); err != nil {
			return err
		}
	}
	return nil
}

// MarshalBytes returns the EBML encoding of roots and their subtrees. It is
// Marshal into memory; the byte-exactness guarantee and the error cases documented
// there apply unchanged.
func MarshalBytes(roots ...*Element) ([]byte, error) {
	var buf bytes.Buffer
	if err := Marshal(&buf, roots...); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// encodeElement returns the complete on-wire bytes of e and its subtree: its
// header followed by its payload.
func encodeElement(e *Element) ([]byte, error) {
	payload, err := encodePayload(e)
	if err != nil {
		return nil, err
	}
	header, err := encodeHeader(e, int64(len(payload)))
	if err != nil {
		return nil, err
	}
	out := make([]byte, 0, len(header)+len(payload))
	out = append(out, header...)
	return append(out, payload...), nil
}

// encodePayload returns the element's payload bytes: its children's encodings for
// a master, and the retained bytes for a leaf.
func encodePayload(e *Element) ([]byte, error) {
	if e.Truncated {
		return nil, e.marshalError(ErrTruncatedPayload)
	}
	if len(e.Children) == 0 {
		return e.Payload, nil
	}
	var out []byte
	for _, child := range e.Children {
		if child == nil {
			continue
		}
		b, err := encodeElement(child)
		if err != nil {
			return nil, err
		}
		out = append(out, b...)
	}
	return out, nil
}

// encodeHeader returns the element's ID VINT followed by its size VINT for a
// payload of n bytes, keeping the size VINT at the width the element was read with
// whenever that width still fits.
func encodeHeader(e *Element, n int64) ([]byte, error) {
	if !writer.ValidID(e.ID) {
		return nil, e.marshalError(&writer.InvalidIDError{ID: e.ID})
	}
	id := writer.EncodeID(e.ID)
	// The retained header length is the ID VINT plus the size VINT, so the
	// difference is the width that size VINT occupied. It is out of range on an
	// element that was never parsed, which then falls back to the minimal width.
	width := e.HeaderLen - len(id)
	inRange := width >= 1 && width <= writer.MaxSizeWidth

	if e.Size == parser.UnknownSize {
		if !inRange {
			width = writer.MaxSizeWidth // the conventional 8-byte marker
		}
		size, err := writer.UnknownSizeVINTWidth(width)
		if err != nil {
			return nil, e.marshalError(err)
		}
		return append(id, size...), nil
	}

	if inRange {
		// A payload that outgrew the original width falls through to a wider one:
		// a modified tree stays writable, at the cost of the byte-exactness that
		// modifying it gave up anyway.
		if size, err := writer.EncodeSizeWidth(n, width); err == nil {
			return append(id, size...), nil
		}
	}
	minimal, err := writer.SizeWidth(n)
	if err != nil {
		return nil, e.marshalError(err)
	}
	size, err := writer.EncodeSizeWidth(n, minimal)
	if err != nil {
		return nil, e.marshalError(err)
	}
	return append(id, size...), nil
}

// marshalError names this element in a failure Marshal reports.
func (e *Element) marshalError(err error) error {
	return &MarshalError{ID: e.ID, Offset: e.Offset, Desc: e.Describe(), Err: err}
}
