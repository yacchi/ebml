// Package ext is the root of this module's optional convenience layer. It has no
// code of its own; the policy that governs every package below it is documented
// here.
//
// # What lives under ext
//
// The packages under ext/ are scope, tags and fragment. They are conveniences
// for common consumption shapes -- scope tracking, tag views and per-Cluster
// fragment assembly -- built exclusively on the exported API of the core
// packages: parser, matroska, writer, tree and stream.
// They add no parsing of their own: whatever an ext package does, a consumer
// could have written with the same public core calls. That is the point of the
// layer. It is a standing proof that the core is sufficient, and it is where
// convenience is allowed to be opinionated so the core does not have to be.
//
// # They are outside the cross-language contract
//
// The contract that spec/SPEC.md defines -- the cursor, its event model and its
// flow control, the element registry, the retained element model, and the
// byte-supply contract a source-owning driver honours -- is what other-language ports
// implement, event for event. Nothing under ext/ is part of it. These packages
// may change shape, be renamed, or be removed independently of the specified
// core, and a port in another language is not expected to mirror them: an
// idiomatic Python or TypeScript consumer will want different conveniences, and
// should build them on that language's core in the same way.
//
// So conformance is judged against the core alone. An ext package matching or
// not matching a port proves nothing about either.
//
// # The inverse rule
//
// If an ext package needs something the core does not expose, that is a bug in
// the core, to be fixed in the core. It is never to be worked around inside ext
// by reaching for unexported state, re-implementing cursor logic, or copying
// element knowledge out of matroska. An ext package that cannot be written
// against the exported core API is reporting a real gap in the specified
// contract, and closing that gap benefits every port; hiding it inside a
// convenience package benefits only Go.
package ext
