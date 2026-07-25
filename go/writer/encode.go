package writer

import (
	"encoding/binary"
	"fmt"
	"math"
	"math/bits"
	"strings"
	"time"

	"github.com/yacchi/ebml/parser"
)

const (
	// MaxIDWidth is the largest number of bytes an EBML element ID VINT may
	// occupy; the cursor rejects a longer one as an over-long VINT.
	MaxIDWidth = parser.MaxElementIDLength
	// MaxSizeWidth is the largest number of bytes an EBML size VINT may occupy.
	MaxSizeWidth = parser.MaxElementSizeLength
	// MaxKnownSize is the largest payload size an EBML size VINT can express:
	// 2^56-2, one below the all-ones value an 8-byte size VINT reserves for the
	// unknown size.
	MaxKnownSize int64 = 1<<56 - 2
)

// ebmlEpoch is the EBML date epoch, 2001-01-01T00:00:00 UTC. An EBML date is a
// signed count of nanoseconds relative to it.
var ebmlEpoch = time.Date(2001, time.January, 1, 0, 0, 0, 0, time.UTC)

// idWidth returns how many bytes the ID value occupies, 0 for the invalid ID 0.
func idWidth(id parser.ElementID) int {
	switch {
	case id > 0xFFFFFF:
		return 4
	case id > 0xFFFF:
		return 3
	case id > 0xFF:
		return 2
	case id > 0:
		return 1
	default:
		return 0
	}
}

// ValidID reports whether id can be written and read back unchanged.
//
// A parser.ElementID is the ID VINT exactly as it appears on the wire, marker bits
// included, so its encoded width is not a free choice: the length marker in the
// leading byte must agree with the number of bytes the value occupies, and that
// width must be 1 to 4 bytes. 0xA3 and 0x1F43B675 satisfy this; 0x1234 does not,
// because its leading 0x12 marks a four-byte VINT while the value is two bytes
// long, so a reader would consume four bytes and see a different ID. Zero is
// invalid too: it carries no marker bit at all.
//
// This is exactly the well-formedness a round trip needs, and no more: values the
// EBML specification reserves for future use, such as an ID whose data bits are
// all ones, are well formed and are not rejected here, because the cursor reads
// them back faithfully.
func ValidID(id parser.ElementID) bool {
	w := idWidth(id)
	if w == 0 || w > MaxIDWidth {
		return false
	}
	lead := byte(id >> (8 * uint(w-1)))
	return bits.LeadingZeros8(lead)+1 == w
}

// EncodeID returns the on-wire bytes of an element ID: its significant bytes,
// big-endian, with leading zero bytes dropped, since the ID value already carries
// its length-marker bits.
//
// It does not validate: an ID that is not well formed (see ValidID) encodes to
// bytes that read back as a different element, and the ID 0 encodes to a single
// zero byte, which is not a legal ID at all. Every Writer method checks ValidID
// first and reports *InvalidIDError, so only a caller assembling headers from the
// primitives has to check for itself.
func EncodeID(id parser.ElementID) []byte {
	w := idWidth(id)
	if w == 0 {
		return []byte{0x00}
	}
	out := make([]byte, w)
	for i := w - 1; i >= 0; i-- {
		out[i] = byte(id)
		id >>= 8
	}
	return out
}

// maxSizeForWidth returns the largest payload size a size VINT of the given width
// can express: 2^(7*width)-2, since the all-ones value is the unknown-size marker.
func maxSizeForWidth(width int) int64 {
	return int64(uint64(1)<<(7*uint(width))) - 2
}

// SizeWidth returns the minimal number of bytes a size VINT needs for the payload
// size n, and reports *SizeRangeError when no size VINT can express it.
//
// It exists so a caller can find that width, or pre-check a size, without risking
// the panic EncodeSize documents. Note that the widths are not powers of the
// obvious kind: one byte carries 7 value bits, of which the all-ones pattern is
// the unknown-size marker, so a 1-byte size VINT stops at 126 and 127 already
// needs two bytes.
func SizeWidth(n int64) (int, error) {
	if n >= 0 {
		for w := 1; w <= MaxSizeWidth; w++ {
			if n <= maxSizeForWidth(w) {
				return w, nil
			}
		}
	}
	return 0, &SizeRangeError{Size: n}
}

// EncodeSize returns the minimal-width size VINT for the payload size n. The
// encoding is never the all-ones unknown-size marker, so it is never mistaken for
// one; see UnknownSizeVINT for that.
//
// It panics when n is negative or above MaxKnownSize, which is a programmer error
// rather than a stream condition: no EBML document can hold such an element.
// Callers that cannot rule it out use SizeWidth or EncodeSizeWidth, which report
// it as an error; every Writer method does so and never panics.
func EncodeSize(n int64) []byte {
	width, err := SizeWidth(n)
	if err != nil {
		panic("writer.EncodeSize: " + err.Error())
	}
	out, err := EncodeSizeWidth(n, width)
	if err != nil {
		panic("writer.EncodeSize: " + err.Error())
	}
	return out
}

