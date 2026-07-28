# Declined additions, and why declining is the design

Every other note here explains a rule. This one explains the disposition that
produces them: what happens when someone asks for an API this library could
obviously provide, and why the answer is usually no. The one-line rule lives in
[`CLAUDE.md`](../../CLAUDE.md); this note is the argument, and the ledger.

It is not normative. [`spec/SPEC.md`](../../spec/SPEC.md) is the portable
contract, and where this note and the specification disagree, the specification
wins and this note is wrong.

## The cross-section is the product

The room to add convenient API here is large, and that is the problem rather
than the opportunity. A pull cursor over a container format touches everything:
observation channels, query helpers, typed views, comparison utilities,
per-format profiles. Each request arrives individually reasonable, backed by a
real cost the asker measured, and none of them is refused because it is a bad
idea in isolation.

What an addition costs is paid by everyone, forever, for a benefit that was one
consumer's:

* **A port must reproduce it or diverge.** `spec/SPEC.md` is reproducible in
  another language because the surface it describes is small enough to hold in
  one head. Every addition either enters the contract — and a Rust, Python or C
  port now owes it — or sits outside the contract and makes the Go
  implementation the odd one out. There is no third option where it is free.
* **A shortcut erodes the distinction the library exists for.** The sharpest
  case is already recorded: `Cursor.Nodes` was ordinary convenience, and a range
  loop over a caller-fed cursor cannot state need-more-data, which is this
  library's central semantic. It was removed, not because iterators are bad, but
  because the convenience quietly deleted a distinction
  ([Pull shape and node lifetime](pull-and-lifetime.md)).
* **A second way to learn the same fact drifts from the first.** Two surfaces
  reporting one event agree on the day they are written and not after. This
  repository has the three-copies-drift-apart failure written into several notes
  because it kept happening.
* **An API cannot be withdrawn.** A rule can be relaxed later; an exported name
  cannot be taken back. So the asymmetry is deliberate: declining is reversible
  and adding is not, which is why the burden of proof sits on the addition.

None of this is a claim that the surface is finished. Real gaps are filled, and
the ledger below records those too. It is a claim that "a consumer needed this
and the library could provide it" is the START of the argument, not the end.

## The three questions

An addition is declined if ANY of these fires, and the need is then answered in
whatever shape the standing rules do allow.

1. **Can the asker build it on the exported surface today?** If yes, decline and
   show them the code. A wrapper this library could write is a wrapper the
   consumer can write, and only the consumer knows which of their own concerns
   belong in it.
2. **Is it a second spelling of a fact an existing surface already delivers?**
   If yes, decline. The fix for a fact that is hard to reach is to make the
   existing surface state it, never to open a second channel beside it.
3. **Does its SHAPE reintroduce something a rule removed?** A field whose meaning
   depends on another field; a discriminator read out of message text; a
   configuration object, a hook registry or a DSL; a push surface beside the pull
   one. If yes, decline the shape even when the need behind it is real and
   well-evidenced — and then answer that need in a shape that passes.

A request that clears all three is accepted, and accepted requests are recorded
here as well, so this ledger cannot be read as a record of saying no.

The ledger starts partway through the project's life. Everything below "Declined
earlier" was decided before this note existed and was recovered from the session
records the work happened in, which are not part of this repository — so those
entries are shorter, and their provenance is a record you cannot check from here.
Each was re-checked against the code as it stands before being written down. New
entries are added when the decision is made, not reconstructed afterwards.

## Declined

### One structured observation channel — `WithObserver(func(Observation))`

*Asked 2026-07, KVS consumer feedback round 4 (`plans/KVS-CONSUMER-FEEDBACK-ROUND4.md`, F9). Declined.*

The ask: replace `ext/fragment`'s two differently-shaped notify callbacks with
one `Observation{Code, Offset, ID, Detail, Cause}` carrying a stable enum, so a
consumer can apply ONE logging policy — first occurrence WARN per code, counter
thereafter, per-task summary — instead of inventing a logging shape per callback.
The underlying cost is real and precisely stated: anomalies observed in
production are spread across several vocabularies with no shared key.

