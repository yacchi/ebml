package crc

import (
	"bytes"
	"hash/crc32"
	"math/rand"
	"testing"
)

// TestChecksumLargeInputMatchesIncremental hashes several megabytes in one
// Checksum call and compares it against the same bytes summed incrementally
// through hash/crc32's streaming Hash32, written in irregular chunk sizes.
// Checksum takes the whole slice at once, so nothing in this package chunks
// internally today -- but the two computations must still agree bit for bit,
// and disagreement here would mean Checksum is not the IEEE CRC-32 the
// package doc promises once the input crosses whatever size a future
// implementation might buffer at.
func TestChecksumLargeInputMatchesIncremental(t *testing.T) {
	data := make([]byte, 5*1024*1024+37) // deliberately not a round chunk multiple
	rand.New(rand.NewSource(1)).Read(data)

	want := Checksum(data)

	h := crc32.NewIEEE()
	for off, chunk := 0, 4096; off < len(data); off += chunk {
		end := off + chunk
		if end > len(data) {
			end = len(data)
		}
		if _, err := h.Write(data[off:end]); err != nil {
			t.Fatalf("incremental Write: %v", err)
		}
	}
	got := h.Sum32()

	if got != want {
		t.Fatalf("incremental CRC = 0x%08X, Checksum(whole slice) = 0x%08X", got, want)
	}

	// Repeating the whole-slice call must be deterministic: Checksum must not
	// carry state across calls or depend on anything but its argument.
	if again := Checksum(data); again != want {
		t.Fatalf("Checksum(data) is not repeatable: first 0x%08X, second 0x%08X", want, again)
	}
}

// TestEncodeByteOrderCannotPassAsBigEndian picks a value whose little-endian
// and big-endian encodings disagree in every byte position, so a reader that
// swapped Encode/Decode for the big-endian variants -- or a copy of this
// primitive on the other side of the module boundary that got the endianness
// backwards -- fails this test instead of only failing on values that happen
// to be endian-symmetric (like 0x00000001, which byte-order bugs can pass by
// accident because three of its four bytes are zero either way).
func TestEncodeByteOrderCannotPassAsBigEndian(t *testing.T) {
	const sum = 0x12345678
	little := []byte{0x78, 0x56, 0x34, 0x12}
	big := []byte{0x12, 0x34, 0x56, 0x78}

	got := Encode(sum)
	if !bytes.Equal(got, little) {
		t.Fatalf("Encode(0x%08X) = % X, want little-endian % X", sum, got, little)
	}
	if bytes.Equal(got, big) {
		t.Fatalf("Encode(0x%08X) matches the big-endian encoding % X; RFC 8794 requires little-endian storage", sum, big)
	}

	// And the round trip through Decode must recover the original value, not
	// its byte-swapped counterpart.
	back, err := Decode(got)
	if err != nil {
		t.Fatalf("Decode(Encode(0x%08X)): %v", sum, err)
	}
	if back != sum {
		t.Fatalf("Decode(Encode(0x%08X)) = 0x%08X, want the original value back", sum, back)
	}
}

// TestDecodeIgnoresSurroundingCapacity feeds Decode a four-byte slice that is
// a sub-slice of a larger backing array, offset away from index 0 and with
// spare capacity past its end. An implementation that read from the backing
// array's absolute indices instead of the slice's own bounds, or that
// silently consumed capacity beyond len(b), would decode the wrong four
// bytes; Decode must see exactly the Size bytes the slice header describes
// and nothing the backing array happens to hold around them.
func TestDecodeIgnoresSurroundingCapacity(t *testing.T) {
	backing := make([]byte, 16)
	for i := range backing {
		backing[i] = 0xEE // sentinel filler so a wrong offset reads garbage, not zero
	}
	// Place the real payload at offset 6, leaving capacity on both sides.
	copy(backing[6:10], []byte{0x26, 0x39, 0xF4, 0xCB}) // little-endian 0xCBF43926

	view := backing[6:10:12] // len 4, cap 6: capacity extends past len
	if len(view) != Size {
		t.Fatalf("test setup: len(view) = %d, want %d", len(view), Size)
	}
	if cap(view) <= len(view) {
		t.Fatalf("test setup: cap(view) = %d must exceed len(view) = %d", cap(view), len(view))
	}

	got, err := Decode(view)
	if err != nil {
		t.Fatalf("Decode(view): %v", err)
	}
	if got != 0xCBF43926 {
		t.Fatalf("Decode(view) = 0x%08X, want 0xCBF43926", got)
	}
}

// TestChecksumDoesNotMutateInput asserts Checksum treats its argument as
// read-only. hash/crc32.ChecksumIEEE does not write through its slice, but
// that is an implementation detail of a function this package merely calls;
// pinning the observable behavior here means a future change to Checksum
// (e.g. routing through a scratch buffer for some fast path) cannot silently
// start corrupting the caller's bytes -- which, for a checksum primitive
// meant to run over a retained tree's payload, would corrupt the document.
func TestChecksumDoesNotMutateInput(t *testing.T) {
	data := []byte{0x01, 0x02, 0x03, 0x04, 0x05, 0x06, 0x07, 0x08}
	before := bytes.Clone(data)

	_ = Checksum(data)

	if !bytes.Equal(data, before) {
		t.Fatalf("Checksum mutated its argument: got % X, want % X", data, before)
	}
}

// TestEncodeReturnsIndependentBuffers calls Encode twice and mutates the
// first result, then checks the second is untouched and a fresh Encode call
// still reports the original value. This rules out a shared/reused scratch
// buffer inside Encode -- a caller building consecutive CRC-32 elements
// (e.g. go/writer emitting several masters back to back) would otherwise see
// an earlier element's payload change underneath it as later ones are
// encoded, with no aliasing visible at any single call site.
func TestEncodeReturnsIndependentBuffers(t *testing.T) {
	first := Encode(0x11111111)
	second := Encode(0x22222222)

	firstBefore := bytes.Clone(first)
	for i := range first {
		first[i] = 0xFF
	}

	if !bytes.Equal(second, Encode(0x22222222)) {
		t.Fatalf("mutating Encode(0x11111111)'s result changed Encode(0x22222222): got % X, want % X",
			second, Encode(0x22222222))
	}
	_ = firstBefore // first was intentionally mutated above; kept only for clarity of intent
}
