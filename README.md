# ebml

`ebml` is a streaming, cursor-based EBML/Matroska library for Go 1.25. Go 1.25
is the supported toolchain baseline; the optional range-over-events convenience
uses the standard-library `iter` support. The portable core emits element events
as bytes arrive; it does not require a document tree or buffer bulk payloads. A known-size `Cluster` therefore closes
as soon as its declared bytes are consumed, even inside an unknown-size
`Segment`.

The core is specified in [`spec/SPEC.md`](spec/SPEC.md). A port implements the
cursor, event model, flow control, and registry contract there. `ts/` and `py/`
are placeholders, not additional implementations.

## Core first

The cursor is a **token pull loop**: input is pushed in chunks with `Feed`, and
events are pulled one at a time with `Next`, so the consumer owns the read loop and
keeps its state in local variables. The three distinguishable event variants are a
master header, a leaf header, and a master end; each offers exactly the operations
that are valid for it — a leaf payload cannot be requested from a master.

Decisions are taken on the header, before payload bytes need to have arrived, so
arbitrary input chunking is split-invariant and PCM can be skipped without
retention. The defaults are the cheap ones: an untouched master is descended into,
an untouched leaf has its payload skipped and never materialised.

This is the complete shape of a core-only scan:

```go
package main

import (
	"errors"
	"fmt"
	"io"

	"github.com/yacchi/ebml/matroska"
	"github.com/yacchi/ebml/parser"
)

func scan(chunks [][]byte) error {
	// An unknown-size master (a KVS Segment) has no declared end, so only a consumer
	// rule can close it before EOF. It is asked about element IDs, never about bytes.
	c := parser.NewCursor(matroska.KindForElementID, parser.WithBoundary(
		func(open, next parser.ElementID) bool {
			return next == matroska.IDEBML || next == matroska.IDSegment
		}))
	next := 0

	for {
		node, err := c.Next()
		if err != nil {
			var needMore parser.NeedMoreData
			switch {
			case errors.As(err, &needMore):
				if next < len(chunks) {
					c.Feed(chunks[next])
					next++
					continue
				}
				if err := c.Finalize(); err != nil { // the input is over
					return err
				}
				continue
			case errors.Is(err, io.EOF):
				return nil
			default:
				return err // structural: parser.IsStructural(err) is true
			}
		}

		switch n := node.(type) {
		case *parser.MasterNode:
			fmt.Println("master", n.ID(), "at", n.Offset()) // Descend (default) or Skip
		case *parser.LeafNode:
			if n.ID() != matroska.IDDocType {
				fmt.Println("leaf", n.ID(), n.Size(), "bytes") // untouched: Skip is the default
				continue
			}
			for {
				payload, err := n.Payload()
				if err == nil {
					fmt.Println("leaf", n.ID(), parser.DecodeString(payload))
					break
				}
				var needMore parser.NeedMoreData
				if !errors.As(err, &needMore) {
					return err
				}
				if next >= len(chunks) {
					if err := c.Finalize(); err != nil {
						return err
					}
				} else {
					c.Feed(chunks[next])
					next++
				}
			}
		case *parser.EndNode:
			fmt.Println("end", n.ID(), "at", n.End()) // its extent is settled
		}
	}
}

func main() {
	_ = scan(nil)
}
```

`Next` reports three outcomes a consumer must tell apart: `NeedMoreData` (feed the
next chunk, or `Finalize` when the input is over), `io.EOF` (the stream ended
cleanly), and a structural failure. A `Nodes` iterator is offered as optional
sugar, but it is not the normative shape: a plain `range` loop cannot distinguish
"the next chunk is due" from "the input is over", and that distinction is
normative for incremental input.

A node is valid only until the next `Next` call (and `Finalize`, which also advances
the cursor); read what you need, or copy the node, before pulling again. Every
exported node method — the extent accessors as much as the decisions — rejects a node
the cursor has moved past with a panic, so a stale node can never silently report a
later event's values. That guarantee has no exception: it covers the pointer the
cursor handed out, a node value the consumer copied (`v := *node`), and either of
those while the current event happens to be of the same variant. What makes it
exceptionless is that `Next` allocates a new node per event instead of refilling one
instance per variant, so a node the cursor has moved past keeps its own stamp rather
than becoming the live node. The measured price is one allocation per event and
nothing else — reading every accessor adds none and `Payload` adds none; a consumer
that cannot pay it drives the low-level `parser.Parser`, whose `Peek` reports an
`ElementHeader` by value and hands out no node, and `BenchmarkCursorScan` /
`BenchmarkParserScan` measure the same scan both ways. `LeafNode.Payload` hands out a
view of the cursor's buffer rather than a copy, so bulk PCM is not copied merely to be
looked at: those bytes are valid only until the next `Next`, must not be modified, and
a consumer that keeps them copies them (`bytes.Clone`).

