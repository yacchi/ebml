# The registry, boundary policy, and the official schemas

Where element knowledge is allowed to live, why the unknown-size boundary rule
has exactly one implementation, and why the normative schemas are never
vendored. The one-line rules live in [`CLAUDE.md`](../../CLAUDE.md); this note is
the reasoning behind them.

## `impl/go/parser` holds no element knowledge

No element table, and not one element ID literal in its non-test source. All
classification comes from the supplied `KindClassifier`, which is a REQUIRED
argument of `parser.New` and `parser.NewCursor` — there is no option form and no
built-in default, and `nil` panics.

Element IDs live only in `impl/go/matroska`. A default would silently read an
unlisted master as one opaque leaf, so never reintroduce one.

## One boundary policy

The boundary policy for a stream of concatenated documents lives in exactly ONE
place, `matroska.StreamBoundary`: a new top-level element ends any open master,
and otherwise the RFC 9559 child rule ends an unknown-size master at the first
element that cannot be its child.

That second half is DENY-ONLY (`Registry.EndsUnknownSizeMaster`). A boundary is
reported only when the registry holds a COMPLETE child list for the open master
and `next` is a built-in, non-global element absent from it, because a false
boundary corrupts the parse while a missed one only closes later than it could.
Unknown IDs classify as readable binary leaves and never trigger a containment
boundary.

`ext/fragment`, `impl/go/cmd/ebml` and `internal/ebmltrace` all call it and none
restates it. All three did once, and each copy went stale on its own schedule:
the CLI rendered a live stream's trailing `Tags` inside its `Cluster` while the
assembler read the same bytes correctly, and `ebmltrace` wrote GOLDEN FILES in
that same wrong shape — a conformance corpus for a reader nobody ships. Never
reintroduce a copy; a golden produced by a rule the library does not use is
worse than no golden, because it looks like evidence.

## The official schemas are never vendored

The normative machine-readable schemas — `ebml.xml` (RFC 8794 header elements)
and `ebml_matroska.xml` (RFC 9559 body elements), published by the IETF CELLAR
working group — are CC-BY-4.0 works. They are a development-time input: the
`conformance-check` skill fetches them into the gitignored `.spec-cache/` and
`internal/specconform` checks the registry against them.

What CC-BY actually covers is the schema's PROSE, so no element
`<documentation>` text is ever copied into a Go doc comment; IDs, names, types
and paths are interoperability facts and are transcribed by hand.

`impl/go/matroska/elements.go` stays HAND-WRITTEN and is never generated: its
omissions are deliberate and a generator would erase that intent.

## What the checker checks

`internal/specconform` reads the registry through its EXPORTED API only, so what
it validates is the behavior a consumer sees, not an internal table that might
disagree with it. It separates a MISMATCH — the registry contradicting the
schema, a defect — from a GAP — the registry being silent, which is only
coverage.

The invariant that makes it worth running is containment: a `LegalChildren` list
documented as COMPLETE may omit a schema child only while that element is also
UNREGISTERED, because `EndsUnknownSizeMaster` refuses to end a master on an
unregistered ID.

Registering a `Segment` or `Cluster` child without adding it to
`completeChildren` in the same change breaks the unknown-size boundary rule,
which is why registering an element is never a local edit.

## Coverage as a checked property

The registry names 270 of the 273 elements the official schemas declare, with
ZERO mismatches against `matroska-specification@f93ab02` /
`ebml-specification@a4b3c4a`. The three it does not name are `SilentTracks`,
`SilentTrackNumber` and `EncryptedBlock`, left out on purpose and explained at
`completeChildren`.

Remaining work is VALUE coverage — decoding helpers for the types now merely
named — not more IDs. What the schema declares and this library answers for
NOTHING about is inventoried by the checker itself: cardinality, `minver`
gating, defaults, ranges, enums, fixed lengths, `recurring`, and the WebM
profile. Each is a library capability that does not exist yet, so the check for
it can only be written after the feature is.
