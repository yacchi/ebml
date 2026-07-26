# Layer boundaries

Why the repository is laid out the way it is, which package a thing belongs in,
and which edges between packages are forbidden. The one-line rules live in
[`CLAUDE.md`](../../CLAUDE.md); this note is the reasoning behind them.

## Repository layout and the module path

EVERY IMPLEMENTATION LIVES UNDER `impl/`, one directory per language, and the
repository root holds only what implementations SHARE — the corpus, the spec,
the notes — so a new language never rearranges it.

The module path FOLLOWS from that layout rather than being chosen against it:
the go tool resolves a `github.com/...` path as the first two elements being the
repository and the REST BEING THE SUBDIRECTORY, so a module at `impl/go` can
only be `github.com/yacchi/ebml/impl/go`. It was `github.com/yacchi/ebml` while
the module sat in `go/`, which no `go get` could ever have resolved.

Neither escape was taken. A vanity domain buys a shorter path with a permanent
hosting dependency, in a library whose whole claim is zero third-party requires.
Splitting the Go module into its own repository would put the corpus on one side
of a repository boundary and half its consumers on the other.

The directories are named for the LANGUAGE ALONE (`impl/go`, not
`impl/go-ebml`): inside a repository named `ebml` the suffix repeats itself, and
it would repeat in every consumer's import line forever.

Because the modules are in subdirectories, their version tags carry the
subdirectory as a prefix — `impl/go/v0.1.0` and
`impl/go/integrations/kvs/v0.1.0` — and the core is tagged FIRST, so that the
integration can require a release rather than a commit.

An integration requires the core by a PUBLISHED VERSION, never by a `replace`,
and the root `go.work` is what makes local work resolve it from the tree.
`integrations/kvs` carried `require v0.0.0` plus
`replace github.com/yacchi/ebml/impl/go => ../../` until that was found to make
the module unresolvable for everyone outside the repository: a `replace` in a
DEPENDENCY's `go.mod` is ignored, only the main module's applies, so a consumer
saw the unsatisfiable `v0.0.0` and nothing else. Waiting for a tag was never the
constraint either — a pseudo-version resolves the moment the repository is
public, which is why the pin went in before any tag existed. `internal/archtest`
cannot see this one, so CI resolves the integration WITHOUT the workspace and
fails if the pin does not stand on its own.

The repository directory is still named `ebml-reader`; that is intentional and
carries no meaning.

## What makes a package core

The CORE is `impl/go/parser`, `impl/go/matroska`, `impl/go/writer`,
`impl/go/tree`, `impl/go/stream` and `impl/go/crc`. Membership is decided by
whether a PORT MUST AGREE ON IT TO INTEROPERATE, not by how generic the code
looks. The cursor, event model, flow control, VINT behavior, element registry,
retained element model, encoder, and the byte-supply contract are that agreement
and are specified in [`spec/SPEC.md`](../../spec/SPEC.md). Ways of USING them
belong in `ext`, where another language may reasonably choose a different shape
or none at all.

`impl/go/crc` is core because WHICH BYTES a CRC-32 covers and which way round
the four are stored are agreements, not implementation details: a port that
covers the parent's header, or that includes the CRC-32 element in its own
coverage, or that stores the value big-endian, still writes a self-consistent
file, and that file reads as damaged here — a mismatch on bytes nothing ever
damaged. It imports nothing from this module because `internal/archtest`
confines `impl/go/writer` to `impl/go/parser`, so the one primitive both the
writer and `tree` need has to sit BELOW both; anywhere else forces a second
copy.

`impl/go/stream` is core for the reason its own doc gives: `io.Reader` is Go's
SPELLING of a byte source, but the contract it stands for is not Go's — keep
supplying bytes and parsing proceeds, and an exhausted source is finalized so a
document that ended mid-element is reported as truncated. A port that skipped
that last half would silently accept a truncated document, which is exactly the
kind of disagreement the contract exists to prevent.

`tree` is the core retained document model defined by EBML's tree-shaped
document; this does not change the parser sanctuary. `parser` is a StAX-shaped
reader and never imports retained document state or `tree`. The import direction
is enforced by `impl/go/internal/archtest`.

## Standard-library sensibility

Standard-library sensibility governs SPELLING, not MEMBERSHIP, and it constrains
the CONTRACT, never the MECHANISM. Membership in the core is still decided by
whether a PORT MUST AGREE ON IT TO INTEROPERATE.

