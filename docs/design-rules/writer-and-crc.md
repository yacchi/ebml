# The writer, round-tripping, and CRC-32

Why exactly one encoder exists, why it holds no element knowledge, and why
CRC-32 is explicit on both sides. The one-line rules live in
[`CLAUDE.md`](../../CLAUDE.md); this note is the reasoning behind them.

## One encoder

`impl/go/writer` is the repository's ONLY EBML encoder. Anything that emits
bytes uses it. No test, fixture generator or extension may carry an encoder of
its own: `internal/kvsgen` builds the corpus through it, `tree.Marshal` composes
its primitives, and hand-shaped test inputs call it too — through
`internal/ebmltest`, the ONE shared shaping layer over the public writer API
(`Leaf`/`Uint`/`String`/`UTF8`/`Master`/`UnknownMaster`/`Encode`).

A test may not re-implement it per file, and an unknown-size master comes from
the writer's `UnknownSize` strategy, never from hand-concatenating an ID with
the unknown-size marker.

The public `impl/go/writer` package replaced four private encoders formerly in
`internal/kvsgen`, `tree/tree_test.go`, `ext/fragment/synthetic_test.go`, and
`matroska/unknown_elements_test.go`.

## The writer holds no element knowledge

Like the cursor, the writer holds no element knowledge — the CALLER picks the
value type, mirroring the reader's `AsUint`/`AsString` choice — and it refuses a
value the reader could not return unchanged: a string carrying a NUL byte is
rejected, since a reader stops at the first NUL.

## Parse-then-marshal is byte-identical

Parse-then-marshal is a core contract and stays byte-identical for every
committed fixture: retained `HeaderLen` reproduces each header's original
size-VINT width. The exception is a payload elided by a retention cap, which the
tree no longer holds.

`ParseSimpleBlock` and its inverse `(*parser.SimpleBlock).Append` are leaf
decoding convenience in `parser`; they do not turn the core into a retained
document model, and `Append` is not a second EBML encoder either. A block's
internals are a payload layout inside one binary leaf, which is why the pair
takes and returns bytes and never an element ID, and the element around it is
still written by `impl/go/writer`.

## When byte equality does not hold, and how to compare then

The byte-identical guarantee above is about THIS repository's fixtures, which the
writer produced. A consumer round-tripping a capture it did not produce usually
cannot get byte equality, and that is not a defect on either side:

* a SERVICE may add elements a producer never wrote, so re-emitting a producer's
  output legitimately yields a shorter document than the one that was read;
* a size VINT has legal non-minimal widths, so two encoders can disagree on
  bytes while agreeing on every element.

For such an input the useful comparison is between the two PARSED
representations, and the discipline that makes it worth anything is this: where
the two differ for a real reason, ASSERT THE DIFFERENCE EXPLICITLY rather than
excluding the field. An exclusion list is where shape bugs hide — a field dropped
from comparison because it was noisy once stops being checked forever, and the
next drift in it is invisible. An asserted difference fails when the reason for
it stops being true, which is the whole point.

This is a technique and deliberately NOT an API. A helper here would have to take
the non-preserved fields as a parameter, which is an exclusion list shipped and
blessed by the library that should be the thing under test — a list each consumer
writes is one it revisits, and a default nobody revisits is worse than none. The
request, and the argument, are in
[Declined additions](declined-additions.md).

Nothing about it is needed to catch writer/reader drift in this repository. The
corpus is regenerated in CI and any diff fails the build, so the stricter check
already runs on every commit; the parsed comparison exists for inputs the writer
cannot reproduce, which the corpus by construction never contains.

## CRC-32 is explicit on both sides and implicit on neither

Verification happens only when a caller asks for it,
`tree.Element.VerifyChecksum`, and nothing in the library ever checks a checksum
on its own: a user must never be handed a failure from validation they did not
request, and a checksum covers the element data AS STORED, so only the retained
model holds the bytes to sum at all — the cursor's payload view dies at the next
pull.

A mismatch is a CONTENT error (`parser.NewContentError`) and never structural,
because the extents were read correctly: the position of the next element is
known and the parse is not in doubt, only this element's bytes are.

Emission is opt-in PER MASTER (`writer.WithChecksum`) on a `Buffered` master,
and the CALLER supplies the CRC-32 element ID, exactly as it supplies every
other ID. That parameter is what keeps the no-element-knowledge rule intact,
since a hard-coded ID would be the first element literal in `impl/go/writer`.

`tree.Marshal` never GENERATES a CRC element: a CRC-bearing input already
retains it as an ordinary child and writes it back verbatim, so generating one
would rewrite bytes the document already had and break the byte-identical
round-trip.

The primitive itself is `impl/go/crc`; why it is core rather than an
implementation detail is in
[Layer boundaries](layer-boundaries.md#what-makes-a-package-core).

## What is deliberately out of scope

Verification is available exactly where the covered bytes were RETAINED. A
consumer that skipped a subtree, or capped payloads with `WithMaxPayload`, has
nothing to sum and is told so (`ChecksumUnavailableError`) rather than passed.
Recursion is left to the caller's `Walk`.

That is not a gap. Nothing verifies implicitly, and `impl/go/parser` still holds
no element knowledge, so a streaming checksum cannot live there. `Void` remains
an opaque skippable leaf.
