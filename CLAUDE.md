# CLAUDE.md - ebml

This repository is English-only, COMMIT MESSAGES INCLUDED — the rule covers
everything written into the repository, not only its files, and it overrides any
personal or global default that prefers another language.

It is a streaming, cursor-based EBML/Matroska library for Go 1.25, rooted at
`impl/go/` and published as `github.com/yacchi/ebml/impl/go`. The CLI binary is
`ebml` (`impl/go/cmd/ebml`). `impl/go/integrations/kvs` is a second, separate
module.

## Where the reasoning lives

The rules below are one line each. Each links to the note that holds the
argument, the alternatives that were rejected, and the defect that produced it.
READ THE NOTE BEFORE CHANGING THE RULE, or before adding an API that touches it;
a rule restated from memory is how the three-copies-drift-apart failures in
these notes started.

| Note | Covers |
| --- | --- |
| [Declined additions](docs/design-rules/declined-additions.md) | why the small cross-section IS the product, the three questions every proposed API faces, and the ledger of what was asked for and refused |
| [Layer boundaries](docs/design-rules/layer-boundaries.md) | repository layout, module path, core vs `ext` vs `integrations`, forbidden imports, retention |
| [Pull shape and node lifetime](docs/design-rules/pull-and-lifetime.md) | the single pull surface, node staleness, where the iterator is allowed |
| [Errors and recovery](docs/design-rules/errors-and-recovery.md) | the two error classes, per-class recovery options, truncated tail, in-band failures, delivery timing |
| [The writer, round-tripping, and CRC-32](docs/design-rules/writer-and-crc.md) | the single encoder, byte-identical round-trip, explicit CRC |
| [Registry, boundary policy, schemas](docs/design-rules/registry-and-schemas.md) | where element knowledge lives, the unknown-size boundary rule, the CC-BY schemas |
| [Evidence over assumption](docs/design-rules/evidence-over-assumption.md) | why fixtures and examples must model the field, not the easy shape |

`spec/SPEC.md` is the normative portable contract; these notes are not
normative, and a note that contradicts the specification is the thing that is
wrong.

## Standing rules

What is NOT added — [details](docs/design-rules/declined-additions.md):

* THE CROSS-SECTION IS THE PRODUCT. The room to add convenient API here is large;
  keeping it unused is a feature and not an oversight.
* Every proposed addition faces three questions, and ANY of them declines it:
  can the asker BUILD IT on the exported surface today; is it a SECOND SPELLING
  of a fact an existing surface delivers; does its SHAPE reintroduce something a
  rule removed. A real, well-evidenced need is answered in a shape that passes,
  never by relaxing one of these.
* A DECLINE IS RECORDED in that note, with the ask, the verdict and what to do
  instead — so it is decided once. An ACCEPTANCE is recorded there too.

Layout and layering — [details](docs/design-rules/layer-boundaries.md):

* EVERY IMPLEMENTATION LIVES UNDER `impl/`, one directory per language; the root
  holds only what implementations SHARE (corpus, spec, notes).
* The CORE is `impl/go/parser`, `matroska`, `writer`, `tree`, `stream`, `crc`.
  Membership is decided by whether A PORT MUST AGREE ON IT TO INTEROPERATE.
* Standard-library sensibility governs SPELLING, not MEMBERSHIP: zero
  third-party requires, no configuration object, no hook registry, no DSL.
* Everything under `ext/` is optional convenience built solely on exported core
  API. Needing an unavailable capability means fixing the core.
* NO `ext` PACKAGE IMPORTS ANOTHER `ext` PACKAGE, pinned by
  `internal/archtest.TestExtPackagesAreLeaves`.
* `parser` never imports `tree` or retained state; direction enforced by
  `internal/archtest`.
* A layer adapting to ONE NAMED OUTSIDE SYSTEM is an INTEGRATION, lives in
  `impl/go/integrations/<name>/` as its OWN MODULE, may import several `ext`
  packages, holds that system's EBML VOCABULARY ONLY and never its API,
  transport or SDK.
