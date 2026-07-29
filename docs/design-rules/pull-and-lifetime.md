# Pull shape and node lifetime

Why the reading core has exactly one surface, why a node dies at the next pull,
and why the iterator lives in `impl/go/stream` and nowhere else. The one-line
rules live in [`CLAUDE.md`](../../CLAUDE.md); this note is the reasoning behind
them. For the same argument worked through other languages, see
[Pull shape across languages](../pull-shape-across-languages.md).

## The arity of a pull

A pull has three outcomes — an event, need-more-data, and end of input — and a
two-outcome iterator protocol (value/done) can carry them only by collapsing
one. The test for a port is not "does it avoid iterators" but "can this protocol
state all three without lying", and the answer follows from WHO OWNS THE BYTE
SUPPLY.

Where the caller pushes bytes (`parser.Cursor`) need-more-data has nowhere to
go, so the operation stays an explicit call — Rust's `futures::Stream` PASSES
the test, since `Poll::Pending` IS need-more-data, while a plain `Iterator` does
not. Where the layer owns the source (`impl/go/stream`) blocking or `await`
absorbs need-more-data, so an iterator is correct there.

A port that offers only the iterator over a caller-pushed cursor has dropped
this library's central distinction, whatever else it gets right.
[Pull shape across languages](../pull-shape-across-languages.md) works both
cases through several languages and is the reference for a port; keep it and
`spec/SPEC.md`'s "Portability of API shape" section in agreement.

## One reading surface

The reading core has exactly ONE surface: a streaming pull operation,
`Cursor.Next`, returning a closed `Node` (`*MasterNode`/`*LeafNode`/`*EndNode`)
one event at a time, not a document model. Never add a second push or callback
event shape.

Flow control is decided on the event before the next event is requested:
`MasterNode.Descend`/`Skip` and `LeafNode.Payload`/`Skip` are valid only for the
node that offers them. An untouched master is descended into and an untouched
leaf is skipped.

`parser.Parser` stays exported as the low-level engine: `internal/ebmltrace`
needs operation-level control to produce the golden traces, and the goldens are
the conformance corpus.

## Node lifetime is enforced, not documented

Each node is valid only until the next `Next`. That lifetime is ENFORCED: the
cursor stamps a generation into every node it hands out and EVERY exported node
method — the extent accessors as much as the decisions — panics on a stale
stamp.

The guarantee has NO exception, and the per-event allocation is what buys it.
`Next` allocates a new node per event instead of refilling one instance per
kind, so a retained pointer, a copy (`v := *node`), and either of those while
the current event is of the same kind are all caught. Never reintroduce instance
reuse to save that allocation — a retained pointer would then BE the live node
and no check could exist, which is the silent corruption the rule exists to
prevent.

The cost is measured, not claimed: exactly one allocation per event
(`allocsPerEvent` in `impl/go/parser/node_validity_test.go`), with
`BenchmarkCursorScan`/`BenchmarkParserScan` pricing it against `parser.Parser`,
the surface for a consumer that needs no node. Keep that statement identical in
`Node`'s doc, `impl/go/README.md` and `spec/SPEC.md` section 3 — never widen the
claim in one of them.

A method added to a node type must take the same check (`nodeExtent.fresh`) and
is covered automatically by the reflection-driven tables in
`impl/go/parser/node_validity_test.go`.

`LeafNode.Payload` returns a VIEW of the cursor buffer, not a copy: the cursor
caches the payload's extent and never the slice it handed out, and the bytes are
valid only until the next `Next` and must not be modified — a consumer that
retains them (`ext/fragment` does) copies them itself.

## `parser.Cursor` offers no iterator, and never will

It had one (`Cursor.Nodes`), documented as non-normative sugar, and it was
removed once `stream` proved the arity rule above: a range loop over a
caller-fed cursor cannot distinguish need-more-data from end of input, which is
this library's central semantic, and it had zero consumers while costing a "not
the normative shape" caveat in three documents.

The pull stays `Next`. `Cursor.Err` remains, because it REPORTS and never
advances — an accessor is not a second spelling of the pull.

## `impl/go/stream` is the one place `NeedMoreData` is answered

