package tree_test

import (
	"bytes"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/yacchi/ebml/impl/go/matroska"
	"github.com/yacchi/ebml/impl/go/parser"
	"github.com/yacchi/ebml/impl/go/tree"
	"github.com/yacchi/ebml/impl/go/writer"
)

// fixturePaths returns every committed .ebml.hex fixture, so a fixture added later
// is covered by the round-trip conformance test without touching this file. The
// corpus is two levels deep at most: fixtures/tiny.ebml.hex and fixtures/kvs/*.
func fixturePaths(t *testing.T) []string {
	t.Helper()
	root := filepath.Join("..", "..", "..", "fixtures")
	var paths []string
	for _, pattern := range []string{"*.ebml.hex", filepath.Join("*", "*.ebml.hex")} {
		matched, err := filepath.Glob(filepath.Join(root, pattern))
		if err != nil {
			t.Fatalf("glob %s: %v", pattern, err)
		}
		paths = append(paths, matched...)
	}
	if len(paths) == 0 {
		t.Fatalf("no fixtures found under %s", root)
	}
	sort.Strings(paths)
	return paths
}

// loadHexFixture decodes the committed commented-hex format: "#" lines are
// comments, everything else is whitespace-separated hex bytes.
func loadHexFixture(t *testing.T, path string) []byte {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	var sb strings.Builder
	for _, line := range strings.Split(string(b), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		for _, token := range strings.Fields(line) {
			sb.WriteString(token)
		}
	}
	raw, err := hex.DecodeString(sb.String())
	if err != nil {
		t.Fatalf("decode hex %s: %v", path, err)
	}
	return raw
}

// TestMarshalRoundTripsEveryFixture is the conformance test of the retained model:
// for every committed fixture, parse the bytes into a tree and marshal them back,
// and the result must be byte-identical to the input. Anything the tree failed to
// retain -- a payload, a child, or a header width, including the tiny fixture's
// one-byte unknown-size marker and any non-minimal size VINT -- would differ here.
func TestMarshalRoundTripsEveryFixture(t *testing.T) {
	for _, path := range fixturePaths(t) {
		name, err := filepath.Rel(filepath.Join("..", "..", ".."), path)
		if err != nil {
			name = filepath.Base(path)
		}
		t.Run(name, func(t *testing.T) {
			raw := loadHexFixture(t, path)
			roots, err := tree.Parse(raw)
			if err != nil {
				t.Fatalf("Parse() error = %v", err)
			}

			got, err := tree.MarshalBytes(roots...)
			if err != nil {
				t.Fatalf("MarshalBytes() error = %v", err)
			}
			if !bytes.Equal(got, raw) {
				t.Fatalf("round trip differs: %d bytes out, %d in; first difference at %d",
					len(got), len(raw), firstDiff(got, raw))
			}

			// Marshal to an io.Writer must produce the same bytes as MarshalBytes.
			var buf bytes.Buffer
			if err := tree.Marshal(&buf, roots...); err != nil {
				t.Fatalf("Marshal() error = %v", err)
			}
			if !bytes.Equal(buf.Bytes(), raw) {
				t.Errorf("Marshal(io.Writer) differs from the input at %d", firstDiff(buf.Bytes(), raw))
			}

			// And the bytes it produced must parse back into the same shape.
			reparsed, err := tree.Parse(got)
			if err != nil {
				t.Fatalf("Parse(marshaled) error = %v", err)
			}
			if len(reparsed) != len(roots) {
				t.Errorf("re-parsed %d roots, want %d", len(reparsed), len(roots))
			}
		})
	}
}

// firstDiff returns the index of the first differing byte, or the shorter length
// when one is a prefix of the other.
func firstDiff(a, b []byte) int {
	n := len(a)
	if len(b) < n {
		n = len(b)
	}
	for i := 0; i < n; i++ {
		if a[i] != b[i] {
			return i
		}
	}
	return n
}

// TestMarshalPreservesNonMinimalHeaders pins the mechanism behind byte-exactness:
// a size VINT wider than the size needs, and an unknown-size marker narrower than
// the conventional eight bytes, are both reproduced as they were read.
func TestMarshalPreservesNonMinimalHeaders(t *testing.T) {
	wide, err := writer.EncodeSizeWidth(2, 4) // a 4-byte size VINT for two bytes
	if err != nil {
		t.Fatalf("EncodeSizeWidth: %v", err)
	}
	narrow, err := writer.UnknownSizeVINTWidth(1) // the one-byte unknown-size marker
	if err != nil {
		t.Fatalf("UnknownSizeVINTWidth: %v", err)
	}

	var raw []byte
	raw = append(raw, writer.EncodeID(matroska.IDSegment)...)
	raw = append(raw, narrow...)
	raw = append(raw, writer.EncodeID(matroska.IDTimestampScale)...)
	raw = append(raw, wide...)
	raw = append(raw, 0x03, 0xE8)

	roots, err := tree.Parse(raw)
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	segment := roots[0]
	if segment.Size != parser.UnknownSize {
		t.Fatalf("Segment size = %d, want unknown", segment.Size)
	}
	if segment.HeaderLen != len(writer.EncodeID(matroska.IDSegment))+1 {
		t.Fatalf("Segment HeaderLen = %d, want the one-byte marker", segment.HeaderLen)
	}

	got, err := tree.MarshalBytes(roots...)
	if err != nil {
		t.Fatalf("MarshalBytes() error = %v", err)
	}
	if !bytes.Equal(got, raw) {
		t.Errorf("MarshalBytes() = % X, want % X", got, raw)
	}
}

