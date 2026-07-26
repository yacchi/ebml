# CLAUDE.md - ebml

This repository is English-only. It is a streaming, cursor-based EBML/Matroska
library for Go 1.25, rooted at `go/` and published as
`github.com/yacchi/ebml`. The CLI binary is `ebml` (`go/cmd/ebml`). The
repository directory is still named `ebml-reader`; that is intentional and
carries no meaning.

## Architecture and standing design rules

* The CORE is `go/parser`, `go/matroska`, `go/writer`, `go/tree` and
  `go/stream`: membership is decided by whether a PORT MUST AGREE ON IT TO
  INTEROPERATE, not by how generic the code looks. The cursor, event model, flow
  control, VINT behavior, element registry, retained element model, encoder, and
  the byte-supply contract are that agreement and are specified in
  `spec/SPEC.md`. Ways of USING them belong in `ext`, where another language may
  reasonably choose a different shape or none at all.
  `go/stream` is core for the reason its own doc gives: `io.Reader` is Go's
  SPELLING of a byte source, but the contract it stands for is not Go's -- keep
  supplying bytes and parsing proceeds, and an exhausted source is finalized so a
  document that ended mid-element is reported as truncated. A port that skipped
  that last half would silently accept a truncated document, which is exactly the
  kind of disagreement the contract exists to prevent.
* Everything under `go/ext/` is optional Go convenience built solely on exported
  core API. If an extension needs an unavailable capability, fix the core; do
  not reach into internals or add a workaround in the extension. An extension
  may reasonably have a different shape, or no equivalent, in another language.
* `tree` is the core retained document model defined by EBML's tree-shaped
  document; this does not change the parser sanctuary. `parser` is a StAX-shaped
  reader and never imports retained document state or `tree`. The import
  direction is enforced by `go/internal/archtest`.
* `ext/scope` is element-agnostic and follows one master by depth regression,
  never by pairing EndNodes, because `MasterNode.Skip` emits no EndNode. It
  retains only directly completed children and has no configuration or lexical
  chaining: neither EBML nor Matroska defines scope inheritance, so a consumer
  wanting two levels runs two Trackers and states its own precedence.
* Where RFC 9559 is silent, the library states its choice in documentation
  rather than leaving behavior implicit. Tag traversal and precedence rules
  have exactly one implementation in `ext/tags`.
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
* The answer to `NeedMoreData` lives in exactly one place, `go/stream`, because
  only the holder of the input source can give it. A consumer that pushes bytes
  itself still sees `NeedMoreData` from `parser.Cursor`; that low-level contract
  stays unchanged, and the two are not alternatives -- `stream` is built on it.
* `parser.Parser` stays exported as the low-level engine: `internal/ebmltrace` needs
  operation-level control to produce the golden traces, and the goldens are the
  conformance corpus.
* `go/parser` holds NO element knowledge: no element table and not one element ID
  literal in its non-test source. All classification comes from the supplied
  `KindClassifier`, which is a REQUIRED argument of `parser.New` and
  `parser.NewCursor` (there is no option form and no built-in default; `nil`
  panics). Element IDs live only in `go/matroska`. A default would silently read
  an unlisted master as one opaque leaf, so never reintroduce one.
* The boundary policy for a stream of concatenated documents lives in exactly ONE
  place, `matroska.StreamBoundary`: a new top-level element ends any open master,
  and otherwise the RFC 9559 child rule ends an unknown-size master at the first
  element that cannot be its child. That second half is DENY-ONLY
  (`Registry.EndsUnknownSizeMaster`) — a boundary is reported only when the
  registry holds a COMPLETE child list for the open master and `next` is a
  built-in, non-global element absent from it, because a false boundary corrupts
  the parse while a missed one only closes later than it could. `ext/fragment`,
  `go/cmd/ebml` and `internal/ebmltrace` all call it and none restates it. All
  three did once, and each copy went stale on its own schedule: the CLI rendered
  a live stream's trailing `Tags` inside its `Cluster` while the assembler read
  the same bytes correctly, and `ebmltrace` wrote GOLDEN FILES in that same wrong
  shape — a conformance corpus for a reader nobody ships. Never reintroduce a
  copy; a golden produced by a rule the library does not use is worse than no
  golden, because it looks like evidence.
* Turning a cursor node into a retained element happens in exactly ONE place,
  `tree.FromNode`. It copies identity and extent only — a node is valid solely
  until the next pull, so a retained element takes what it needs immediately —
  and never sets `Payload`, because delivering bytes is a flow-control decision
  belonging to whoever holds the node. `ext/scope` and `ext/fragment` both call
  it; they each carried an identical copy before.
