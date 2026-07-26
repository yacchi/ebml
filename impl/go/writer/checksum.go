package writer

import (
	"github.com/yacchi/ebml/impl/go/crc"
	"github.com/yacchi/ebml/impl/go/parser"
)

// masterOptions collects what the MasterOptions of one StartMaster call asked
// for. It is unexported because the set of answers is the Writer's business: a
// caller states an intent (WithChecksum) and never assembles the record itself,
// so a later option can be added without breaking a call site.
type masterOptions struct {
	// checksum records that WithChecksum was applied, separately from crcID,
	// because a valid element ID has no zero-like "unset" value to test.
	checksum bool
	crcID    parser.ElementID
}

// MasterOption configures a single master element at StartMaster. It affects only
// the master it is passed to; a nested master states its own options, and inherits
// nothing.
type MasterOption func(*masterOptions)

// WithChecksum makes the master emit an EBML CRC-32 element as its FIRST child,
// covering the master's other Element Data exactly as package crc defines
// coverage: the sibling elements as stored, headers included, with the CRC-32
// element itself — its own header and payload — excluded.
//
//	w.StartMaster(matroska.IDCluster, writer.Buffered(), writer.WithChecksum(matroska.IDCRC32))
//
// crcID is a parameter rather than a constant because a Writer knows no element:
// there is no element table here and not one element ID literal, so the CRC-32
// element's ID is supplied at the call site for the same reason the ID of a Uint
// or a Leaf is. Package matroska remains the single place an ID is written down,
// and hard-coding one here would be the first crack in that.
//
// It is valid only with Buffered. The CRC-32 element has to precede the children
// it covers, so those children must still be in memory when the value is computed
// — which is what Buffered, and only Buffered, guarantees. StartMaster reports
// *ChecksumStrategyError for any other strategy, and *InvalidIDError naming crcID
// when that ID is ill-formed; both before a byte is written, so the caller may
// correct the call and continue.
//
// Emission is strictly opt-in: a master without this option gets no CRC-32
// element. Nested masters each carry their own option, and an inner master's
// CRC-32 element is part of what an outer master's checksum covers, because it is
// part of the outer master's Element Data.
func WithChecksum(crcID parser.ElementID) MasterOption {
	return func(o *masterOptions) {
		o.checksum = true
		o.crcID = crcID
	}
}

// applyMasterOptions resolves opts and rejects the combinations that cannot be
// carried out, so StartMaster can validate before its strategy switch writes
// anything. A nil option is ignored rather than panicking: it is the caller
// passing a not-taken branch, not a malformed document.
func applyMasterOptions(size SizeStrategy, opts []MasterOption) (masterOptions, error) {
	var o masterOptions
	for _, opt := range opts {
		if opt != nil {
			opt(&o)
		}
	}
	if o.checksum {
		if !ValidID(o.crcID) {
			return o, &InvalidIDError{ID: o.crcID}
		}
		if size.kind != strategyBuffered {
			return o, &ChecksumStrategyError{Strategy: size}
		}
	}
	return o, nil
}

// checksumElement returns the complete CRC-32 element for payload: its header and
// the little-endian value covering payload as it stands.
//
// The coverage rule falls out of summing the buffer BEFORE prepending: the six
// bytes this function returns are not among the bytes it hashed, which is exactly
// what RFC 8794 requires and what a port that summed the finished payload would
// get wrong while still producing a self-consistent file.
func checksumElement(crcID parser.ElementID, payload []byte) []byte {
	value := crc.Encode(crc.Checksum(payload))
	id := EncodeID(crcID)
	size := EncodeSize(int64(crc.Size))

	elem := make([]byte, 0, len(id)+len(size)+len(value))
	elem = append(elem, id...)
	elem = append(elem, size...)
	elem = append(elem, value...)
	return elem
}