// EncodeSizeWidth returns the size VINT for the payload size n in exactly width
// bytes, which may be wider than necessary.
//
// EBML explicitly permits a non-minimal size VINT — the length marker states the
// width, so a reader that honours it reads the same size either way, and reports
// the wider header through its header length. Two things need that: the Reserved
// strategy, which sets a width aside before the size is known, and a document model
// reproducing an element's original header byte for byte.
//
// It reports *SizeWidthError for a width outside 1 to MaxSizeWidth, *SizeRangeError
// for a negative n, and *SizeOverflowError when n does not fit the requested width.
func EncodeSizeWidth(n int64, width int) ([]byte, error) {
	if width < 1 || width > MaxSizeWidth {
		return nil, &SizeWidthError{Width: width}
	}
	if n < 0 {
		return nil, &SizeRangeError{Size: n}
	}
	if max := maxSizeForWidth(width); n > max {
		return nil, &SizeOverflowError{Size: n, Width: width, Max: max}
	}
	v := uint64(n) | uint64(1)<<(7*uint(width))
	out := make([]byte, width)
	for i := width - 1; i >= 0; i-- {
		out[i] = byte(v)
		v >>= 8
	}
	return out, nil
}

// UnknownSizeVINT returns the unknown-size marker: a size VINT whose value bits
// are all ones, in the 8-byte form 0x01FFFFFFFFFFFFFF that Matroska writers
// conventionally use.
//
// It is valid for master elements only; see UnknownSize for what a reader then has
// to do to find the master's end. Each call returns a fresh slice, so a caller may
// keep or modify it. Use UnknownSizeVINTWidth for the same marker in a narrower
// width.
func UnknownSizeVINT() []byte {
	out, err := UnknownSizeVINTWidth(MaxSizeWidth)
	if err != nil {
		panic("writer.UnknownSizeVINT: " + err.Error()) // unreachable: the width is a constant
	}
	return out
}

// UnknownSizeVINTWidth returns the unknown-size marker in exactly width bytes: a
// size VINT whose length marker announces that width and whose value bits are all
// ones.
//
// EBML defines the unknown size as the all-ones value of a size VINT of ANY width,
// so the single byte 0xFF declares an unknown size just as the 8-byte
// 0x01FFFFFFFFFFFFFF that UnknownSizeVINT returns does. The width is therefore
// never a semantic choice — it only decides how many bytes the header occupies —
// and it matters for one purpose: reproducing a document's original bytes, which is
// what ext/tree.Marshal does with the header length it retained. Write the 8-byte
// form for anything new.
//
// It is valid for master elements only (see UnknownSize) and reports
// *SizeWidthError for a width outside 1 to MaxSizeWidth.
func UnknownSizeVINTWidth(width int) ([]byte, error) {
	if width < 1 || width > MaxSizeWidth {
		return nil, &SizeWidthError{Width: width}
	}
	marker := uint64(1) << (7 * uint(width))
	v := marker | (marker - 1) // the length marker plus all-ones value bits
	out := make([]byte, width)
	for i := width - 1; i >= 0; i-- {
		out[i] = byte(v)
		v >>= 8
	}
	return out, nil
}

// EncodeUint returns the minimal-width big-endian payload for an EBML unsigned
// integer, and is the exact inverse of parser.DecodeUint.
//
// The result is at least one byte, so 0 encodes as a single zero byte rather than
// the empty payload EBML also allows for it. Use Writer.Leaf with a nil payload for
// that form.
func EncodeUint(v uint64) []byte {
	width := 1
	for x := v >> 8; x > 0; x >>= 8 {
		width++
	}
	out := make([]byte, width)
	for i := width - 1; i >= 0; i-- {
		out[i] = byte(v)
		v >>= 8
	}
	return out
}

// EncodeInt returns the minimal-width big-endian two's-complement payload for an
// EBML signed integer, and is the exact inverse of parser.DecodeInt.
//
// Minimal here means sign-preserving: a byte is only dropped while the value still
// decodes to the same number, so -1 is one byte (0xFF) and 128 needs two
// (0x00 0x80) even though its magnitude fits in one. The result is at least one
// byte, so 0 encodes as a single zero byte.
func EncodeInt(v int64) []byte {
	width := 1
	for width < 8 {
		lo := int64(-1) << (8*uint(width) - 1)
		hi := int64(1)<<(8*uint(width)-1) - 1
		if v >= lo && v <= hi {
			break
		}
		width++
	}
	u := uint64(v)
	out := make([]byte, width)
	for i := width - 1; i >= 0; i-- {
		out[i] = byte(u)
		u >>= 8
	}
	return out
}

