// This file is the external test package (matroska_test) because the round-trip
// test imports tree, which imports matroska: only a package's external test
// package may close that loop. The core itself must never import ext.
package matroska_test

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"strings"
	"testing"

	"github.com/yacchi/ebml/internal/ebmltest"
	"github.com/yacchi/ebml/matroska"
	"github.com/yacchi/ebml/parser"
	"github.com/yacchi/ebml/tree"
)

// Vendor element IDs: valid 2-byte EBML IDs that RFC 9559 does not define, so
// the built-in registry cannot know them.
const (
	idVendorBox   parser.ElementID = 0x4F01 // in truth a master
	idVendorCount parser.ElementID = 0x4F02 // uint leaf
	idVendorInner parser.ElementID = 0x4F03 // in truth a nested master
	idVendorNote  parser.ElementID = 0x4F04 // string leaf
)

// vendorRegistry is the registry a consumer with vendor elements builds: derived
// from the standard table, so it answers for the vendor elements and for all of
// Matroska.
func vendorRegistry(t *testing.T) *matroska.Registry {
	t.Helper()
	reg := matroska.NewRegistry(matroska.Default())
	for _, info := range []matroska.ElementInfo{
		{ID: idVendorBox, Name: "VendorBox", Type: matroska.TypeMaster},
		{ID: idVendorCount, Name: "VendorCount", Type: matroska.TypeUint},
		{ID: idVendorInner, Name: "VendorInner", Type: matroska.TypeMaster},
		{ID: idVendorNote, Name: "VendorNote", Type: matroska.TypeString},
	} {
		if err := reg.Register(info); err != nil {
			t.Fatalf("Register(%s): %v", info.Name, err)
		}
	}
	return reg
}

// vendorStream builds a Segment holding a vendor master followed by a standard
// Info, and returns the stream plus the payload bytes of the vendor master. That
// payload is the vendor master's children encoded on their own, which is exactly
// what a reader that does not know the element reads as one opaque leaf.
func vendorStream() (stream, vendorPayload []byte) {
	vendorChildren := []ebmltest.Node{
		ebmltest.Leaf(idVendorCount, []byte{0x07}),
		ebmltest.Master(idVendorInner, ebmltest.Leaf(idVendorNote, []byte("hi"))),
	}
	vendorPayload = ebmltest.Encode(vendorChildren...)
	stream = ebmltest.Encode(ebmltest.Master(matroska.IDSegment,
		ebmltest.Master(idVendorBox, vendorChildren...),
		ebmltest.Master(matroska.IDInfo, ebmltest.Leaf(matroska.IDTimestampScale, []byte{0x0F, 0x42, 0x40})),
	))
	return stream, vendorPayload
}

// TestUnregisteredElementNeverBreaksTheReader is the first unknown-element
// guarantee: an element no registry knows classifies as a binary leaf, its
// declared size is honoured, its bytes are reachable in full, and the elements
// after it are read normally.
func TestUnregisteredElementNeverBreaksTheReader(t *testing.T) {
	stream, vendorPayload := vendorStream()

	var got []byte
	trace := scan(t, stream, matroska.KindForElementID, func(id parser.ElementID, payload []byte) {
		if id == idVendorBox {
			got = append([]byte(nil), payload...)
		}
	})

	want := []string{
		"master 0x18538067 d0",
		"leaf 0x4F01 d1",
		"master 0x1549A966 d1",
		"leaf 0x2AD7B1 d2",
		"close 0x1549A966 d1",
		"close 0x18538067 d0",
	}
	if strings.Join(trace, "\n") != strings.Join(want, "\n") {
		t.Errorf("trace =\n%s\nwant\n%s", strings.Join(trace, "\n"), strings.Join(want, "\n"))
	}
	if !bytes.Equal(got, vendorPayload) {
		t.Errorf("payload of the unknown element = %x, want %x", got, vendorPayload)
	}

	// The unknown element has no name and no type information, but it is still
	// printable -- neither call panics or fails.
	if name := matroska.NameForID(idVendorBox); name != "" {
		t.Errorf("NameForID(unregistered) = %q, want empty", name)
	}
	if got, want := matroska.Describe(idVendorBox), "0x4F01"; got != want {
		t.Errorf("Describe(unregistered) = %q, want %q", got, want)
	}
	if typ, ok := matroska.TypeFor(idVendorBox); ok || typ != matroska.TypeUnknown {
		t.Errorf("TypeFor(unregistered) = %v, %v, want unknown, false", typ, ok)
	}
}

