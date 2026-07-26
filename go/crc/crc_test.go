package crc

import (
	"bytes"
	"errors"
	"testing"
)

// TestChecksumCheckValue pins the algorithm itself. 0xCBF43926 over the ASCII
// digits "123456789" is the published check value for IEEE CRC-32 (ISO 3309 /
// ITU-T V.42, initial value 0xFFFFFFFF), the variant RFC 8794 section 11.3.1
// requires. If this test ever fails, the library has silently changed which
// checksum it speaks and every document it writes is unreadable to every other
// EBML implementation.
func TestChecksumCheckValue(t *testing.T) {
	const want = 0xCBF43926
	if got := Checksum([]byte("123456789")); got != want {
		t.Fatalf("Checksum(\"123456789\") = 0x%08X, want 0x%08X", got, want)
	}
}

func TestChecksumEmpty(t *testing.T) {
	if got := Checksum(nil); got != 0 {
		t.Errorf("Checksum(nil) = 0x%08X, want 0", got)
	}
	if got := Checksum([]byte{}); got != 0 {
		t.Errorf("Checksum([]byte{}) = 0x%08X, want 0", got)
	}
}

func TestEncodeIsLittleEndian(t *testing.T) {
	want := []byte{0x26, 0x39, 0xF4, 0xCB}
	if got := Encode(0xCBF43926); !bytes.Equal(got, want) {
		t.Fatalf("Encode(0xCBF43926) = % X, want % X", got, want)
	}
	if got := Encode(0x00000001); !bytes.Equal(got, []byte{0x01, 0x00, 0x00, 0x00}) {
		t.Fatalf("Encode(1) = % X, want 01 00 00 00", got)
	}
}

func TestEncodeDecodeRoundTrip(t *testing.T) {
	for _, sum := range []uint32{0, 1, 0x000000FF, 0x12345678, 0xCBF43926, 0xFFFFFFFF} {
		b := Encode(sum)
		if len(b) != Size {
			t.Fatalf("Encode(0x%08X) produced %d bytes, want %d", sum, len(b), Size)
		}
		got, err := Decode(b)
		if err != nil {
			t.Fatalf("Decode(Encode(0x%08X)) failed: %v", sum, err)
		}
		if got != sum {
			t.Errorf("round trip of 0x%08X yielded 0x%08X", sum, got)
		}
	}
}

func TestDecodeWrongLength(t *testing.T) {
	for _, n := range []int{0, 3, 5} {
		_, err := Decode(make([]byte, n))
		var lenErr *LengthError
		if !errors.As(err, &lenErr) {
			t.Fatalf("Decode(%d bytes) error = %v, want *LengthError", n, err)
		}
		if lenErr.Len != n {
			t.Errorf("Decode(%d bytes) reported Len %d", n, lenErr.Len)
		}
	}
}

func TestVerify(t *testing.T) {
	data := []byte("123456789")
	if err := Verify(data, Checksum(data)); err != nil {
		t.Fatalf("Verify with the matching checksum failed: %v", err)
	}

	const stored = 0xDEADBEEF
	err := Verify(data, stored)
	var mismatch *MismatchError
	if !errors.As(err, &mismatch) {
		t.Fatalf("Verify with a wrong checksum returned %v, want *MismatchError", err)
	}
	if mismatch.Want != stored {
		t.Errorf("MismatchError.Want = 0x%08X, want the stored 0x%08X", mismatch.Want, uint32(stored))
	}
	if mismatch.Got != Checksum(data) {
		t.Errorf("MismatchError.Got = 0x%08X, want the computed 0x%08X", mismatch.Got, Checksum(data))
	}
}
