package tree_test

import (
	"bytes"
	"errors"
	"io"
	"testing"

	"github.com/yacchi/ebml/crc"
	"github.com/yacchi/ebml/internal/ebmltest"
	"github.com/yacchi/ebml/matroska"
	"github.com/yacchi/ebml/parser"
	"github.com/yacchi/ebml/tree"
	"github.com/yacchi/ebml/writer"
)

// TestVerifyChecksumZeroCoverage covers a CRC-32 element that is the ONLY child of
// its parent: the covered data is zero bytes, and RFC 8794 gives crc32.ChecksumIEEE
// a defined answer for empty input rather than treating it as nothing to verify. A
// verifier that special-cased "no other children" to skip the check would silently
// accept a wrong value in exactly this shape.
func TestVerifyChecksumZeroCoverage(t *testing.T) {
	info := parseOne(t, ebmltest.Encode(
		ebmltest.Master(matroska.IDInfo, checksumOver()),
	))
	if got := len(info.Children); got != 1 {
		t.Fatalf("parsed %d children, want 1 (the CRC-32 element alone)", got)
	}
	if err := info.VerifyChecksum(); err != nil {
		t.Fatalf("VerifyChecksum with zero covered bytes: %v", err)
	}
}

// TestVerifyChecksumUnknownSizeMaster covers a CRC-32 element inside an
// unknown-size master. Nothing in VerifyChecksum's own logic reads Size, but an
// unknown-size Segment is the shape KVS actually sends (see CLAUDE.md), so a
// checksum computed and verified against a master whose own extent is undeclared
// is worth confirming rather than assuming.
func TestVerifyChecksumUnknownSizeMaster(t *testing.T) {
	title := ebmltest.UTF8(matroska.IDTitle, "unknown-size parent")
	segment := parseOne(t, ebmltest.Encode(
		ebmltest.UnknownMaster(matroska.IDSegment, checksumOver(title), title),
	))
	if segment.Size != parser.UnknownSize {
		t.Fatalf("Segment Size = %d, want UnknownSize (the test built the wrong shape)", segment.Size)
	}
	if err := segment.VerifyChecksum(); err != nil {
		t.Fatalf("VerifyChecksum on an unknown-size master: %v", err)
	}
}

