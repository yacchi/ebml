package tree_test

import (
	"errors"
	"testing"

	"github.com/yacchi/ebml/crc"
	"github.com/yacchi/ebml/internal/ebmltest"
	"github.com/yacchi/ebml/matroska"
	"github.com/yacchi/ebml/parser"
	"github.com/yacchi/ebml/tree"
)

// checksumOver builds the CRC-32 element that covers exactly the given siblings.
// The value is computed with package crc over the bytes those siblings encode to,
// never written down as a constant: a checksum a test states by hand only proves
// the test and the implementation were wrong in the same way.
func checksumOver(siblings ...ebmltest.Node) ebmltest.Node {
	return ebmltest.Leaf(matroska.IDCRC32, crc.Encode(crc.Checksum(ebmltest.Encode(siblings...))))
}

// parseOne parses a document expected to hold exactly one top-level element.
func parseOne(t *testing.T, raw []byte, opts ...tree.Option) *tree.Element {
	t.Helper()
	roots, err := tree.Parse(raw, opts...)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if len(roots) != 1 {
		t.Fatalf("parsed %d top-level elements, want 1", len(roots))
	}
	return roots[0]
}

func TestVerifyChecksumCorrect(t *testing.T) {
	title := ebmltest.UTF8(matroska.IDTitle, "verified")
	muxing := ebmltest.String(matroska.IDMuxingApp, "ebml-test")
	info := parseOne(t, ebmltest.Encode(
		ebmltest.Master(matroska.IDInfo, checksumOver(title, muxing), title, muxing),
	))
	if err := info.VerifyChecksum(); err != nil {
		t.Fatalf("VerifyChecksum: %v", err)
	}
}

func TestVerifyChecksumMismatch(t *testing.T) {
	// One byte of a covered payload differs from what the stored checksum was
	// computed over, which is the damage a CRC-32 exists to catch.
	original := ebmltest.UTF8(matroska.IDTitle, "value-a")
	damaged := ebmltest.UTF8(matroska.IDTitle, "value-b")
	info := parseOne(t, ebmltest.Encode(
		ebmltest.Master(matroska.IDInfo, checksumOver(original), damaged),
	))

	err := info.VerifyChecksum()
	if err == nil {
		t.Fatal("VerifyChecksum accepted a damaged payload")
	}
	if parser.IsStructural(err) {
		t.Errorf("IsStructural(%v) = true, want false: a mismatch is a verdict about content", err)
	}
	var mismatch *crc.MismatchError
	if !errors.As(err, &mismatch) {
		t.Fatalf("errors.As did not reach *crc.MismatchError: %v", err)
	}
	wantStored := crc.Checksum(ebmltest.Encode(original))
	wantGot := crc.Checksum(ebmltest.Encode(damaged))
	if mismatch.Want != wantStored || mismatch.Got != wantGot {
		t.Errorf("mismatch = {Want: 0x%08X, Got: 0x%08X}, want {0x%08X, 0x%08X}",
			mismatch.Want, mismatch.Got, wantStored, wantGot)
	}
}

func TestVerifyChecksumAbsent(t *testing.T) {
	info := parseOne(t, ebmltest.Encode(
		ebmltest.Master(matroska.IDInfo, ebmltest.UTF8(matroska.IDTitle, "no checksum here")),
	))
	if err := info.VerifyChecksum(); err != nil {
		t.Fatalf("VerifyChecksum of a master carrying no CRC-32: %v", err)
	}
	// A leaf can hold no CRC-32 child at all.
	if err := info.Find(matroska.IDTitle).VerifyChecksum(); err != nil {
		t.Fatalf("VerifyChecksum of a leaf: %v", err)
	}
}

func TestVerifyChecksumUnavailableAfterRetentionCap(t *testing.T) {
	title := ebmltest.UTF8(matroska.IDTitle, "long enough to be elided")
	raw := ebmltest.Encode(ebmltest.Master(matroska.IDInfo, checksumOver(title), title))

	// The cap keeps the four-byte checksum but elides the payload it covers, so
	// the verdict cannot be reached from what was retained.
	info := parseOne(t, raw, tree.WithMaxPayload(crc.Size))
	if !info.Find(matroska.IDTitle).Truncated {
		t.Fatal("the covered child was retained in full; the cap did not bite")
	}

	err := info.VerifyChecksum()
	if err == nil {
		t.Fatal("VerifyChecksum passed an element none of whose bytes it had")
	}
	var unavailable *tree.ChecksumUnavailableError
	if !errors.As(err, &unavailable) {
		t.Fatalf("errors.As did not reach *tree.ChecksumUnavailableError: %v", err)
	}
	var mismatch *crc.MismatchError
	if errors.As(err, &mismatch) {
		t.Errorf("elided bytes were reported as a mismatch: %v", err)
	}
	if !errors.Is(err, tree.ErrTruncatedPayload) {
		t.Errorf("errors.Is did not reach ErrTruncatedPayload: %v", err)
	}
}

