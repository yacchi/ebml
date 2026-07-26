package crc

import (
	"encoding/binary"
	"hash/crc32"
)

// Size is the encoded length of a CRC-32 payload in bytes. RFC 8794 fixes it at
// four: a payload of any other length is not a CRC-32 value, which is why Decode
// refuses one rather than padding or truncating it into shape.
const Size = 4

// Checksum returns the EBML CRC-32 of data: IEEE CRC-32 with an initial value of
// 0xFFFFFFFF, per RFC 8794 section 11.3.1. The caller passes the covered bytes as
// the package doc defines them — the parent master's Element Data as stored,
// without the CRC-32 element's own header and payload — because this function
// sums what it is given and cannot tell a wrong selection from a damaged one.
//
// Empty and nil data both check as 0, which is the value the algorithm yields for
// no input at all; it is not a sentinel for "not computed".
func Checksum(data []byte) uint32 { return crc32.ChecksumIEEE(data) }

// Encode returns sum in the little-endian storage RFC 8794 requires, as exactly
// Size bytes ready to be written as a CRC-32 element's payload.
func Encode(sum uint32) []byte {
	b := make([]byte, Size)
	binary.LittleEndian.PutUint32(b, sum)
	return b
}

// Decode reads a stored CRC-32 payload. It requires len(b) == Size and reports a
// *LengthError otherwise: a shorter or longer payload is a malformed element, and
// reading four bytes out of it anyway would turn a defect in the document into a
// checksum mismatch attributed to the parent's data.
func Decode(b []byte) (uint32, error) {
	if len(b) != Size {
		return 0, &LengthError{Len: len(b)}
	}
	return binary.LittleEndian.Uint32(b), nil
}

// Verify reports whether the covered bytes still check out against the value
// stored in the document. It returns nil when Checksum(data) equals stored, and a
// *MismatchError carrying both values otherwise.
//
// A mismatch is a verdict about an element's CONTENT, not about the structure of
// the stream: the extents were read correctly, so the position of the next
// element is known and the cursor is not poisoned. A caller propagating one marks
// it with parser.NewContentError so it stays out of the structural class; that
// marking is the caller's, because this package knows nothing of the cursor.
func Verify(data []byte, stored uint32) error {
	got := Checksum(data)
	if got != stored {
		return &MismatchError{Want: stored, Got: got}
	}
	return nil
}
