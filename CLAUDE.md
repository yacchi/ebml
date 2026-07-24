# CLAUDE.md — ebml-reader

Guidance for Claude Code (and human contributors) working in this repository.

**Language: this repo is English-only.** It is OSS-destined and the audience
(streaming-capable EBML readers) is niche, so English maximizes reach. Write all
new docs, comments, manifests, and identifiers in English.

## What this is

A **streaming, cursor-based EBML/Matroska reader** (MVP/experiment). Its
distinguishing goal versus typical whole-document or push (SAX) parsers: parse a
continuous MKV byte stream **incrementally** and let the caller observe each
element as it completes — in particular, fire "end master" for a **known-size
`Cluster` nested inside an unknown-size `Segment`** the moment the Cluster's
declared size is consumed, **without waiting for the Segment to close**.

## Why (motivating use case)

Live **Amazon Kinesis Video Streams (KVS) GetMedia** returns a single continuous
HTTP body of concatenated **unknown-size Matroska `Segment`s** — one per KVS
fragment. Each Segment contains `Tracks` + `Tags` + exactly one **known-size
`Cluster`** of `SimpleBlock`s (for Amazon Connect: 8 kHz / 16-bit PCM, one mono
stream per channel).

A consumer using a whole-document parser can only emit a fragment once its
Segment is known to be over — i.e. when the **next** fragment's EBML header
arrives, or on connection EOF. That produces two latency problems:

1. **Boundary-wait** (~one fragment): a fragment can't be emitted until the next
   one's header shows up.
2. **End-of-stream tail**: the **last** fragment has no following header, so it is
   held until the connection actually closes (EOF) — adding seconds of latency at
   call end.

A cursor that fires `end_master` for the Cluster as soon as its known size is
consumed removes **both**. It also removes the need to **byte-scan** the stream
for the EBML magic (`1A 45 DF A3`) to find fragment boundaries — a fragile
heuristic, because that 4-byte sequence can occur inside SimpleBlock PCM. This
library aims to be that cursor.

## Current state

**Working** (`go/`, module `github.com/yacchi/ebml-reader`, Go 1.22):

- Cursor primitives in `go/parser/parser.go`: `Feed`, `Peek`, `ConsumeHeader`,
  `EnterMaster`, `LeaveMaster`, `CloseMaster`, `SkipPayload`/`SkipCurrentPayload`,
  `ReadPayload`, `FinalizeEOF`. Incremental via `Feed(chunk)` + a `NeedMoreData`
  signal; **split-invariant** (identical result regardless of how the input is
  chunked). `ReadPayload` returns a leaf element's full bytes (not just skip).
- VINT parsing incl. unknown-size detection (`go/parser/vint.go`), enforcing
  `MaxElementIDLength`/`MaxElementSizeLength` (`go/parser/elements.go`) and
  reporting over-length VINTs as `VINTLengthError` (`go/parser/errors.go`).
- **Typed element IDs and registry API**: `parser.ElementID`
  (`go/parser/elements.go`) is the wire VINT value *including* its length-marker
  bits, and flows through `ElementHeader.ID`, `KindClassifier`, `ClosedMaster`
  and the typed errors. Its `String()` is the EBML-conventional form
  (`0xA3`, `0x1F43B675`). `parser` keeps **no element table**: it has only an
  unexported default classifier that treats the EBML header and `Segment` as
  masters and everything else as a binary leaf, so real streams must be driven
  with `matroska.KindForElementID`. `go/matroska` is the single source of truth:
  its registry API provides `Lookup`, `NameForID`, `Describe`, `IDForName`,
  `Elements`, `TypeFor`, and `ValueType` (whose `String()` gives the value type
  label), alongside an `ID<Name>` constant for every registered element, such
  as `matroska.IDSegmentUUID`. Element names/IDs/types follow **RFC 9559** (it
  supersedes the older matroska.org names — `SegmentUUID`, `FileMediaType`,
  `Timestamp`/`TimestampScale`); `IDForName` additionally accepts well-known
  pre-RFC names as aliases, and only `IDForName` does. `TypeBlock`
  (`SimpleBlock`/`Block`) is a deliberate library-level refinement of the RFC's
  binary type, not a spec type.
