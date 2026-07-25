package parser

import (
	"errors"
	"fmt"
	"testing"
)

// TestErrStructuralCoversEveryCursorFailure checks the class marker: every
// structural failure the cursor itself raises matches ErrStructural, so a consumer
// can recognize "these bytes cannot be read as EBML" without listing types or
// matching messages.
func TestErrStructuralCoversEveryCursorFailure(t *testing.T) {
	cases := []struct {
		name string
		err  error
	}{
		{"element_id_vint_too_long", VINTLengthError{
			What: "element ID", Length: 9, Max: MaxElementIDLength, Cause: ErrElementIDTooLong,
		}},
		{"element_size_vint_too_long", VINTLengthError{
			What: "element size", Length: 9, Max: MaxElementSizeLength, Cause: ErrElementSizeTooLong,
		}},
		{"vint_length_without_cause", VINTLengthError{What: "element ID", Length: 9, Max: 4}},
		{"truncated", TruncatedError{Msg: "element header"}},
		{"element_overflows_parent", ElementOverflowError{
			ChildID: 0xA3, ChildEnd: 40, ParentID: 0x1F43B675, ParentEnd: 32,
		}},
		{"unknown_size_leaf", UnknownSizeLeafError{ID: 0xA3, Offset: 7, Kind: KindBinary}},
		{"premature_close", PrematureCloseError{ID: 0x1F43B675, Offset: 10, DeclaredEnd: 40}},
		{"invalid", Invalid{Msg: "master stack is empty"}},
		{"invalid_zero_value", Invalid{}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if !IsStructural(c.err) {
				t.Fatalf("IsStructural(%#v) = false, want true", c.err)
			}
			if !errors.Is(c.err, ErrStructural) {
				t.Fatalf("errors.Is(%#v, ErrStructural) = false, want true", c.err)
			}
			// Wrapping must not hide the class, which is how it survives the
			// layers a consumer stacks on top of the cursor.
			wrapped := fmt.Errorf("at offset 7: %w", c.err)
			if !IsStructural(wrapped) {
				t.Fatal("the structural class did not survive fmt.Errorf wrapping")
			}
			if !errors.Is(wrapped, ErrStructural) {
				t.Fatal("the structural sentinel did not survive fmt.Errorf wrapping")
			}
		})
	}

	// The sentinels themselves are structural too, so a caller may classify one
	// it kept without re-deriving the typed error it came from.
	for _, sentinel := range []error{
		ErrElementIDTooLong, ErrElementSizeTooLong, ErrTruncated,
		ErrElementOverflowsParent, ErrUnknownSizeLeaf, ErrPrematureClose,
		ErrStructural,
	} {
		if !errors.Is(sentinel, ErrStructural) {
			t.Fatalf("sentinel %v does not match ErrStructural", sentinel)
		}
		if !IsStructural(sentinel) {
			t.Fatalf("IsStructural(%v) = false, want true", sentinel)
		}
	}

	// Anything unrelated is not structural: the predicate must not answer yes by
	// default for an error it cannot recognize.
	if IsStructural(errors.New("unrelated")) {
		t.Fatal("IsStructural(unrelated error) = true, want false")
	}
	if IsStructural(nil) {
		t.Fatal("IsStructural(nil) = true, want false")
	}
}

// TestStructuralSentinelsStayDistinct checks the class marker did not merge the
// individual diagnoses: each sentinel still identifies only its own failure.
func TestStructuralSentinelsStayDistinct(t *testing.T) {
	err := error(TruncatedError{Msg: "element payload"})
	if !errors.Is(err, ErrTruncated) {
		t.Fatal("TruncatedError no longer matches ErrTruncated")
	}
	if errors.Is(err, ErrUnknownSizeLeaf) {
		t.Fatal("TruncatedError must not match another sentinel")
	}
	if got, want := err.Error(), "truncated input: element payload"; got != want {
		t.Fatalf("Error() = %q, want %q (the class marker must not leak into messages)", got, want)
	}
}

