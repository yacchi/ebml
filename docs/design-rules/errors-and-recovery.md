# Errors, recovery, and delivery

Why there are exactly two error classes, why each recovery option acts on one
class only, and why two behaviors that look like recovery — the salvaged
truncated tail and the wait for settled metadata — are defaults with no option
attached. The one-line rules live in [`CLAUDE.md`](../../CLAUDE.md); this note is
the reasoning behind them.

## Two classes plus flow control

Errors have exactly two classes plus flow control, and the classification is
part of the core contract. `parser.IsStructural(err)` is the canonical test and
is true for every STRUCTURAL failure of the cursor.

A CONSUMER's verdict about an element's content is marked with
`parser.NewContentError`, is never structural whatever value it carries, and
still unwraps to the consumer's own error. `NeedMoreData` is neither class.

A pull cursor never runs consumer code, so the core cannot raise a content error
itself — the marker exists precisely so a consumer's error stays classifiable.
The predicate owns the content boundary because `errors.Is` traverses the whole
chain and cannot stop there; `parser.ErrStructural` remains only as the marker
the cursor's own errors unwrap to.

Keep every new cursor failure in the structural class and never classify by
message text.

## Recovery is split by class, and neither option may cross

Byte scanning is allowed only for opt-in post-failure resynchronization AFTER a
structural failure (`parser.IsStructural(err)`). It is never boundary detection,
and a content error must never trigger it.

There is one opt-in `ext/fragment` option per class:

* `WithResync` answers a structural failure by scanning forward and losing the
  bytes between.
* `WithSkipContentErrors` answers a content error by dropping just the offending
  element — no scanning and no lost Fragment, because a content error leaves the
  structural position intact.

Both are terminal by default and both report every recovery to a `notify`; a nil
`notify` disables the option rather than silencing it.

## A truncated tail is salvaged, and that is the default

`ext/fragment.Assembler.Finalize` emits the `Cluster` that was still open when
the input ended, marked `Fragment.Truncated`, TOGETHER WITH the structural error
— which is still returned, still latched, and still reported by every later
call.

It is not a third recovery option because it is not recovery. `WithResync` and
`WithSkipContentErrors` are opt-in precisely because they change an error's
TERMINALITY, and this changes none of it, only whether already-decoded blocks
are reachable. `Feed` already documents that an error comes with the fragments
that completed before it, so `Finalize` discarding the tail was the
inconsistency, not the fix.

The fragment is emitted even with zero decoded blocks: "the Cluster was open" is
structural, "is this fragment worth anything" is the caller's `len(Blocks)`.

The marker CANNOT be `tree.Element.Truncated`, which is already set on every
retained `SimpleBlock` and so cannot tell a salvaged fragment from a complete
one; inside the tree that flag keeps its ordinary meaning and marks the element
the cut fell inside.

`kvs.Reader.Next` carries the same rule: queued fragments are delivered first
and the error is reported once they run out.

This shape is NOT in the shared fixture corpus and must not be added to it —
every fixture must parse cleanly and round-trip byte-identically, which a
truncated one cannot. It is covered by package-level synthetic tests in both
modules instead.

## The truncated tail carries evidence, never a verdict

Whether a truncated tail is a fault is the CONSUMER's question, and the library
cannot answer it: a live GetMedia connection ending at an arbitrary byte is
normal, while a finite `GetMediaForFragmentList` body ending mid-element is a
transfer fault, and the bytes are identical in both cases. So the classification
stays with the caller and `parser.TruncatedError` supplies what the caller
cannot measure for itself — `EndOffset`, the absolute offset at which the input
ended, and `ID`/`HasID`, the element that was open there. An Info-versus-Warn
split then rests on a measurement instead of an assumption
(`plans/KVS-CONSUMER-FEEDBACK-ROUND4.md`, F10).