// TestRegisteredVendorMasterNests is the second guarantee: registering the vendor
// elements and driving the cursor with that registry's KindForElementID makes the
// vendor masters nest and their names appear.
func TestRegisteredVendorMasterNests(t *testing.T) {
	stream, _ := vendorStream()
	reg := vendorRegistry(t)

	var count uint64
	var note string
	trace := scan(t, stream, reg.KindForElementID, func(id parser.ElementID, payload []byte) {
		switch id {
		case idVendorCount:
			value, err := parser.DecodeUint(payload)
			if err != nil {
				t.Errorf("DecodeUint(VendorCount): %v", err)
			}
			count = value
		case idVendorNote:
			note = string(payload)
		}
	})

	want := []string{
		"master 0x18538067 d0",
		"master 0x4F01 d1",
		"leaf 0x4F02 d2",
		"master 0x4F03 d2",
		"leaf 0x4F04 d3",
		"close 0x4F03 d2",
		"close 0x4F01 d1",
		"master 0x1549A966 d1",
		"leaf 0x2AD7B1 d2",
		"close 0x1549A966 d1",
		"close 0x18538067 d0",
	}
	if strings.Join(trace, "\n") != strings.Join(want, "\n") {
		t.Errorf("trace =\n%s\nwant\n%s", strings.Join(trace, "\n"), strings.Join(want, "\n"))
	}
	if count != 7 {
		t.Errorf("VendorCount = %d, want 7", count)
	}
	if note != "hi" {
		t.Errorf("VendorNote = %q, want %q", note, "hi")
	}

	// The names of the vendor elements now resolve, next to the standard ones.
	if got, want := reg.Describe(idVendorBox), "VendorBox (0x4F01)"; got != want {
		t.Errorf("Describe(VendorBox) = %q, want %q", got, want)
	}
	if got, want := reg.NameForID(matroska.IDInfo), "Info"; got != want {
		t.Errorf("NameForID(Info) = %q, want %q", got, want)
	}
}

// TestOpaqueMasterRecoveredWithTree is the fourth guarantee, the round trip:
// an unregistered master read as one opaque leaf loses nothing, because its
// payload bytes can be re-parsed afterwards with the finite-buffer parser in
// tree -- with the vendor registry, recovering the full nesting and names.
func TestOpaqueMasterRecoveredWithTree(t *testing.T) {
	stream, vendorPayload := vendorStream()
	reg := vendorRegistry(t)

	// Read the stream with the standard registry, keeping only the opaque bytes
	// of the element it does not know.
	var opaque []byte
	scan(t, stream, matroska.KindForElementID, func(id parser.ElementID, payload []byte) {
		if id == idVendorBox {
			opaque = append([]byte(nil), payload...)
		}
	})
	if !bytes.Equal(opaque, vendorPayload) {
		t.Fatalf("opaque payload = %x, want %x", opaque, vendorPayload)
	}

	roots, err := tree.Parse(opaque, tree.WithClassifier(reg.KindForElementID), tree.WithRegistry(reg))
	if err != nil {
		t.Fatalf("tree.Parse() error = %v", err)
	}
	if len(roots) != 2 {
		t.Fatalf("tree.Parse() returned %d top-level elements, want 2", len(roots))
	}
	if roots[0].Name() != "VendorCount" {
		t.Errorf("first recovered element = %s, want VendorCount", roots[0].Describe())
	}
	value, err := roots[0].AsUint()
	if err != nil {
		t.Fatalf("AsUint() error = %v", err)
	}
	if value != 7 {
		t.Errorf("recovered VendorCount = %d, want 7", value)
	}
	note := roots[1].Find(idVendorNote)
	if !note.Exists() {
		t.Fatalf("recovered tree lost VendorInner>VendorNote: %s", roots[1].Describe())
	}
	if got := note.AsString(); got != "hi" {
		t.Errorf("recovered VendorNote = %q, want %q", got, "hi")
	}

	// Recovery does not even require the registry: with the standard classifier
	// the same bytes still yield the structure, just without names.
	plain, err := tree.Parse(opaque)
	if err != nil {
		t.Fatalf("tree.Parse() without the vendor registry error = %v", err)
	}
	if len(plain) != 2 {
		t.Fatalf("tree.Parse() without the vendor registry returned %d elements, want 2", len(plain))
	}
	if plain[0].Name() != "" {
		t.Errorf("unregistered element reported name %q, want empty", plain[0].Name())
	}
}

