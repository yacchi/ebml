# The shape of a pull, across languages

This is a design note for porting `ebml` to another language. It answers one
question a port hits immediately: *should the reading surface be my language's
iterator?*

The short answer is that it depends on which layer you are spelling, and the
test is mechanical. The long answer is the rest of this document.

The code below is signature sketches, not runnable programs. Each shows the
shape of a surface; nothing here is a complete implementation.

## What a port must reproduce, and what it must not copy

`spec/SPEC.md` specifies an OBSERVABLE CONTRACT. How a port reproduces it is the
port's own business, and no mechanism the Go implementation uses is itself a
requirement.

A port is expected to spell that contract the way its own standard library
would: nothing outside that standard library, no configuration object, no hook
registry, no query language, and errors, naming and lifetimes named the way the
host language names them. `io.Reader` is Go's spelling of a byte source, not the
contract; `Poll::Pending` may be yours.

So: reproduce the contract, spell it natively, and do not port Go's mechanisms.

## The one thing that is not free: the arity of a pull

Acquiring an event has THREE outcomes:

1. an event,
2. need-more-data — nothing is wrong, there are simply not enough bytes yet,
3. end of input.

(Errors are a fourth channel, and every language already has one.)

An iterator protocol carries TWO: a value, or done. Mapping three onto two means
collapsing one of them, and the pair that gets collapsed is always the same one:
need-more-data and end of input. That distinction is this library's central
semantic — a reader that cannot tell them apart cannot tell a stream that is
still arriving from a document that ended, and it will report a truncated
document as a clean one.

So the test for a port is **not** "does it avoid iterators". It is:

> Can this protocol state all three outcomes without lying?

## Who owns the byte supply decides the answer

| The bytes come from | Where need-more-data goes | Iterator? |
| --- | --- | --- |
| the caller, pushed in (`parser.Cursor`) | nowhere — it must be visible | **no**, unless the protocol has a third state |
| the layer itself, which owns the source (`go/stream`) | absorbed by blocking or `await` | **yes** — only two outcomes remain |

That is the whole rule. The lower layer keeps an explicit acquire operation
because it has three things to say. The driver above it has already answered
need-more-data by reading, so it has two, and the language's iterator fits
exactly.

One corollary, and it is the one that keeps the rule honest: a layer that offers
an iterator should NOT also offer an explicit acquire operation. Two spellings of
the same pull is where the collapse creeps back in — a consumer reaches for the
familiar one and the distinction quietly stops being enforced anywhere. This is
why `stream.Stream` has `Nodes` and deliberately no exported `Next`.

## Language by language

### Go

The push-fed cursor keeps `Next`, returning a sentinel error for the third
outcome:

```go
node, err := cur.Next()          // parser.Cursor: caller feeds the bytes
var needMore parser.NeedMoreData
if errors.As(err, &needMore) {
    cur.Feed(moreBytes)          // ... or cur.Finalize() when the source is over
}
```

The source-owning driver is an iterator, and only an iterator:

```go
for node, err := range s.Nodes() { // stream.Stream: it owns the io.Reader
    if err != nil {
        return err
    }
    // ...
}
```

`iter.Seq2[Node, error]` rather than `iter.Seq[Node]` plus an `Err` method, so a
failure cannot be lost by forgetting the second call. End of input ends the
range; any other failure is yielded once, as the final pair, with a nil node.

`parser.Cursor` deliberately has no iterator. It had one, offered as
non-normative sugar, and it was removed: a range loop over a caller-fed cursor
swallows the distinction above, and once `stream` existed the sugar had no
consumers left while still costing a "not the normative shape" caveat wherever
the reading surface was described. `Cursor.Err` remains, because it reports the
last outcome without advancing — an accessor is not a second spelling of the
pull.

### Rust

`Iterator` carries two outcomes and is therefore wrong for the cursor. Spell the
third outcome in the type:

```rust
enum Step<'a> {
    Event(Node<'a>),
    NeedMoreData,
    End,
}

impl Cursor {
    fn next(&mut self) -> Result<Step<'_>, Error>;
    fn feed(&mut self, bytes: &[u8]);
    fn finalize(&mut self) -> Result<(), Error>;
}
```

The async iterator family, however, *does* carry three outcomes, and passes the
test as-is:

```rust
// futures_core::Stream (std's async_iter::AsyncIterator is still unstable)
fn poll_next(self: Pin<&mut Self>, cx: &mut Context<'_>)
    -> Poll<Option<Self::Item>>;
//     ^ Pending          = need more data
//       Ready(Some(..))  = an event
//       Ready(None)      = end of input
```

So a Rust port may implement `Stream` for the source-owning driver **and** has a
legitimate three-state shape available even at the cursor layer. This is the
clearest evidence that the rule is about arity and not about iterators.

