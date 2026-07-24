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

`Fragment` exposes `Tag`/`FragmentNumber`/`ContinuationToken`/
`ProducerTimestamp` for the well-known KVS tags, `Tracks` for decoded
`TrackEntry`s (`Number`/`Type`/`CodecID`), `ClusterTimestamp`, and `Blocks`
(`[]*parser.SimpleBlock`) plus a `TrackPCM(trackNumber)` helper that
concatenates a track's frame bytes across all blocks in the Cluster.

## Lower-level API: `parser` package

For callers that need the raw cursor instead of fragment assembly, the
`parser` package exposes:

- **Cursor primitives**: `Feed`, `Peek`, `ConsumeHeader`, `EnterMaster`,
  `LeaveMaster`, `CloseMaster`, `SkipPayload`, `ReadPayload`, `FinalizeEOF`.
  Driven incrementally via `Feed(chunk)` plus a `NeedMoreData` signal;
  split-invariant regardless of how the input is chunked. `ReadPayload`
  returns a leaf element's full bytes; `SkipPayload` advances past it without
  copying.
- **Classification**: `WithKindClassifier` selects how element IDs map to
  master/leaf kinds. `KVSKindForElementID` (in `go/parser/kvs_elements.go`)
  classifies the KVS/Matroska element set — `Segment`/`Cluster`/`Tags`/
  `Tracks`/`TrackEntry`/`SimpleBlock`/... — as master vs leaf correctly.
  Without a classifier, unknown IDs default to a binary leaf, so e.g. a
  `Cluster` would be read as one opaque blob instead of entered.
- **Decode helpers**: `DecodeUint`, `DecodeInt`, `DecodeFloat`, `DecodeString`
  turn a leaf's raw payload bytes into Go values, and `ParseSimpleBlock`
  decodes a Matroska `SimpleBlock` (track-number VINT, relative timecode,
  flags, lacing) into a `*parser.SimpleBlock` with per-frame `[][]byte`.

```go
p := parser.New(parser.WithKindClassifier(parser.KVSKindForElementID))

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

## Repo layout

- `go/parser/` — the streaming cursor, VINT parsing, element ID tables and
  classifiers, decode helpers, `SimpleBlock` parsing.
- `go/fragment/` — the per-fragment assembly layer built on top of `parser`.
- `go/internal/kvsgen/` + `go/cmd/genkvs/` — the synthetic KVS fixture
  generator.
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

## Build / test

Run from the `go/` directory:

```bash
go test ./...        # fixtures + all KVS fixtures across every split pattern
go vet ./...
go run ./cmd/genkvs   # regenerate fixtures/kvs/*.ebml.hex + golden/kvs/*.jsonl
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
