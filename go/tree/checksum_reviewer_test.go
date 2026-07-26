package tree_test

import (
	"bytes"
	"errors"
	"testing"

	"github.com/yacchi/ebml/crc"
	"github.com/yacchi/ebml/internal/ebmltest"
	"github.com/yacchi/ebml/matroska"
	"github.com/yacchi/ebml/parser"
	"github.com/yacchi/ebml/tree"
	"github.com/yacchi/ebml/writer"
)

// TestVerifyChecksumSkipsNilChildInCoverageAndPosition covers a Children slice
// carrying a literal nil entry ahead of the CRC-32 element -- the shape Marshal's
// own doc says it tolerates ("a nil element ... among Children, is skipped") but
// which none of the existing checksum tests actually construct. VerifyChecksum
// walks e.Children directly, so a nil that Marshal skips but VerifyChecksum's own
// counters mishandled would either panic dereferencing it or miscount Index against
// what Marshal actually wrote; this proves neither happens and that the CRC-32
// element is still recognised as effectively first, matching what the re-marshalled
// bytes -- which also drop the nil -- were summed over.
func TestVerifyChecksumSkipsNilChildInCoverageAndPosition(t *testing.T) {
	title := ebmltest.UTF8(matroska.IDTitle, "covered")
	info := parseOne(t, ebmltest.Encode(
		ebmltest.Master(matroska.IDInfo, checksumOver(title), title),
	))

	// Splice a nil in front of the CRC-32 element, exactly the shape a caller gets
	// from e.g. clearing a slot in place rather than removing it.
	info.Children = append([]*tree.Element{nil}, info.Children...)

	if err := info.VerifyChecksum(); err != nil {
		t.Fatalf("VerifyChecksum with a leading nil child: %v", err)
	}

	// Now splice the nil between the CRC-32 element and Title instead: the CRC-32
	// element is still the first REAL child, so the position check must still pass.
	info2 := parseOne(t, ebmltest.Encode(
		ebmltest.Master(matroska.IDInfo, checksumOver(title), title),
	))
	info2.Children = []*tree.Element{info2.Children[0], nil, info2.Children[1]}
	if err := info2.VerifyChecksum(); err != nil {
		t.Fatalf("VerifyChecksum with an interior nil child: %v", err)
	}
}

// TestVerifyChecksumMultipleOutranksUnavailable covers two CRC-32 children where
// the FIRST is truncated by a retention cap and the second is intact: RFC 8794
// already makes this document ambiguous (which one is the real checksum?), and
// VerifyChecksum's doc only states the mismatch-vs-position precedence, leaving
// multiple-vs-unavailable unstated. This proves, rather than assumes, which one the
// implementation actually reports: *MultipleChecksumsError, not
// *ChecksumUnavailableError, because the ambiguity is detected before the first
// candidate's own payload is even inspected.
func TestVerifyChecksumMultipleOutranksUnavailable(t *testing.T) {
	title := ebmltest.UTF8(matroska.IDTitle, "covered")
	raw := ebmltest.Encode(
		ebmltest.Master(matroska.IDInfo, checksumOver(title), checksumOver(title), title),
	)

	// Cap retention so tightly that only the first CRC-32 element's four bytes
	// survive; the second CRC-32 element and Title are both truncated.
	info := parseOne(t, raw, tree.WithMaxPayload(crc.Size))

	err := info.VerifyChecksum()
	if err == nil {
		t.Fatal("VerifyChecksum accepted two CRC-32 children")
	}
	var multiple *tree.MultipleChecksumsError
	if !errors.As(err, &multiple) {
		t.Fatalf("errors.As did not reach *tree.MultipleChecksumsError: %v", err)
	}
	if multiple.Count != 2 {
		t.Errorf("MultipleChecksumsError.Count = %d, want 2", multiple.Count)
	}
	var unavailable *tree.ChecksumUnavailableError
	if errors.As(err, &unavailable) {
		t.Errorf("ChecksumUnavailableError was reported instead of the ambiguity: %v", err)
	}
}