* A CROSS-PACKAGE CLAIM IS EITHER COMPILER-CHECKED OR DELETED, never left in
  prose.
* `ext/scope` is element-agnostic, unwinds by depth regression, has no
  inheritance and no allowlist.
* `ext/tags` is the ONLY place a tag accessor lives; `Read(roots ...*tree.Element)`
  and `ReadFrom(Source)` are the ceiling. Never name a special case with the
  base name.
* `tree.FromNode` is the ONE place a cursor node becomes a retained element.
  `ext/fragment.Assembler` is deliberately not built on `ext/scope.Tracker`.
* No query DSL, no second index type, no per-element retention allowlist.

Reading surface — [details](docs/design-rules/pull-and-lifetime.md):

* ONE surface: `Cursor.Next` returning a closed `Node`. Never add a push or
  callback shape.
* Flow control is decided on the event before the next is requested. Each node
  is valid only until the next `Next`, ENFORCED by a generation stamp, panicking
  on every exported method, WITH NO EXCEPTION. Exactly one allocation per event
  buys it — never reintroduce instance reuse.
* `LeafNode.Payload` is a VIEW valid until the next `Next`; a retaining consumer
  copies.
* `EndNode.Reason` names WHY a master closed — declared end, boundary, or end of
  input, EXHAUSTIVE. A missing fact goes on the event that already reports it;
  never open a channel for one.
* `parser.Cursor` offers NO iterator and never will; `impl/go/stream` is the one
  layer where the iterator is exact, and its whole reading surface is
  `Nodes() iter.Seq2[parser.Node, error]` with no exported `Next`.
* A pull has THREE outcomes; a two-outcome protocol may only carry them where
  the layer owns the byte source.

Errors — [details](docs/design-rules/errors-and-recovery.md):

* Exactly two classes plus flow control: `parser.IsStructural` is canonical,
  `parser.NewContentError` marks a consumer's content verdict, `NeedMoreData` is
  neither. Never classify by message text.
* Byte scanning is opt-in post-structural-failure resync ONLY, never boundary
  detection. `WithResync` and `WithSkipContentErrors` each act on their own class
  only.
* A truncated tail is SALVAGED with the error, by DEFAULT; an in-band
  `*StreamError` is REPORTED by `kvs.Reader.Next`, not merely available; a
  Fragment is DELIVERED once Segment metadata has settled, by DEFAULT.
* A documented guarantee is never weakened to match an implementation gap.

Writing — [details](docs/design-rules/writer-and-crc.md):

* `impl/go/writer` is the repository's ONLY EBML encoder; anything emitting
  bytes uses it, tests included (through `internal/ebmltest`).
* The writer holds no element knowledge; the caller picks the value type.
* Parse-then-marshal stays BYTE-IDENTICAL for every committed fixture.
* CRC-32 is explicit on both sides: `tree.Element.VerifyChecksum` to verify,
  `writer.WithChecksum(crcID)` to emit, and nothing implicit either way.

Element knowledge — [details](docs/design-rules/registry-and-schemas.md):

* `impl/go/parser` holds NO element knowledge; `KindClassifier` is required and
  has no default. Element IDs live only in `impl/go/matroska`.
* `matroska.StreamBoundary` is the ONE boundary policy; the child rule is
  DENY-ONLY. Never restate it in a caller.
* The CELLAR schemas are CC-BY-4.0, NEVER VENDORED, and `matroska/elements.go`
  stays hand-written. Registering a `Segment`/`Cluster` child means editing
  `completeChildren` in the same change.
* Where RFC 9559 is silent, state the choice in documentation.

Fixtures and examples — [details](docs/design-rules/evidence-over-assumption.md):

* A fixture, corpus or reference example models the shape THE FIELD PRODUCES,
  not the shape easiest to generate.

## State and roadmap

Reading, writing, round-trip conformance, CRC-32 and element coverage are
complete and tested; the package-by-package description of what exists lives in
[`impl/go/README.md`](impl/go/README.md) and is not repeated here.