// TestVerifyChecksumNonMinimalWidthChild covers a covered child whose size VINT was
// written wider than the minimum needed -- legal EBML -- rather than the minimal
// width Marshal would choose on its own. VerifyChecksum re-marshals the covered
// children to get the bytes to sum (see its doc), and that re-marshal is only
// byte-identical to the original because the parsed HeaderLen preserves the
// original width (see Marshal's doc): if that width were silently normalised to
// minimal, the re-marshalled bytes would differ from what the stored checksum was
// computed over, and a perfectly sound document would report a spurious mismatch.
//
// The padded child is built directly with package writer's LeafWith and
// writer.Reserved -- still the repository's one encoder, just its lower-level call,
// because ebmltest's Master/Leaf helpers only ever produce minimal-width headers.
// The enclosing Info master's own header is assembled from writer's public VINT
// encoders (EncodeID, EncodeSize) for the same reason: no second encoder is
// introduced, only the existing one's primitives used directly instead of through
// the higher-level Node wrappers, exactly as tree.encodeHeader itself does.
func TestVerifyChecksumNonMinimalWidthChild(t *testing.T) {
	const paddedWidth = 4 // "covered" is 7 bytes; the minimal width is 1

	var buf bytes.Buffer
	w := writer.New(&buf)
	if err := w.LeafWith(matroska.IDTitle, []byte("covered"), writer.Reserved(paddedWidth)); err != nil {
		t.Fatalf("building the padded-width child: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("closing the padded-width writer: %v", err)
	}
	paddedTitle := buf.Bytes()
	if gotWidth := len(paddedTitle) - len(writer.EncodeID(matroska.IDTitle)) - len("covered"); gotWidth != paddedWidth {
		t.Fatalf("built a %d-byte size VINT, want %d (the test itself is malformed)", gotWidth, paddedWidth)
	}

	crcLeaf := ebmltest.Encode(ebmltest.Leaf(matroska.IDCRC32, crc.Encode(crc.Checksum(paddedTitle))))

	payload := append(append([]byte(nil), crcLeaf...), paddedTitle...)
	sizeVINT, err := writer.EncodeSizeWidth(int64(len(payload)), writer.MaxSizeWidth)
	if err != nil {
		t.Fatalf("EncodeSizeWidth: %v", err)
	}
	raw := append(append(writer.EncodeID(matroska.IDInfo), sizeVINT...), payload...)

	info := parseOne(t, raw)
	title := info.Find(matroska.IDTitle)
	if title == nil {
		t.Fatal("padded-width Title child was not parsed")
	}
	if width := title.HeaderLen - len(writer.EncodeID(matroska.IDTitle)); width != paddedWidth {
		t.Fatalf("retained HeaderLen implies a %d-byte size VINT, want %d", width, paddedWidth)
	}
	if err := info.VerifyChecksum(); err != nil {
		t.Fatalf("VerifyChecksum rejected a non-minimal but legal size VINT: %v", err)
	}
}

// TestVerifyChecksumCorruptedChildHeader covers damage to a covered child's HEADER
// bytes rather than its payload: the CRC-32 element covers the parent's Element
// Data AS STORED (RFC 8794 11.3.1), which includes every covered child's header, so
// a header byte flip must be caught exactly like a payload byte flip -- and the VINT
// class (length-marker) bits are left untouched here, so the corruption is invisible
// to structural parsing and only VerifyChecksum can catch it.
func TestVerifyChecksumCorruptedChildHeader(t *testing.T) {
	title := ebmltest.UTF8(matroska.IDTitle, "covered")
	muxing := ebmltest.String(matroska.IDMuxingApp, "ebml-test")
	raw := ebmltest.Encode(ebmltest.Master(matroska.IDInfo, checksumOver(title, muxing), title, muxing))

	idBytes := writer.EncodeID(matroska.IDTitle)
	crcBytes := ebmltest.Encode(checksumOver(title, muxing))
	infoHeaderLen := len(raw) - len(crcBytes) - len(ebmltest.Encode(title)) - len(ebmltest.Encode(muxing))
	titleIDStart := infoHeaderLen + len(crcBytes)

	corrupted := append([]byte(nil), raw...)
	// Flip a low bit of the ID's LAST byte: for a multi-byte element ID VINT only
	// the first byte carries the length-marker bits, so this changes the ID's
	// value without changing how many bytes the VINT occupies.
	corrupted[titleIDStart+len(idBytes)-1] ^= 0x01

	info := parseOne(t, corrupted)
	err := info.VerifyChecksum()
	if err == nil {
		t.Fatal("VerifyChecksum accepted a document with a corrupted child header")
	}
	if parser.IsStructural(err) {
		t.Errorf("IsStructural(%v) = true, want false: extents were still read correctly", err)
	}
	var mismatch *crc.MismatchError
	if !errors.As(err, &mismatch) {
		t.Fatalf("errors.As did not reach *crc.MismatchError: %v", err)
	}

	// The structural position must stay intact: the sibling written after the
	// corrupted header is still there, at its correct value, and Parse raised no
	// ParseError reaching it (parseOne would have failed the test otherwise).
	got := info.Find(matroska.IDMuxingApp)
	if got == nil {
		t.Fatal("MuxingApp after the corrupted header was not parsed at all")
	}
	if s := got.AsString(); s != "ebml-test" {
		t.Errorf("MuxingApp after the corrupted header = %q, want %q", s, "ebml-test")
	}
}

// TestVerifyChecksumCorruptedHeaderCursorLevel proves the same claim as
// TestVerifyChecksumCorruptedChildHeader one layer down, with the low-level
// parser.Cursor instead of tree.Parse: a corrupted covered child's header is not a
// structural failure at all, so a cursor never even reports an error for it, and
// every element that follows -- including a whole top-level master after the one
// that carried the damage -- is read normally. That is the concrete meaning of "a
// mismatch leaves the structural position intact": nothing downstream of the cursor
// needs to recover anything, because there was nothing structural to recover from.
func TestVerifyChecksumCorruptedHeaderCursorLevel(t *testing.T) {
	title := ebmltest.UTF8(matroska.IDTitle, "covered")
	muxing := ebmltest.String(matroska.IDMuxingApp, "ebml-test")
	raw := ebmltest.Encode(
		ebmltest.Master(matroska.IDInfo, checksumOver(title, muxing), title, muxing),
		ebmltest.Master(matroska.IDTracks),
	)

	idBytes := writer.EncodeID(matroska.IDTitle)
	crcBytes := ebmltest.Encode(checksumOver(title, muxing))
	infoOnly := ebmltest.Encode(ebmltest.Master(matroska.IDInfo, checksumOver(title, muxing), title, muxing))
	infoHeaderLen := len(infoOnly) - len(crcBytes) - len(ebmltest.Encode(title)) - len(ebmltest.Encode(muxing))
	titleIDStart := infoHeaderLen + len(crcBytes)

	corrupted := append([]byte(nil), raw...)
	corrupted[titleIDStart+len(idBytes)-1] ^= 0x01

	c := parser.NewCursor(matroska.KindForElementID)
	c.Feed(corrupted)
	if err := c.Finalize(); err != nil {
		t.Fatalf("Finalize: %v", err)
	}

	var sawTracks bool
	for {
		n, err := c.Next()
		if err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			t.Fatalf("Next: %v (a corrupted covered child header must never be a structural failure)", err)
		}
		switch v := n.(type) {
		case *parser.MasterNode:
			if v.ID() == matroska.IDTracks {
				sawTracks = true
			}
			v.Descend()
		case *parser.LeafNode:
			v.Skip()
		case *parser.EndNode:
			// nothing to do
		default:
			t.Fatalf("unexpected node type %T", n)
		}
	}
	if !sawTracks {
		t.Fatal("the top-level Tracks master after the corrupted header was never reached")
	}
}

