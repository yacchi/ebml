# ebml

**Read and write Matroska, WebM and any other EBML document as a stream — while
the bytes are still arriving.**

An element is reported the moment its header is in, and you decide on that header
whether its payload is worth reading. Nothing is buffered on your behalf, so a
multi-gigabyte recording and a small file cost the same memory, and a live stream
that has not finished yet is an ordinary input rather than a special case.

## What you can do with it

* **Read incrementally.** Element events as bytes arrive, in bounded memory, with
  no document tree required and no bulk payload buffered. Feed it one byte or one
  megabyte at a time: the result is identical.
* **Skip what you do not want, for free.** Decline a `Cluster`, a video track or a
  PCM payload on its header and those bytes are never materialised.
* **Handle the shapes live streams actually send.** Unknown-size elements,
  documents concatenated back to back, and an input cut mid-element — whose
  decoded prefix is still handed to you, marked truncated, instead of thrown away.
* **Keep only what you need.** Retain any subtree as a navigable tree; look
  elements up loosely, ignoring containment, or strictly by exact path.
* **Write it back out.** Parse and re-marshal a retained document to
  BYTE-IDENTICAL output, down to each header's original size-VINT width.
* **Verify and emit CRC-32**, explicitly, wherever the covered bytes were retained.
* **Name every element.** The RFC 9559 registry covers 270 of the 273 elements the
  official IETF CELLAR schemas declare, checked element by element against them,
  with the three omissions deliberate and documented. Unknown elements stay
  readable, and vendor elements can be registered.
* **Tell errors apart.** Structural damage, a verdict about an element's content,
  and "need more data" are three different things; recovery from the first two is
  opt-in and per class.
* **Assemble live fragment streams.** One value per completed `Cluster` together
  with the Segment-level metadata describing it — including metadata a producer
  writes AFTER the Cluster, which is where hand-rolled readers quietly lose data.
  Amazon Kinesis Video Streams GetMedia and Amazon Connect are the worked case.
* **Inspect a document from the shell.** `ebml dump` and `ebml xml`.

No third-party dependencies, in any implementation.

## Quick start

Go is the only implementation today. Each implementation carries its own quick
start; this is its shortest form, and everything else about it — installation into
a workspace, the package map, the extensions, writing — is in
[`go/README.md`](go/README.md).

```bash
go get github.com/yacchi/ebml
```

```go
package main

import (
	"fmt"
	"os"

	"github.com/yacchi/ebml/matroska"
	"github.com/yacchi/ebml/parser"
	"github.com/yacchi/ebml/stream"
)

func main() {
	file, err := os.Open(os.Args[1])
	if err != nil {
		panic(err)
	}
	defer file.Close()

	registry := matroska.Default()
	for node, err := range stream.New(file, matroska.KindForElementID).Nodes() {
		if err != nil {
			panic(err)
		}
		if _, closing := node.(*parser.EndNode); closing {
			continue // a master's end is its own event; print only headers here
		}
		// Every element is named, placed and sized from its header alone. No
		// payload is read unless you ask -- the megabytes of PCM in a Cluster
		// cost nothing here.
		size := fmt.Sprintf("%d bytes", node.Size())
		if node.Size() == parser.UnknownSize {
			size = "size not yet known" // what a live stream declares, and it is fine
		}
		fmt.Printf("%*s%s (%s at %d)\n",
			node.Depth()*2, "", registry.NameForID(node.ID()), size, node.Offset())
	}
}
```

On a live-stream document that prints:

```text
EBML (19 bytes at 0)
  DocType (8 bytes at 13)
Segment (size not yet known at 24)
  Tracks (88 bytes at 67)
    TrackEntry (43 bytes at 72)
      Name (19 bytes at 95)
  Cluster (size not yet known at 497)
    Timestamp (2 bytes at 509)
    SimpleBlock (28 bytes at 513)
```

Ask for what you want and nothing else: `stream.Payload(leaf)` delivers one
payload, `MasterNode.Skip` declines a whole known-size subtree, and an unknown-size
master — the shape a live stream sends — is descended into and closed structurally
when the first element that cannot be its child arrives.

