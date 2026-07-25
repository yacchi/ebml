# CLAUDE.md - ebml

This repository is English-only. It is a streaming, cursor-based EBML/Matroska
library for Go 1.25, rooted at `go/` and published as
`github.com/yacchi/ebml`. The CLI binary is `ebml` (`go/cmd/ebml`). The
repository directory is still named `ebml-reader`; that is intentional and
carries no meaning.

## Architecture and standing design rules

* The CORE is `go/parser` plus `go/matroska`: the cursor, event model, flow
  control, VINT behavior, and element registry. It is the only cross-language
  contract and is specified in `spec/SPEC.md`.
* Everything under `go/ext/` is optional Go convenience built solely on exported
  core API. If an extension needs an unavailable capability, fix the core; do
  not reach into internals or add a workaround in the extension.
* The reading core has exactly ONE surface: a streaming pull operation, `Cursor.Next`,
  returning a closed `Node` (`*MasterNode`/`*LeafNode`/`*EndNode`) one event at a
  time, not a document model. Never add a second push or callback event shape.
  Flow control is decided on the event before the next event is requested:
  `MasterNode.Descend`/`Skip` and `LeafNode.Payload`/`Skip` are valid only for the
  node that offers them. An untouched master is descended into and an untouched
  leaf is skipped. Each node is valid only until the next `Next`. That lifetime is
  ENFORCED, not merely documented: the cursor stamps a generation into every node it
  hands out and EVERY exported node method — the extent accessors as much as the
  decisions — panics on a stale stamp. The guarantee has NO exception, and the
  per-event allocation is what buys it: `Next` allocates a new node per event instead
  of refilling one instance per kind, so a retained pointer, a copy (`v := *node`),
  and either of those while the current event is of the same kind are all caught. Never
  reintroduce instance reuse to save that allocation — a retained pointer would then BE
  the live node and no check could exist, which is the silent corruption the rule
  exists to prevent. The cost is measured, not claimed: exactly one allocation per
  event (`allocsPerEvent` in `go/parser/node_validity_test.go`), with
  `BenchmarkCursorScan`/`BenchmarkParserScan` pricing it against `parser.Parser`, the
  surface for a consumer that needs no node. Keep that statement identical in `Node`'s
  doc, `README.md` and `spec/SPEC.md` section 3 — never widen the claim in one of them.
  A method added to a node type must take the same check (`nodeExtent.fresh`) and is
  covered automatically by the reflection-driven tables in
  `go/parser/node_validity_test.go`.
  `LeafNode.Payload` returns a VIEW of the cursor buffer, not a copy: the cursor
  caches the payload's extent and never the slice it handed out, and the bytes are
  valid only until the next `Next` and must not be modified — a consumer that
  retains them (`ext/fragment` does) copies them itself.
* Iterator sugar (`Cursor.Nodes`) may exist but is NOT the normative shape: a range
  loop cannot distinguish need-more-data from end of input, and that distinction is
  this library's central semantic. Keep it explicit in `Next`.
* `parser.Parser` stays exported as the low-level engine: `internal/ebmltrace` needs
  operation-level control to produce the golden traces, and the goldens are the
  conformance corpus.
* `go/parser` holds NO element knowledge: no element table and not one element ID
  literal in its non-test source. All classification comes from the supplied
  `KindClassifier`, which is a REQUIRED argument of `parser.New` and
  `parser.NewCursor` (there is no option form and no built-in default; `nil`
  panics). Element IDs live only in `go/matroska`. A default would silently read
  an unlisted master as one opaque leaf, so never reintroduce one.
* `CloseMaster` is explicit boundary closure only. It accepts an unknown-size
  master, or a known-size master already at its declared end, and rejects a
  known-size master with payload outstanding (`PrematureCloseError`); a
  known-size boundary belongs to the stream and must not be discarded.
  `LeaveMaster` remains the ordinary close at a declared end.
