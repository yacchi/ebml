// Package tree retains a parsed EBML document as a navigable element tree.
//
// It is an optional convenience: see package ext for the policy that applies to
// every package under ext/. In short, this package is built only on the exported
// API of parser and matroska, it is deliberately outside the cross-language
// contract that spec/SPEC.md defines, and other-language ports are not expected
// to provide an equivalent. The core is a streaming cursor that emits events and
// forgets them; retention is a consumer policy, and this package is one such
// policy -- retain everything from a finite buffer -- expressed once so callers
// do not each write it again.
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
// Loose and strict therefore compose rather than compete. Neither notion exists
// in the core: the cursor only emits events, and both modes are consequences of
// retention, which is why they live here and not there.
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
