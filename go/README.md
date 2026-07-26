# `go/` — the Go implementation

This directory is the Go module `github.com/yacchi/ebml`, plus a second module
`github.com/yacchi/ebml/kvs` under `kvs/`. It is a MAP: what each package is for,
which way the dependencies point, and which entry point to pick. The reasoning
behind any rule mentioned here lives somewhere else, and the pointer is given
rather than the prose — a second copy of a policy is how three copies of the
stream-boundary rule came to disagree.

| To read about | Go to |
| --- | --- |
| The portable contract a port must reproduce | [`../spec/SPEC.md`](../spec/SPEC.md) |
| The library explained, core first then extensions | [`../README.md`](../README.md) |
| Why the shape is what it is (non-normative notes) | [`../docs/`](../docs/) |
| Any single package's exact rules | that package's doc comment |

## Packages

The core is what a port must agree on; `ext/` is Go convenience built only on
exported core API.

| Package | Kind | What it is |
| --- | --- | --- |
| `parser` | core | The reading core: `Cursor.Next`/`Feed`/`Finalize`, nodes, flow control, VINTs, error classes. Holds NO element knowledge — `KindClassifier` is a required argument. |
| `crc` | core | The CRC-32 primitive, a leaf package importing nothing from this module. |
| `matroska` | core | The immutable RFC 9559 registry: IDs, names, types, containment, `StreamBoundary`. The only place element IDs live. |
| `writer` | core | The one EBML encoder in the repository. Holds no element knowledge either; the caller picks the value type. |
| `tree` | core | The retained document model: loose `Descendants`, strict `Find`, `Marshal`, `VerifyChecksum`. |
| `stream` | core | Owns an `io.Reader` and answers `NeedMoreData`. Whole reading surface: `Nodes() iter.Seq2[parser.Node, error]`. |
| `ext/scope` | ext | Tracks one master and the elements that completed directly inside it. |
| `ext/tags` | ext | Target-aware views over observed `Tags`. The only tag-traversal implementation. |
| `ext/fragment` | ext | Assembles one `Fragment` per completed `Cluster`, with recovery and delivery options. |
| `kvs` | separate module | All Amazon KVS knowledge: tag names, typed `Metadata`, wall-clock times, `MetadataComplete`. No AWS SDK dependency. |
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

ext/scope    -> core
ext/tags     -> ext/scope + core        (Read takes a *scope.Scope)
ext/fragment -> ext/tags + core         (for Tag/Tags; NOT built on scope.Tracker)
kvs          -> ext/fragment + core     (module 2)
```

Two rules are enforced, not merely intended: `parser` never imports `tree` (the
StAX reader may not reach retained document state), and no core package imports
anything from `ext/`. Within `ext/`, one package may use another's exported API —
what `ext/fragment` deliberately does NOT do is build its assembly on
`scope.Tracker`, and its doc says why a future reader should not "fix" that.

## Choosing an entry point

Every reading layer exists because of WHO OWNS THE BYTES. That is the library's
central distinction, and the answer decides both the layer and its shape — see
[`../docs/pull-shape-across-languages.md`](../docs/pull-shape-across-languages.md).

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

## Things worth knowing before reading the code

* **Node lifetime is enforced.** A node is valid only until the next pull; every
  exported node method panics on a stale one. `parser`'s `Node` doc says why the
  per-event allocation is what buys that.
* **Errors have two classes plus flow control.** `parser.IsStructural(err)` is the
  canonical test; `parser.NewContentError` marks a consumer's verdict about
  content; `NeedMoreData` is neither.
* **A `Fragment` is assembled at its `Cluster`'s end and delivered once its
  Segment-level metadata has settled.** The wait is the default;
  `fragment.WithMetadataComplete` is the one escape and takes a caller-supplied
  predicate — `kvs.MetadataComplete` is that predicate for GetMedia.
* **A truncated tail is salvaged, not discarded**, and the error still comes with
  it.
* **Recovery is opt-in and split by error class:** `fragment.WithResync` for
  structural failures, `fragment.WithSkipContentErrors` for content ones. Byte
  scanning happens only in the former, only after a failure.
* **Time is a duration from the Segment's own origin,** which Matroska does not
  fix. `kvs.ClusterTime`/`StartTime`/`EndTime`/`BlockTime` read it as wall clock
  for streams that write an epoch-based `Cluster.Timestamp`, as KVS does.

## The two modules

`kvs/` is a separate module so the core never carries service-specific
knowledge, which means it needs its own requirement — adding
`github.com/yacchi/ebml` does not make `github.com/yacchi/ebml/kvs`
resolvable:

```bash
go get github.com/yacchi/ebml
go get github.com/yacchi/ebml/kvs
```

Its `go.mod` currently carries `replace github.com/yacchi/ebml => ../`, which
stays until the core module has its first tagged release. A change that spans
both modules therefore has to land in one commit to keep `kvs` green.

## Build and test

From this directory:

```bash
go test ./...
go vet ./...
go run ./internal/kvsgen/genfixtures   # regenerate fixtures/kvs and golden/kvs
go run ./cmd/ebml dump path/to/file.mkv
```

From `kvs/`:

```bash
go test ./...
go vet ./...
go run ./examples/getmedia path/to/stream.mkv
```

Checking the registry against the official IETF CELLAR schemas needs them fetched
first (they are never vendored); see `../CLAUDE.md` for the `conformance-check`
step and the command it runs.

## `internal/`

| Package | Role |
| --- | --- |
| `archtest` | Pins the core dependency graph above. |
| `ebmltest` | The one shaping layer over the public writer API for hand-built test inputs (`Leaf`/`Uint`/`String`/`UTF8`/`Master`/`UnknownMaster`/`Encode`). |
| `ebmltrace` | Produces the golden cursor traces in `../golden/`. |
| `kvsgen` | Builds the synthetic fixture corpus through the public writer; `genfixtures` writes it out. |
| `specconform` | Checks the registry against the official schemas through its exported API only. |

No test, fixture generator or extension carries an encoder of its own: everything
that emits bytes goes through `writer`.