Element and value coverage are DONE, and the roadmap said otherwise for longer
than it was true. Measured 2026-07-29 against CELLAR `f93ab02`/`a4b3c4a`
(`docType="matroska" version="4"`): 0 mismatches, 270 of 273 elements registered,
and ZERO of the elements the schema still declares current are missing — the
three unregistered ones are REMOVED after version 0 and stay unregistered on
purpose, so they cannot end an unknown-size master. All 133 WebM-profile elements
are registered. Every schema value type has a decoder on `tree.Element`. The
numbers, and how to re-measure them, are in
[`impl/go/README.md`](impl/go/README.md#measured-conformance).

Remaining work is VALIDATION, none of which exists: cardinality, `minver`
gating, defaults, ranges, enums, fixed lengths, `recurring`, and WebM profile
ENFORCEMENT — registration is complete, but nothing consults `DocType`, so a
`webm` document using a Matroska-only element is read without comment. Each is a
library capability to design first; the conformance check then follows for free.
The core stays terminal on structural corruption; recovery belongs to the opt-in
`ext/fragment` paths.

## Build and fixtures

Run from `impl/go/`:

```bash
go test ./...
go vet ./...
go run ./internal/kvsgen/genfixtures
```

Run from `impl/go/integrations/kvs/`:

```bash
go test ./...
go vet ./...
go run ./examples/getmedia
```

Checking the registry against the official schemas needs those schemas fetched
first, which is what the `conformance-check` skill is for. The command it ends
up running, from the repository root:

```bash
go -C impl/go run ./internal/specconform/checkschema \
  -schema ../../.spec-cache/ebml.xml -schema ../../.spec-cache/ebml_matroska.xml -missing
```

`.golangci.yml` is the lint configuration for both modules, and
`golangci-lint run ./...` reproduces `.github/workflows/lint.yml` locally. It
runs the default linter set; the standing rules a linter cannot see are pinned
by `internal/archtest` and the test suite instead. Writing to an
already-obtained writer is the one unchecked return this tree keeps — every
other ignored return is spelled `_ =` at the call site, so it reads as a
decision.

CI runs those same commands, under `-race`, once per module. Two gates are not
reproducible by reading the code alone and are worth knowing about before a
change lands: `.github/workflows/ci.yml` regenerates the corpus and FAILS ON ANY
DIFF, because a corpus that cannot be reproduced from the public writer is a
corpus nothing keeps honest; `.github/workflows/conformance.yml` runs
`internal/specconform` against PINNED schema commits, so upstream moving can
never turn the build red on its own, and reports drift against their `master`
weekly without failing.

Fixtures under `fixtures/**/*.ebml.hex` are commented hexadecimal and entirely
synthetic; the corpus is 19 fixtures and models an unknown-size `Cluster`
throughout, each one described in `fixtures/kvs/README.json`. Golden files under `golden/**/*.jsonl` contain one JSON object per
cursor operation, replayed under all split patterns from
`tests/split_patterns.json`.

## Documentation homes

* `spec/SPEC.md` — the normative portable contract.
* Root `README.md` — LANGUAGE-NEUTRAL: what the library is, the contract, the
  implementation table, and what every implementation shares, plus the two
  things that belong to the PROJECT rather than to a language — the quick start
  of whichever implementations exist, and the `ebml` CLI. A second
  implementation costs it one table row and one quick start, nothing else.
* The `ebml` CLI is ONE TOOL FOR THE PROJECT, not one per implementation, so
  what it does, how to install it and what it prints live at the ROOT. The
  implementation that happens to build it documents only running it from a
  checkout.
* `impl/go/README.md` — the whole of the Go LIBRARY documentation: package map,
  dependency direction, entry-point selection, usage core-first then extensions,
  writing. Go usage has exactly ONE home; never restate it at the root, and
  never add a per-language section there.
* `docs/` — design notes too long for a package doc, NOT normative. Each is
  listed in `docs/README.md`; a new one goes there in the same change.
