package crc

import "fmt"

// LengthError reports a stored CRC-32 payload whose length is not Size. RFC 8794
// fixes the payload at four bytes, so a different length is a defect in the
// document rather than a checksum that happens to disagree — and the two must not
// be confused, since a reader answers them differently.
type LengthError struct {
	Len int
}

func (e *LengthError) Error() string {
	return fmt.Sprintf("CRC-32 payload is %d bytes: RFC 8794 fixes it at %d", e.Len, Size)
}

// MismatchError reports covered bytes that do not check out against the value the
// document stores. Want is the STORED checksum, Got the one computed from the
// bytes now in hand; both are rendered in hex, because the only useful next step
// after a mismatch is comparing them against a checksum computed elsewhere, and a
// decimal rendering makes that comparison harder than it needs to be.
//
// A mismatch says nothing about where the next element begins: the extents were
// read correctly, so this is a verdict about content and never a structural
// failure.
type MismatchError struct {
	Want uint32
	Got  uint32
}

func (e *MismatchError) Error() string {
	return fmt.Sprintf("CRC-32 mismatch: stored 0x%08X, computed 0x%08X", e.Want, e.Got)
}