* `ext/fragment.Assembler` is deliberately NOT built on `ext/scope.Tracker`, and
  a future reader should not "fix" that. They retain different things: the
  assembler builds ONE nested tree spanning Segment and Cluster, elides
  `SimpleBlock` payloads into decoded blocks with `Truncated` set, retries a
  payload ACROSS `Feed` calls, and never skips a master — which is why its
  EndNode-paired stack is safe where a Tracker must unwind on depth. Merging
  them would either bloat `Tracker` for a single caller or change
  `Fragment.Segment`, whose shared-and-growing contract is documented and
  relied on.
* `CloseMaster` is explicit boundary closure only. It accepts an unknown-size
  master, or a known-size master already at its declared end, and rejects a
  known-size master with payload outstanding (`PrematureCloseError`); a
  known-size boundary belongs to the stream and must not be discarded.
  `LeaveMaster` remains the ordinary close at a declared end.
* `go/writer` is the repository's ONLY EBML encoder. No test, fixture generator or
  extension may carry an encoder of its own: `internal/kvsgen` builds the corpus
  through it, `tree.Marshal` composes its primitives, and hand-shaped test
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
* (j) Parse-then-marshal is a core contract and stays byte-identical for every
  committed fixture: retained `HeaderLen` reproduces each header's original
  size-VINT width.
* `tree` provides two access modes: loose extraction (`Descendants`) ignores
  containment, while strict access (`Find` and ancestry) uses exact paths.
  Loose results retain their structure so the modes compose.
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
* The same applies to the FIXTURE CORPUS, and more sharply, because a corpus is
  what the test suite believes the world looks like. Round 2 (F5) found every
  KVS fixture built with a known-size `Cluster` while the field only ever sends
  an unknown-size one — which is why 450+ tests passed against a stream the
  consumer could not read. A corpus generated from an assumed shape validates
  the assumption, not the world. `known_size_cluster` is retained as the one
  deliberate counter-case: legal Matroska that KVS does not send.
* The normative machine-readable schemas — `ebml.xml` (RFC 8794 header elements)
  and `ebml_matroska.xml` (RFC 9559 body elements), published by the IETF CELLAR
  working group — are CC-BY-4.0 works and are NEVER VENDORED here. They are a
  development-time input: the `conformance-check` skill fetches them into the
  gitignored `.spec-cache/` and `internal/specconform` checks the registry
  against them. What CC-BY actually covers is the schema's PROSE, so no element
  `<documentation>` text is ever copied into a Go doc comment; IDs, names, types
  and paths are interoperability facts and are transcribed by hand.
  `go/matroska/elements.go` stays HAND-WRITTEN and is never generated: its
  omissions are deliberate and a generator would erase that intent. The checker
  reads the registry through its EXPORTED API only, so what it validates is the
  behavior a consumer sees, not an internal table that might disagree with it.
  It separates a MISMATCH — the registry contradicting the schema, a defect —
  from a GAP — the registry being silent, which is only coverage. The invariant
  that makes it worth running is containment: a `LegalChildren` list documented
  as COMPLETE may omit a schema child only while that element is also
  UNREGISTERED, because `EndsUnknownSizeMaster` refuses to end a master on an
  unregistered ID. Registering a `Segment` or `Cluster` child without adding it
  to `completeChildren` in the same change breaks the unknown-size boundary
  rule, which is why registering an element is never a local edit.

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
  top-level element ID. Nested unknown-size masters are covered by conformance
  tests: boundary closure is innermost-first, one level per event, with the
  triggering header re-peeked at the remaining depth.
* VINT parsing recognizes unknown sizes, rejects element-ID VINTs over 4 bytes
  and size VINTs over 8 bytes with `VINTLengthError`, and reports
  `NeedMoreData`, `TruncatedError`, `ElementOverflowError`,
  `UnknownSizeLeafError`, and `PrematureCloseError` as specified. Every one of
  those failures except `NeedMoreData` satisfies `parser.IsStructural`, while
  `parser.NewContentError` marks a consumer's content verdict as the other class.
* `go/matroska` is the immutable RFC 9559 registry. `Default`, `NewRegistry`,
  `Register`, `Override`, `Lookup`, `NameForID`, `Describe`, `IDForName`,
  `Elements`, `TypeFor`, `LegalChildren`, `EndsUnknownSizeMaster`, `ValueType`,
  and `KindForElementID` provide standard knowledge and vendor extensibility.
  `LegalChildren` and `EndsUnknownSizeMaster` use deny-only complete RFC 9559
  containment lists; `ext/fragment` now ends an unknown-size Cluster at the
  first registered element that cannot be its child. Unknown IDs classify as
  readable binary leaves and never trigger a containment boundary. The table
  names 270 of the schema's 273 elements, checked element by element by
  `internal/specconform`; the three exceptions are the deprecated Cluster
  children and are documented where the containment lists are.
