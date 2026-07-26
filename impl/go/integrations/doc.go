// Package integrations is the root of this repository's per-system adaptation
// layer. It has no code of its own; the policy that governs every module below
// it is documented here.
//
// # What an integration is
//
// An outside system -- a hosted service, a specification, a container profile --
// may use EBML or Matroska in a particular way and add its own vocabulary on top:
// which tags it writes, how it lays out documents, how its timestamps map to a
// wall clock. An integration is the package that names that vocabulary and
// interprets that layout. kvs, for Amazon Kinesis Video Streams, is the first
// one.
//
// The layer exists because the first integration was written, and the naming
// records what was learned doing it: the outside system is not necessarily a
// service. A specification that fixes a Matroska usage belongs here on the same
// terms, which is why this directory is not called services.
//
// # Conventions, never API or transport
//
// An integration holds the outside system's EBML and Matroska conventions and
// nothing else. It never holds that system's API or transport: bytes are
// supplied by the caller, already obtained, and an integration has no client, no
// endpoint and no credentials. kvs therefore has no GetMedia wrapper and no AWS
// SDK dependency at all -- a consumer keeps its own API orchestration.
//
// That line is a deliberate scope decision and not an accident of what has been
// written so far. Moving it -- deciding that some integration should call the
// system it names -- means amending this rule first, in this file, so the change
// is a decision on the record rather than one module quietly growing a client.
//
// # Why not ext
//
// Each integration is its own Go module, so the core module never carries a
// dependency, a vocabulary or a release cadence that belongs to one outside
// system. Being a separate module means a consumer requires it separately:
// naming github.com/yacchi/ebml/impl/go does not make an integration resolvable.
//
// An integration may import several ext packages, and that is precisely what
// separates this layer from ext. An ext package is one way of using the core and
// is a leaf: no ext package imports another, pinned by
// internal/archtest.TestExtPackagesAreLeaves. An integration composes those ways
// -- kvs reads fragments through ext/fragment and their tags through ext/tags --
// so it sits above ext and could not live inside it without breaking that rule.
// Integrations do not import each other either, for the reason ext packages do
// not: two outside systems' conventions are not prerequisites of one another.
//
// # They are outside the cross-language contract
//
// Nothing here is part of the contract spec/SPEC.md defines, for the reason
// ext's own doc gives at more length: what a port must reproduce is the core.
// A port may carry an equivalent integration, a differently shaped one, or none.
package integrations
