package parser_test

import (
	"errors"
	"testing"

	"github.com/yacchi/ebml/impl/go/internal/ebmltest"
	. "github.com/yacchi/ebml/impl/go/parser"
)

const (
	truncSegment ElementID = 0x18538067
	truncLeaf    ElementID = 0x4487
)

func truncClassifier(id ElementID) Kind {
	if id == truncSegment {
		return KindMaster
	}
	return KindBinary
}

// finalizeTruncated feeds raw to a cursor, drains every event it completes, and
// returns the TruncatedError Finalize reports. It fails the test when the input
// turns out not to be truncated at all, so a case that stops evidencing anything
// cannot pass by accident.
func finalizeTruncated(t *testing.T, raw []byte) TruncatedError {
	t.Helper()
	c := NewCursor(truncClassifier)
	c.Feed(raw)
	for {
		_, err := c.Next()
		if err == nil {
			continue
		}
		var need NeedMoreData
		if !errors.As(err, &need) {
			t.Fatalf("Next: %v", err)
		}
		break
	}
	err := c.Finalize()
	var truncated TruncatedError
	if !errors.As(err, &truncated) {
		t.Fatalf("Finalize of a truncated stream = %v, want TruncatedError", err)
	}
	if !IsStructural(err) {
		t.Fatalf("IsStructural(%v) = false, want true", err)
	}
	return truncated
}

// TestTruncatedErrorEvidence pins the fields a consumer classifies a truncated
// tail by: where the input ended, and what was open there. The offsets are the
// number of bytes fed, which the case controls directly.
func TestTruncatedErrorEvidence(t *testing.T) {
	// One leaf inside an unknown-size Segment, then a second leaf whose header is
	// where the cuts fall.
	first := ebmltest.Leaf(truncLeaf, []byte{0x01, 0x02})
	second := ebmltest.Leaf(truncLeaf, []byte{0x03, 0x04, 0x05, 0x06})
	secondLen := len(ebmltest.Encode(second))
	document := ebmltest.Encode(ebmltest.UnknownMaster(truncSegment, first, second))

	cases := []struct {
		name    string
		raw     []byte
		wantMsg string
		wantID  ElementID
		wantHas bool
		wantHdr bool
	}{
		{
			// Cut one byte into the second leaf's header: the ID VINT is
			// incomplete, so the open element is the Segment around it.
			name:    "inside_header",
			raw:     document[:len(document)-secondLen+1],
			wantMsg: "truncated input: element header",
			wantID:  truncSegment,
			wantHas: true,
			wantHdr: true,
		},
		{
			// Cut inside the second leaf's declared payload: its header arrived in
			// full, so the open element is the leaf itself.
			name:    "inside_payload",
			raw:     document[:len(document)-2],
			wantMsg: "truncated input: element payload",
			wantID:  truncLeaf,
			wantHas: true,
			wantHdr: false,
		},
		{
			// Cut one byte into a top-level element's header, with no master open.
			name:    "inside_header_nothing_open",
			raw:     ebmltest.Encode(second)[:1],
			wantMsg: "truncated input: element header",
			wantHas: false,
			wantHdr: true,
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			truncated := finalizeTruncated(t, c.raw)
			if got := int(truncated.EndOffset); got != len(c.raw) {
				t.Errorf("EndOffset = %d, want %d (the bytes fed)", got, len(c.raw))
			}
			if truncated.HasID != c.wantHas {
				t.Errorf("HasID = %t, want %t", truncated.HasID, c.wantHas)
			}
			if c.wantHas && truncated.ID != c.wantID {
				t.Errorf("ID = %s, want %s", truncated.ID, c.wantID)
			}
			if !c.wantHas && truncated.ID != 0 {
				t.Errorf("ID = %s, want the zero value when nothing was open", truncated.ID)
			}
			// Which of the two cuts occurred is a field, never the message: a
			// consumer must never have to match Msg to know what ID names.
			if truncated.InHeader != c.wantHdr {
				t.Errorf("InHeader = %t, want %t", truncated.InHeader, c.wantHdr)
			}
			// The evidence is carried in fields only: the message is what it
			// always was, and golden traces and consumers depend on that.
			if got := truncated.Error(); got != c.wantMsg {
				t.Errorf("Error() = %q, want %q", got, c.wantMsg)
			}
		})
	}
}

// TestTruncatedErrorEvidenceCountsStartOffset checks EndOffset is on the same
// absolute axis as Node.Offset, WithStartOffset included -- a consumer reading a
// range request's body would otherwise report an offset into its own slice.
func TestTruncatedErrorEvidenceCountsStartOffset(t *testing.T) {
	const start = 4096
	raw := ebmltest.Encode(ebmltest.Leaf(truncLeaf, []byte{0x01, 0x02, 0x03}))
	c := NewCursor(truncClassifier, WithStartOffset(start))
	c.Feed(raw[:len(raw)-1])
	for {
		_, err := c.Next()
		if err == nil {
			continue
		}
		var need NeedMoreData
		if !errors.As(err, &need) {
			t.Fatalf("Next: %v", err)
		}
		break
	}
	var truncated TruncatedError
	if err := c.Finalize(); !errors.As(err, &truncated) {
		t.Fatalf("Finalize of a truncated stream = %v, want TruncatedError", err)
	}
	if want := int64(start + len(raw) - 1); truncated.EndOffset != want {
		t.Errorf("EndOffset = %d, want %d", truncated.EndOffset, want)
	}
	if !truncated.HasID || truncated.ID != truncLeaf {
		t.Errorf("ID = %s (HasID %t), want %s", truncated.ID, truncated.HasID, truncLeaf)
	}
}