The classifier is a required argument of `parser.NewCursor` and `parser.New`,
not an option: the core knows no element ID and holds no element table, so
without a classifier it could not tell a master from a leaf. There is no
built-in default to fall back on — one would silently read an unlisted master
such as a `Cluster` as a single opaque leaf — and a `nil` classifier panics at
construction.

Without a `parser.WithBoundary` rule every unknown-size master stays open until
`Finalize`, so concatenated `Segment`s would nest instead of following one another.
The rule is driven by element structure and never by scanning the payload for the
EBML magic, which is what lets PCM containing those four bytes parse without a
spurious split.

The lower-level `parser.Parser` remains exported for a consumer that needs
operation-level control (the golden op-trace tool does): `Feed`, `Peek`,
`ConsumeHeader`, `EnterMaster`, `LeaveMaster`, `CloseMaster`, `SkipPayload`,
`SkipCurrentPayload`, `ReadPayload`, and `FinalizeEOF`. `CloseMaster` is the
explicit boundary close for a master with no declared end: it accepts an
unknown-size master, or a known-size master whose declared end has already been
reached, and rejects a known-size master with payload still outstanding
(`PrematureCloseError` / `ErrPrematureClose`) rather than reparent those bytes
into the enclosing master.

## Error classification

`Cursor.Next` reports one non-failure outcome and one failure class; a consumer
built on top of it adds the second failure class:

| Class | Test | Meaning |
| --- | --- | --- |
| Structural | `parser.IsStructural(err)` | The bytes cannot be read as EBML, so the next element's position is unknown |
| Content | `errors.As(err, &ce)` with `ce *parser.ContentError` | The stream was read correctly and a consumer refused what the bytes MEAN |

`IsStructural` is true for every failure the cursor raises: `VINTLengthError`,
`TruncatedError`, `ElementOverflowError`, `UnknownSizeLeafError`,
`PrematureCloseError`, and `Invalid`, however deeply wrapped. `NeedMoreData` is
normal flow control for incremental input and is neither class — it means the next
`Feed` is the answer, and `IsStructural` is false for it.

A pull cursor never runs consumer code, so it cannot raise a content error itself;
`parser.NewContentError(id, offset, err)` is how a consumer marks its own verdict
as one — `ext/fragment` wraps an undecodable `SimpleBlock` in it. `IsStructural` is
false for it whatever it carries — even a value that is `parser.ErrStructural` or
`parser.TruncatedError` verbatim — because the classification stops at that
boundary, and only a structural failure justifies a recovery strategy that scans
bytes. `Unwrap` still reaches the consumer's own error, so `errors.Is`/`errors.As`
on its sentinels keep working through the wrapper.

`parser.ErrStructural` still exists as the class marker the cursor's own errors
unwrap to, so `errors.Is` works on an error a cursor operation returned directly.
It is not the classification test: `errors.Is` walks the whole chain, so it cannot
stop at the content boundary. Use `IsStructural`.

## Registry

`matroska` owns the RFC 9559 names, IDs, value types, and master/leaf
classification. `Default()` is immutable. Derive an extendable registry with
`NewRegistry(matroska.Default())`, then register vendor elements with
`ElementInfo` and pass its `KindForElementID` to the parser. Registering a
vendor element as `TypeMaster` makes the cursor descend into it.

Unknown elements remain readable binary leaves with their declared extent.
Unknown master-shaped payloads are not lost: parse the complete payload later
with `ext/tree`, optionally using a registry that knows the element.

## Optional Go conveniences: `ext/`

Everything under `go/ext/` is outside the cross-language contract and is built
only on exported core APIs. A missing capability in an extension is a core
capability bug, not a reason to reach into parser internals. No retention path
uses a per-element allowlist.

### `ext/fragment`

`fragment.New` assembles a continuous KVS GetMedia stream and emits one
`*fragment.Fragment` per completed `Cluster`. It is a plain pull loop over
`parser.Cursor` and is the worked example of building on the core. It retains generic element nodes,
decodes `SimpleBlock`s into `Blocks`, and provides metadata and timing helpers:
`Tag`, `Tags`, `Tracks`, `Track`, `TrackByName`, `TimestampScale`,
`ClusterTimestamp`, `BlockTime`, `StartTime`, `EndTime`, and `TrackPCM`.