* `go/writer` is the repository's ONLY EBML encoder. No test, fixture generator or
  extension may carry an encoder of its own: `internal/kvsgen` builds the corpus
  through it, `ext/tree.Marshal` composes its primitives, and hand-shaped test
  inputs call it too — through `internal/ebmltest`, the ONE shared shaping layer
  over the public writer API (`Leaf`/`Uint`/`String`/`UTF8`/`Master`/
  `UnknownMaster`/`Encode`). A test may not re-implement it per file, and an
  unknown-size master comes from the writer's `UnknownSize` strategy, never from
  hand-concatenating an ID with the unknown-size marker. Like the cursor the writer
  holds no element knowledge — the CALLER picks the value type, mirroring the
  reader's `AsUint`/`AsString` choice — and it refuses a value the reader could not
  return unchanged: a string carrying a NUL byte is rejected, since a reader stops
  at the first NUL.
* (h) Exactly one EBML encoder exists in the repository: anything that emits
  bytes uses `go/writer`.
* (i) The writer holds no element knowledge either; the caller picks the value
  type.
* (j) Parse-then-marshal stays byte-identical for every committed fixture.
* The two access modes exist only in `ext`: loose extraction ignores containment
  and returns every matching node; strict access uses exact paths and ancestry.
  Loose nodes retain their structure so the modes compose.
* No retention path uses a per-element allowlist. Unknown elements remain
  readable, and an unknown master-shaped payload can be re-parsed later.
* Errors have exactly two classes plus flow control, and the classification is
  part of the core contract: `parser.IsStructural(err)` is the canonical test and
  is true for every STRUCTURAL failure of the cursor. A CONSUMER's verdict about an
  element's content is marked with `parser.NewContentError`, is never structural
  whatever value it carries, and still unwraps to the consumer's own error;
  `NeedMoreData` is neither class. A pull cursor never runs consumer code, so the
  core cannot raise a content error itself — the marker exists precisely so a
  consumer's error stays classifiable. The predicate owns the content boundary
  because `errors.Is`
  traverses the whole chain and cannot stop there; `parser.ErrStructural` remains
  only as the marker the cursor's own errors unwrap to. Keep every new cursor
  failure in the structural class and never classify by message text.
* Byte scanning is allowed only for opt-in post-failure resynchronization AFTER a
  structural failure (`parser.IsStructural(err)`). It is never boundary detection,
  and a content error must never trigger it. Recovery is split by error class, one
  opt-in `ext/fragment` option each, and neither may act on the other's class:
  `WithResync` answers a structural failure by scanning forward and losing the bytes
  between, `WithSkipContentErrors` answers a content error by dropping just the
  offending element — no scanning and no lost Fragment, because a content error
  leaves the structural position intact. Both are terminal by default and both
  report every recovery to a `notify`; a nil `notify` disables the option rather
  than silencing it.
* There is no query DSL or second index type.
* A documented guarantee is never weakened to match an implementation gap.
  When the KVS consumer review (`KVS-CONSUMER-FEEDBACK.md`, F1) reported the
  node-staleness panic intermittently not firing, the response was never to
  soften the "no exception" wording above — the fix belongs in the
  implementation, or, if a guarantee genuinely could not hold, the documented
  text is restated as what is actually true. A guarantee never drifts toward
  matching a bug.
* A reference example must model the shape the field actually produces, not
  the shape that is easiest to generate. The same review (F2) found
  `examples/kvs-getmedia`'s original tag-inheritance policy only handled a
  whole-`Tags`-absence shape that Amazon Connect never produces; the field
  shape is partial (`Tags` present, identity keys missing). The fix was a
  per-key inheritance policy plus the `partial_tags` fixture proving it, not a
  disclaimer next to the simpler, easier-to-generate shape.

## Current state

The core is working and tested:

* `go/parser` provides incremental cursor primitives and the token pull loop
  `Cursor.Next`/`Feed`/`Finalize`, with `Nodes` as non-normative iterator sugar.
  `Next` returns distinguishable master-start, leaf, and master-end nodes; a node
  carries ID, depth, offset, header length, declared size and end offset. Flow
  control is decided on each node before the next `Next`; payload requests can
  return `NeedMoreData` and be retried after `Feed`. Ancestry is the consumer's
  own loop state, not a per-event allocation. `parser.WithBoundary` closes
  unknown-size masters structurally when a consumer recognizes the next
  top-level element ID.
