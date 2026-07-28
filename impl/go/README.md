# `impl/go/` — the Go implementation

[![Go Reference](https://pkg.go.dev/badge/github.com/yacchi/ebml/impl/go.svg)](https://pkg.go.dev/github.com/yacchi/ebml/impl/go)
[![Lint](https://github.com/yacchi/ebml/actions/workflows/lint.yml/badge.svg?branch=main)](https://github.com/yacchi/ebml/actions/workflows/lint.yml)
[![Go 1.25](https://img.shields.io/badge/go-1.25-00ADD8?logo=go&logoColor=white)](https://go.dev/dl/)
[![codecov](https://codecov.io/gh/yacchi/ebml/branch/main/graph/badge.svg?flag=core)](https://codecov.io/gh/yacchi/ebml?flags%5B0%5D=core)

The Go-specific badges live here rather than at the repository root, which stays
language-neutral.

The Go module `github.com/yacchi/ebml/impl/go`, plus one module per integration —
currently `github.com/yacchi/ebml/impl/go/integrations/kvs` under `integrations/kvs/`.
This is the Go documentation: the map of
the packages, then how to use them. The repository root stays language-neutral, so
nothing about Go lives above this file.

Go 1.25 is the supported toolchain baseline: the source-owning drivers' range-over-events
surfaces use the standard-library `iter` support.

| To read about | Go to |
| --- | --- |
| The normative portable contract, which this implements | [`../../spec/SPEC.md`](../../spec/SPEC.md) |
| What the project is, and what every implementation shares | [`../../README.md`](../../README.md) |
| Why the shape is what it is (non-normative notes) | [`../../docs/`](../../docs/) |
| Any single package's exact rules | that package's doc comment |

## Packages

The core is what a port must agree on; `ext/` is Go convenience built only on
exported core API; `integrations/` adapts to one named outside system and is a
separate module per system.

| Package | Kind | What it is |
| --- | --- | --- |
| `parser` | core | The reading core: `Cursor.Next`/`Feed`/`Finalize`, nodes, flow control, VINTs, error classes. Holds NO element knowledge — `KindClassifier` is a required argument. |
| `crc` | core | The CRC-32 primitive, a leaf package importing nothing from this module. |
| `matroska` | core | The immutable RFC 9559 registry: IDs, names, types, containment, `StreamBoundary`. The only place element IDs live. |
| `writer` | core | The one EBML encoder in the repository. Holds no element knowledge either; the caller picks the value type. |
| `tree` | core | The retained document model: loose `Descendants`, strict `Find`, `Marshal`, `VerifyChecksum`. |
| `stream` | core | Owns an `io.Reader` and answers `NeedMoreData`. Whole reading surface: `Nodes() iter.Seq2[parser.Node, error]`. |
| `ext/scope` | ext | Tracks one master and the elements that completed directly inside it. |
| `ext/tags` | ext | Target-aware views over retained `Tags`. The only tag-traversal implementation, and the only place tag accessors live. |
| `ext/fragment` | ext | Assembles one `Fragment` per completed `Cluster`, with recovery and delivery options. |
| `integrations/kvs` | integration (separate module) | All Amazon KVS knowledge: tag names, typed `Metadata`, wall-clock times, `MetadataComplete`. Conventions only — no AWS SDK dependency and no GetMedia wrapper. |
| `cmd/ebml` | binary | The `ebml` CLI: `dump` and `xml`, both driving `stream`. |

## Dependency direction

Pinned by `internal/archtest`, computed from `go list` rather than maintained by
hand, so an intentional change shows up in a diff:

```
parser  crc                 <- import nothing from this module
   |     |
   +--+--+
      |
   writer            matroska        stream
      |                 |               |
      +--------+--------+           (parser only)
               |
             tree

ext/scope    -> core                    <- every ext package is a leaf:
ext/tags     -> core                       none imports another
ext/fragment -> core

integrations/kvs -> ext/fragment + ext/tags + core   (its own module)
```

Three rules are enforced, not merely intended: `parser` never imports `tree` (the
StAX reader may not reach retained document state), no core package imports
anything from `ext/`, and **no `ext` package imports another `ext` package**. An
`ext` package is a way of USING the core, and a way of using something is not a
prerequisite of another way of using it.

Composing several of those ways is what an integration does, and it is why
`integrations/` is a layer of its own rather than more of `ext/`: `kvs` reads
fragments through `ext/fragment` and their tags through `ext/tags`, so it could
not live under `ext/` without breaking the leaf rule. `integrations/doc.go`
states the rest of the policy — conventions only, never the outside system's API
or transport, and no integration imports another.

That third rule is recent, and both edges it removed were the same mistake:
`ext/fragment` imported `ext/tags` for `Fragment.Tag`/`Tags`, which were
`ext/tags` applied to `Target{}` behind a plainer name, and `ext/tags` imported
`ext/scope` for a `Read(*scope.Scope)` that gave the base name to the narrow
case. A convenience accessor is how such an edge grows back, which is why the
rule is a test and not a paragraph. A `kvs`-style consumer composes the two ext
packages itself, which is the composition point that was always meant to hold
it. Separately, `ext/fragment` deliberately does NOT build its assembly on
`scope.Tracker`, and its doc says why a future reader should not "fix" that.

## Choosing an entry point

Every reading layer exists because of WHO OWNS THE BYTES. That is the library's
central distinction, and the answer decides both the layer and its shape — see
[`../../docs/pull-shape-across-languages.md`](../../docs/pull-shape-across-languages.md).

| You want | Use | Surface |
| --- | --- | --- |
| Element events, and you push the bytes | `parser.NewCursor` | `Next` returns an event, need-more-data, or end of input |
| Element events, and the library reads | `stream.New` | `Nodes()` iterator; no exported `Next` |
| One value per completed `Cluster`, you push | `fragment.New` | `Feed(chunk)` returns what was released, then `Finalize` |
| One value per completed `Cluster`, library reads | `fragment.NewReader` | `Fragments()` iterator; no exported `Next` |
| KVS fragments with typed metadata | `kvs.NewReader` | `Next()` returns fragment, `Metadata`, error |
| To write EBML | `writer.New` | `StartMaster`/`EndMaster` plus one method per value type (`Uint`, `Int`, `Float`, `String`, `UTF8`, `Date`, `Binary`, `Leaf`) |
| To write a retained tree back out | `tree.Marshal` | Byte-identical for every committed fixture |

An iterator appears exactly where the layer owns the source, because only there
does need-more-data have somewhere to go. Where the caller pushes bytes, the pull
stays an explicit call — a two-outcome protocol cannot state three outcomes
without lying about one.

The rest of this file is how to use them: the core first, then the optional
extensions, then writing and the CLI.

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

	"github.com/yacchi/ebml/impl/go/matroska"
	"github.com/yacchi/ebml/impl/go/parser"
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
			// Its extent is settled, and Reason says which of the three ways it
			// closed: ClosedByDeclaredEnd, ClosedByBoundary, or ClosedByEndOfInput.
			fmt.Println("end", n.ID(), "at", n.End(), "by", n.Reason())
		}
	}
}

func main() {
	_ = scan(nil)
}
```

An `*parser.EndNode` also says WHY the master closed, which its extent cannot:
`ClosedByDeclaredEnd` is a known-size master reaching its declared end,
`ClosedByBoundary` is the boundary rule ending an unknown-size master on an element
that cannot be its child, and `ClosedByEndOfInput` is a master still open when
`Finalize` declared the input over. The last one is **not** a truncation verdict —
a complete live stream whose Segment is unknown-size ends exactly that way, and
whether bytes were lost is `parser.TruncatedError`'s question.

`Next` reports three outcomes a consumer must tell apart: `NeedMoreData` (feed the
next chunk, or `Finalize` when the input is over), `io.EOF` (the stream ended
cleanly), and a structural failure. The cursor therefore offers no iterator: a
`range` loop carries two outcomes and cannot distinguish "the next chunk is due"
from "the input is over", and that distinction is normative for incremental input.
An iterator belongs to the layer that owns the byte source and has already
answered `NeedMoreData` — that is [`stream`](#stream), below.

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
The fragment boundary rule ends any open master when a new top-level document
begins, and ends an unknown-size `Cluster` at the first registered element that
cannot be its child. It is deny-only: an element the registry does not know never
ends a master, because a false boundary corrupts the parse while a missed
boundary only delays closure. The rule is driven by element structure and never
by scanning the payload for the EBML magic, which is what lets PCM containing
those four bytes parse without a spurious split.

The lower-level `parser.Parser` remains exported for a consumer that needs
operation-level control (the golden op-trace tool does): `Feed`, `Peek`,
`ConsumeHeader`, `EnterMaster`, `LeaveMaster`, `CloseMaster`, `SkipPayload`,
`SkipCurrentPayload`, `ReadPayload`, and `FinalizeEOF`. `CloseMaster` is the
explicit boundary close for a master with no declared end: it accepts an
unknown-size master, or a known-size master whose declared end has already been
reached, and rejects a known-size master with payload still outstanding
(`PrematureCloseError` / `ErrPrematureClose`) rather than reparent those bytes
into the enclosing master.

### `stream`

`stream.Stream` owns an `io.Reader` and answers `NeedMoreData`, so consumers
above it see only nodes or a real failure. It preserves the header-first
property: skipping a master or leaf does not materialise its payload.

`Nodes` is the whole reading surface -- there is deliberately no `Next`. A pull
has three outcomes (an event, need-more-data, end of input) and an iterator
carries two, which is why `parser.Cursor`, whose caller supplies the bytes, keeps
an explicit `Next`. This layer owns the source and answers need-more-data by
reading, so only two outcomes ever reach the consumer and the iterator is an
exact, not a lossy, spelling of the contract --
[docs/pull-shape-across-languages.md](../../docs/pull-shape-across-languages.md) states
the same split for other languages. The end of the input ends the loop; any other
failure arrives once, as the final pair, with a nil node.

```go
package main

import (
	"bytes"
	"fmt"

	"github.com/yacchi/ebml/impl/go/stream"
	"github.com/yacchi/ebml/impl/go/matroska"
	"github.com/yacchi/ebml/impl/go/parser"
)

func main() {
	s := stream.New(bytes.NewReader(nil), matroska.KindForElementID)
	for node, err := range s.Nodes() {
		if err != nil {
			panic(err)
		}
		fmt.Println(node.ID(), node.Kind(), node.Offset())
		if leaf, ok := node.(*parser.LeafNode); ok {
			_, _ = s.Payload(leaf)
		}
	}
}
```

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

`parser.TruncatedError` — the input ended inside an element — carries the evidence
for a decision this library cannot make. Whether a truncated tail is a fault
depends on the transport, not on the bytes: a live connection ending at an
arbitrary offset is normal, a finite body ending mid-element is a transfer fault.
So `errors.As` it and read the fields: `EndOffset` is the absolute offset at which
the input ended, on the same axis as `Node.Offset` (`WithStartOffset` included),
and `ID` is the innermost element open there once `HasID` says it names one.
`InHeader` tells the two cuts apart: false means the cut fell inside a declared
payload, so `ID` is the element that was cut; true means it fell inside a header,
whose ID VINT is part of what was lost, so `ID` names the enclosing master
instead. The message stays `truncated input` / `truncated input: <msg>`; the
evidence is in fields, never in the text, and never read out of `Msg`.

A tail that ends on an element boundary inside a **known-size** master is not a
`TruncatedError` at all — nothing was cut mid-element, the master's declared end
is just never reached — and arrives as an `Invalid`. Classify a short tail on
both.

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
with `tree`, optionally using a registry that knows the element.

## `tree`: two orthogonal access modes

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

`Marshal` never GENERATES a CRC-32 element. A document that carried one retained
it as an ordinary child and it goes back out verbatim, which is what keeps the
round trip byte-identical; writing a checksum the input did not have would
rewrite the document.

### CRC-32 verification

Verification is EXPLICIT and never happens on its own — nothing in this library
checks a checksum unless you ask. It lives on `tree` rather than on the cursor
because a checksum covers the element data AS STORED, and only the retained model
holds those bytes: the cursor's payload view dies at the next `Next`.

`(*tree.Element).VerifyChecksum` checks THIS element and nothing deeper. It
returns nil when the checksum is correct and nil when there is nothing to check —
a leaf, or a master with no CRC-32 child, since most masters carry none.
Recursion is yours, because only you know whether to stop at the first bad
element or collect every one; `Walk` expresses both.

A mismatch is a **content** error, not a structural one, and that is the part
worth getting right: the extents were read correctly, so the position of the next
element is known, the parse is not in doubt, and nothing here justifies scanning
bytes for a resume point. Every verdict about the document — `*crc.MismatchError`,
`*crc.LengthError` for a payload that is not four bytes,
`*tree.MultipleChecksumsError`, and `*tree.ChecksumPositionError` for a correct
checksum that is not the first child — comes back wrapped in
`parser.NewContentError`, so `parser.IsStructural` is false for it while
`errors.As` still reaches the concrete type.

One answer is neither pass nor mismatch: `*tree.ChecksumUnavailableError` says the
covered bytes are not all in hand, because a payload cap (`WithMaxPayload`)
elided one. That is not a gap — a consumer that skipped or capped a subtree has
nothing to sum, and reporting a pass for an element nothing was checked about
would be worse than reporting anything else.

```go
package main

import (
	"errors"
	"fmt"

	"github.com/yacchi/ebml/impl/go/crc"
	"github.com/yacchi/ebml/impl/go/parser"
	"github.com/yacchi/ebml/impl/go/tree"
)

func verify(data []byte) error {
	roots, err := tree.Parse(data)
	if err != nil {
		return err
	}
	for _, root := range roots {
		var bad error
		root.Walk(func(el *tree.Element) bool {
			bad = el.VerifyChecksum()
			return bad == nil
		})
		if bad == nil {
			continue
		}
		var mismatch *crc.MismatchError
		if errors.As(bad, &mismatch) {
			// Content, not structural: the parse is intact and only these
			// bytes are in doubt, so keep going.
			fmt.Println(mismatch, "structural:", parser.IsStructural(bad))
			continue
		}
		return bad
	}
	return nil
}

func main() {
	if err := verify(nil); err != nil {
		fmt.Println(err)
	}
}
```

## Ways of using the core

Packages are in the core when ports must agree on them to interoperate; this
layer contains ordinary ways of using that core whose shape another language
may choose differently.

Everything under `impl/go/ext/` is outside the cross-language contract and is built
only on exported core APIs. A missing capability in an extension is a core
capability bug, not a reason to reach into parser internals. No retention path
uses a per-element allowlist.

### `ext/fragment`

`fragment.New` assembles a continuous KVS GetMedia stream and produces one
`*fragment.Fragment` per completed `Cluster`. It is a plain pull loop over
`parser.Cursor` and is the worked example of building on the core. It retains generic element nodes,
decodes `SimpleBlock`s into `Blocks`, and provides metadata and timing helpers:
`Tag`, `Tags`, `Tracks`, `Track`, `TrackByName`, `TimestampScale`,
`ClusterTimestamp`, `BlockTime`, `StartTime`, `EndTime`, `TrackPCM`, and
`TrackPCMByName`.

There are two entry points, differing in nothing but **who supplies the bytes**,
and the same options mean the same thing in both:

| Entry point | Supply | Reading surface |
| --- | --- | --- |
| `fragment.New(opts...)` | The caller pushes chunks with `Feed`, then calls `Finalize` | `Feed` returns the Fragments that completed |
| `fragment.NewReader(r, opts...)` | The `Reader` owns an `io.Reader` | `Fragments() iter.Seq2[*Fragment, error]` |

That split is the same one the core makes between `parser.Cursor` and
`stream.Stream`, for the same reason: a pull has three outcomes where the caller
feeds the bytes, and only two where the layer owns the source, so the iterator is
an exact spelling only on the source-owning side. `Reader` therefore has **no
exported `Next`**, and the end of the input ends the iteration while any other
failure is yielded once as the final pair — after every Fragment that completed
before it.

```go
package main

import (
	"fmt"
	"os"

	"github.com/yacchi/ebml/impl/go/ext/fragment"
)

func main() {
	for f, err := range fragment.NewReader(os.Stdin).Fragments() {
		if err != nil {
			// The fragments that completed before this failure have already
			// been delivered by the iterations above, including the Cluster a
			// cut connection left open (Fragment.Truncated).
			panic(err)
		}
		fmt.Println(f.ClusterTimestamp(), len(f.Blocks))
	}
}
```

`NewReaderSize` sets the read chunk size for a caller whose source has a
granularity of its own; it changes nothing about the Fragments, since assembly is
split-invariant.

#### When a Fragment is delivered

A Fragment is **assembled** when its `Cluster` closes and **delivered** once its
Segment-level metadata has settled. Those are not the same moment, because a live
stream writes some of that metadata *after* the Cluster it describes — Amazon
Connect's KVS output writes two `Tags` elements before its `Cluster` and two after.
Delivering at the Cluster's end would hand over a partial view with nothing in it
to say so, and a consumer reading tags on receipt would silently see fewer than the
stream stated.

So by default a fragment is held until **the next `Cluster` begins, its `Segment`
ends, or the input ends**, whichever comes first. At that point every
Segment-level element preceding the next Cluster has been retained. This is not
opt-in and changes no error's terminality — only when an already-assembled
fragment becomes reachable.

Waiting for the next Cluster is the only *element-agnostic* way to know that no
further `Tags` follow, so in a stream that puts one Cluster per document — the KVS
shape — the wait lasts until the next document starts. A consumer that knows its
stream's layout pays nothing:

| | Delivery |
| --- | --- |
| Default (`nil` predicate) | Next `Cluster`, `Segment` end, or end of input |
| `WithMetadataComplete(pred)` | As soon as `pred` says the metadata is settled |
| `WithMetadataComplete(always true)` | At the `Cluster`'s end — the eager snapshot, caveats included |

```go
// impl/go/integrations/kvs knows that GetMedia writes the continuation token last, so it supplies
// the predicate — exactly as matroska.StreamBoundary is supplied to
// parser.WithBoundary. kvs.NewReader passes it already.
asm := fragment.New(fragment.WithMetadataComplete(kvs.MetadataComplete))
```

Absolute block time is `(ClusterTimestamp + block.Timecode) * TimestampScale`;
both operands are in scale units and the relative timecode is signed. The origin
is the Segment's own timeline, which Matroska does not fix: a stream that writes
an epoch-based `Cluster.Timestamp`, as Amazon Connect does, yields an offset from
the Unix epoch rather than an elapsed media time.

A stream that ends **inside an element** — which is what every dropped live
connection looks like — still reports its truncation from `Finalize`, and the
`Cluster` that was open when the bytes stopped is emitted alongside that error
with `Fragment.Truncated` set. This is not opt-in and softens nothing: the error
is returned, latched, and reported again by every later call, exactly as before;
what changes is only that the blocks which decoded completely before the cut are
reachable instead of discarded. It is the same rule `Feed` already states — an
error never discards the good prefix — applied to the tail itself. The fragment
is emitted even when no block decoded, because whether one is worth keeping is a
judgement the caller makes with `len(Blocks)`, while whether one exists is
structural. Inside the tree the cut element keeps its place with
`tree.Element.Truncated` set and no payload, so the `Cluster`'s shape still
accounts for the bytes; `Fragment.Truncated` is the flag that distinguishes a
salvaged fragment, because the element-level flag is set on every retained
`SimpleBlock` anyway.

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

### `ext/scope`

`scope.Tracker` follows any master element and retains the elements that
completed directly inside it. It holds no element knowledge: what is not
observed is not in the scope, including everything under a skipped master.
`Get` and `GetAll` never search descendants; use `tree.Element.Descendants` on
a returned child when deeper access is wanted.

```go
package main

import (
	"bytes"

	"github.com/yacchi/ebml/impl/go/ext/scope"
	"github.com/yacchi/ebml/impl/go/stream"
	"github.com/yacchi/ebml/impl/go/matroska"
	"github.com/yacchi/ebml/impl/go/parser"
)

func main() {
	s := stream.New(bytes.NewReader(nil), matroska.KindForElementID)
	t := scope.NewTracker(matroska.IDSegment, s)
	for n, err := range s.Nodes() {
		if err != nil {
			panic(err)
		}
		_, _ = t.Observe(n)
		if m, ok := n.(*parser.MasterNode); ok {
			if m.ID() == matroska.IDCluster {
				m.Skip()
			} else {
				m.Descend()
			}
		}
	}
	_ = t.Finish()
}
```

### `ext/tags`

`tags.Read` computes a view of the `Tag` elements found under the roots it is
given. A tag applies to the whole Segment by default; `Targets` narrows it by
target type or UID. Multiple `Tags` elements are cumulative and positionless
under RFC 9559, so tags after a Cluster remain readable. RFC 9559 does not
define precedence for a repeated `TagName`; this library chooses last-wins,
unlike `net/http.Header.Get`, which returns the first value.

`Read` takes roots because every retention path in this library ends in a
`*tree.Element`. A root is an element that *contains* the tags and is searched to
any depth — never a `Tag` itself, which would yield an empty view. `ReadFrom` is
the adapter for a producer that indexes elements by ID:

| what the caller holds | the call |
| --- | --- |
| a `*fragment.Fragment` | `tags.Read(frag.Segment)` |
| a retained Segment tree | `tags.Read(segment)` |
| a `*scope.Scope` | `tags.ReadFrom(sc)` |

Roots must not overlap: an element passed together with one of its own ancestors
contributes its tags twice.

`ReadFrom` exists so that naming the `Tags` ID stays this package's job. The call
it replaces — `tags.Read(sc.GetAll(matroska.IDTags)...)` — has two silent failure
modes and no loud one: `IDTag` instead of `IDTags` yields an empty view, and
`Get` instead of `GetAll` keeps only the last `Tags` element, discarding what a
live stream wrote before its Cluster. Its `Source` interface is satisfied by the
`GetAll` that `scope` already has for its own sake, so `scope` implements nothing
and stays element-agnostic, and `tags` imports no other `ext` package.

That is the general rule here: **no `ext` package imports another**, pinned by
`internal/archtest`. `ext/fragment` therefore has no tag accessor of its own — a
fragment's `Segment` is an ordinary retained element, and reading its tags is
this package applied to it:

```go
package main

import (
	"fmt"
	"os"

	"github.com/yacchi/ebml/impl/go/ext/fragment"
	"github.com/yacchi/ebml/impl/go/ext/tags"
)

func main() {
	for frag, err := range fragment.NewReader(os.Stdin).Fragments() {
		if err != nil {
			panic(err)
		}
		set := tags.Read(frag.Segment)
		contact, _ := set.Get(tags.Target{}, "ContactId")
		title, _ := set.Get(tags.Target{TypeValue: 30, TrackUID: 1}, "TITLE")
		fmt.Println(contact, title)
	}
}
```

## Amazon Kinesis Video Streams: the `integrations/kvs` module

`github.com/yacchi/ebml/impl/go/integrations/kvs` is an integration: a separate Go
module holding one outside system's Matroska conventions, so the core module
never carries AWS-specific dependencies. It currently has **no AWS SDK dependency at
all**, and it does not include a GetMedia API wrapper: callers provide the
already-obtained byte stream. This package is not affiliated with, endorsed by,
or sponsored by Amazon Web Services; AWS service names appear descriptively only.

Being a separate module means it needs its **own** requirement: adding
`github.com/yacchi/ebml/impl/go` does not make `github.com/yacchi/ebml/impl/go/integrations/kvs` resolvable,
and `go list github.com/yacchi/ebml/impl/go/integrations/kvs` reports `no required module provides
package` until the second one is named too.

```bash
go get github.com/yacchi/ebml/impl/go
go get github.com/yacchi/ebml/impl/go/integrations/kvs
```

In a workspace, both directories are listed:

```
go 1.25

use (
	./ebml/go
	./ebml/go/integrations/kvs
)
```

```go
package main

import (
	"fmt"
	"io"
	"os"

	"github.com/yacchi/ebml/impl/go/integrations/kvs"
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

### In-band stream errors

GetMedia reports a failure inside the stream, as
`AWS_KINESISVIDEO_ERROR_CODE`/`_ERROR_ID` tags on a fragment, rather than by
cutting the connection. `Next` **reports** it: the fragment carrying the tags is
handed over first, then the next call returns a `*kvs.StreamError`, which is
sticky. There is no option either way.

The alternative — leaving it to the caller to ask `Metadata.Err()` — was
rejected because forgetting to ask makes a stream that KVS stopped on an error
look like a short clean end, and a consumer reported losing time to exactly that
class of silent failure. `Metadata.Err()` remains for reading the failure off one
fragment. The error follows its fragment rather than accompanying it so that
`if err != nil { break }` cannot drop data, which is the ordering the truncated
tail already uses.

One shape stays outside this: error tags in a document that assembles no
`Cluster` produce no fragment, so `Next` never sees them.

### Finite input

`GetMediaForFragmentList` returns the same document shape as GetMedia, only
finite. The same `kvs.NewReader` reads it — nothing assumes the input continues,
and the end of the input closes the open document exactly as the end of a live
capture does.

### Tag inheritance

Tag inheritance is not something `ext/fragment` decides: `tags.Read` over a
fragment's `Segment` returns exactly what that fragment's `Tags` element
carried, and `All` is non-nil and empty when it carried none. The policy lives
one layer up, in `kvs`, and it inherits
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

`WithoutTagInheritance()` turns the policy off, and `Metadata.Tags` is then
exactly what the fragment carried.

### Writing a PutMedia producer

The module records one useful producer recipe as well. It is a buffering choice,
not an AWS compatibility requirement:

| | a real capture from the field | this producer recipe |
| --- | --- | --- |
| `Segment` | unknown-size | unknown-size (same) |
| `Cluster` | unknown-size | **known-size, `writer.Buffered()`** |

`Buffered()` emits a Cluster in one writer call at `EndMaster`, while an
unknown-size Cluster streams its children as they are written. Both shapes are
valid PutMedia input and this library reads either. HTTP/TCP write boundaries do
not define fragments: PutMedia maps each MKV Cluster to one fragment. The corpus
models unknown-size Clusters throughout because that is what the observed field
producer sends.

What `Cluster.Timestamp` MEANS is decided by the `x-amzn-fragment-timecode-type`
header of the caller's own PutMedia request, so the module names both values and
supplies the conversion for the one that needs it:

```go
scale := fragment.DefaultTimestampScale // 1 ms; write the same value into Info
ticks, err := kvs.ClusterTimestamp(time.Now(), scale)
```

`kvs.ClusterTimestamp` is the inverse of `kvs.ClusterTime`: under
`kvs.FragmentTimecodeTypeAbsolute` a Cluster timestamp counts from the UNIX
EPOCH, not from the start of the stream. Under
`kvs.FragmentTimecodeTypeRelative` it is the elapsed duration in ticks from the
`x-amzn-producer-start-timestamp` of that PutMedia request and no helper is
needed. One request may carry several Clusters, so this value continues to
advance and must not reset at each fragment. A document written under one
convention and read under the other parses perfectly and means something else,
which is why the choice is named here rather than left to a comment.

This module does not validate the PutMedia service profile. In particular,
`ClusterTimestamp` converts using the supplied Matroska `TimestampScale` but
does not enforce the service's accepted scale range. The caller remains
responsible for the single-Segment rule, fragment duration and size limits,
track count and frame-to-track consistency, timestamp ordering, and streaming
metadata limits. These constraints can change independently of this library;
the current list belongs to the
[PutMedia API reference](https://docs.aws.amazon.com/kinesisvideostreams/latest/APIReference/API_dataplane_PutMedia.html).

There is deliberately no `kvs.FragmentWriter`: `writer` is the only encoder, and
a type that composed its calls would be a wrapper hiding the two decisions above
rather than stating them. See
[Declined additions](../../docs/design-rules/declined-additions.md).

### `examples/getmedia`

`impl/go/integrations/kvs/examples/getmedia` is a runnable, end-to-end demonstration of driving
the module over a live Amazon Connect KVS GetMedia byte stream. Run it from
`impl/go/integrations/kvs`.

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

	"github.com/yacchi/ebml/impl/go/writer"
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

### CRC-32 emission

`writer.WithChecksum(crcID)` makes one master emit an EBML CRC-32 element as its
first child, covering that master's other element data as stored — sibling
headers included, the CRC-32 element's own header and payload excluded. It is
opt-in per master and inherits nothing: a nested master states its own option,
and its CRC-32 element is then part of what the outer checksum covers.

The element ID is a parameter, not a constant, for the same reason every other ID
is: the writer holds no element table. `matroska.IDCRC32` is where that ID is
written down.

`WithChecksum` is valid only with `Buffered`, because the value has to precede
the bytes it covers, so those bytes must still be in memory. `StartMaster`
reports `*writer.ChecksumStrategyError` for any other strategy and
`*writer.InvalidIDError` for an ill-formed `crcID`, both before writing a byte.

```go
package main

import (
	"bytes"
	"fmt"

	"github.com/yacchi/ebml/impl/go/matroska"
	"github.com/yacchi/ebml/impl/go/writer"
)

func writeCluster() ([]byte, error) {
	var buf bytes.Buffer
	w := writer.New(&buf)
	err := w.StartMaster(
		matroska.IDCluster,
		writer.Buffered(),
		writer.WithChecksum(matroska.IDCRC32),
	)
	if err != nil {
		return nil, err
	}
	if err := w.Uint(matroska.IDTimestamp, 0); err != nil {
		return nil, err
	}
	if err := w.EndMaster(); err != nil {
		return nil, err
	}
	if err := w.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func main() {
	b, err := writeCluster()
	if err != nil {
		fmt.Println(err)
		return
	}
	fmt.Printf("% X\n", b)
}
```

The writer has no element knowledge: use registry IDs such as
`matroska.IDEBML` at the call site when writing a known vocabulary. For a
retained tree, `tree.Marshal` and `tree.MarshalBytes` compose the same writer
primitives and preserve original legal size-VINT widths and payload bytes.
The fixture corpus is generated by the public writer and parse-then-marshal
round trips are checked byte-for-byte in the test suite.

## CLI

`cmd/ebml` builds the project's ONE CLI, which is not a Go-specific tool and is
therefore documented at the root: what `dump` and `xml` do, how to install the
binary, and what its output looks like are in
[the root `README.md`](../../README.md#the-ebml-command).

What belongs here is running it from a checkout, without installing anything.
From this directory:

```bash
go run ./cmd/ebml dump path/to/file.mkv
go run ./cmd/ebml dump --hex ../../fixtures/kvs/topology_basic.ebml.hex
go run ./cmd/ebml xml --hex ../../fixtures/kvs/topology_basic.ebml.hex
```

It is an ordinary consumer of the exported API — `stream`, `matroska` and
`tree`, nothing internal — so its own package holds only flag parsing and
rendering.

## Working across the two modules

How to require `kvs` from outside is under
[the `integrations/kvs` module](#amazon-kinesis-video-streams-the-integrationskvs-module)
above. Inside this repository there are two things to know.

`integrations/kvs/go.mod` requires the core by a **published version**, and the
root `go.work` is what makes local work resolve it from the tree instead of the
proxy. It carried a `replace` instead until that was found to make the module
unresolvable for everyone else: a `replace` in a DEPENDENCY's `go.mod` is
ignored — only the main module's applies — so a consumer saw the unsatisfiable
`v0.0.0` and nothing else.

The requirement is a MINIMUM, as every Go requirement is, so it moves only when
`kvs` needs a core API newer than the pin. When it does, the core change has to
be PUSHED before the pin can name it, so that pair of changes is two commits and
not one. Everything else spanning both modules is a single commit, because the
workspace already builds `kvs` against the working tree.

```bash
# From integrations/kvs, after the core commit is pushed:
GOWORK=off go get github.com/yacchi/ebml/impl/go@main
```

### Releasing

Both modules live in subdirectories of the repository, so their version tags
carry the subdirectory as a prefix — that is how the go tool finds a module that
is not at the repository root:

```
impl/go/v0.1.0
impl/go/integrations/kvs/v0.1.0
```

The core is tagged first, and `integrations/kvs` then requires that tag in place
of the pseudo-version, so a release names a release rather than a commit.

## Build and test

From this directory:

```bash
go test ./...
go vet ./...
go run ./internal/kvsgen/genfixtures   # regenerate ../../fixtures/kvs and ../../golden/kvs
```

From `integrations/kvs/`:

```bash
go test ./...
go vet ./...
go run ./examples/getmedia path/to/stream.mkv
```

Checking the registry against the official IETF CELLAR schemas needs them fetched
first (they are never vendored); see `../../CLAUDE.md` for the `conformance-check`
step and the command it runs.

### Measured conformance

Against CELLAR `matroska-specification` at `f93ab02` and `ebml-specification` at
`a4b3c4a` (schema `docType="matroska" version="4"`), last run 2026-07-29:

| | |
| --- | --- |
| Mismatches — the registry contradicting the schema | **0** |
| Elements registered | **270 of the 273** the two schemas declare |
| Elements the schema still declares CURRENT and the registry lacks | **0** |
| WebM-profile elements registered | **133 of 133** |

The three unregistered elements — `SilentTracks`, `SilentTrackNumber` and
`EncryptedBlock` — are ones the schema marks REMOVED after version 0, and leaving
them out is deliberate rather than pending: an unregistered ID can never end an
unknown-size master, so a `Cluster` carrying one in an old file still closes
where the boundary rule says it does. Registering them without adding them to
`completeChildren` would break exactly that.

Every value type the schema uses has a decoder — `AsUint`, `AsInt`, `AsFloat`,
`AsString`, `AsTime` and `Bytes` on `tree.Element`, plus `ParseSimpleBlock` for
the block framing inside a binary payload — so element coverage is not the
outstanding work. What is outstanding is validation, and none of it exists:

| The schema declares | On how many elements | What it would take |
| --- | --- | --- |
| `maxOccurs` / `minOccurs` | 241 | cardinality validation |
| `minver` | 113 | `DocTypeVersion` gating |
| `default` | 77 | resolving an absent element to its default |
| `range` | 71 | value-range validation |
| `restriction`/`enum` | 29 | enumerated value names |
| `length` | 12 | fixed payload-length validation |
| `recurring` | 3 | the once-per-Segment rule |
| the WebM profile marker | 133 | restricting a `webm` document to its subset |

Each row is a capability this library does not have, not a hole in the checker.
The last one is worth reading carefully: every WebM element IS registered, so
WebM documents read and write correctly — what is absent is ENFORCEMENT. Nothing
here consults `DocType`, so a document claiming `webm` while using a
Matroska-only element is read exactly as the bytes say, and no one is told.

## `internal/`

| Package | Role |
| --- | --- |
| `archtest` | Pins the core dependency graph above, and that every `ext` package is a leaf. |
| `ebmltest` | The one shaping layer over the public writer API for hand-built test inputs (`Leaf`/`Uint`/`String`/`UTF8`/`Master`/`UnknownMaster`/`Encode`). |
| `ebmltrace` | Produces the golden cursor traces in `../../golden/`. |
| `kvsgen` | Builds the synthetic fixture corpus through the public writer; `genfixtures` writes it out. |
| `specconform` | Checks the registry against the official schemas through its exported API only. |

No test, fixture generator or extension carries an encoder of its own: everything
that emits bytes goes through `writer`.