Absolute block time is `(ClusterTimestamp + block.Timecode) * TimestampScale`;
both operands are in scale units and the relative timecode is signed.

Assembler options include `WithMaxRetainedPayload` for a payload cap,
`WithRegistry` for custom classification, and one option per error class, each
opt-in and each terminal by default:

| Option | Class | What it does |
| --- | --- | --- |
| `WithResync` | Structural (`parser.IsStructural(err)`) | Scans forward for the next top-level element ID, drops everything up to it, resets Segment state, resumes |
| `WithSkipContentErrors` | Content (`*parser.ContentError`) | Drops the offending element only, keeps and emits the Fragment with the rest, scans nothing |

`WithResync` is the only byte-scanning path; normal boundaries are structural. A
content error, such as a `SimpleBlock` that will not decode, is returned from
`Feed` unchanged even with `WithResync` set: it scans nothing, resets no Segment
state, and never calls that `notify`. It is `WithSkipContentErrors` that survives
it, and it can do so without losing the fragment precisely because a content
error leaves the structural position intact — the element is skipped, its ID,
absolute offset and error go to `notify`, and the Cluster still emits. Neither
option touches the other's class, and a nil `notify` disables either one, so no
failure is ever dropped silently. `UnknownSizeLeafError` is typed so an
unregistered unknown-size master can be diagnosed and fixed by extending the
registry.

### `go/kvs/examples/getmedia`

`go/kvs/examples/getmedia` is a runnable, end-to-end demonstration of driving
the separate `kvs` module over a live Amazon Connect KVS GetMedia byte stream.