Node lifetime needs no runtime mechanism at all: `Step<'_>` borrows from
`&mut self`, so holding a node across the next pull does not compile. Do not
port Go's generation stamp.

### Python

The closest thing in the standard library is
`xml.etree.ElementTree.XMLPullParser`, and it is instructive because it shows
both the trap and a way out. `feed()` pushes bytes and `read_events()` returns an
iterator over whatever is available — so the iterator ending means "nothing more
*right now*", while the document actually ending is signalled separately by
`close()`.

That is a legitimate third shape worth naming: a **batch iterator**. The
iterator's exhaustion means need-more-data, and an explicit finalize means end of
input. All three outcomes remain distinguishable, just spread over two calls:

```python
class Cursor:
    def feed(self, data: bytes) -> None: ...
    def events(self) -> Iterator[Node]: ...   # ends = need more data
    def close(self) -> None: ...              # raises on a truncated document
```

What a port must not do is expose only `events()` and let its exhaustion stand
for the end of the document.

The source-owning driver is a plain iterator, because `read()` blocks:

```python
class Stream:
    def __iter__(self) -> Iterator[Node]: ...       # sync, owns a file object
    def __aiter__(self) -> AsyncIterator[Node]: ... # or async, owns a transport
```

Node lifetime has no compile-time expression here, so it is spelled the way Go
spells it: a generation counter and an exception on a stale node.

### JavaScript / TypeScript

A synchronous iterator has two outcomes; use a discriminated union at the cursor
layer:

```ts
type Step =
  | { kind: "event"; node: Node }
  | { kind: "need-more-data" }
  | { kind: "end" };

declare function next(): Step;
```

The source-owning driver is an async iterable, where `await` absorbs
need-more-data:

```ts
for await (const node of stream) { /* ... */ }
```

### Java

A sealed interface states the three outcomes and pattern matching consumes them:

```java
sealed interface Step permits Event, NeedMoreData, End {}

Step next();   // the cursor: the caller supplies the bytes
```

`Iterator<Node>` is wrong at that layer — `hasNext()` would have to either block
(it cannot; there is no source) or claim the input is over. Above the driver,
`Iterator<Node>` or `Stream<Node>` is correct, and a reactive
`Flow.Publisher<Node>` is another honest option: reactive protocols carry the
not-yet state.

### C#

`System.Text.Json.Utf8JsonReader` is the standard library's own push-fed
incremental reader, and it faces this exact problem: `Read()` returns `false`,
and the caller distinguishes need-more-data from end of input by consulting
`IsFinalBlock`. A port may copy that shape, but stating the third outcome
directly is clearer:

```csharp
enum StepKind { Event, NeedMoreData, End }
readonly ref struct Step { public StepKind Kind { get; } public Node Node { get; } }

Step Next();
```

The source-owning driver is `IAsyncEnumerable<Node>`; `await` absorbs
need-more-data.

Note that `Utf8JsonReader` is a `ref struct`, which is C#'s way of enforcing a
node lifetime at compile time — the same job Go does with a generation stamp.

### C

C has no iterator to be tempted by, which is why its shape is the one everything
else above is a dressing of:

```c
typedef enum {
    EBML_OK,              /* an event */
    EBML_NEED_MORE_DATA,
    EBML_END,
    /* structural errors ... */
} ebml_status;

ebml_status ebml_next(ebml_cursor *cur, ebml_node *out);
```

Node lifetime is documented, plus a generation field checked by an assert in
debug builds.

## Node lifetime is the same contract with different mechanisms

`spec/SPEC.md` section 3 requires that a node is valid only until the next pull,
and that using a stale one is *caught*, not merely undefined. What catches it is
entirely the port's choice:

| Language | Mechanism |
| --- | --- |
| Rust | `&'_ mut self` borrow — rejected at compile time |
| C# | `ref struct` — rejected at compile time |
| Go | generation stamp on every node, panic on a stale one |
| Java / Python / JS | generation stamp, exception on a stale one |
| C | documented, plus a debug assert on a generation field |

A port that gets this from its type system and therefore has no stamp and no
panic is fully conforming. Porting the stamp into a language that does not need
it would be copying a mechanism instead of a contract.

## Checklist for a port

* Does the caller-fed cursor state all three pull outcomes without collapsing
  any two?
* Does the source-owning driver never surface need-more-data at all?
* Does the driver finalize an exhausted source, so a document that ended inside
  an element is reported as truncated rather than complete?
* Does the driver offer exactly one spelling of the pull, not an iterator *and*
  an explicit acquire?
* Is a stale node caught, by whatever mechanism the language makes natural?
* Does the whole core depend on nothing outside the language's standard library?