// scan drives the core cursor over data with the given classifier and returns one
// line per event, verifying that the trace is identical when the input arrives
// one byte at a time. onPayload receives every leaf payload of the whole-input
// run.
func scan(t *testing.T, data []byte, classifier parser.KindClassifier, onPayload func(parser.ElementID, []byte)) []string {
	t.Helper()
	run := func(chunk int, payloads func(parser.ElementID, []byte)) []string {
		var lines []string
		c := parser.NewCursor(classifier)
		pos := 0
		feed := func() bool {
			if pos >= len(data) {
				return false
			}
			end := min(pos+chunk, len(data))
			c.Feed(data[pos:end])
			pos = end
			return true
		}
		finalized := false
		for {
			node, err := c.Next()
			if err != nil {
				var needMore parser.NeedMoreData
				switch {
				case errors.As(err, &needMore):
					if feed() {
						continue
					}
					if finalized {
						t.Fatalf("NeedMoreData after Finalize at offset %d", c.Offset())
					}
					finalized = true
					if err := c.Finalize(); err != nil {
						t.Fatalf("Finalize: %v", err)
					}
					continue
				case errors.Is(err, io.EOF):
					return lines
				default:
					t.Fatalf("Next at offset %d: %v", c.Offset(), err)
				}
			}
			switch n := node.(type) {
			case *parser.MasterNode:
				lines = append(lines, fmt.Sprintf("master %s d%d", n.ID(), n.Depth()))
			case *parser.LeafNode:
				lines = append(lines, fmt.Sprintf("leaf %s d%d", n.ID(), n.Depth()))
				// The node survives a Feed, so a payload that has not all arrived is
				// simply retried.
				payload, err := n.Payload()
				for {
					var needMore parser.NeedMoreData
					if !errors.As(err, &needMore) {
						break
					}
					if !feed() {
						t.Fatalf("payload of %s still needs data at end of input", n.ID())
					}
					payload, err = n.Payload()
				}
				if err != nil {
					t.Fatalf("Payload of %s: %v", n.ID(), err)
				}
				if payloads != nil {
					payloads(n.ID(), payload)
				}
			case *parser.EndNode:
				lines = append(lines, fmt.Sprintf("close %s d%d", n.ID(), n.Depth()))
			}
		}
	}

	whole := run(len(data), onPayload)
	if byByte := run(1, nil); strings.Join(byByte, "\n") != strings.Join(whole, "\n") {
		t.Errorf("one-byte trace differs from the whole-input trace:\n%s\n---\n%s",
			strings.Join(byByte, "\n"), strings.Join(whole, "\n"))
	}
	return whole
}

// ---- input shaping ----
//
// These tests hand-shape their input through internal/ebmltest, which builds it with
// package writer, the library's only EBML encoder: a test that encoded bytes of its
// own could agree with itself while disagreeing with the library.