Tag inheritance is not something `ext/fragment` decides: `Fragment.Tags`
returns exactly what that fragment's `Tags` element carried, non-nil and empty
when it carried none. The policy lives one layer up, in `kvs`, and it inherits
per key rather than per whole `Tags` element — a key seen on any fragment of a
given SegmentUUID stays available to later fragments of that same UUID,
regardless of which other keys the current fragment carries.
This matches the field shape observed in production Amazon Connect streams,
which is **partial** rather than all-or-nothing: `Tags` is present and
populated, but an identity key such as `ContactId` can be missing from one
fragment (typically the run's last) while the rest of the map is intact and
the SegmentUUID is unchanged. A policy that only inherits when the whole
`Tags` map is empty never fires on that shape and silently drops the identity
key exactly when it is needed; the `partial_tags` fixture exercises it.

### `ext/tree`: two orthogonal access modes

| Mode | Operations | Meaning |
| --- | --- | --- |
| Loose extraction | `Descendants` | Containment ignored; every occurrence is returned as a node |
| Strict structure | `Find` plus `Parent`, `Ancestor`, `Ancestors`, and `Path` | Exact paths and ancestry express containment |

Loose results carry their own structure, so a caller can extract broadly and
tighten a result later without re-reading the stream. There is no query DSL or
second index type.

`tree.Marshal` and `tree.MarshalBytes` write a retained tree back out through
package `writer`, the only EBML encoder in this repository. Parse followed by
marshal is byte-identical for every committed fixture, with one precondition:
nothing was elided by a payload cap. Leaf payloads go out verbatim and each
header is rebuilt at its original size-VINT width — which the retained
`HeaderLen` still states, so a non-minimal size VINT or a one-byte unknown-size
marker survives. A round trip over the corpus is therefore a conformance test of
retention itself.

## Amazon Kinesis Video Streams: the `kvs` submodule

`github.com/yacchi/ebml/kvs` is a separate Go module so the core module never
carries AWS-specific dependencies. It currently has **no AWS SDK dependency at
all**, and it does not include a GetMedia API wrapper: callers provide the
already-obtained byte stream. This package is not affiliated with, endorsed by,
or sponsored by Amazon Web Services; AWS service names appear descriptively only.

```go
package main

import (
	"fmt"
	"io"
	"os"

	"github.com/yacchi/ebml/kvs"
)

func main() {
	reader := kvs.NewReader(os.Stdin)
	for {
		fragment, metadata, err := reader.Next()
		if err == io.EOF {
			return
		}
		if err != nil {
			panic(err)
		}
		fmt.Println(metadata.FragmentNumber, len(fragment.Blocks))
	}
}
```

## Writing

The core `writer` package is the dual of the cursor: the caller supplies element
IDs and chooses each value encoder, while the writer emits the ID, size, and
payload. It provides unsigned and signed integers, 32- and 64-bit floats,
ASCII strings, UTF-8 strings, dates, binary payloads, and exact raw leaves.

Masters support three size strategies. `Buffered` retains the subtree and emits
its size at `EndMaster`, so it works with any sink and is best for small or
moderate elements. `Reserved(width)` emits a fixed-width placeholder and patches
it at `EndMaster`, so payloads stream immediately; use it with a patchable sink
and reserve enough width. `UnknownSize` streams a master without patching and
requires the reader's structural-termination rule, making it suitable for live
or concatenated streams. `Buffered` is the default.

```go
package main

import (
	"bytes"

	"github.com/yacchi/ebml/writer"
)

func write() error {
	var buf bytes.Buffer
	w := writer.New(&buf)
	if err := w.StartMaster(0x4001, writer.Buffered()); err != nil {
		return err
	}
	if err := w.Uint(0x80, 1); err != nil {
		return err
	}
	if err := w.UTF8(0x81, "hello"); err != nil {
		return err
	}
	if err := w.EndMaster(); err != nil {
		return err
	}
	return w.Close()
}

func main() {
	_ = write()
}
```

A `SimpleBlock`'s internals are a payload layout rather than EBML structure, so
its encoder sits with its decoder in `parser`: `(*parser.SimpleBlock).Append(dst)`
appends the encoded block to `dst` — track-number VINT, `int16` relative
timecode, flags, and the frames in the declared lacing — and the result is handed
to `writer.Leaf` as one binary leaf. It is the exact inverse of
`parser.ParseSimpleBlock`: byte-identical for a canonically encoded payload
(every block in the corpus, asserted in the test suite), and for a laced block a
canonical encoding that parses back to an equal value.

With a non-seekable sink such as an `io.Pipe`, the goroutine draining the read
end must already be running before the first sink write, which is the first
`StartMaster` and not `New`.

The writer has no element knowledge: use registry IDs such as
`matroska.IDEBML` at the call site when writing a known vocabulary. For a
retained tree, `tree.Marshal` and `tree.MarshalBytes` compose the same writer
primitives and preserve original legal size-VINT widths and payload bytes.
The fixture corpus is generated by the public writer and parse-then-marshal
round trips are checked byte-for-byte in the test suite.

## CLI

Run from `go/`:

```bash
go run ./cmd/ebml dump --hex ../fixtures/kvs/topology_basic.ebml.hex
go run ./cmd/ebml xml --hex ../fixtures/kvs/topology_basic.ebml.hex
go run ./internal/kvsgen/genfixtures
```

The dump is an indented structural view. A current sample from `topology_basic.ebml.hex` begins:

```text
EBML (0x1A45DFA3) [offset 0, size 19]
  EBMLVersion (0x4286) [type uint, offset 5, size 1] = 1
  EBMLReadVersion (0x42F7) [type uint, offset 9, size 1] = 1
  DocType (0x4282) [type string, offset 13, size 8] = "matroska"
Segment (0x18538067) [offset 24, size unknown]
  Info (0x1549A966) [offset 36, size 26]
```

`dump` and `xml` accept raw EBML from a file or stdin; `--hex` decodes the
commented fixture format.

## Fixture corpus

The synthetic KVS corpus covers `topology_basic`, `tail_last_fragment`,
`false_ebml_magic_in_pcm`, `multi_cluster`, `multi_segment`, `tagless_single`,
`tagless_consecutive`, `filter_mismatch`, `gap`, `scaled_timestamps`,
`unknown_elements`, `partial_tags` (a fragment with a populated but partial
`Tags` element missing its identity keys), and `two_tracks` (one `Cluster`
carrying `SimpleBlock`s for two named audio tracks). It is exercised across
every split pattern. Fixtures never contain real capture data.

## Build and test

Run from `go/`:

```bash
go test ./...
go vet ./...
go run ./internal/kvsgen/genfixtures
```

## License

Apache-2.0 (`SPDX-License-Identifier: Apache-2.0`). See [LICENSE](LICENSE).

This project is not affiliated with, endorsed by, or sponsored by Amazon Web
Services. The fixture corpus and `examples/kvs-getmedia` are written for use
with Amazon Kinesis Video Streams; that service name appears here descriptively
only, and all fixture data is synthetic.