// TestMarshalHandBuiltTree covers the other direction: a tree assembled in memory
// carries no header lengths, so Marshal writes minimal headers, and the result
// parses back into the same elements.
func TestMarshalHandBuiltTree(t *testing.T) {
	root := &tree.Element{ID: matroska.IDInfo}
	root.AppendChild(&tree.Element{ID: matroska.IDTimestampScale, Payload: []byte{0x0F, 0x42, 0x40}})
	root.AppendChild(nil) // skipped, not a failure
	root.AppendChild(&tree.Element{ID: matroska.IDSegmentUUID, Payload: bytes.Repeat([]byte{0xAB}, 16)})

	got, err := tree.MarshalBytes(root)
	if err != nil {
		t.Fatalf("MarshalBytes() error = %v", err)
	}
	// Info's ID is 4 bytes, its payload 4+3 + 3+16 = 26 bytes, so a minimal
	// 1-byte size VINT: the hand-built header is as compact as EBML allows.
	if len(got) != 4+1+26 {
		t.Fatalf("MarshalBytes() produced %d bytes, want %d", len(got), 4+1+26)
	}

	roots, err := tree.Parse(got)
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	if v, err := roots[0].Find(matroska.IDTimestampScale).AsUint(); err != nil || v != 1000000 {
		t.Errorf("TimestampScale = %d, %v; want 1000000", v, err)
	}
	if len(roots[0].Find(matroska.IDSegmentUUID).Bytes()) != 16 {
		t.Error("SegmentUUID payload did not survive the round trip")
	}
}

// TestMarshalRejectsTruncatedElement is the documented precondition: a payload a
// retention cap elided cannot be reproduced, and Marshal says which element and
// where instead of writing a document with a hole in it.
func TestMarshalRejectsTruncatedElement(t *testing.T) {
	f := fixture(t, "topology_basic")
	roots, err := tree.Parse(f.Data, tree.WithMaxPayload(8))
	if err != nil {
		t.Fatalf("Parse(WithMaxPayload(8)) error = %v", err)
	}

	got, err := tree.MarshalBytes(roots...)
	if err == nil {
		t.Fatalf("MarshalBytes() of a capped tree returned %d bytes and no error", len(got))
	}
	if got != nil {
		t.Errorf("MarshalBytes() returned %d bytes together with an error", len(got))
	}
	if !errors.Is(err, tree.ErrTruncatedPayload) {
		t.Errorf("error = %v, want it to unwrap to ErrTruncatedPayload", err)
	}
	var merr *tree.MarshalError
	if !errors.As(err, &merr) {
		t.Fatalf("error = %T, want *tree.MarshalError", err)
	}
	if merr.ID == 0 {
		t.Error("MarshalError does not name the element")
	}
	if !strings.Contains(merr.Error(), matroska.NameForID(merr.ID)) {
		t.Errorf("MarshalError.Error() = %q, want the element name in it", merr.Error())
	}
	if merr.Offset <= 0 || merr.Offset >= int64(len(f.Data)) {
		t.Errorf("MarshalError.Offset = %d, want an offset inside the %d-byte fixture",
			merr.Offset, len(f.Data))
	}
}

// TestMarshalRejectsIllFormedID: an ID that could not be read back as itself is
// reported rather than written, and the reason comes from the writer's own
// validation, so there is one definition of a well-formed ID.
func TestMarshalRejectsIllFormedID(t *testing.T) {
	const illFormed parser.ElementID = 0x1234 // a 4-byte length marker over a 2-byte value
	if writer.ValidID(illFormed) {
		t.Fatal("0x1234 is expected to be an ill-formed element ID")
	}
	_, err := tree.MarshalBytes(&tree.Element{ID: illFormed, Payload: []byte{1}})
	if !errors.Is(err, writer.ErrInvalidID) {
		t.Errorf("error = %v, want it to unwrap to writer.ErrInvalidID", err)
	}
	var merr *tree.MarshalError
	if !errors.As(err, &merr) || merr.ID != illFormed {
		t.Errorf("error = %v, want a *tree.MarshalError naming 0x1234", err)
	}
}

// TestMarshalNoRoots: marshaling nothing writes nothing, and a nil root is skipped
// rather than reported, matching the nil-safety the rest of the package promises.
func TestMarshalNoRoots(t *testing.T) {
	got, err := tree.MarshalBytes()
	if err != nil || len(got) != 0 {
		t.Errorf("MarshalBytes() = % X, %v; want no bytes and no error", got, err)
	}
	var buf bytes.Buffer
	if err := tree.Marshal(&buf, nil, nil); err != nil || buf.Len() != 0 {
		t.Errorf("Marshal(nil roots) wrote %d bytes, error %v", buf.Len(), err)
	}
}
