package parser

import "math/bits"

// vintLen returns the total VINT length (in bytes) encoded by the leading
// byte's length-marker bit, per EBML's "count leading zero bits, +1" rule.
// A leading byte of 0x00 has no marker bit within the first 8 bits, which
// EBML defines as encoding a 9-byte VINT -- always longer than the 8-byte
// max for either element IDs or element sizes, so callers reject it via the
// same too-long path as any other over-length VINT rather than treating it
// as a distinct "invalid" case.
func vintLen(b byte) int {
	if b == 0 {
		return 9
	}
	return bits.LeadingZeros8(b) + 1
}

// parseElementID parses EBML Element ID VINT (1..4 bytes).
// The returned id includes the length marker bits (as defined by EBML).
func parseElementID(b []byte) (id uint32, n int, err error) {
	if len(b) < 1 {
		return 0, 0, NeedMoreData{MinBytes: 1}
	}
	n = vintLen(b[0])
	if n > MaxElementIDLength {
		return 0, 0, VINTLengthError{
			What: "element ID", Length: n, Max: MaxElementIDLength, Cause: ErrElementIDTooLong,
		}
	}
	if len(b) < n {
		return 0, 0, NeedMoreData{MinBytes: n - len(b)}
	}
	var v uint32
	for i := 0; i < n; i++ {
		v = (v << 8) | uint32(b[i])
	}
	return v, n, nil
}

// parseSize parses EBML size VINT (1..8 bytes).
// Unknown-size is represented as size = -1.
func parseSize(b []byte) (size int64, n int, err error) {
	if len(b) < 1 {
		return 0, 0, NeedMoreData{MinBytes: 1}
	}
	n = vintLen(b[0])
	if n > MaxElementSizeLength {
		return 0, 0, VINTLengthError{
			What: "element size", Length: n, Max: MaxElementSizeLength, Cause: ErrElementSizeTooLong,
		}
	}
	if len(b) < n {
		return 0, 0, NeedMoreData{MinBytes: n - len(b)}
	}

	valueBits := 7 * n
	var value uint64

	// First byte contributes only lower (8-n) bits (after the length marker bit).
	firstMask := byte(0xFF >> n)
	value = uint64(b[0] & firstMask)
	for i := 1; i < n; i++ {
		value = (value << 8) | uint64(b[i])
	}

	if valueBits == 0 {
		return 0, 0, Invalid{Msg: "invalid size vint value bits"}
	}
	max := uint64(1)<<uint(valueBits) - 1
	if value == max {
		return UnknownSize, n, nil
	}
	return int64(value), n, nil
}
