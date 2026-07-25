# CLAUDE.md - ebml

This repository is English-only. It is a streaming, cursor-based EBML/Matroska
library for Go 1.22, rooted at `go/` and published as
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
* The cursor is a streaming event source, not a document model. Retention is
  consumer policy. Header decisions are `Descend` or `SkipSubtree` for masters
  and `ReadPayload` or `SkipPayload` for leaves.
* `go/parser` holds NO element knowledge: no element table and not one element ID
  literal in its non-test source. All classification comes from the supplied
  `KindClassifier`, which is a REQUIRED argument of `parser.New` and
  `parser.NewScanner` (there is no option form and no built-in default; `nil`
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
  is true for every STRUCTURAL failure of the cursor. An error a `Handler`
  returned is wrapped in `*parser.HandlerError`, is never structural whatever
  value it carries, and still unwraps to the handler's own error; `NeedMoreData`
  is neither class. The predicate owns the handler boundary because `errors.Is`
  traverses the whole chain and cannot stop there; `parser.ErrStructural` remains
  only as the marker the cursor's own errors unwrap to. Keep every new cursor
  failure in the structural class and never classify by message text.
* Byte scanning is allowed only for opt-in post-failure resynchronization AFTER a
  structural failure (`parser.IsStructural(err)`). It is never
  boundary detection, and a handler/content error must never trigger it: such an
  error is terminal and returned to the caller unchanged.
* There is no query DSL or second index type.

## Current state

The core is working and tested:

* `go/parser` provides incremental cursor primitives and `Scanner`/`Handler`
  events. `Node` carries ID, kind, depth, offset, header length, declared size,
  end offset, and `OpenMasters()`. `BoundaryDecider` closes unknown-size masters
  structurally when a consumer recognizes the next top-level header.
* VINT parsing recognizes unknown sizes, rejects element-ID VINTs over 4 bytes
  and size VINTs over 8 bytes with `VINTLengthError`, and reports
  `NeedMoreData`, `TruncatedError`, `ElementOverflowError`,
  `UnknownSizeLeafError`, and `PrematureCloseError` as specified. Every one of
  those failures except `NeedMoreData` satisfies `parser.IsStructural`, and
  `Scanner` wraps handler errors in `*parser.HandlerError`.
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
  `Fragment` per completed `Cluster` using only exported core APIs.
* The module is `github.com/yacchi/ebml`, and the CLI is `ebml` under
  `go/cmd/ebml`. The public `go/writer` package replaced the four private
  encoders formerly in `internal/kvsgen`, `ext/tree/tree_test.go`,
  `ext/fragment/synthetic_test.go`, and `matroska/unknown_elements_test.go`.
  The fixture corpus is generated through the public writer, and round-trip
  conformance checks retained trees byte-for-byte.
* The CLI supports `dump`, `xml`, and `genkvs`. The synthetic corpus covers
  KVS topology, tail emission, false EBML magic in PCM, multiple clusters and
  segments, tagless and filtered streams, gaps, `scaled_timestamps`, and
  `unknown_elements`. Golden traces are replayed under all split patterns.

Leaf decoding helpers and `ParseSimpleBlock` are convenience functionality in
`parser`; they do not turn the core into a retained document model.

## Roadmap

Writing and round-trip conformance are complete; remaining work is focused on
broader reading conformance and Matroska coverage:

1. Add conformance coverage for nested unknown-size masters and verify their
   outward structural closure behavior.
2. Add CRC-32 validation; CRC-32 and Void are currently opaque skippable leaves.
3. Broaden Matroska value and element coverage beyond the KVS fragment shape.
   The core remains terminal on structural corruption; recovery belongs to the
   opt-in `ext/fragment` `WithResync` path, which acts on structural failures
   only.

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