// TestVerifyChecksumHandBuiltTree covers an Element tree that was never parsed at
// all -- HeaderLen is zero on every node, so Marshal falls back to minimal-width
// headers (see encodeHeader) -- proving VerifyChecksum works from Children alone
// and does not secretly depend on parse-time bookkeeping like Offset or a retained
// HeaderLen.
func TestVerifyChecksumHandBuiltTree(t *testing.T) {
	title := &tree.Element{ID: matroska.IDTitle, Payload: []byte("hand-built")}

	covered, err := tree.MarshalBytes(title)
	if err != nil {
		t.Fatalf("MarshalBytes of the hand-built child: %v", err)
	}
	sum := crc.Encode(crc.Checksum(covered))
	crcElem := &tree.Element{ID: matroska.IDCRC32, Payload: sum}

	// The CRC-32 element must be the first ordered child, as RFC 8794 requires and
	// as ChecksumPositionError checks for.
	info := &tree.Element{ID: matroska.IDInfo}
	info.AppendChild(crcElem)
	info.AppendChild(title)

	if err := info.VerifyChecksum(); err != nil {
		t.Fatalf("VerifyChecksum on a hand-built tree: %v", err)
	}
}

// TestVerifyChecksumAppendedSiblingInvalidatesChecksum covers a tree that verified
// correctly right after Parse, to which a caller then appends one more child
// in-memory (AppendChild), without touching the stored CRC-32 element. The checksum
// is a property of the SUBTREE as it stands now, not a one-time parse-time verdict
// cached anywhere, so re-running VerifyChecksum after a mutation must see the new
// data and fail -- proving there is no cached "verified" bit a caller could be
// fooled by.
func TestVerifyChecksumAppendedSiblingInvalidatesChecksum(t *testing.T) {
	title := ebmltest.UTF8(matroska.IDTitle, "covered")
	info := parseOne(t, ebmltest.Encode(
		ebmltest.Master(matroska.IDInfo, checksumOver(title), title),
	))
	if err := info.VerifyChecksum(); err != nil {
		t.Fatalf("VerifyChecksum before mutation: %v", err)
	}

	info.AppendChild(&tree.Element{ID: matroska.IDMuxingApp, Payload: []byte("appended-after-parse")})

	err := info.VerifyChecksum()
	if err == nil {
		t.Fatal("VerifyChecksum passed after a sibling was appended post-parse")
	}
	if parser.IsStructural(err) {
		t.Errorf("IsStructural(%v) = true, want false", err)
	}
	var mismatch *crc.MismatchError
	if !errors.As(err, &mismatch) {
		t.Fatalf("errors.As did not reach *crc.MismatchError: %v", err)
	}
}