Declined on all three questions.

**Question 3.** The two signatures differ on purpose.
`WithSkipContentErrors(id, offset, cause)` names an `ElementID` because exactly
one element was dropped. `WithResync(offset, skipped, cause)` carries a byte
count because what was lost is a range no single element names. Merging them
yields an `ID` whose meaning depends on `Code`, and a `Detail string` consumers
will parse — the two defects `parser.TruncatedError` was reshaped to remove
([Errors, recovery, and delivery](errors-and-recovery.md)). A `Code` enum plus
one registration point is also, precisely, the hook registry and the DSL that
[standard-library sensibility](layer-boundaries.md#standard-library-sensibility)
excludes from the core.

**Question 2.** Half the proposed codes are not callback material.
`UnknownElementRetained` is not an anomaly at all — it is an ordinary event the
pull surface already delivers, and routing it through a callback is the second
spelling [pull-and-lifetime.md](pull-and-lifetime.md) forbids. `SizeMismatch` is
`parser.ElementOverflowError`, an already-typed structural error.
`TruncatedTailSalvaged` is already reachable three ways: the salvaged fragment
arrives marked `fragment.Fragment.Truncated`, the error carries `EndOffset`,
`ID` and `InHeader`, and the fragments released intact arrive from `Next` BEFORE
the error, so the caller counts them by having received them.

**Question 1, and the decisive one.** The stable vocabulary this asks for cannot
be closed by the library, because half of the asker's own codes — tagless runs,
a `ContactId` changing mid-stream, PCM length disagreeing with its time span —
are facts no EBML library can know. So the enum is necessarily the consumer's,
and the library's job is to feed it, which two closures already do:

```go
obs := func(o Observation) { /* first occurrence WARN per code, then count */ }
fragment.WithSkipContentErrors(func(id parser.ElementID, off int64, cause error) {
    obs(Observation{Code: SkippedContent, ID: id, Offset: off, Cause: cause})
})
fragment.WithResync(func(off, skipped int64, cause error) {
    obs(Observation{Code: Resynced, Offset: off, Skipped: skipped, Cause: cause})
})
```

The asker's real critique — "every new callback shape pushes that decision back
onto each consumer" — is correct, and the answer to it is not a bus. It is that
THE CALLBACK COUNT DOES NOT GROW. Two options answer the two error classes, and
[errors-and-recovery.md](errors-and-recovery.md) requires a third class before a
third option, which is a much higher bar than a third concern.

One item was separated out rather than declined, and then accepted: whether a
master was closed by its declared end, by the boundary rule, or by end of input
was genuinely not readable — `parser.EndNode` reported the extent and not the
reason. It passed all three questions where the channel failed them. A consumer
cannot derive it (telling the two unknown-size closes apart means restating the
boundary rule in the caller, which `matroska.StreamBoundary` exists to prevent);
it is no second spelling, since nothing delivered it; and its shape is a field on
the event that already reports the close, so no callback grows and nothing is read
out of a message. It shipped as `EndNode.Reason`, in `spec/SPEC.md` section 3.

That is the pattern, and it is the useful half of this entry: a missing fact
belongs on the event that already reports the thing it is about. A fact with no
event of its own is a reason to ask which event should carry it, never a reason to
open a channel.

### An exported round-trip structural comparison helper

*Asked 2026-07, KVS consumer feedback round 4 (`plans/KVS-CONSUMER-FEEDBACK-ROUND4.md`, F12). Declined.*

The ask: an `ebmltest`-style helper that compares two PARSED representations —
parse a capture, re-emit it, parse that, compare — with the legitimately
non-preserved fields named, since byte equality does not hold when service-added
tags are absent from a producer's output.

Declined on question 3, using the asker's own principle. They wrote that an
exclusion list is where shape bugs go to hide, and then asked for an API whose
parameter is an exclusion list. A list each consumer writes is one they revisit;
a list this library ships becomes the default nobody revisits, blessed by the
library that should be the thing under test.

It is also answering a need this repository does not have. The drift the helper
would catch is already caught more strictly: parse-then-marshal is BYTE-IDENTICAL
for every committed fixture and CI fails on any diff
([The writer, round-tripping, and CRC-32](writer-and-crc.md)). The weaker
parsed-representation comparison is required by the ASKER'S corpus — real
captures the writer cannot byte-reproduce — which is a property of their input,
not of this library.

What survives is the technique, which is worth writing down and is not an API. It
is written down: [The writer, round-tripping, and CRC-32](writer-and-crc.md), "When
byte equality does not hold, and how to compare then".

## Declined earlier, and recorded nowhere else

The entries above are this ledger's first round. The project had been declining
things for as long as it had been built, and those declines were argued once and
then kept only in the session records the work happened in — outside this
repository, and so invisible to everyone who reads it. Recovered from those
records and re-checked against the code as it stands; where a reason turned on an
API, that API was verified to still behave the way the reason assumed.

**A GetMedia wrapper over the AWS SDK, inside the KVS integration.** The scope
rule that an integration holds no client is in
[`impl/go/integrations/doc.go`](../../impl/go/integrations/doc.go); the reason
recorded when it was decided is stronger and was never written down. A wrapper
worth shipping has to handle reconnection and continuation tokens correctly, and
writing that precisely amounts to publishing the operational know-how of running
against the service. The library's job is to make that work possible; navigating
the service's traps stays the consumer's. This was decided as a matter of scope
and NOT of scheduling — it is not a deferred item and does not come back as "next
work".

**A notify or OnClose handler on `ext/scope`.** Declined for the reason F9 above
was, years of decisions earlier: a finished scope is the RETURN VALUE of
`Tracker.Observe`, which keeps the layer pull-shaped and matching
`Assembler.Feed` returning completed fragments. That the same argument now
answers a second request is the point of writing it down.

**`parser.WithPayloadThreshold`.** A size below which payloads would be buffered
eagerly. It reduces how often `NeedMoreData` is reported and cannot remove it
from the contract, so the consumer still writes the same loop — and the media
payloads that motivated it exceed any threshold worth setting.

**`LeafNode.Payload` returning an owned copy.** It would simplify every layer
above, and it costs a full media copy for the consumers that only look at bytes.
It also weakens the measured one-allocation-per-event guarantee, which
`Node`'s doc, `impl/go/README.md` and `spec/SPEC.md` section 3 state identically
— a guarantee stated in three places is not quietly narrowed in one.

**`parser.Detach(Node)`.** The one CONDITIONAL entry here. It is sound, and it is
worth adding when a consumer needs a retainable node for its own sake; it was
declined because `impl/go/stream` removed the ergonomic pressure that had
motivated it. A request that brings back an actual need, rather than the
ergonomics, is a different request.

**`parser.WithByteTap`.** A byte tap to enable streaming CRC-32 verification. It
is element-agnostic, so it breaks no rule — it was declined as unproven core
surface, and because a consumer that correctly skipped a subtree has nothing to
verify. See [The writer, round-tripping, and CRC-32](writer-and-crc.md) for what
checksum verification does instead.

**`WithEagerEmit` on `ext/fragment`.** A predicate that returns true at the
Cluster's own end IS eager emit, so `WithMetadataComplete` already covers both
escapes and a second option would only give one of them a name.

**`WithSalvageTruncatedTail`.** Declined because it would be an option that
changes no error's behaviour: the salvage is the default and nothing turns it off
([Errors, recovery, and delivery](errors-and-recovery.md)).

## Accepted

Recorded here so the ledger states what passes, not only what does not.

* **Discoverability of `integrations/kvs` from the core packages** (round 4 F8).
  A consumer re-implemented four pieces of this module and missed the in-band
  error handling entirely, because a separate module is invisible to whoever
  requires only the core. Accepted as doc-comment pointers in `ext/fragment` and
  `ext/tags` — no import, no new API, and the named symbols are pinned by a test
  in the module that can see both.
* **Evidence on `parser.TruncatedError`** (round 4 F10). Accepted because it adds
  no surface: the consumer must classify a truncated tail by transport, the
  library cannot, and the fields let that classification rest on a measurement.
  Question 3 applied to the FIRST implementation of it, which discriminated two
  meanings of `ID` by matching `Msg` — reshaped to a uniform `ID` plus `InHeader`
  before it landed.
* **A named PutMedia profile in `integrations/kvs`** (round 4 F11). Accepted:
  a Cluster size strategy and what `x-amzn-fragment-timecode-type` makes
  `Cluster.Timestamp` MEAN are that system's layout knowledge, which is exactly
  what an integration holds. THE SHAPE WAS DECLINED: no `kvs.FragmentWriter`,
  because a type that composes `writer` calls is a thin wrap that blurs the
  single-encoder rule. What the library can hold and a caller cannot derive is
  the write-side counterpart of `ClusterTime` — the epoch basis that cost the
  asker a multi-hour debugging session — plus named constants and a documented
  size strategy. One function, some constants, and a package-doc section.

## Declines already recorded elsewhere

Linked, never restated; the note that owns each rule owns its argument.

| Declined | Recorded in |
| --- | --- |
| An iterator over `parser.Cursor` (`Cursor.Nodes`, removed) | [Pull shape and node lifetime](pull-and-lifetime.md#parsercursor-offers-no-iterator-and-never-will) |
| A push or callback READING surface beside `Cursor.Next` | [Pull shape and node lifetime](pull-and-lifetime.md#one-reading-surface) |
| Node instance reuse, to save the per-event allocation | [Pull shape and node lifetime](pull-and-lifetime.md#node-lifetime-is-enforced-not-documented) |
| `Stream.Next`, or softening `Nodes` to `iter.Seq` plus `Err` | [Pull shape and node lifetime](pull-and-lifetime.md#implgostream-is-the-one-place-needmoredata-is-answered) |
| A configuration object, a hook registry, a DSL, any third-party dependency | [Layer boundaries](layer-boundaries.md#standard-library-sensibility) |
| A query DSL, a second index type, a per-element retention allowlist | [Layer boundaries](layer-boundaries.md#retention) |
| Rebuilding `ext/fragment.Assembler` on `ext/scope.Tracker` | [Layer boundaries](layer-boundaries.md#retention) |
| One `ext` package importing another | [Layer boundaries](layer-boundaries.md#ext-is-optional-convenience-and-every-ext-package-is-a-leaf) |
| A tag accessor outside `ext/tags`, or one naming a producer | [Layer boundaries](layer-boundaries.md#exttags-is-the-only-home-of-a-tag-accessor-and-it-names-no-producer) |
| An API client, transport or SDK dependency inside an integration | [`impl/go/integrations/doc.go`](../../impl/go/integrations/doc.go) |
| Implicit CRC-32 on either side, recursive verification, a streaming checksum in `parser` | [The writer, round-tripping, and CRC-32](writer-and-crc.md#what-is-deliberately-out-of-scope) |
| A second EBML encoder, or element knowledge inside `writer` | [The writer, round-tripping, and CRC-32](writer-and-crc.md#the-writer-holds-no-element-knowledge) |
| Element knowledge in `parser`; a default `KindClassifier` | [The registry, boundary policy, and the official schemas](registry-and-schemas.md#implgoparser-holds-no-element-knowledge) |
| Restating the boundary rule in a caller; an allow-list form of it | [The registry, boundary policy, and the official schemas](registry-and-schemas.md#one-boundary-policy) |
| Vendoring the CELLAR schemas; generating `matroska/elements.go` | [The registry, boundary policy, and the official schemas](registry-and-schemas.md#the-official-schemas-are-never-vendored) |
