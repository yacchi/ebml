// Package parser is the streaming EBML engine: it turns bytes into element
// events and nothing else. It implements RFC 8794 -- VINT decoding, element
// headers, extents, unknown sizes -- and holds NO element knowledge of its own.
// Not one element ID literal appears in its source. Which IDs exist, what they
// are called and what their payloads mean live in package matroska, and reach
// the engine only through the KindClassifier a caller supplies, which is a
// required argument rather than an option because without it the engine cannot
// tell a master from a leaf.
//
// # Two ways in, for two different consumers
//
// Cursor is the reading surface: bytes are pushed in with Feed and one event is
// pulled out at a time with Next, so the CONSUMER owns the read loop and keeps
// its state in local variables. Because the engine does not own the input, Next
// reports NeedMoreData when the next event needs bytes that have not arrived --
// that is flow control, not failure, and answering it is the caller's job.
//
// A consumer that would rather hand over the source uses package stream instead.
// It owns an io.Reader, answers NeedMoreData itself, and reports only nodes,
// io.EOF, or a real failure. It is built ON this package, not as an alternative
// to it: the split exists so that a consumer feeding bytes from a socket
// callback, a test that controls chunking exactly, and a consumer reading a file
// can all be served without any of them paying for the others' shape.
//
// Parser, the operation-level engine beneath Cursor, stays exported for a
// consumer that needs to drive each read operation individually. The golden
// trace tool does; most consumers should not.
//
// # What it never does
//
// This package retains no document. An event is valid until the next pull, and a
// leaf's payload is a VIEW of the engine's buffer rather than a copy, so bulk
// data -- PCM in a SimpleBlock -- is never held merely to be looked at. Anything
// a consumer keeps, it copies. The retained model lives in package tree, and the
// direction of that dependency is the guarantee: tree imports this package and
// this package imports nothing, which internal/archtest enforces.
package parser
