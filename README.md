# ebml-reader

A streaming, cursor-based EBML/Matroska reader for Go. It parses a continuous
byte stream incrementally and lets the caller observe each element as it
completes — in particular, it fires an "end master" event for a **known-size
`Cluster` nested inside an unknown-size `Segment`** the moment the Cluster's
declared size is consumed, **without waiting for the Segment to close**.

## Why

Live **Amazon Kinesis Video Streams (KVS) GetMedia** returns a single
continuous HTTP body of concatenated **unknown-size Matroska `Segment`s** —
one per KVS fragment. Each Segment contains `Tracks` + `Tags` + exactly one
**known-size `Cluster`** of `SimpleBlock`s.

A consumer using a whole-document parser can only emit a fragment once its
Segment is known to be over — i.e. when the **next** fragment's EBML header
arrives, or on connection EOF. That produces two latency problems:

1. **Boundary-wait** (~one fragment): a fragment can't be emitted until the
   next one's header shows up.
2. **End-of-stream tail**: the **last** fragment has no following header, so
   it is held until the connection actually closes (EOF) — adding seconds of
   latency at call end.

A cursor that fires `end_master` for the Cluster as soon as its known size is
consumed removes both. It also removes the need to byte-scan the stream for
the EBML magic (`1A 45 DF A3`) to find fragment boundaries — a fragile
heuristic, since that 4-byte sequence can occur inside SimpleBlock PCM.
`ebml-reader` drives the parse structurally instead.

## Packages

| Package | Purpose |
| --- | --- |
| `parser` | Incremental streaming cursor, VINT handling, leaf decode helpers, and `SimpleBlock` parsing |
| `matroska` | Standard Matroska element registry and generic master/leaf classifier |
| `fragment` | Generic per-`Cluster` assembly with decoded tracks, tags, timestamps, and blocks |

## Install

```bash
go get github.com/yacchi/ebml-reader
```

## Quick start: `fragment` package

The `fragment` package is a per-fragment assembly layer over the streaming
cursor. It buffers a Segment's `Tags`/`Tracks` and emits one `*fragment.Fragment`
per completed `Cluster`, attaching that metadata.

```go
package main

import (
	"fmt"
	"io"

	"github.com/yacchi/ebml-reader/fragment"
)

// process reads a KVS GetMedia HTTP body from r and prints each fragment's
// ContactId plus per-track PCM byte counts as soon as its Cluster completes.
func process(r io.Reader) error {
	a := fragment.New()

	buf := make([]byte, 4096)
	for {
		n, err := r.Read(buf)
		if n > 0 {
			frags, ferr := a.Feed(buf[:n])
			if ferr != nil {
				return ferr
			}
			for _, f := range frags {
				printFragment(f)
			}
		}
		if err == io.EOF {
			tail, ferr := a.Finalize()
			if ferr != nil {
				return ferr
			}
			for _, f := range tail {
				printFragment(f)
			}
			return nil
		}
		if err != nil {
			return err
		}
	}
}

func printFragment(f *fragment.Fragment) {
	contactID, _ := f.Tag("ContactId")
	fmt.Printf("ContactId=%s\n", contactID)
	for _, tr := range f.Tracks {
		fmt.Printf("track %d: %d PCM bytes\n", tr.Number, len(f.TrackPCM(tr.Number)))
	}
}

func main() {}
```

This mirrors `go/fragment/example_test.go`, which feeds a committed fixture in
fixed-size chunks to demonstrate that chunk boundaries don't affect the
result (see `Example_streamingAssembly`).

`Assembler.Feed` is push-based: feed arbitrary `[]byte` chunks, get back any
`*Fragment`s that completed within that chunk, then call `Finalize()` once at
EOF to close the trailing unknown-size Segment and surface any structural
error. The whole pipeline is **split-invariant** — the sequence of Fragments
is identical regardless of how the input bytes are chunked.

`Fragment` exposes a generic `Tag(name)` accessor and the `Tags` field mapping
tag names to values — callers read AWS metadata such as
`AWS_KINESISVIDEO_FRAGMENT_NUMBER` or `AWS_KINESISVIDEO_PRODUCER_TIMESTAMP`
themselves through it — `Tracks` for decoded `TrackEntry`s
(`Number`/`Type`/`CodecID`), `ClusterTimestamp`, and `Blocks`
(`[]*parser.SimpleBlock`) plus a `TrackPCM(trackNumber)` helper that
concatenates a track's frame bytes across all blocks in the Cluster.