Where to go next:

* **Reading a live KVS or Amazon Connect stream** →
  [`go/README.md`, the `kvs` submodule](go/README.md#amazon-kinesis-video-streams-the-kvs-submodule)
* **Assembling fragments from any Matroska stream** →
  [`go/README.md`, `ext/fragment`](go/README.md#extfragment)
* **Writing EBML, or round-tripping a document** →
  [`go/README.md`, Writing](go/README.md#writing)
* **Just looking at a file** → [`go/README.md`, CLI](go/README.md#cli)

## What it is not

* Not a media player or codec. Block payloads are handed over as bytes, never
  decoded: `SimpleBlock` framing is parsed, its contents are not.
* Not an indexing or seeking API. `SeekHead` and `Cues` are readable elements like
  any other; nothing builds an index for you.
* Not a validator. Cardinality, `minver` gating, value ranges, enums and the WebM
  profile are not enforced, and what the library does not answer for is
  inventoried rather than implied.
* Not a KVS SDK. The Amazon-specific parts read bytes you already obtained; there
  is no service client and no AWS dependency.

## Implementations

Usage, API spelling, package layout and commands are per implementation, and each
one documents its own:

| Directory | Language | State | Documentation |
| --- | --- | --- | --- |
| [`go/`](go/) | Go | Complete: reading, writing, round-trip conformance, CLI | [`go/README.md`](go/README.md) |
| `ts/` | — | Placeholder. Not an implementation. | — |
| `py/` | — | Placeholder. Not an implementation. | — |

This file stays language-neutral apart from the quick start, which belongs to
whichever implementations exist: what the library is, what the contract is, and
what every implementation shares. A second implementation therefore costs it a
row in the table above and a quick start of its own — never a rewrite.

## What every implementation shares

| Path | What it is |
| --- | --- |
| [`spec/SPEC.md`](spec/SPEC.md) | The normative portable contract. Where a document here and the specification disagree, the specification wins. |
| `fixtures/**/*.ebml.hex` | The synthetic corpus, as commented hexadecimal so it is readable and diffable in any language. |
| `golden/**/*.jsonl` | One JSON object per cursor operation: the conformance traces an implementation replays. |
| `tests/split_patterns.json` | The chunk boundaries every implementation must be invariant to. |
| [`docs/`](docs/) | Design notes: why the shape is what it is. Non-normative. |

The corpus is the reason those directories sit at the repository root rather than
inside an implementation: a port demonstrates conformance by reading the same
bytes and producing the same traces.

### The fixtures

Fifteen synthetic fixtures, modelling an unknown-size `Cluster` throughout
because that is the shape a live stream sends: `topology_basic`,
`tail_last_fragment`, `false_ebml_magic_in_pcm`, `multi_cluster`,
`multi_segment`, `tagless_single`, `tagless_consecutive`, `filter_mismatch`,
`gap`, `scaled_timestamps`, `unknown_elements`, `partial_tags` (a populated but
partial `Tags` element missing its identity keys), `two_tracks` (one `Cluster`
carrying `SimpleBlock`s for two named audio tracks), `known_size_cluster` (legal
Matroska, but not what the field sends — the deliberate counter-case), and
`connect_real_shape` (two `Tags` elements before the `Cluster` and two after,
with an epoch-based `Cluster.Timestamp`). Every one is exercised across every
split pattern.

Fixtures never contain real capture data, and a fixture models the shape the
field actually produces rather than the shape that is easiest to generate: a
corpus built from an assumed shape validates the assumption, not the world.

## License

Apache-2.0 (`SPDX-License-Identifier: Apache-2.0`). See [LICENSE](LICENSE).

This project is not affiliated with, endorsed by, or sponsored by Amazon Web
Services. The fixture corpus and the runnable example under
[`go/kvs/examples/getmedia`](go/kvs/examples/getmedia) are written for use with
Amazon Kinesis Video Streams; that service name appears here descriptively only,
and all fixture data is synthetic.