- **Malformed-input guards**: `FinalizeEOF` reports EOF inside a partial header
  or unfinished declared payload as `TruncatedError`, and a child element whose
  extent overflows its known-size parent master yields `ElementOverflowError`
  (`go/parser/errors.go`, exercised in `go/parser/defects_test.go`).
  `ParseSimpleBlock` rejects track number 0; reserved flag bits (0x70) are
  deliberately tolerated.
- **Standard Matroska element registry + generic classifier** (`go/matroska/`):
  `KindForElementID`, the element table (keyed by `parser.ElementID`) and the
  `ID<Name>` constants — the single source of truth for element IDs and
  names, used by `fragment`, the CLI and the fixture generator. Drive the parser with
  `parser.New(parser.WithKindClassifier(matroska.KindForElementID))` so
  `Segment`/`Cluster`/`Tags`/`Tracks`/`TrackEntry`/`SimpleBlock`/... classify
  as master vs leaf correctly. (Without it the parser's minimal default
  classifier applies, so a `Cluster` would be read as one opaque binary blob.) `CRC-32` and
  `Void` elements classify as skippable binary leaves — their bytes are not
  validated/interpreted, just skipped by declared size. `parser` stays the
  bottom layer and does not import `matroska`; the KVS stream needs no
  library-side special elements — the standard registry covers it and
  consumers read AWS metadata themselves via the generic tag map.
- **Leaf value decoding** (`go/parser/decode.go`, `go/parser/block.go`):
  `DecodeUint`, `DecodeInt`, `DecodeFloat`, `DecodeString` for scalar leaf
  payloads, and `ParseSimpleBlock` for a Matroska `SimpleBlock` (track-number
  VINT, int16 relative timecode, flags, and Xiph/fixed/EBML lacing) into a
  `*SimpleBlock` with per-frame `[][]byte`.
- **Generic per-fragment assembly** (`go/fragment/`): `fragment.New()` returns an
  `Assembler` that drives the cursor over a continuous EBML/Matroska byte stream
  and emits one `*Fragment` per completed `Cluster`, with that Segment's buffered
  `Tags`/`Tracks` attached (`Tags`, `Tracks`, `ClusterTimestamp`, `Blocks`).
  `Fragment` exposes a generic `Tag(name)` accessor (consumers read
  AWS metadata like `AWS_KINESISVIDEO_FRAGMENT_NUMBER` themselves) and
  `TrackPCM(trackNumber)`. A Segment with multiple
  Clusters yields multiple Fragments sharing its Tags/Tracks; a tagless
  Segment yields a Fragment with an empty tag map. See
  `go/fragment/example_test.go` (`Example_streamingAssembly`).
- **Synthetic KVS test corpus** proving the KVS topology and real-world quirks:
  `fixtures/kvs/*.ebml.hex` + `golden/kvs/*.jsonl`, exercised by
  `go/matroska/kvs_fixture_test.go` across every split pattern. 9 cases:
  - `topology_basic`, `tail_last_fragment` — known-size Cluster inside an
    unknown-size Segment; the Segment is closed **only** by `FinalizeEOF`, while
    the Cluster's end is observable earlier (the tail-fix property).
  - `false_ebml_magic_in_pcm` — a SimpleBlock whose PCM literally contains
    `1A 45 DF A3`; the cursor must read it as one sized leaf and **not** mis-split
    (proves the structural reader beats byte-scanning).
  - `multi_cluster` — multiple known-size Clusters in one Segment, each
    emitting its own Fragment.
  - `multi_segment`, `tagless_single`, `tagless_consecutive`, `filter_mismatch`
    (ContactId change mid-stream), `gap` (dropped fragment).
- **Fixture generator** (100% synthetic): `go/internal/kvsgen/`, driven by the
  `genkvs` subcommand of `go/cmd/ebml-reader/`.