// TestNeedMoreDataIsNotStructural checks the one condition that must never be
// taken for corruption: needing more input is flow control, and a consumer that
// resynchronized on it would throw away perfectly good bytes.
func TestNeedMoreDataIsNotStructural(t *testing.T) {
	err := error(NeedMoreData{MinBytes: 3})
	if IsStructural(err) {
		t.Fatal("IsStructural(NeedMoreData) = true, want false: it is flow control")
	}
	if errors.Is(err, ErrStructural) {
		t.Fatal("NeedMoreData must not match ErrStructural")
	}
	if IsStructural(fmt.Errorf("peek: %w", err)) {
		t.Fatal("a wrapped NeedMoreData must not be classified structural either")
	}
	if errors.Is(fmt.Errorf("peek: %w", err), ErrStructural) {
		t.Fatal("a wrapped NeedMoreData must not match ErrStructural either")
	}
	var nmd NeedMoreData
	if !errors.As(err, &nmd) || nmd.MinBytes != 3 {
		t.Fatalf("errors.As lost NeedMoreData: %v", err)
	}
}

// TestHandlerErrorIsNotStructural checks the second half of the classification: an
// error the CONSUMER returned is reported as handler-originated, is not structural,
// and still carries the handler's own error for errors.Is/errors.As.
func TestHandlerErrorIsNotStructural(t *testing.T) {
	sentinel := errors.New("cannot decode this payload")
	err := error(handlerErr(OpPayload, Node{ID: idSimpleBlock, Offset: 42}, sentinel))

	if IsStructural(err) {
		t.Fatal("a handler error must not be structural: the stream's shape was fine")
	}
	if errors.Is(err, ErrStructural) {
		t.Fatal("a handler error must not match ErrStructural: the stream's shape was fine")
	}
	if !errors.Is(err, sentinel) {
		t.Fatalf("the handler's own error is not reachable through the wrapper: %v", err)
	}
	var he *HandlerError
	if !errors.As(err, &he) {
		t.Fatalf("errors.As did not find *HandlerError in %v (%T)", err, err)
	}
	if he.Op != OpPayload || he.Node.ID != idSimpleBlock || he.Node.Offset != 42 {
		t.Fatalf("HandlerError = %+v, want the payload event of %s at offset 42", he, idSimpleBlock)
	}
	if !errors.Is(he.Err, sentinel) {
		t.Fatalf("HandlerError.Err = %v, want the handler's error", he.Err)
	}

	// A structural failure is the other class and must not look handler-originated.
	if errors.As(error(TruncatedError{}), &he) {
		t.Fatal("a structural error must not be reported as a handler error")
	}
	if handlerErr(OpClose, Node{}, nil) != nil {
		t.Fatal("handlerErr(nil) must stay nil")
	}
}