Once something IS core, its API must be one the host language's standard library
could plausibly carry: nothing outside the language's own standard library (both
modules have zero third-party requires and that stays true), no configuration
object, no hook registry, no DSL, and errors, naming and lifetimes spelled the
way the host language spells them.

What a port must reproduce is the OBSERVABLE CONTRACT; how it reproduces it is
the port's own business, and a mechanism this repository uses is never itself
the requirement. Go stamps a generation into every node and panics on a stale
one because Go has no borrow checker; a Rust port spells the SAME lifetime
guarantee as `&'_ mut self` and needs neither stamp nor panic, and that is full
compliance, not a divergence. `io.Reader` is likewise Go's spelling of a byte
source, not the contract.

The one shape that is NOT free is the arity of a pull, which has its own note:
[Pull shape and node lifetime](pull-and-lifetime.md).

## `ext` is optional convenience, and every `ext` package is a leaf

Everything under `impl/go/ext/` is optional Go convenience built solely on
exported core API. If an extension needs an unavailable capability, fix the
core; do not reach into internals or add a workaround in the extension. An
extension may reasonably have a different shape, or no equivalent, in another
language.

NO `ext` PACKAGE IMPORTS ANOTHER `ext` PACKAGE, pinned by
`internal/archtest.TestExtPackagesAreLeaves`, which discovers the package list
rather than holding one (a prohibition that skips a package it has not heard of
is not a prohibition). An ext package is a way of USING the core, and a way of
using something is not a prerequisite of another way of using it.

Both edges this removed were the same mistake — a capability wearing a plainer
name: `ext/fragment` imported `ext/tags` for `Fragment.Tag`/`Tags`, each of them
`ext/tags` applied to `Target{}`, and `ext/tags` imported `ext/scope` for a
`Read(*scope.Scope)`. A convenience accessor is exactly how such an edge grows
back, which is why the rule is a TEST and not a paragraph. A consumer composes
the two, as `impl/go/integrations/kvs` does.

`ext/fragment` therefore offers NO tag accessor: a fragment's tags are
`tags.Read(frag.Segment)`, which also stops the per-call rebuild the methods hid
— five names cost five walks of the Segment where one `Set` answers all five.

A TEST may still import a sibling ext package (`go list -deps` reports the
shipped, non-test graph), and proving that `ext/tags` reads what `ext/fragment`
retains is what such a test is for.

## A cross-package claim is either compiler-checked or deleted

It is never left standing in prose. Removing the `ext/fragment` -> `ext/tags`
import while leaving `Fragment.Segment`'s doc explaining `tags.Read`,
`tags.Target{}` and why `Tag`/`Tags` went away just moved the dependency
somewhere nothing recompiles — and it was a THIRD copy of a rationale
`internal/archtest` and `CLAUDE.md` already carried, which is the same
three-copies-drift-apart shape as the stream boundary rule.

What replaced it: `var _ tags.Source = (*scope.Scope)(nil)` in `ext/tags`'s test
asserts what the `Source` doc used to merely say, and `ext/fragment`'s Example —
runnable and output-checked — shows `tags.Read(frag.Segment)` instead of a
comment describing it.

A package doc may state its own contract and describe the SHAPES it accepts in
core types; it may not assert what a sibling package documents, what design rule
a sibling follows, or which type satisfies its interface. `ext/scope` no longer
justifies its last-wins `Get` by citing `ext/tags` either: an element-agnostic
package that names a Matroska tag reader has already stopped being one.

## `ext/scope`

Element-agnostic, and it follows one master by depth regression, never by
pairing EndNodes, because `MasterNode.Skip` emits no EndNode. It retains only
directly completed children and has no configuration or lexical chaining:
neither EBML nor Matroska defines scope inheritance, so a consumer wanting two
levels runs two Trackers and states its own precedence. Only observed nodes
exist in a scope, with no configured retain policy or element allowlist;
descendants remain in retained child subtrees but are not direct-child queries.

## `ext/tags` is the only home of a tag accessor, and it names no producer

Where RFC 9559 is silent, the library states its choice in documentation rather
than leaving behavior implicit. Tag traversal and precedence rules have exactly
one implementation, in `ext/tags`: Segment-default tags are cumulative and
positionless, and repeated names are last-wins by library choice.