// FloatSize selects the width of an EBML float payload. EBML floats are IEEE 754,
// so the only two widths are binary32 and binary64.
type FloatSize int

const (
	// Float32 selects a 4-byte IEEE 754 binary32 payload.
	Float32 FloatSize = 32
	// Float64 selects an 8-byte IEEE 754 binary64 payload.
	Float64 FloatSize = 64
)

func (s FloatSize) String() string {
	switch s {
	case Float32:
		return "float32"
	case Float64:
		return "float64"
	default:
		return fmt.Sprintf("float(%d)", int(s))
	}
}

// EncodeFloat returns the big-endian IEEE 754 payload for an EBML float in the
// requested width, and is the inverse of parser.DecodeFloat, which reads a 4-byte
// payload as binary32 and an 8-byte one as binary64.
//
// With Float32 the value is rounded to binary32 first, so the round trip yields
// float64(float32(v)) rather than v. It reports *FloatSizeError for any other
// width.
func EncodeFloat(v float64, size FloatSize) ([]byte, error) {
	switch size {
	case Float32:
		out := make([]byte, 4)
		binary.BigEndian.PutUint32(out, math.Float32bits(float32(v)))
		return out, nil
	case Float64:
		out := make([]byte, 8)
		binary.BigEndian.PutUint64(out, math.Float64bits(v))
		return out, nil
	default:
		return nil, &FloatSizeError{Size: size}
	}
}

// EncodeString returns the payload bytes of an EBML string: exactly the bytes of s,
// with no terminator and no padding. It serves both EBML string types — printable
// ASCII and UTF-8 — since the two differ only in the character repertoire the
// schema promises, not in the encoding. Writer.String and Writer.UTF8 are its two
// typed entry points.
//
// A string value may not contain a NUL byte, and one is rejected as
// *StringNULError. EBML allows a string payload to be zero-padded, so a reader
// stops at the first NUL: a value carrying one could not be read back, which is
// exactly the round trip this encoder promises. Trailing zero padding is a
// reader-side concern and is not a value's content. Use Writer.Leaf when arbitrary
// bytes, NUL included, are intended — that is a binary value, not a string.
//
// With that rejection in place it is the exact inverse of parser.DecodeString for
// every s it accepts.
func EncodeString(s string) ([]byte, error) {
	if i := strings.IndexByte(s, 0); i >= 0 {
		return nil, &StringNULError{Index: i}
	}
	return []byte(s), nil
}

// EncodeDate returns the payload of an EBML date: 8 bytes of signed nanoseconds
// relative to 2001-01-01T00:00:00 UTC, big-endian two's complement, which negative
// values place before that epoch. It is the inverse of the date decoding in
// ext/tree (Element.AsTime).
//
// The result is always 8 bytes, so the epoch itself encodes as eight zero bytes
// rather than the empty payload EBML also reads as the epoch; use Writer.Leaf with
// a nil payload for that form.
//
// The representable range is the ±292 years around the epoch that a nanosecond
// count in 64 bits allows, the same limit the decoding side has. A time outside it
// is rejected with a DateRangeError rather than silently saturated, so the function
// is the exact inverse of the date decoding for every t it accepts. Note that
// time.Time.Sub cannot be used to compute the offset: it saturates at the
// time.Duration bounds instead of reporting the overflow.
func EncodeDate(t time.Time) ([]byte, error) {
	ns, ok := nanosSinceEpoch(t)
	if !ok {
		return nil, &DateRangeError{Time: t}
	}
	out := make([]byte, 8)
	binary.BigEndian.PutUint64(out, uint64(ns))
	return out, nil
}

// nanosSinceEpoch returns t as signed nanoseconds relative to the EBML epoch,
// reporting whether the value fits in 64 bits. It works in seconds and
// nanoseconds so that the overflow is detected rather than saturated, which is
// what time.Time.Sub would do.
func nanosSinceEpoch(t time.Time) (int64, bool) {
	const (
		nsPerSec = int64(time.Second)
		maxSec   = math.MaxInt64 / nsPerSec
	)
	sec := t.Unix() - ebmlEpoch.Unix()
	ns := int64(t.Nanosecond()) - int64(ebmlEpoch.Nanosecond())
	if sec > maxSec || sec < -maxSec-1 {
		return 0, false
	}
	total := sec * nsPerSec
	if ns > 0 && total > math.MaxInt64-ns {
		return 0, false
	}
	if ns < 0 && total < math.MinInt64-ns {
		return 0, false
	}
	return total + ns, true
}