func TestVerifyChecksumBadPayloadLength(t *testing.T) {
	info := parseOne(t, ebmltest.Encode(
		ebmltest.Master(matroska.IDInfo,
			ebmltest.Leaf(matroska.IDCRC32, []byte{0x01, 0x02, 0x03}),
			ebmltest.UTF8(matroska.IDTitle, "covered"),
		),
	))

	err := info.VerifyChecksum()
	if err == nil {
		t.Fatal("VerifyChecksum accepted a three-byte CRC-32 payload")
	}
	if parser.IsStructural(err) {
		t.Errorf("IsStructural(%v) = true, want false", err)
	}
	var length *crc.LengthError
	if !errors.As(err, &length) {
		t.Fatalf("errors.As did not reach *crc.LengthError: %v", err)
	}
	if length.Len != 3 {
		t.Errorf("LengthError.Len = %d, want 3", length.Len)
	}
}

func TestVerifyChecksumWrongPosition(t *testing.T) {
	// RFC 8794 requires the CRC-32 element to come first. The value here is
	// correct, so the position is the only thing to report.
	title := ebmltest.UTF8(matroska.IDTitle, "covered")
	info := parseOne(t, ebmltest.Encode(
		ebmltest.Master(matroska.IDInfo, title, checksumOver(title)),
	))

	err := info.VerifyChecksum()
	if err == nil {
		t.Fatal("VerifyChecksum accepted a CRC-32 element that is not the first child")
	}
	if parser.IsStructural(err) {
		t.Errorf("IsStructural(%v) = true, want false", err)
	}
	var position *tree.ChecksumPositionError
	if !errors.As(err, &position) {
		t.Fatalf("errors.As did not reach *tree.ChecksumPositionError: %v", err)
	}
	if position.Index != 1 {
		t.Errorf("ChecksumPositionError.Index = %d, want 1", position.Index)
	}
}

func TestVerifyChecksumMismatchOutranksPosition(t *testing.T) {
	// Both faults at once: the library reports the mismatch, because damage
	// outranks disorder.
	info := parseOne(t, ebmltest.Encode(
		ebmltest.Master(matroska.IDInfo,
			ebmltest.UTF8(matroska.IDTitle, "covered"),
			ebmltest.Leaf(matroska.IDCRC32, crc.Encode(0xDEADBEEF)),
		),
	))

	err := info.VerifyChecksum()
	var mismatch *crc.MismatchError
	if !errors.As(err, &mismatch) {
		t.Fatalf("errors.As did not reach *crc.MismatchError: %v", err)
	}
	if mismatch.Want != 0xDEADBEEF {
		t.Errorf("MismatchError.Want = 0x%08X, want 0xDEADBEEF", mismatch.Want)
	}
	var position *tree.ChecksumPositionError
	if errors.As(err, &position) {
		t.Errorf("the position error was reported instead of the mismatch: %v", err)
	}
}

func TestVerifyChecksumMultiple(t *testing.T) {
	title := ebmltest.UTF8(matroska.IDTitle, "covered")
	info := parseOne(t, ebmltest.Encode(
		ebmltest.Master(matroska.IDInfo, checksumOver(title), checksumOver(title), title),
	))

	err := info.VerifyChecksum()
	if err == nil {
		t.Fatal("VerifyChecksum accepted two CRC-32 children")
	}
	if parser.IsStructural(err) {
		t.Errorf("IsStructural(%v) = true, want false", err)
	}
	var multiple *tree.MultipleChecksumsError
	if !errors.As(err, &multiple) {
		t.Fatalf("errors.As did not reach *tree.MultipleChecksumsError: %v", err)
	}
	if multiple.Count != 2 {
		t.Errorf("MultipleChecksumsError.Count = %d, want 2", multiple.Count)
	}
}

func TestVerifyChecksumNilAndZeroValue(t *testing.T) {
	var absent *tree.Element
	if err := absent.VerifyChecksum(); err != nil {
		t.Errorf("nil receiver: %v", err)
	}
	if err := (&tree.Element{}).VerifyChecksum(); err != nil {
		t.Errorf("zero value: %v", err)
	}
	// A navigation miss is the usual way a nil receiver arrives here.
	if err := (&tree.Element{}).Find(matroska.IDInfo).VerifyChecksum(); err != nil {
		t.Errorf("navigation miss: %v", err)
	}
}

func TestVerifyChecksumNested(t *testing.T) {
	// The inner master's CRC-32 element is ordinary covered data for the outer
	// one: coverage stops at the parent's own checksum and never descends.
	title := ebmltest.UTF8(matroska.IDTitle, "inner")
	inner := ebmltest.Master(matroska.IDInfo, checksumOver(title), title)
	tracks := ebmltest.Master(matroska.IDTracks)
	segment := parseOne(t, ebmltest.Encode(
		ebmltest.Master(matroska.IDSegment, checksumOver(inner, tracks), inner, tracks),
	))

	if err := segment.VerifyChecksum(); err != nil {
		t.Fatalf("outer VerifyChecksum: %v", err)
	}
	if err := segment.Find(matroska.IDInfo).VerifyChecksum(); err != nil {
		t.Fatalf("inner VerifyChecksum: %v", err)
	}

	// Walk is the recursion, which is why Element carries no recursive method.
	var verified int
	var walkErr error
	segment.Walk(func(el *tree.Element) bool {
		if len(el.Children) > 0 {
			verified++
		}
		walkErr = el.VerifyChecksum()
		return walkErr == nil
	})
	if walkErr != nil {
		t.Fatalf("Walk-based verification: %v", walkErr)
	}
	if verified != 2 {
		t.Errorf("visited %d masters, want 2", verified)
	}
}