`Read(roots ...*tree.Element)` is the base entry: every retention path in this
library ends in a `*tree.Element`, so the retained element is the shared
currency and a fragment passes `frag.Segment`.

`ReadFrom(Source)` is the one adapter, for a producer that indexes elements BY
ID, and it exists so that naming `matroska.IDTags` stays this package's job. The
call it replaced, `Read(sc.GetAll(matroska.IDTags)...)`, had two silent failure
modes and no loud one: `IDTag` for `IDTags` yields an EMPTY view, because a root
is searched rather than counted, and `Get` for `GetAll` keeps only the LAST
`Tags` element, discarding what a live stream wrote before its `Cluster`.

`Source` is satisfied by the `GetAll` that `ext/scope` already has FOR ITS OWN
SAKE; a purpose-built method (`TagRoots`, say) would put Matroska tag knowledge
into whatever implemented it, and `ext/scope` is element-agnostic by rule, so it
could not. That distinction — an incidental method versus one invented for the
consumer — is what makes this interface admissible where the other is not.

Two entries are the ceiling and each must keep its correct name. The package
once had `Read(*scope.Scope)` beside `ReadElement`, giving the PLAINER name to
the NARROWER case, and the result was a consumer holding a Fragment reading
"from a retained Segment scope" and concluding tags were a scope feature
reachable only by running a Tracker — when `ReadElement(f.Segment)` was what
`Fragment.Tag` already called internally. Never name a special case with the
base name.

## Integrations

A layer that adapts to ONE NAMED OUTSIDE SYSTEM — a hosted service, a
specification, a container profile — is an INTEGRATION and lives in
`impl/go/integrations/<name>/` as its OWN MODULE, never in the core and never in
`ext/`.

What defines the layer is that it MAY IMPORT SEVERAL `ext` PACKAGES:
`integrations/kvs` reads fragments through `ext/fragment` and their tags through
`ext/tags`, which is exactly the composition the leaf rule forbids inside
`ext/`, so `ext/kvs` was never available. Integrations do not import each other,
for the same reason ext packages do not.

The directory is not called `services` because the first one being a service is
an accident of order: a SPECIFICATION that fixes a Matroska usage belongs here
on identical terms, and naming the layer after its first inhabitant would have
had to be undone by the second.

An integration holds the outside system's EBML/Matroska CONVENTIONS AND
VOCABULARY ONLY — which tags it writes, how it lays out documents, how its
timestamps map to a wall clock — and never that system's API or transport: bytes
are supplied by the caller, so `integrations/kvs` has no GetMedia wrapper, no
client and no AWS SDK dependency. That line answers a question a consumer asked
outright (`plans/KVS-FULL-MIGRATION-REQUIREMENTS.md`, requirement 4), which is
why it is written down rather than left to inference; moving it means amending
`impl/go/integrations/doc.go` first, so the change is a decision on the record
and not one module quietly growing a client.

Being separate modules is what keeps a dependency, a vocabulary and a release
cadence belonging to one outside system out of the core's.

## Retention

Turning a cursor node into a retained element happens in exactly ONE place,
`tree.FromNode`. It copies identity and extent only — a node is valid solely
until the next pull, so a retained element takes what it needs immediately — and
never sets `Payload`, because delivering bytes is a flow-control decision
belonging to whoever holds the node. `ext/scope` and `ext/fragment` both call
it; they each carried an identical copy before.

`ext/fragment.Assembler` is deliberately NOT built on `ext/scope.Tracker`, and a
future reader should not "fix" that. They retain different things: the assembler
builds ONE nested tree spanning Segment and Cluster, elides `SimpleBlock`
payloads into decoded blocks with `Truncated` set, retries a payload ACROSS
`Feed` calls, and never skips a master — which is why its EndNode-paired stack
is safe where a Tracker must unwind on depth. Merging them would either bloat
`Tracker` for a single caller or change `Fragment.Segment`, whose
shared-and-growing contract is documented and relied on.

`tree` provides two access modes: loose extraction (`Descendants`) ignores
containment, while strict access (`Find` and ancestry) uses exact paths. Loose
results retain their structure so the modes compose. There is no query DSL and
no second index type.

No retention path uses a per-element allowlist. Unknown elements remain
readable, and an unknown master-shaped payload can be re-parsed later.
