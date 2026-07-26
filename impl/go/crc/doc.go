// Package crc holds the EBML CRC-32 checksum primitive: the algorithm, the
// storage layout, and the rule about which bytes a checksum covers.
//
// It is a leaf package and imports nothing from this module, deliberately. Both
// the reader side (a retained tree verifying a stored checksum) and the writer
// side (emitting one) need the same primitive, and package writer may not import
// package matroska — the architecture test in internal/archtest confines it to
// package parser. A copy of the checksum on each side is exactly the N-copies
// defect this repository has already had to repair for the stream boundary rule,
// where each copy went stale on its own schedule. One address, no copies.
//
// # Coverage
//
// The covered bytes are all the Element Data of the CRC-32 element's PARENT
// master, AS STORED, minus the CRC-32 element itself — its header and its payload
// both. The master's own header is not covered; the sibling elements' headers
// are, because they are part of the parent's data.
//
// This is the cross-language agreement, and it is written down here once instead
// of at each call site because the two halves of a disagreement are not
// symmetric: a port that includes the CRC-32 element in its own coverage, or that
// covers the parent's header, still produces a self-consistent file, and that
// file reads as corrupt here — a checksum mismatch on bytes that were never
// damaged. A mismatch is the one failure a reader cannot investigate further, so
// the definition of what is summed has to be unambiguous before any of it is
// computed.
//
// Because the checksum is computed over the data as stored, verification needs
// the bytes themselves. That is why verification in this library lives on the
// retained model in package tree and is EXPLICIT: the streaming cursor hands out
// a view valid only until the next pull and does not retain a master's payload,
// so it has nothing to sum. Nothing here verifies implicitly.
//
// # Algorithm and storage
//
// RFC 8794 section 11.3.1 specifies IEEE CRC-32, as in ISO 3309 and ITU-T V.42
// section 8.1.1.6.2, with an initial value of 0xFFFFFFFF — the polynomial and
// conditioning that hash/crc32.ChecksumIEEE computes, which is what Checksum
// calls. The value is computed on a little-endian bytestream and is STORED
// little-endian in a payload of exactly Size bytes; Encode and Decode are that
// storage and nothing else. A port that stores the four bytes the other way round
// is the same failure as a coverage disagreement, seen from a different angle.
//
// RFC 8794 also requires the CRC-32 element to be the FIRST ordered child of its
// parent master. That is a placement rule about the surrounding document, not
// about the checksum, so it is enforced where elements are ordered rather than
// here; it is stated here because a reader looking for the rule looks for it next
// to the coverage rule.
//
// # No element ID
//
// This package names no element ID. Element IDs live in package matroska, and
// keeping the CRC-32 ID out of here is precisely what lets package writer — which
// may not import matroska — share this one implementation. On the write side the
// CALLER supplies the CRC-32 element ID, the same way the caller supplies every
// other element ID to a Writer.
package crc