// TestVerifyChecksumUnmarshalableCoveredChild covers a covered child whose ID is
// not well-formed EBML (see writer.ValidID) -- a shape that can only arise from a
// hand-built or mutated tree, never from tree.Parse. VerifyChecksum's own doc says
// this is "the tree's own defect" and is "passed through as Marshal stated it",
// distinct from both the content class (parser.NewContentError) and the structural
// one. This proves that claim rather than reading it: the returned error unwraps to
// *writer.InvalidIDError, is NOT wrapped in a content marker (errors.As misses
// *tree.MultipleChecksumsError-style content types entirely; more directly, the
// concrete error is *tree.MarshalError, which contentErr never produces), and
// parser.IsStructural is false for it too, since nothing about the cursor or the
// document's structural position is implicated by an in-memory mutation.
func TestVerifyChecksumUnmarshalableCoveredChild(t *testing.T) {
	const illFormedID parser.ElementID = 0x1234 // marks 4 bytes, fits in 2: not ValidID
	if writer.ValidID(illFormedID) {
		t.Fatalf("test fixture is malformed: 0x%X is accepted by writer.ValidID", illFormedID)
	}

	title := ebmltest.UTF8(matroska.IDTitle, "covered")
	info := parseOne(t, ebmltest.Encode(
		ebmltest.Master(matroska.IDInfo, checksumOver(title), title),
	))

	// Replace the covered child with one carrying an ID no EBML encoding can
	// express, so MarshalBytes(covered...) fails for a reason that is neither a
	// retention cap nor a checksum verdict.
	info.Children[1] = &tree.Element{ID: illFormedID, Payload: []byte("covered")}

	err := info.VerifyChecksum()
	if err == nil {
		t.Fatal("VerifyChecksum accepted a covered child with an ill-formed ID")
	}
	var marshalErr *tree.MarshalError
	if !errors.As(err, &marshalErr) {
		t.Fatalf("errors.As did not reach *tree.MarshalError: %v", err)
	}
	if !errors.Is(err, writer.ErrInvalidID) {
		t.Errorf("errors.Is did not reach writer.ErrInvalidID: %v", err)
	}
	if parser.IsStructural(err) {
		t.Errorf("IsStructural(%v) = true, want false: an in-memory mutation is not a structural failure", err)
	}
	var unavailable *tree.ChecksumUnavailableError
	if errors.As(err, &unavailable) {
		t.Errorf("an ill-formed ID was reported as ChecksumUnavailableError, not a MarshalError: %v", err)
	}
	var mismatch *crc.MismatchError
	if errors.As(err, &mismatch) {
		t.Errorf("an ill-formed ID was reported as a checksum mismatch, which never ran: %v", err)
	}
}

// TestVerifyChecksumRoundTripThroughWriter is the one test in the module that
// actually calls tree.VerifyChecksum on a document package writer produced with
// WithChecksum. writer/adversarial_crc_test.go's
// TestChecksummedDocumentParsesCleanlyWithTreeParse recomputes the checksum
// independently instead of calling VerifyChecksum, and no test under tree/ starts
// from a written document rather than one hand-assembled by ebmltest -- so the
// public write-then-verify path RFC 8794 exists to support was, until this test,
// asserted only piecewise, never end to end through both packages' real APIs.
func TestVerifyChecksumRoundTripThroughWriter(t *testing.T) {
	var buf bytes.Buffer
	w := writer.New(&buf)
	if err := w.StartMaster(matroska.IDInfo, writer.Buffered(), writer.WithChecksum(matroska.IDCRC32)); err != nil {
		t.Fatalf("StartMaster: %v", err)
	}
	if err := w.UTF8(matroska.IDTitle, "written by package writer"); err != nil {
		t.Fatalf("UTF8: %v", err)
	}
	if err := w.String(matroska.IDMuxingApp, "ebml-test"); err != nil {
		t.Fatalf("String: %v", err)
	}
	if err := w.EndMaster(); err != nil {
		t.Fatalf("EndMaster: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	info := parseOne(t, buf.Bytes())
	if len(info.Children) != 3 {
		t.Fatalf("got %d children, want 3 (CRC-32, Title, MuxingApp)", len(info.Children))
	}
	if info.Children[0].ID != matroska.IDCRC32 {
		t.Fatalf("first child is %s, want the CRC-32 element", info.Children[0].ID)
	}
	if err := info.VerifyChecksum(); err != nil {
		t.Fatalf("VerifyChecksum on a document package writer produced: %v", err)
	}

	// Damage the payload after writing and confirm the SAME public entry point
	// used above now reports the mismatch: the round trip catches real corruption,
	// not just a value it computed itself both times.
	corrupted := append([]byte(nil), buf.Bytes()...)
	titleValue := []byte("written by package writer")
	idx := indexOf(corrupted, titleValue)
	if idx < 0 {
		t.Fatal("could not locate the Title payload in the written document")
	}
	corrupted[idx] ^= 0x01

	damaged := parseOne(t, corrupted)
	err := damaged.VerifyChecksum()
	if err == nil {
		t.Fatal("VerifyChecksum accepted a written-then-corrupted document")
	}
	var mismatch *crc.MismatchError
	if !errors.As(err, &mismatch) {
		t.Fatalf("errors.As did not reach *crc.MismatchError: %v", err)
	}
}

// indexOf returns the first offset of needle in haystack, or -1.
func indexOf(haystack, needle []byte) int {
	for i := 0; i+len(needle) <= len(haystack); i++ {
		if string(haystack[i:i+len(needle)]) == string(needle) {
			return i
		}
	}
	return -1
}