## KVS GetMedia context

Parsing KVS GetMedia output is otherwise officially supported by AWS only through
the Java `amazon-kinesis-video-streams-parser-library`. This library provides the
equivalent capability in Go: streaming, per-fragment, low-latency assembly with no
KVS-specific API. AWS metadata remains ordinary Matroska `SimpleTag` data, so read
it through the generic accessor:

```go
fragmentNumber, _ := f.Tag("AWS_KINESISVIDEO_FRAGMENT_NUMBER")
contactID, _ := f.Tag("ContactId")
```

See the runnable end-to-end example in
[`go/examples/kvs-getmedia`](go/examples/kvs-getmedia), which also demonstrates
producer timestamp parsing and per-track PCM reporting.

## Lower-level API: `parser` package

For callers that need the raw cursor instead of fragment assembly, the
`parser` package exposes:

- **Cursor primitives**: `Feed`, `Peek`, `ConsumeHeader`, `EnterMaster`,
  `LeaveMaster`, `CloseMaster`, `SkipPayload`, `ReadPayload`, `FinalizeEOF`.
  Driven incrementally via `Feed(chunk)` plus a `NeedMoreData` signal;
  split-invariant regardless of how the input is chunked. `ReadPayload`
  returns a leaf element's full bytes; `SkipPayload` advances past it without
  copying.
- **Element IDs**: `parser.ElementID` is the wire VINT value *including* its
  length-marker bits (so `SimpleBlock` is `0xA3` and `Cluster` is
  `0x1F43B675`). Its `String()` method prints that conventional hex form. The
  `parser` package carries no element table of its own; names and value types
  live in `go/matroska`.
- **Classification**: `WithKindClassifier` selects how element IDs map to
  master/leaf kinds — any `func(parser.ElementID) parser.Kind` will do.
  `matroska.KindForElementID` (in the `go/matroska` registry package)
  classifies the standard Matroska element set —
  `Segment`/`Cluster`/`Tags`/`Tracks`/`TrackEntry`/`SimpleBlock`/... — as
  master vs leaf correctly. Without it, only the EBML header and `Segment` are
  treated as masters and every other ID defaults to a binary leaf, so e.g. a
  `Cluster` would be read as one opaque blob instead of entered.
- **Decode helpers**: `DecodeUint`, `DecodeInt`, `DecodeFloat`, `DecodeString`
  turn a leaf's raw payload bytes into Go values, and `ParseSimpleBlock`
  decodes a Matroska `SimpleBlock` (track-number VINT, relative timecode,
  flags, lacing) into a `*parser.SimpleBlock` with per-frame `[][]byte`.

```go
p := parser.New(parser.WithKindClassifier(matroska.KindForElementID))

for _, chunk := range chunks { // e.g. one byte at a time
	p.Feed(chunk)
	for {
		h, err := p.Peek()
		if err != nil {
			if _, ok := err.(parser.NeedMoreData); ok {
				break // wait for the next chunk
			}
			panic(err)
		}

		if h.Kind == parser.KindEndMaster {
			_ = p.LeaveMaster()
			continue
		}

		_, _ = p.ConsumeHeader()
		if h.Kind == parser.KindMaster {
			_ = p.EnterMaster()
		} else {
			_ = p.SkipPayload()
		}
	}
}

// EOF: close any remaining unknown-size masters (e.g. the trailing Segment).
_, _ = p.FinalizeEOF()
```

### Matroska registry API

The `matroska` package is the single source of truth for standard element
metadata. `Lookup` finds an element by its typed `parser.ElementID`;
`NameForID` and `Describe` return its registered name or a readable hex ID;
`IDForName` performs the exact-name reverse lookup; `Elements` returns the
complete sorted registry; and `TypeFor` returns its `ValueType`. Call
`ValueType.String()` for labels such as `master`, `uint`, `string`, `binary`,
and `block`. The package also exposes `ID<Name>` constants for every registered
element, including `matroska.IDSegmentUUID`, plus `KindForElementID` for parser
classification.