Only the holder of the input source can give that answer. A consumer that pushes
bytes itself still sees `NeedMoreData` from `parser.Cursor`; that low-level
contract stays unchanged, and the two are not alternatives — `stream` is built
on it.

Because `stream` has absorbed need-more-data, only two outcomes remain there,
and it is the ONE layer where the iterator is exact rather than lossy: its whole
reading surface is `Nodes() iter.Seq2[parser.Node, error]`, with `Payload` and
`Offset` beside it and NO exported `Next`. That absence is the point — two
spellings of the same pull is how the three-outcome collapse creeps back in, and
`stream` is the working proof of the arity rule above, not merely its
description.

The end of the input ends the iteration; every other failure is yielded once, as
the final pair, with a nil node, so a consumer cannot lose it by forgetting a
separate `Err` call — which is exactly how the removed `Cursor.Nodes` could lose
one. Never add a `Stream.Next` back, and never soften `Nodes` to an `iter.Seq`
plus `Err`.

## Explicit closure

`CloseMaster` is explicit boundary closure only. It accepts an unknown-size
master, or a known-size master already at its declared end, and rejects a
known-size master with payload outstanding (`PrematureCloseError`); a known-size
boundary belongs to the stream and must not be discarded. `LeaveMaster` remains
the ordinary close at a declared end.

## A master end says why, and that is a field rather than a channel

`EndNode.Reason` reports which of the three ways a master closed: its declared end
was reached, the boundary rule ended it, or the input ran out with it still open.
The three are exhaustive because the cursor has exactly three places that close
one, all of them reaching `issueEnd`; a fourth would be a change to `Cursor`
rather than something a stream can produce.

It exists because the extent cannot state it. An unknown-size master closed by the
boundary rule and one closed at end of input are otherwise the same event, and a
consumer cannot tell them apart for itself without restating the boundary rule in
its own code — the duplication `matroska.StreamBoundary` exists to prevent
([Registry, boundary policy, schemas](registry-and-schemas.md)). It is normative:
`spec/SPEC.md` section 3 carries it, so a port reproduces it.

WHAT IT IS NOT is the more useful half. It was asked for as one member of a
`WithObserver(func(Observation))` channel carrying a stable code for every anomaly
a reader can see, and that channel was declined
([Declined additions](declined-additions.md)). This item survived the decline
precisely because it is the opposite shape: a fact the pull surface was already
delivering an event for, added to that event, changing no callback and adding no
second way to learn anything. That is the pattern for the next such request — a
missing fact belongs on the event that already reports the thing it is about, and
a fact with no event of its own is a reason to ask which event should carry it,
never a reason to open a channel.

`ClosedByEndOfInput` is deliberately not a truncation verdict: a complete live
stream whose outermost master is unknown-size ends exactly that way. Whether bytes
were lost is `TruncatedError`'s question
([Errors, recovery, and delivery](errors-and-recovery.md)), and keeping the two
apart is why the reason names a mechanism rather than a judgement.

## Whatever starts a pull coroutine publishes the way to release it

Turning an `iter.Seq2` back into a `Next` means `iter.Pull2`, and `iter.Pull2`
starts a coroutine the caller must stop. A type that starts one and exports only
`Next` has made the resource UNRELEASABLE BY CONSTRUCTION, not merely awkward to
release: the `stop` closure is unexported, the coroutine is parked in the
iterator's `yield` rather than in a `Read`, and so nothing the caller can do to
the byte source reaches it. So the rule is the narrow one — whatever starts the
coroutine owns releasing it, and must publish a way to do so.

`integrations/kvs.Reader` is the only place in this repository that starts one,
and it exports `Close`. The layers below do not need the rule and must not grow a
method for it: `ext/fragment.Reader` runs no goroutine, and its `Fragments()` is a
plain `iter.Seq2` that returns on `!yield` like any other.

The trap is that "iterate to completion" is a reasonable reading of a type whose
only method is `Next`, and consumers of a STREAM will routinely not. Every normal
termination of a live read is an early stop, so the early stop is the case the API
must cover, not the exception. `Close` is scoped accordingly: it releases the
coroutine, it does not touch the caller's source, and it does not discard a
latched failure — an exhausted reader that reported an error keeps reporting it,
because [a documented guarantee is never weakened](errors-and-recovery.md) by a
method added beside it.
