# ebml

`ebml` is a streaming, cursor-based EBML/Matroska library for Go. The
portable core emits element events as bytes arrive; it does not require a
document tree or buffer bulk payloads. A known-size `Cluster` therefore closes
as soon as its declared bytes are consumed, even inside an unknown-size
`Segment`.

The core is specified in [`spec/SPEC.md`](spec/SPEC.md). A port implements the
cursor, event model, flow control, and registry contract there. `ts/` and `py/`
are placeholders, not additional implementations.

## Core first

The scanner reports a master or leaf on its header. The handler decides whether
to descend or skip a master subtree, and whether to receive or skip a leaf
payload. Decisions happen before payload arrival, so arbitrary input chunking is
split-invariant and PCM can be skipped without retention.

This is the complete shape of a core-only scan:

```go
package main

import (
	"github.com/yacchi/ebml/matroska"
	"github.com/yacchi/ebml/parser"
)

func scan(chunks [][]byte) error {
	handler := parser.HandlerFuncs{
		MasterFunc: func(parser.Node) (parser.Action, error) {
			return parser.Descend, nil
		},
		LeafFunc: func(parser.Node) (parser.Action, error) {
			return parser.SkipPayload, nil
		},
	}
	scanner := parser.NewScanner(handler, matroska.KindForElementID)
	for _, chunk := range chunks {
		if err := scanner.Feed(chunk); err != nil {
			return err
		}
	}
	return scanner.Finalize()
}

func main() {
	_ = scan(nil)
}
```

The classifier is a required argument of `parser.NewScanner` and `parser.New`,
not an option: the core knows no element ID and holds no element table, so
without a classifier it could not tell a master from a leaf. There is no
built-in default to fall back on — one would silently read an unlisted master
such as a `Cluster` as a single opaque leaf — and a `nil` classifier panics at
construction.

The lower-level cursor exposes the same contract through `Feed`, `Peek`,
`ConsumeHeader`, `EnterMaster`, `LeaveMaster`, `CloseMaster`, `SkipPayload`,
`SkipCurrentPayload`, `ReadPayload`, and `FinalizeEOF`. `CloseMaster` is the
explicit boundary close for a master with no declared end: it accepts an
unknown-size master, or a known-size master whose declared end has already been
reached, and rejects a known-size master with payload still outstanding
(`PrematureCloseError` / `ErrPrematureClose`) rather than reparent those bytes
into the enclosing master.

## Error classification

`Scanner.Feed` and `Scanner.Finalize` return exactly two classes of error:

| Class | Test | Meaning |
| --- | --- | --- |
| Structural | `parser.IsStructural(err)` | The bytes cannot be read as EBML, so the next element's position is unknown |
| Handler-originated | `errors.As(err, &he)` with `he *parser.HandlerError` | The stream was read correctly and the consumer's handler refused the content |

`IsStructural` is true for every failure the cursor raises: `VINTLengthError`,
`TruncatedError`, `ElementOverflowError`, `UnknownSizeLeafError`,
`PrematureCloseError`, and `Invalid`, however deeply wrapped. `NeedMoreData` is
normal flow control for incremental input and is neither class — the scanner
absorbs it, and `IsStructural` is false for it.

A `HandlerError` records the failing event (`Op` and `Node`) and unwraps to the
handler's own error, so `errors.Is`/`errors.As` on a handler's sentinel keep
working through the wrapper. `IsStructural` is false for it whatever it carries —
even a handler that returns `parser.ErrStructural` or `parser.TruncatedError`
verbatim — because the classification stops at the handler boundary, and only a
structural failure justifies a recovery strategy that scans bytes.

`parser.ErrStructural` still exists as the class marker the cursor's own errors
unwrap to, so `errors.Is` works on an error a cursor operation returned directly.
It is not the classification test: `errors.Is` walks the whole chain, so it cannot
stop at the handler boundary. Use `IsStructural`.

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
`*fragment.Fragment` per completed `Cluster`. It retains generic element nodes,
decodes `SimpleBlock`s into `Blocks`, and provides metadata and timing helpers:
`Tag`, `Tags`, `Tracks`, `Track`, `TrackByName`, `TimestampScale`,
`ClusterTimestamp`, `BlockTime`, `StartTime`, `EndTime`, and `TrackPCM`.

Absolute block time is `(ClusterTimestamp + block.Timecode) * TimestampScale`;
both operands are in scale units and the relative timecode is signed.

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

Assembler options include `WithMaxRetainedPayload` for a payload cap,
`WithResync` for opt-in post-failure recovery, and `WithRegistry` for custom
classification. `WithResync` is the only byte-scanning path; normal boundaries
are structural. It recovers from structural failures only — those for which
`parser.IsStructural(err)` is true. A content error, such as a `SimpleBlock`
that will not decode, is returned from `Feed` unchanged even with `WithResync`
set: it scans nothing, resets no Segment state, and never calls `notify`.
`UnknownSizeLeafError` is typed so an unregistered unknown-size master can be
diagnosed and fixed by extending the registry.

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
go run ./cmd/ebml genkvs
```

The dump is an indented structural view. A current sample begins:

```text
EBML (0x1A45DFA3) [offset 0, size 19]
  EBMLVersion (0x4286) [type uint, offset 5, size 1] = 1
  EBMLReadVersion (0x42F7) [type uint, offset 9, size 1] = 1
```

`dump` and `xml` accept raw EBML from a file or stdin; `--hex` decodes the
commented fixture format.

## Fixture corpus

The synthetic KVS corpus covers `topology_basic`, `tail_last_fragment`,
`false_ebml_magic_in_pcm`, `multi_cluster`, `multi_segment`, `tagless_single`,
`tagless_consecutive`, `filter_mismatch`, `gap`, `scaled_timestamps`, and
`unknown_elements`. It is exercised across every split pattern. Fixtures never
contain real capture data.

## Build and test

Run from `go/`:

```bash
go test ./...
go vet ./...
go run ./cmd/ebml genkvs
```
