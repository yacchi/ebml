# ebml

`ebml` is a streaming, cursor-based EBML/Matroska library. Its core emits element
events as bytes arrive; it does not require a document tree and does not buffer bulk
payloads. A known-size `Cluster` therefore closes as soon as its declared bytes are
consumed, even inside an unknown-size `Segment`.

The core is specified in [`spec/SPEC.md`](spec/SPEC.md). An implementation provides
the cursor, event model, flow control, retained model, byte-supply and registry
contracts stated there.

What a port must reproduce is that observable CONTRACT, spelled the way its own
standard library would spell it: a generation stamp on a node and an `io.Reader` are
Go's mechanisms, not requirements. The one shape that is not free is the ARITY OF A
PULL -- an event, need-more-data, and end of input are three outcomes, and which of
them a given layer must be able to state follows from who owns the byte supply.
[`docs/`](docs/) collects the design notes behind that, starting with
[the shape of a pull across languages](docs/pull-shape-across-languages.md).

## Implementations

Usage, API spelling, package layout and commands are per implementation, and each
one documents its own:

| Directory | Language | State | Documentation |
| --- | --- | --- | --- |
| [`go/`](go/) | Go | Complete: reading, writing, round-trip conformance, CLI | [`go/README.md`](go/README.md) |
| `ts/` | — | Placeholder. Not an implementation. | — |
| `py/` | — | Placeholder. Not an implementation. | — |

This file stays language-neutral: what the library is, what the contract is, and
what every implementation shares. It deliberately shows no code in any one
language, so that adding a second implementation changes nothing here but a row
in the table above.

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
