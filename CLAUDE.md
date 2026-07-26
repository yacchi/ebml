# CLAUDE.md - ebml

This repository is English-only. It is a streaming, cursor-based EBML/Matroska
library for Go 1.25, rooted at `go/` and published as
`github.com/yacchi/ebml`. The CLI binary is `ebml` (`go/cmd/ebml`). The
repository directory is still named `ebml-reader`; that is intentional and
carries no meaning.

## Architecture and standing design rules

* The CORE is `go/parser`, `go/matroska`, `go/writer`, `go/tree`, `go/stream`
  and `go/crc`: membership is decided by whether a PORT MUST AGREE ON IT TO
  INTEROPERATE, not by how generic the code looks. The cursor, event model, flow
  control, VINT behavior, element registry, retained element model, encoder, and
  the byte-supply contract are that agreement and are specified in
  `spec/SPEC.md`. Ways of USING them belong in `ext`, where another language may
  reasonably choose a different shape or none at all.
  `go/crc` is core because WHICH BYTES a CRC-32 covers and which way round the
  four are stored are agreements, not implementation details: a port that covers
  the parent's header, or that includes the CRC-32 element in its own coverage,
  or that stores the value big-endian, still writes a self-consistent file, and
  that file reads as damaged here — a mismatch on bytes nothing ever damaged. It
  imports nothing from this module because `internal/archtest` confines
  `go/writer` to `go/parser`, so the one primitive both the writer and `tree`
  need has to sit BELOW both; anywhere else forces a second copy.
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
* Standard-library sensibility governs SPELLING, not MEMBERSHIP, and it
  constrains the CONTRACT, never the MECHANISM. Membership in the core is still
  decided by whether a PORT MUST AGREE ON IT TO INTEROPERATE. Once something IS
  core, its API must be one the host language's standard library could plausibly
  carry: nothing outside the language's own standard library (both modules have
  zero third-party requires and that stays true), no configuration object, no
  hook registry, no DSL, and errors, naming and lifetimes spelled the way the
  host language spells them.
  What a port must reproduce is the OBSERVABLE CONTRACT; how it reproduces it is
  the port's own business, and a mechanism this repository uses is never itself
  the requirement. Go stamps a generation into every node and panics on a stale
  one because Go has no borrow checker; a Rust port spells the SAME lifetime
  guarantee as `&'_ mut self` and needs neither stamp nor panic, and that is full
  compliance, not a divergence. `io.Reader` is likewise Go's spelling of a byte
  source, not the contract.
  The one shape that is NOT free is the ARITY OF A PULL. A pull has three
  outcomes — an event, need-more-data, and end of input — and a two-outcome
  iterator protocol (value/done) can carry them only by collapsing one. The test
  for a port is not "does it avoid iterators" but "can this protocol state all
  three without lying", and the answer follows from WHO OWNS THE BYTE SUPPLY:
  where the caller pushes bytes (`parser.Cursor`) need-more-data has nowhere to
  go, so the operation stays an explicit call — Rust's `futures::Stream` PASSES
  the test, since `Poll::Pending` IS need-more-data, while a plain `Iterator`
  does not; where the layer owns the source (`go/stream`) blocking or `await`
  absorbs need-more-data, so an iterator is correct there. A port that offers
  only the iterator over a caller-pushed cursor has dropped this library's
  central distinction, whatever else it gets right.
  `docs/pull-shape-across-languages.md` works both cases through several
  languages and is the reference for a port; keep it and `spec/SPEC.md`'s
  "Portability of API shape" section in agreement.
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
* `parser.Cursor` offers NO iterator, and never will. It had one (`Cursor.Nodes`),
  documented as non-normative sugar, and it was removed once `stream` proved the
  arity rule above: a range loop over a caller-fed cursor cannot distinguish
  need-more-data from end of input, which is this library's central semantic, and
  it had zero consumers while costing a "not the normative shape" caveat in three
  documents. The pull stays `Next`. `Cursor.Err` remains, because it REPORTS and
  never advances — an accessor is not a second spelling of the pull.
* The answer to `NeedMoreData` lives in exactly one place, `go/stream`, because
  only the holder of the input source can give it. A consumer that pushes bytes
  itself still sees `NeedMoreData` from `parser.Cursor`; that low-level contract
  stays unchanged, and the two are not alternatives -- `stream` is built on it.
  Because `stream` has absorbed need-more-data, only two outcomes remain there,
  and it is the ONE layer where the iterator is exact rather than lossy: its whole
  reading surface is `Nodes() iter.Seq2[parser.Node, error]`, with `Payload` and
  `Offset` beside it and NO exported `Next`. That absence is the point — two
  spellings of the same pull is how the three-outcome collapse creeps back in, and
  `stream` is the working proof of the arity rule above, not merely its
  description. The end of the input ends the iteration; every other failure is
  yielded once, as the final pair, with a nil node, so a consumer cannot lose it
  by forgetting a separate `Err` call — which is exactly how the removed
  `Cursor.Nodes` could lose one. Never add a `Stream.Next` back, and never soften
  `Nodes` to an `iter.Seq` plus `Err`.
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
* CRC-32 is EXPLICIT on both sides and implicit on neither. Verification happens
  only when a caller asks for it, `tree.Element.VerifyChecksum`, and nothing in
  the library ever checks a checksum on its own: a user must never be handed a
  failure from validation they did not request, and a checksum covers the element
  data AS STORED, so only the retained model holds the bytes to sum at all — the
  cursor's payload view dies at the next pull. A mismatch is a CONTENT error
  (`parser.NewContentError`) and never structural, because the extents were read
  correctly: the position of the next element is known and the parse is not in
  doubt, only this element's bytes are. Emission is opt-in PER MASTER
  (`writer.WithChecksum`) and the CALLER supplies the CRC-32 element ID, exactly
  as it supplies every other ID — that parameter is what keeps rule (i) intact,
  since a hard-coded ID would be the first element literal in `go/writer`. And
  `tree.Marshal` never GENERATES a CRC element: a CRC-bearing input already
  retains it as an ordinary child and writes it back verbatim, so generating one
  would rewrite bytes the document already had and break (j).
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
  `Cursor.Next`/`Feed`/`Finalize`, and no iterator: an iterator belongs to the
  layer that owns the byte source, which is `go/stream`.
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
  cursor. Its whole reading surface is `Nodes() iter.Seq2[parser.Node, error]`;
  there is no exported `Next`, and `stream` is the working proof of the arity rule
  above. The CLI's private stream driver is gone; `go/kvs.Reader` remains a
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
control, iterator placement, and Go extensions are documented; remaining work is
focused on broader reading conformance and Matroska coverage:

1. CRC-32 is DONE on both sides and is not a parser feature: the primitive is
   `go/crc`, emission is `writer.WithChecksum(crcID)` on a `Buffered` master,
   and verification is `tree.Element.VerifyChecksum` on one element, with
   recursion left to the caller's `Walk`. What remains out of scope is not a
   gap: verification is available exactly where the covered bytes were RETAINED,
   so a consumer that skipped a subtree, or capped payloads with
   `WithMaxPayload`, has nothing to sum and is told so
   (`ChecksumUnavailableError`) rather than passed. Nothing verifies implicitly,
   and `go/parser` still holds no element knowledge, so a streaming checksum
   cannot live there. `Void` remains an opaque skippable leaf.
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
core first and then the optional Go extensions. `docs/` holds the design notes
that are too long for a package doc and are NOT normative — a note that
contradicts `spec/SPEC.md` is the thing that is wrong. Each note is listed in
`docs/README.md`; a new one goes there in the same change.