* VINT parsing recognizes unknown sizes, rejects element-ID VINTs over 4 bytes
  and size VINTs over 8 bytes with `VINTLengthError`, and reports
  `NeedMoreData`, `TruncatedError`, `ElementOverflowError`,
  `UnknownSizeLeafError`, and `PrematureCloseError` as specified. Every one of
  those failures except `NeedMoreData` satisfies `parser.IsStructural`, while
  `parser.NewContentError` marks a consumer's content verdict as the other class.
* `go/matroska` is the immutable RFC 9559 registry. `Default`, `NewRegistry`,
  `Register`, `Override`, `Lookup`, `NameForID`, `Describe`, `IDForName`,
  `Elements`, `TypeFor`, `ValueType`, and `KindForElementID` provide standard
  knowledge and vendor extensibility. Unknown IDs classify as readable binary
  leaves.
* `go/ext/tree` retains a generic tree and implements loose `Descendants` and
  strict `Find`/ancestry navigation. `Marshal`/`MarshalBytes` write a tree back
  out; parse then marshal is BYTE-IDENTICAL for every committed fixture unless a
  payload was elided by a retention cap, because the retained `HeaderLen` still
  gives each header's original size-VINT width. `go/ext/fragment` assembles one
  `Fragment` per completed `Cluster` by pulling `Cursor.Next` in its own loop,
  using only exported core APIs, with `WithResync` and `WithSkipContentErrors`
  as its two opt-in, per-error-class recovery options.
* The module is `github.com/yacchi/ebml`, and the CLI is `ebml` under
  `go/cmd/ebml`. The public `go/writer` package replaced the four private
  encoders formerly in `internal/kvsgen`, `ext/tree/tree_test.go`,
  `ext/fragment/synthetic_test.go`, and `matroska/unknown_elements_test.go`.
  The fixture corpus is generated through the public writer, and round-trip
  conformance checks retained trees byte-for-byte.
* The CLI supports `dump`, `xml`, and `genkvs`. The synthetic corpus covers 13
  fixtures: KVS topology, tail emission, false EBML magic in PCM, multiple
  clusters and segments, tagless and filtered streams, gaps,
  `scaled_timestamps`, `unknown_elements`, `partial_tags` (a populated but
  partial `Tags` element missing its identity keys), and `two_tracks` (one
  `Cluster` carrying `SimpleBlock`s for two named audio tracks). Golden
  traces are replayed under all split patterns.

Leaf decoding helpers, `ParseSimpleBlock` and its inverse
`(*parser.SimpleBlock).Append` are convenience functionality in `parser`; they do
not turn the core into a retained document model. `Append` is not a second EBML
encoder either: a block's internals are a payload layout inside one binary leaf,
which is why the pair takes and returns bytes and never an element ID, and the
element around it is still written by `go/writer`.

## Roadmap

Writing and round-trip conformance are complete. The pull cursor, its lazy flow
control, iterator caveat, and Go extensions are documented; remaining work is
focused on broader reading conformance and Matroska coverage:

1. Add conformance coverage for nested unknown-size masters and verify their
   outward structural closure behavior.
2. Add CRC-32 validation; CRC-32 and Void are currently opaque skippable leaves.
3. Broaden Matroska value and element coverage beyond the KVS fragment shape.
   The core remains terminal on structural corruption; recovery belongs to the
   opt-in `ext/fragment` paths — `WithResync` for structural failures,
   `WithSkipContentErrors` for content ones — and each acts on its own class only.

## Build, fixtures, and documentation

Run commands from `go/`:

```bash
go test ./...
go vet ./...
go run ./cmd/ebml genkvs
```

Fixtures under `fixtures/**/*.ebml.hex` are commented hexadecimal and entirely
synthetic. Golden files under `golden/**/*.jsonl` contain one JSON object per
cursor operation. Split definitions are in `tests/split_patterns.json`.
`spec/SPEC.md` is the normative portable contract; `README.md` documents the
core first and then the optional Go extensions.
