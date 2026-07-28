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

## Standing design rules

[`CLAUDE.md`](../CLAUDE.md) states each standing rule in one line. The notes
under [`design-rules/`](design-rules/) hold the argument behind each one: the
alternatives that were rejected, and the defect that produced the rule. Read the
note before changing the rule it explains.

* [Declined additions](design-rules/declined-additions.md) — why the small
  cross-section is the product rather than an unfinished state, the three
  questions every proposed API faces, and the ledger of what was asked for,
  refused, and accepted. Read this before adding an exported name.
* [Layer boundaries](design-rules/layer-boundaries.md) — repository layout, the
  module path, what makes a package core, `ext` leaves, integrations, retention.
* [Pull shape and node lifetime](design-rules/pull-and-lifetime.md) — the single
  pull surface, the enforced node lifetime, where an iterator is allowed.
* [Errors, recovery, and delivery](design-rules/errors-and-recovery.md) — the two
  error classes, per-class recovery, the salvaged truncated tail, in-band
  failures, when a Fragment is delivered.
* [The writer, round-tripping, and CRC-32](design-rules/writer-and-crc.md) — one
  encoder, no element knowledge in it, byte-identical round-trip, how to compare
  when byte equality legitimately does not hold, explicit CRC.
* [The registry, boundary policy, and the official schemas](design-rules/registry-and-schemas.md)
  — where element knowledge lives, the deny-only unknown-size boundary rule, why
  the CC-BY schemas are never vendored.
* [Evidence over assumption](design-rules/evidence-over-assumption.md) — why a
  fixture, corpus or reference example must model the shape the field produces.