* `go/tree` retains a generic tree and implements loose `Descendants` and
  strict `Find`/ancestry navigation. `Marshal`/`MarshalBytes` write a tree back
  out; parse then marshal is BYTE-IDENTICAL for every committed fixture unless a
  payload was elided by a retention cap, because the retained `HeaderLen` still
  gives each header's original size-VINT width. `go/ext/fragment` assembles one
  `Fragment` per completed `Cluster` by pulling `Cursor.Next` in its own loop,
  using only exported core APIs, with `WithResync` and `WithSkipContentErrors`
  as its two opt-in, per-error-class recovery options.
* `go/stream` owns an `io.Reader` and answers `NeedMoreData` while driving a
  cursor. The CLI's private stream driver is gone; `go/kvs.Reader` remains a
  byte-oriented assembler driver because it consumes `Assembler.Feed`, not cursor
  nodes.
* `go/ext/scope` (ext: a way of USING the core, not part of the agreement) tracks any master and the elements that completed directly
  inside it. It is element-agnostic and unwinds by node-depth regression, never
  by pairing EndNodes, because `MasterNode.Skip` emits no EndNode. Only observed
  nodes exist in a scope, with no configured retain policy or element allowlist;
  descendants remain in retained child subtrees but are not direct-child
  queries. Scopes have no lexical chaining or inheritance: neither EBML nor
  Matroska defines it, so a consumer wanting two levels runs two Trackers and
  states its own precedence.
* `go/ext/tags` computes target-aware views over observed `Tags` elements.
  Segment-default tags are cumulative and positionless, repeated names are
  last-wins by library choice, and `ext/fragment.Fragment.Tag` and `Tags` are
  re-expressed on it.
* `go/kvs` is a separate module holding all KVS-specific tag and metadata
  knowledge; the core module has no KVS element or tag knowledge left. The
  runnable example moved to `go/kvs/examples/getmedia`.
* The module is `github.com/yacchi/ebml`, and the CLI is `ebml` under
  `go/cmd/ebml`. The public `go/writer` package replaced the four private
  encoders formerly in `internal/kvsgen`, `tree/tree_test.go`,
  `ext/fragment/synthetic_test.go`, and `matroska/unknown_elements_test.go`.
  The fixture corpus is generated through the public writer, and round-trip
  conformance checks retained trees byte-for-byte.
* The CLI supports `dump` and `xml`. The fixture corpus is regenerated by the
  internal tool `go run ./internal/kvsgen/genfixtures`, which is not part of the
  published CLI. The synthetic corpus covers 15 fixtures and models an
  unknown-size `Cluster` throughout: KVS topology, tail emission, false EBML
  magic in PCM, multiple clusters and segments, tagless and filtered streams,
  gaps, `scaled_timestamps`, `unknown_elements`, `partial_tags` (a populated but
  partial `Tags` element missing its identity keys), `two_tracks` (one
  `Cluster` carrying `SimpleBlock`s for two named audio tracks),
  `known_size_cluster` (legal Matroska but not KVS), and
  `connect_real_shape` (the real two-before/two-after Tags layout). Golden
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

1. Add CRC-32 validation; CRC-32 and Void are currently opaque skippable leaves.
   Note it cannot live in `go/parser`, which holds no element knowledge.
2. Element coverage is DONE and is now a checked property, not a claim: the
   registry names 270 of the 273 elements the official schemas declare, with
   ZERO mismatches against `matroska-specification@f93ab02` /
   `ebml-specification@a4b3c4a`. The three it does not name are `SilentTracks`,
   `SilentTrackNumber` and `EncryptedBlock`, left out on purpose and explained
   at `completeChildren`. Remaining work is VALUE coverage — decoding helpers
   for the types now merely named — not more IDs. What the schema declares and
   this library answers for NOTHING about is inventoried by the checker itself
   (cardinality, `minver` gating, defaults, ranges, enums, fixed lengths,
   `recurring`, the WebM profile). Each is a library capability that does not
   exist yet, so the check for it can only be written after the feature is.
   The core remains terminal on structural corruption; recovery belongs to the
   opt-in `ext/fragment` paths — `WithResync` for structural failures,
   `WithSkipContentErrors` for content ones — and each acts on its own class only.

## Build, fixtures, and documentation

Run commands from `go/`:

```bash
go test ./...
go vet ./...
go run ./internal/kvsgen/genfixtures
```

Checking the registry against the official schemas needs those schemas fetched
first, which is what the `conformance-check` skill is for. The command it ends
up running, from the repository root:

```bash
go -C go run ./internal/specconform/checkschema \
  -schema ../.spec-cache/ebml.xml -schema ../.spec-cache/ebml_matroska.xml -missing
```

Run the separate KVS module commands from `go/kvs/`:

```bash
go test ./...
go vet ./...
go run ./examples/getmedia
```

Fixtures under `fixtures/**/*.ebml.hex` are commented hexadecimal and entirely
synthetic. Golden files under `golden/**/*.jsonl` contain one JSON object per
cursor operation. Split definitions are in `tests/split_patterns.json`.
`spec/SPEC.md` is the normative portable contract; `README.md` documents the
core first and then the optional Go extensions.