// TestScannerClassifiesFeedErrors checks the classification end to end, on the two
// errors a Scanner caller actually receives: a handler's refusal and corrupt bytes.
func TestScannerClassifiesFeedErrors(t *testing.T) {
	raw := topologyBasic(t)

	t.Run("handler_error", func(t *testing.T) {
		sentinel := errors.New("the consumer refuses this element")
		h := &HandlerFuncs{LeafFunc: func(n Node) (Action, error) {
			if n.ID == idSimpleBlock {
				return 0, sentinel
			}
			return SkipPayload, nil
		}}
		s := NewScanner(h, testKindClassifier)
		err := feedChunks(s, raw, 7)
		if !errors.Is(err, sentinel) {
			t.Fatalf("Feed error = %v, want the handler's sentinel", err)
		}
		if IsStructural(err) {
			t.Fatalf("Feed error %v is classified structural, but the bytes were readable", err)
		}
		var he *HandlerError
		if !errors.As(err, &he) {
			t.Fatalf("Feed error = %v (%T), want *HandlerError", err, err)
		}
		if he.Op != OpLeaf || he.Node.ID != idSimpleBlock {
			t.Fatalf("HandlerError = %+v, want the leaf event of %s", he, idSimpleBlock)
		}
	})

	t.Run("structural_error", func(t *testing.T) {
		// A leading 0x00 byte encodes a 9-byte element ID VINT, over the 4-byte
		// maximum, so no element can be located from there on.
		s := NewScanner(&HandlerFuncs{}, testKindClassifier)
		err := feedChunks(s, append(append([]byte(nil), raw...), 0x00, 0x11, 0x22), 7)
		if err == nil {
			err = s.Finalize()
		}
		if !IsStructural(err) {
			t.Fatalf("Feed error = %v, want a structural classification", err)
		}
		if !errors.Is(err, ErrElementIDTooLong) {
			t.Fatalf("Feed error = %v, want the ID-length diagnosis as well", err)
		}
		var he *HandlerError
		if errors.As(err, &he) {
			t.Fatalf("Feed error = %v, want no handler origin", err)
		}
	})

	t.Run("close_error_from_finalize", func(t *testing.T) {
		sentinel := errors.New("the consumer refuses this close")
		h := &HandlerFuncs{CloseFunc: func(n Node) error {
			if n.ID == idSegment {
				return sentinel
			}
			return nil
		}}
		s := NewScanner(h, testKindClassifier)
		if err := feedChunks(s, raw, 7); err != nil {
			t.Fatalf("Feed error = %v", err)
		}
		err := s.Finalize()
		var he *HandlerError
		if !errors.As(err, &he) || he.Op != OpClose || he.Node.ID != idSegment {
			t.Fatalf("Finalize error = %v, want a %s HandlerError for the Segment", err, OpClose)
		}
		if IsStructural(err) {
			t.Fatalf("Finalize error %v is classified structural", err)
		}
		if !errors.Is(err, sentinel) {
			t.Fatalf("Finalize error = %v, want the handler's sentinel", err)
		}
	})
}

// TestHandlerReturningCursorErrorIsNotStructural checks the boundary rule that a
// sentinel alone cannot express: a Handler is free to return one of the cursor's
// OWN structural error values, and the result must still be classified as
// handler-originated. errors.Is walks the whole chain and therefore reports
// ErrStructural here, which is exactly why IsStructural exists and is the
// documented test.
func TestHandlerReturningCursorErrorIsNotStructural(t *testing.T) {
	raw := topologyBasic(t)

	cases := []struct {
		name string
		err  error
	}{
		{"err_structural_verbatim", ErrStructural},
		{"invalid", Invalid{Msg: "the consumer's own verdict"}},
		{"truncated", TruncatedError{Msg: "the consumer's own verdict"}},
		{"wrapping_a_cursor_error", fmt.Errorf("cannot decode: %w", TruncatedError{Msg: "payload"})},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			h := &HandlerFuncs{LeafFunc: func(n Node) (Action, error) {
				if n.ID == idSimpleBlock {
					return 0, c.err
				}
				return SkipPayload, nil
			}}
			s := NewScanner(h, testKindClassifier)
			err := feedChunks(s, raw, 7)
			if err == nil {
				t.Fatal("Feed of a stream the handler refuses returned no error")
			}

			// The classification: the bytes were read perfectly, so the failure
			// belongs to the consumer whatever value it chose.
			if IsStructural(err) {
				t.Fatalf("IsStructural(%v) = true, want false: the handler refused readable bytes", err)
			}
			// Wrapping by an outer layer must not change that.
			if IsStructural(fmt.Errorf("assembling: %w", err)) {
				t.Fatal("a wrapped handler error was classified structural")
			}

			// Reachability is a separate question and stays intact: the origin
			// wrapper and the handler's own value are both still findable.
			var he *HandlerError
			if !errors.As(err, &he) {
				t.Fatalf("errors.As did not find *HandlerError in %v (%T)", err, err)
			}
			if he.Op != OpLeaf || he.Node.ID != idSimpleBlock {
				t.Fatalf("HandlerError = %+v, want the leaf event of %s", he, idSimpleBlock)
			}
			if !errors.Is(err, c.err) {
				t.Fatalf("the handler's own error %v is not reachable through the wrapper: %v", c.err, err)
			}
			var truncated TruncatedError
			if _, isTruncated := c.err.(TruncatedError); isTruncated && !errors.As(err, &truncated) {
				t.Fatalf("errors.As no longer reaches the handler's TruncatedError: %v", err)
			}
		})
	}
}
