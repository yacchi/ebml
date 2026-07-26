# Evidence over assumption

Three findings from consumer review, and the rule each one produced. Every one
of them is the same defect: something in this repository modelled the shape that
was easiest to produce rather than the shape the field actually sends, and the
test suite then confirmed the assumption instead of the world. The one-line
rules live in [`CLAUDE.md`](../../CLAUDE.md); this note is the reasoning behind
them.

## A documented guarantee is never weakened to match an implementation gap

When the KVS consumer review (`KVS-CONSUMER-FEEDBACK.md`, F1) reported the
node-staleness panic intermittently not firing, the response was never to soften
the "no exception" wording of the lifetime guarantee. The fix belongs in the
implementation, or, if a guarantee genuinely could not hold, the documented text
is restated as what is actually true.

A guarantee never drifts toward matching a bug.

## A reference example must model the shape the field produces

The same review (F2) found `examples/kvs-getmedia`'s original tag-inheritance
policy only handled a whole-`Tags`-absence shape that Amazon Connect never
produces; the field shape is partial — `Tags` present, identity keys missing.

The fix was a per-key inheritance policy plus the `partial_tags` fixture proving
it, not a disclaimer next to the simpler, easier-to-generate shape.

## The same applies to the fixture corpus, and more sharply

A corpus is what the test suite believes the world looks like.

Round 2 (F5) found every KVS fixture built with a known-size `Cluster` while the
field only ever sends an unknown-size one — which is why 450+ tests passed
against a stream the consumer could not read. A corpus generated from an assumed
shape validates the assumption, not the world.

`known_size_cluster` is retained as the one deliberate counter-case: legal
Matroska that KVS does not send.

`connect_real_shape` carries the same lesson a second time. It models the real
two-before/two-after `Tags` layout, with an EPOCH-BASED `Cluster.Timestamp`
naming the same instant as its `PRODUCER_TIMESTAMP`. It declared 0 until that
was found to model a timeline origin the field never sends — the same defect
class as F5. The other fixtures keep a zero-based timeline on purpose, since
Matroska fixes no origin and a file does start at zero.
