# Design notes

Longer-form design documents that do not belong in a package doc comment.

These are notes about WHY the library has the shape it has, and about what a port
to another language would have to reproduce. They are not normative:
[`spec/SPEC.md`](../spec/SPEC.md) is the portable contract, and where a note and
the specification disagree, the specification wins and the note is wrong.

* [Pull shape across languages](pull-shape-across-languages.md) — why the
  caller-fed cursor keeps an explicit `Next` while the source-owning driver is an
  iterator, worked through Go, Rust, Python, JavaScript, Java, C# and C. Read
  this before porting the reading surface.