`ID` has ONE meaning in every case: the innermost element still open at
`EndOffset`. What changes is whether that element is the one the cut destroyed,
and `InHeader` says which without reading the message — because a cut inside a
HEADER loses the ID VINT itself, so no ID is invented for the element that was
arriving and `ID` names its enclosing master instead. `HasID` is false when the
cut fell at the top level with nothing open, a distinction a zero ID cannot
state. Discriminating on `Msg` instead would be classifying by message text,
which this repository does not do anywhere else and does not start here.

NOT EVERY SHORT INPUT IS A `TruncatedError`. A tail that ends on an element
boundary inside a known-size master is not cut mid-element at all: the master's
declared end is simply never reached, which the cursor reports as an `Invalid`.
That shape carries none of this evidence, so a consumer classifying a short tail
handles both errors rather than assuming the evidence is always there.

The evidence lives in FIELDS and never in the message. `Error()` stays exactly
`truncated input` or `truncated input: <msg>`, because the committed golden
traces and every consumer that logs the string would otherwise change meaning
for a diagnostic that added nothing to what they already print.

## An in-band failure is reported, never merely available

GetMedia states a failure as `AWS_KINESISVIDEO_ERROR_CODE` tags on a fragment
instead of cutting the connection, and `kvs.Reader.Next` returns it as a
`*StreamError` — sticky, with no option to enable or disable it.

Leaving it to `Metadata.Err()` made a stream KVS STOPPED ON AN ERROR read as a
short clean end to any consumer that forgot the call, and a consumer reported
losing time to exactly that silent class
(`plans/KVS-FULL-MIGRATION-REQUIREMENTS.md`, requirement 2); a default that goes
quiet is the one shape that was not acceptable, so there is no
`WithStreamErrors()` opt-in either.

The error FOLLOWS the fragment that carried it rather than accompanying it. The
requirement asked for them together, and the ordering was chosen against that
request on the request's own ground: `if err != nil { break }` must never drop a
fragment, which is the rule the truncated tail already follows.

`Metadata.Err()` stays, for reading the failure off one fragment; it is no
longer how one DISCOVERS it. What this cannot report is stated where it is
offered: error tags in a document that assembles no `Cluster` yield no fragment,
so `Next` never sees them.

## A Fragment is delivered once its metadata has settled

A Fragment is ASSEMBLED at its `Cluster`'s end and DELIVERED once its
Segment-level metadata has settled, and the wait is DEFAULT behavior with no
option to disable it — the same reasoning that makes truncated-tail salvage
default: it changes no error's terminality, only when an already-assembled
fragment becomes reachable, and the opt-in options exist precisely for the
changes that DO alter terminality.

The default release points are the next `Cluster`'s header, the `Segment`'s end,
and the end of input, all of them structural, so split-invariance holds.

This OVERTURNS the earlier answer to Q3 in `KVS-CONSUMER-FEEDBACK.md` — that a
Fragment is a snapshot taken at Cluster close, with post-Cluster metadata
reachable only through `ext/scope` — because field measurement retired it. A
consumer reading tags on receipt lost identity keys silently (112 fragments
became 27, 76% of the audio), the loss was READ-SIZE DEPENDENT so it never
reproduced in tests that fed a whole capture at once, and `connect_real_shape`
shows why: Amazon Connect writes two `Tags` before its `Cluster` and two after.
A layout every consumer must compensate for belongs in the library.

Waiting for the next `Cluster` is the only ELEMENT-AGNOSTIC proof that no
further `Tags` follow, so in the KVS one-Cluster-per-document shape the default
wait lasts a fragment. `WithMetadataComplete` is the one escape and takes a
CALLER-SUPPLIED predicate, exactly as `parser.WithBoundary` takes
`matroska.StreamBoundary` — which is why `kvs.MetadataComplete` (release on the
continuation token GetMedia writes last) lives in `impl/go/integrations/kvs` and
`ext/fragment` still knows no tag name.

A predicate that always returns true IS the old eager emission, which is why
there is no second option for it; its view is read-size dependent by nature and
that is documented where it is offered, never hidden.
