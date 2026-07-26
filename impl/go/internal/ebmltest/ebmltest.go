// Package ebmltest builds hand-shaped EBML byte streams for tests.
//
// It exists so that no test carries an EBML encoder of its own: every byte a test
// input is made of comes from package writer, the repository's only EBML encoder.
// That includes the two shapes a test is otherwise tempted to assemble by hand — a
// known-size master (the writer's Buffered strategy) and an unknown-size master
// (its UnknownSize strategy) — so a test can never agree with itself while
// disagreeing with the library.
//
// A test states the SHAPE it needs as a tree of Nodes and asks for the bytes:
//
//	raw := ebmltest.Encode(
//	    ebmltest.Master(matroska.IDEBML, ebmltest.Uint(matroska.IDEBMLVersion, 1)),
//	    ebmltest.UnknownMaster(matroska.IDSegment,
//	        ebmltest.Master(matroska.IDCluster, ebmltest.Uint(matroska.IDTimestamp, 0)),
//	    ),
//	)
//
// Encode of a subtree yields exactly the bytes that subtree contributes to a larger
// document, because an EBML element's encoding does not depend on its context. So a
// test that needs the length of one part encodes that part on its own.
//
// A failure means the test itself is malformed — an ill-formed element ID, a string
// value carrying an interior NUL — so these helpers panic instead of reporting: no
// caller could handle it, and no test input can be salvaged from it.
package ebmltest

import (
	"bytes"

	"github.com/yacchi/ebml/impl/go/parser"
	"github.com/yacchi/ebml/impl/go/writer"
)

// Element IDs this repository reserves for test inputs that need an element NO
// registry can know: an unregistered leaf and an unregistered master-shaped
// element. They live here, in one place, because a test or fixture that picks
// its own "surely nobody uses this" ID picks wrong sooner or later — the
// unregistered leaf was 0xEE until the schema check pointed out that 0xEE is
// Matroska's BlockAddID, so the fixture documenting an "element no registry
// knows" was one registry extension away from documenting a lie.
//
// Both sit in the 0x4FFx range, which the Matroska schema leaves unassigned,
// and internal/specconform verifies against the published schema that they
// still are.
const (
	UnassignedLeafID   parser.ElementID = 0x4FFE
	UnassignedMasterID parser.ElementID = 0x4FFF
)

// UnassignedIDs is every ID above, for the conformance check that proves they
// are absent from the schema.
var UnassignedIDs = []parser.ElementID{UnassignedLeafID, UnassignedMasterID}

// Node is one element of a test input: a leaf with a value, or a master with
// children. It is built by the constructors below and turned into bytes by Encode.
type Node struct {
	emit func(*writer.Writer) error
}

// Leaf is a leaf element whose payload is exactly the bytes given, which may be nil
// for a zero-length element. It is the form for a binary value, and for a payload
// whose bytes the test states directly.
func Leaf(id parser.ElementID, payload []byte) Node {
	return Node{emit: func(w *writer.Writer) error { return w.Leaf(id, payload) }}
}

// Uint is an unsigned-integer leaf.
func Uint(id parser.ElementID, v uint64) Node {
	return Node{emit: func(w *writer.Writer) error { return w.Uint(id, v) }}
}

// String is a printable-ASCII string leaf.
func String(id parser.ElementID, s string) Node {
	return Node{emit: func(w *writer.Writer) error { return w.String(id, s) }}
}

// UTF8 is a UTF-8 string leaf. It shares String's encoding; both exist so the call
// states which EBML value type the test means.
func UTF8(id parser.ElementID, s string) Node {
	return Node{emit: func(w *writer.Writer) error { return w.UTF8(id, s) }}
}

// Master is a KNOWN-size master over the given children, with a minimal size VINT.
// Passing no children builds an empty master.
func Master(id parser.ElementID, children ...Node) Node {
	return master(id, writer.Buffered(), children)
}

// UnknownMaster is a master carrying the 8-byte UNKNOWN-size marker, which is what a
// real KVS Segment declares: nothing in the bytes says where it ends, so a consumer
// closes it structurally or at end of input.
func UnknownMaster(id parser.ElementID, children ...Node) Node {
	return master(id, writer.UnknownSize(), children)
}

func master(id parser.ElementID, size writer.SizeStrategy, children []Node) Node {
	return Node{emit: func(w *writer.Writer) error {
		if err := w.StartMaster(id, size); err != nil {
			return err
		}
		for _, c := range children {
			if err := c.write(w); err != nil {
				return err
			}
		}
		return w.EndMaster()
	}}
}

// write writes n through w, rejecting the zero Node, which carries no element at all.
func (n Node) write(w *writer.Writer) error {
	if n.emit == nil {
		panic("ebmltest: the zero Node describes no element")
	}
	return n.emit(w)
}

// Encode returns the bytes of nodes, written in order through one writer.Writer, and
// panics when the writer rejects a call (see the package documentation).
func Encode(nodes ...Node) []byte {
	var buf bytes.Buffer
	w := writer.New(&buf)
	for _, n := range nodes {
		must(n.write(w))
	}
	must(w.Close())
	return buf.Bytes()
}

// Concat joins already-encoded parts. It is byte concatenation, not encoding: it is
// how a test splices encoded documents together, or splices raw garbage between them.
func Concat(parts ...[]byte) []byte {
	var out []byte
	for _, p := range parts {
		out = append(out, p...)
	}
	return out
}

func must(err error) {
	if err != nil {
		panic("ebmltest: " + err.Error())
	}
}
