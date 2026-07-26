// Package tree retains a parsed EBML document as a navigable element tree.
//
// It is part of the core cross-language contract: RFC 8794 defines an EBML
// document as a tree of elements. The package is built only on the exported API
// of parser, matroska and writer. The core parser remains a streaming cursor
// that emits events and forgets them; tree is the separate retained data model,
// so parser never imports it.
//
// # Parse and Element
//
// Parse turns a complete in-memory buffer into its top-level elements, each with
// its children. Every element that occurs in the input is retained by ID,
// including elements the registry has never heard of, so any element is reachable
// and readable with no library change. The registry informs only Name, Describe
// and Type; it never gates retention and never gates decoding, which work from
// the raw payload bytes alone.
//
// The type is Element, not TreeElement: within this package the "tree" is the
// package, so a node is just an element. Likewise the entry point is Parse, read
// as tree.Parse(data), rather than a stuttering ParseTree.
//
// # Marshal, and why it proves the model is lossless
//
// Marshal and MarshalBytes write a tree back out as EBML, through package writer,
// the library's only encoder. Parse followed by Marshal reproduces the input byte
// for byte as long as no payload was elided by a retention cap: leaf payloads go
// out verbatim, and each header is rebuilt at its original size-VINT width, which
// the retained HeaderLen still states. A round trip over the committed fixture
// corpus is therefore a conformance test of retention itself -- anything the model
// dropped or normalised would show up as a differing byte.
//
// # CRC-32 verification
//
// VerifyChecksum checks one element's stored CRC-32 against the bytes it covers.
// It lives here and nowhere else because a checksum covers the element data AS
// STORED, so only the retained model has those bytes; and it is EXPLICIT, never
// implicit, so a caller decides what a checksum costs and what a failure means.
// Byte-exact marshalling is what makes it exact -- the covered bytes are the
// element's other children written back out -- which is the round-trip guarantee
// above being used as evidence rather than as a convenience.
//
// # Two orthogonal access modes
//
// The tree offers two deliberately distinguishable ways to reach a value, and
// keeping them distinguishable is a feature, not an accident of the API.
//
// Strict, structural access is for when you care WHERE a value lives: Find and
// FindAll walk an exact path of element IDs downward, ChildrenByID takes one
// level, and Parent, Ancestors, Ancestor, Path, Depth and Index describe an
// element's position. Use it when the document shape carries meaning -- a
// TagTrackUID is only a track target when it sits under that Tag's Targets.
//
// Loose, extractive access is for when you only want the value of some element
// and containment is irrelevant: Descendants returns every occurrence of an ID at
// any depth, under any parent, in stream order. Use it when the question is "give
// me the fragment number", not "give me Tags>Tag>SimpleTag>TagString of the Tag
// whose TagName is ...". Live-stream metadata extraction is almost always this.
//
// The bridge between them is that loose lookups return NODES, never bare scalars.
// A node found loosely still knows its Path, Parent and Ancestor, so a caller can
// start loose and tighten only where it turns out to matter:
//
//	for _, name := range segment.Descendants(matroska.IDName) {
//	    if entry := name.Ancestor(matroska.IDTrackEntry); entry.Exists() {
//	        // a track name, not a chapter name
//	    }
//	}
//
// Loose and strict therefore compose rather than compete. The cursor only emits
// events, while tree provides the format's retained document model.
//
// # Element knowledge
//
// Name, Describe and Type resolve through a Registry. The package-level
// DefaultRegistry is backed by package matroska, which stays the single source of
// element names, IDs and value types; Parse accepts WithRegistry to override it
// for one tree, and a node whose tree carries no registry -- including a
// hand-built Element -- falls back to DefaultRegistry.
//
// # Nil safety
//
// Every method is safe on a nil receiver and on the zero value. Navigation yields
// nil for a miss and the value accessors yield zero values or errors, so no
// method panics and chains never need intermediate nil checks.
package tree