Names follow **RFC 9559**, which supersedes the older matroska.org spelling of
several elements (`SegmentUUID`, not `SegmentUID`; `FileMediaType`, not
`FileMimeType`; `Timestamp`/`TimestampScale`, not `Timecode`/`TimecodeScale`).
`IDForName` still accepts those well-known pre-RFC names as aliases, but the
canonical RFC name is the only one `Elements`, `Lookup`, `NameForID`, and
`Describe` ever return. `SimpleBlock` and `Block` are registered as
`TypeBlock`: the RFC types them as binary, and `TypeBlock` is this library's
refinement marking the binary payloads it can decode with
`parser.ParseSimpleBlock`.

## Repo layout

- `go/parser/` — the streaming cursor, VINT parsing, decode helpers,
  `SimpleBlock` parsing.
- `go/matroska/` — the standard Matroska element registry (`Lookup`,
  `NameForID`, `Describe`, `IDForName`, `Elements`, `TypeFor`, `ValueType`),
  the `ID<Name>` constants (one for every registered element, such as
  `matroska.IDSegmentUUID`), and the `KindForElementID` classifier passed to
  `parser` (the single source of truth for element IDs, names, value types, and
  kinds, used by `fragment`, the CLI, and the fixture generator).
- `go/fragment/` — the per-fragment assembly layer built on top of `parser`.
- `go/examples/kvs-getmedia/` — runnable example reading a KVS GetMedia byte
  stream and printing per-fragment AWS metadata, tracks, and PCM byte counts.
- `go/internal/kvsgen/` — the synthetic KVS fixture generator, driven by the
  `genkvs` subcommand of `go/cmd/ebml-reader/`.
- `go/cmd/ebml-reader/` — the `ebml-reader` CLI: `dump` (indented element
  tree), `xml` (well-formed XML), and `genkvs` (regenerate the fixture corpus).
- `fixtures/` — commented hex fixtures (`*.ebml.hex`): `#` lines describe the
  layout, the body is whitespace-separated hex bytes. Every fixture is 100%
  synthetic (fake UUIDs, counter/tone PCM, synthetic tokens) — this repo never
  carries real capture data.
- `golden/` — one JSON object per cursor event
  (`{step, op, offset, depth, id, size, kind, header_len}`), one file per
  fixture.
- `tests/split_patterns.json` — the split-invariance testing scheme: every
  fixture is replayed under `one_byte`, `fibonacci`, and `random` chunkings
  and must produce identical golden output under all of them, proving the
  cursor's result does not depend on how the caller happens to chunk the
  input stream.
- `spec/SPEC.md` — the cursor's behavioral spec.

## CLI

Run from the `go/` directory:

```bash
go run ./cmd/ebml-reader dump --hex ../fixtures/kvs/topology_basic.ebml.hex
go run ./cmd/ebml-reader xml --hex ../fixtures/kvs/topology_basic.ebml.hex
go run ./cmd/ebml-reader genkvs
```

The `dump` command prints an indented element tree. For example:

```text
Segment (0x18538067) [offset 0, size unknown]
  Info (0x1549A966) [offset 12, size 29]
    SegmentUUID (0x73A4) [type binary, offset 17, size 16] = binary 16 bytes: ...
```

## Build / test / regenerate

Run from the `go/` directory:

```bash
go test ./...        # fixtures + all KVS fixtures across every split pattern
go vet ./...
go run ./cmd/ebml-reader genkvs   # regenerate fixtures/kvs/*.ebml.hex + golden/kvs/*.jsonl
```

A fuzz target (`FuzzParser` in `go/parser/fuzz_test.go`) seeds from the
committed fixtures and drives the cursor over arbitrary/mutated bytes to guard
against panics on malformed input.

## Scope

This library streams the KVS fragment shape correctly and cheaply — it is
**not** a general-purpose Matroska muxer/demuxer. The intended division of
labor on the consumer side: a batch parser handles already-finite payloads
(e.g. list-then-fetch APIs that return complete fragments), and this
streaming cursor handles only the continuous GetMedia-style byte stream,
where per-Cluster incremental emission is what removes the boundary-wait and
end-of-stream tail.
