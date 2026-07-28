# ebml

[![CI](https://github.com/yacchi/ebml/actions/workflows/ci.yml/badge.svg?branch=main)](https://github.com/yacchi/ebml/actions/workflows/ci.yml)
[![Conformance](https://github.com/yacchi/ebml/actions/workflows/conformance.yml/badge.svg?branch=main)](https://github.com/yacchi/ebml/actions/workflows/conformance.yml)
[![codecov](https://codecov.io/gh/yacchi/ebml/branch/main/graph/badge.svg)](https://codecov.io/gh/yacchi/ebml)
[![License](https://img.shields.io/badge/license-Apache--2.0-blue.svg)](LICENSE)
[![Last commit](https://img.shields.io/github/last-commit/yacchi/ebml/main)](https://github.com/yacchi/ebml/commits/main)

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
* **Inspect a document from the shell.** [`ebml dump` and `ebml xml`](#the-ebml-command),
  one CLI for the project.

No third-party dependencies, in any implementation.

## Quick start

Go is the only implementation today. Each implementation carries its own quick
start; this is its shortest form, and everything else about it — installation into
a workspace, the package map, the extensions, writing — is in
[`impl/go/README.md`](impl/go/README.md).

```bash
go get github.com/yacchi/ebml/impl/go
```

```go
package main

import (
	"fmt"
	"os"

	"github.com/yacchi/ebml/impl/go/matroska"
	"github.com/yacchi/ebml/impl/go/parser"
	"github.com/yacchi/ebml/impl/go/stream"
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
  [`impl/go/README.md`, the `integrations/kvs` module](impl/go/README.md#amazon-kinesis-video-streams-the-integrationskvs-module)
* **Assembling fragments from any Matroska stream** →
  [`impl/go/README.md`, `ext/fragment`](impl/go/README.md#extfragment)
* **Writing EBML, or round-tripping a document** →
  [`impl/go/README.md`, Writing](impl/go/README.md#writing)
* **Just looking at a file** → [the CLI](#the-ebml-command), below

## The `ebml` command

There is ONE CLI for the project, not one per implementation: `ebml`, with two
subcommands, `dump` for an indented structural view and `xml` for an XML
rendering. Both read raw EBML from a file or from stdin, and `--hex` decodes the
commented hexadecimal fixture format this repository stores its corpus in.

It is built from the Go implementation, so `go install` is how you get it:

```bash
go install github.com/yacchi/ebml/impl/go/cmd/ebml@main
```

That puts the binary in `$(go env GOPATH)/bin`. There is no release tag yet,
which is why the query above names a branch; once one exists, `@latest` or an
explicit `@v0.1.0` works the same way. The version is written WITHOUT the
`impl/go/` tag prefix the repository tags carry — the go tool derives that from
the module path itself.

```bash
ebml dump path/to/file.mkv
ebml dump --hex fixtures/kvs/topology_basic.ebml.hex
ebml xml  --hex fixtures/kvs/topology_basic.ebml.hex
cat recording.mkv | ebml dump
```

`dump` on `topology_basic.ebml.hex` begins:

```text
EBML (0x1A45DFA3) [offset 0, size 19]
  EBMLVersion (0x4286) [type uint, offset 5, size 1] = 1
  EBMLReadVersion (0x42F7) [type uint, offset 9, size 1] = 1
  DocType (0x4282) [type string, offset 13, size 8] = "matroska"
Segment (0x18538067) [offset 24, size unknown]
  Info (0x1549A966) [offset 36, size 26]
```

Running it from a checkout instead of installing it is in
[`impl/go/README.md`, CLI](impl/go/README.md#cli).

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

Every implementation lives under `impl/`, one directory per language, so the
repository root holds the things they SHARE — the corpus, the spec, the notes —
and adding a language never rearranges it.

| Directory | Language | State | Documentation |
| --- | --- | --- | --- |
| [`impl/go/`](impl/go/) | Go | Complete: reading, writing, round-trip conformance, CLI | [`impl/go/README.md`](impl/go/README.md) |
| `impl/ts/` | — | Placeholder. Not an implementation. | — |
| `impl/py/` | — | Placeholder. Not an implementation. | — |

This file stays language-neutral apart from the two things that belong to the
project rather than to a language: the quick start of whichever implementations
exist, and the `ebml` command, which is one CLI for the project and not one per
implementation. Everything else here is what the library is, what the contract
is, and what every implementation shares. A second implementation therefore
costs it a row in the table above and a quick start of its own — never a
rewrite.

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
[`impl/go/integrations/kvs/examples/getmedia`](impl/go/integrations/kvs/examples/getmedia) are written for use with
Amazon Kinesis Video Streams; that service name appears here descriptively only,
and all fixture data is synthetic.