- **CLI** (`go/cmd/ebml-reader/`, binary `ebml-reader`): `dump` (indented
  element tree), `xml` (well-formed XML), and `genkvs` (regenerate the fixture
  corpus). `dump`/`xml` stream raw EBML from a FILE or stdin; `--hex` decodes the
  commented-hex fixture format instead.
- **Fuzz target**: `FuzzParser` (`go/parser/fuzz_test.go`) seeds from the
  committed fixtures and drives the cursor over arbitrary/mutated bytes,
  guarding against panics on malformed input.

**Not done — remaining roadmap:**

1. **Nested unknown-size masters**: only a single level (the top-level
   Segment) is supported as unknown-size; nesting is irrelevant for KVS, whose
   Clusters are known-size, but is unsupported for general Matroska input.
2. **CRC-32 validation**: `CRC-32` elements are currently skipped as opaque
   binary leaves rather than verified against their parent's payload.
3. **Broader Matroska coverage**: corrupt/garbage-byte recovery beyond what
   the fuzz target exercises for panic-safety, and element types outside the
   KVS fragment shape (`Segment`/`Info`/`Tracks`/`Tags`/`Cluster` and their
   children) are not modeled.

Previously tracked here and now done: leaf value decoding helpers
(`DecodeUint`/`DecodeInt`/`DecodeFloat`/`DecodeString`/`ParseSimpleBlock`),
the per-fragment assembly API (`go/fragment`), and the module path
(`github.com/yacchi/ebml-reader`) — see the "Working" list above.

## Build / test / regenerate

Run from the `go/` directory:

```bash
go test ./...        # tiny fixture + all KVS fixtures across every split pattern
go vet ./...
go run ./cmd/ebml-reader genkvs  # regenerate fixtures/kvs/*.ebml.hex + golden/kvs/*.jsonl + README.json
```

## Conventions

- **Commit messages are English too** (Conventional Commits). The English-only
  rule at the top of this file covers git history, not just the tree — it
  overrides any personal/global convention that defaults to another language.
- **Fixtures** (`fixtures/**/*.ebml.hex`): commented hex — `#` lines describe the
  layout; the body is whitespace-separated hex bytes.
- **Golden** (`golden/**/*.jsonl`): one JSON object per cursor event —
  `{step, op, offset, depth, id, size, kind, header_len}`.
- **Split patterns** (`tests/split_patterns.json`): `one_byte` / `fibonacci` /
  `random`. Every fixture must produce identical golden under all of them.
- **Data safety (hard rule):** every fixture is **100% synthetic** — fake UUIDs,
  counter/tone PCM, synthetic tokens. **Never** commit real capture data (real
  ContactId / InstanceId / customer audio). This repo is OSS-destined; regenerate
  synthetic data via `ebml-reader genkvs`, never derive fixtures from a real capture.

## KVS / Matroska structure reference

- Stream = concatenated **unknown-size** `Segment`s (one per KVS fragment).
- Each Segment: `[Info?] Tracks Tags Cluster`. The `Cluster` is **known-size** and
  holds a `Timestamp` + `SimpleBlock`s. A `SimpleBlock` = track-number VINT,
  int16 relative timecode, flags byte, then PCM.
- AWS metadata lives in `Tags` → `SimpleTag` (`TagName`/`TagString`):
  `AWS_KINESISVIDEO_PRODUCER_TIMESTAMP`, `AWS_KINESISVIDEO_FRAGMENT_NUMBER`,
  `AWS_KINESISVIDEO_CONTINUATION_TOKEN`, `ContactId`, `InstanceId`.
- All element IDs used above are defined in `go/matroska/elements.go`; verify
  any additions against the Matroska specification.

## Scope note

The intended division of labor on the consumer side is: a batch parser handles
already-finite payloads (e.g. list-then-fetch APIs that return complete
fragments), and **this streaming cursor handles only the continuous GetMedia-style
byte stream**, where per-Cluster incremental emission is what removes the
boundary-wait and end-of-stream tail. Keep that boundary in mind: this library
does not need to be a general-purpose Matroska muxer/demuxer — it needs to stream
the KVS fragment shape correctly and cheaply.
