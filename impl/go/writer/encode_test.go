package writer_test

import (
	"bytes"
	"errors"
	"math"
	"testing"
	"time"

	"github.com/yacchi/ebml/impl/go/parser"
	"github.com/yacchi/ebml/impl/go/writer"
)

// testEpoch is the EBML date epoch, restated here so the test does not take the
// implementation's word for it.
var testEpoch = time.Date(2001, time.January, 1, 0, 0, 0, 0, time.UTC)

func TestValidID(t *testing.T) {
	valid := []parser.ElementID{
		0x80, 0xA3, 0xFE, 0xFF, // one byte
		0x4000, 0x4286, 0x7FFF, // two bytes
		0x200000, 0x3F0001, 0x3FFFFF, // three bytes
		0x10000000, 0x1A45DFA3, 0x1FFFFFFF, // four bytes
	}
	for _, id := range valid {
		if !writer.ValidID(id) {
			t.Errorf("ValidID(0x%X) = false, want true", uint32(id))
		}
	}

	invalid := []parser.ElementID{
		0x00,       // no length marker at all
		0x01,       // marks a nine-byte VINT
		0x40,       // marks two bytes, one byte of value
		0x1234,     // marks four bytes, two bytes of value
		0x20000000, // marks three bytes, four bytes of value
		0x7F0000,   // marks two bytes, three bytes of value
	}
	for _, id := range invalid {
		if writer.ValidID(id) {
			t.Errorf("ValidID(0x%X) = true, want false", uint32(id))
		}
	}
}

func TestEncodeID(t *testing.T) {
	cases := []struct {
		id   parser.ElementID
		want []byte
	}{
		{0xA3, []byte{0xA3}},
		{0x80, []byte{0x80}},
		{0x4286, []byte{0x42, 0x86}},
		{0x3F0001, []byte{0x3F, 0x00, 0x01}},
		{0x1A45DFA3, []byte{0x1A, 0x45, 0xDF, 0xA3}},
		{0x1F43B675, []byte{0x1F, 0x43, 0xB6, 0x75}},
	}
	for _, c := range cases {
		if got := writer.EncodeID(c.id); !bytes.Equal(got, c.want) {
			t.Errorf("EncodeID(0x%X) = % X, want % X", uint32(c.id), got, c.want)
		}
	}
}

// sizeCase is one size-VINT width boundary. Every width's largest value is one
// below the all-ones pattern the unknown size reserves, so 126 is the last size a
// one-byte VINT can carry and 127 already needs two.
type sizeCase struct {
	n    int64
	want []byte
}

var sizeCases = []sizeCase{
	{0, []byte{0x80}},
	{1, []byte{0x81}},
	{126, []byte{0xFE}},                                                 // last one-byte size
	{127, []byte{0x40, 0x7F}},                                           // 0x7F cannot be one byte: 0xFF is the unknown size
	{128, []byte{0x40, 0x80}},                                           // 0x80
	{1<<14 - 2, []byte{0x7F, 0xFE}},                                     // last two-byte size
	{1<<14 - 1, []byte{0x20, 0x3F, 0xFF}},                               // first three-byte size
	{1<<21 - 2, []byte{0x3F, 0xFF, 0xFE}},                               // last three-byte size
	{1<<21 - 1, []byte{0x10, 0x1F, 0xFF, 0xFF}},                         // first four-byte size
	{1<<28 - 2, []byte{0x1F, 0xFF, 0xFF, 0xFE}},                         // last four-byte size
	{1<<28 - 1, []byte{0x08, 0x0F, 0xFF, 0xFF, 0xFF}},                   // first five-byte size
	{1<<56 - 2, []byte{0x01, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFE}}, // MaxKnownSize
}

func TestEncodeSizeMinimalWidths(t *testing.T) {
	for _, c := range sizeCases {
		got := writer.EncodeSize(c.n)
		if !bytes.Equal(got, c.want) {
			t.Errorf("EncodeSize(%d) = % X, want % X", c.n, got, c.want)
		}
		width, err := writer.SizeWidth(c.n)
		if err != nil {
			t.Errorf("SizeWidth(%d) error = %v", c.n, err)
		} else if width != len(c.want) {
			t.Errorf("SizeWidth(%d) = %d, want %d", c.n, width, len(c.want))
		}
	}
	if writer.MaxKnownSize != 1<<56-2 {
		t.Errorf("MaxKnownSize = %d, want %d", writer.MaxKnownSize, int64(1<<56-2))
	}
}

// TestSizeVINTsReadBackByCursor uses the cursor itself as the oracle: the size a
// Peek reports for a header the writer produced must be the size that was asked
// for, at every width including the eight-byte maximum, and the header length must
// be the encoded width.
func TestSizeVINTsReadBackByCursor(t *testing.T) {
	for _, c := range sizeCases {
		header := append(writer.EncodeID(idRoot), writer.EncodeSize(c.n)...)
		p := parser.New(classify)
		p.Feed(header)
		h, err := p.Peek()
		if err != nil {
			t.Fatalf("Peek on a header declaring size %d: %v", c.n, err)
		}
		if h.Size != c.n {
			t.Errorf("cursor read size %d, want %d", h.Size, c.n)
		}
		if want := 4 + len(c.want); h.HeaderLen != want {
			t.Errorf("cursor read header length %d for size %d, want %d", h.HeaderLen, c.n, want)
		}
	}
}

func TestEncodeSizeWidthNonMinimal(t *testing.T) {
	// A non-minimal size VINT is legal EBML: the marker states the width, so the
	// cursor reads the same size and reports the wider header.
	for width := 1; width <= 8; width++ {
		got, err := writer.EncodeSizeWidth(3, width)
		if err != nil {
			t.Fatalf("EncodeSizeWidth(3, %d) error = %v", width, err)
		}
		if len(got) != width {
			t.Fatalf("EncodeSizeWidth(3, %d) = % X, want %d bytes", width, got, width)
		}
		header := append(writer.EncodeID(idRoot), got...)
		p := parser.New(classify)
		p.Feed(header)
		h, err := p.Peek()
		if err != nil {
			t.Fatalf("Peek on a %d-byte size VINT: %v", width, err)
		}
		if h.Size != 3 {
			t.Errorf("cursor read size %d from % X, want 3", h.Size, got)
		}
		if want := 4 + width; h.HeaderLen != want {
			t.Errorf("cursor read header length %d, want %d", h.HeaderLen, want)
		}
	}
	if got, err := writer.EncodeSizeWidth(1, 4); err != nil || !bytes.Equal(got, []byte{0x10, 0x00, 0x00, 0x01}) {
		t.Errorf("EncodeSizeWidth(1, 4) = % X, %v; want 10 00 00 01", got, err)
	}
}

func TestEncodeSizeWidthErrors(t *testing.T) {
	for _, width := range []int{-1, 0, 9, 100} {
		_, err := writer.EncodeSizeWidth(1, width)
		var we *writer.SizeWidthError
		if !errors.As(err, &we) || !errors.Is(err, writer.ErrSizeWidth) {
			t.Errorf("EncodeSizeWidth(1, %d) error = %v, want *SizeWidthError", width, err)
		}
	}

	_, err := writer.EncodeSizeWidth(-1, 4)
	var re *writer.SizeRangeError
	if !errors.As(err, &re) || !errors.Is(err, writer.ErrSizeRange) {
		t.Errorf("EncodeSizeWidth(-1, 4) error = %v, want *SizeRangeError", err)
	}

	_, err = writer.EncodeSizeWidth(127, 1)
	var oe *writer.SizeOverflowError
	if !errors.As(err, &oe) || !errors.Is(err, writer.ErrSizeOverflow) {
		t.Fatalf("EncodeSizeWidth(127, 1) error = %v, want *SizeOverflowError", err)
	}
	if oe.Size != 127 || oe.Width != 1 || oe.Max != 126 {
		t.Errorf("SizeOverflowError = %+v, want size 127, width 1, max 126", *oe)
	}

	if _, err := writer.SizeWidth(writer.MaxKnownSize + 1); !errors.Is(err, writer.ErrSizeRange) {
		t.Errorf("SizeWidth(MaxKnownSize+1) error = %v, want ErrSizeRange", err)
	}
	if _, err := writer.SizeWidth(-1); !errors.Is(err, writer.ErrSizeRange) {
		t.Errorf("SizeWidth(-1) error = %v, want ErrSizeRange", err)
	}
}

// TestEncodeSizePanicsOutOfRange documents the one place this package panics: the
// convenience encoder with a size no EBML document could hold. Every Writer method
// goes through the error-returning form instead.
func TestEncodeSizePanicsOutOfRange(t *testing.T) {
	for _, n := range []int64{-1, writer.MaxKnownSize + 1} {
		func() {
			defer func() {
				if recover() == nil {
					t.Errorf("EncodeSize(%d) did not panic", n)
				}
			}()
			writer.EncodeSize(n)
		}()
	}
}

func TestUnknownSizeVINT(t *testing.T) {
	want := []byte{0x01, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF}
	got := writer.UnknownSizeVINT()
	if !bytes.Equal(got, want) {
		t.Fatalf("UnknownSizeVINT() = % X, want % X", got, want)
	}
	got[0] = 0x00 // each call must hand out a fresh slice
	if again := writer.UnknownSizeVINT(); !bytes.Equal(again, want) {
		t.Errorf("UnknownSizeVINT() after mutating an earlier result = % X, want % X", again, want)
	}

	header := append(writer.EncodeID(idRoot), want...)
	p := parser.New(classify)
	p.Feed(header)
	h, err := p.Peek()
	if err != nil {
		t.Fatalf("Peek on the unknown-size marker: %v", err)
	}
	if h.Size != parser.UnknownSize {
		t.Errorf("cursor read size %d, want parser.UnknownSize", h.Size)
	}
}

// TestUnknownSizeVINTWidth: the all-ones marker exists in every VINT width, the
// 8-byte one is what UnknownSizeVINT returns, and the cursor reads each of them as
// an unknown size -- the property that lets a document model reproduce a header
// whose marker is narrower than the conventional 8 bytes.
func TestUnknownSizeVINTWidth(t *testing.T) {
	want := map[int][]byte{
		1: {0xFF},
		2: {0x7F, 0xFF},
		3: {0x3F, 0xFF, 0xFF},
		8: {0x01, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF},
	}
	for width := 1; width <= writer.MaxSizeWidth; width++ {
		got, err := writer.UnknownSizeVINTWidth(width)
		if err != nil {
			t.Fatalf("UnknownSizeVINTWidth(%d): %v", width, err)
		}
		if len(got) != width {
			t.Errorf("UnknownSizeVINTWidth(%d) is %d bytes", width, len(got))
		}
		if exp, ok := want[width]; ok && !bytes.Equal(got, exp) {
			t.Errorf("UnknownSizeVINTWidth(%d) = % X, want % X", width, got, exp)
		}

		p := parser.New(classify)
		p.Feed(append(writer.EncodeID(idRoot), got...))
		h, err := p.Peek()
		if err != nil {
			t.Fatalf("Peek on a %d-byte unknown-size marker: %v", width, err)
		}
		if h.Size != parser.UnknownSize {
			t.Errorf("cursor read size %d for a %d-byte marker, want parser.UnknownSize", h.Size, width)
		}
		if h.HeaderLen != len(writer.EncodeID(idRoot))+width {
			t.Errorf("cursor read header length %d for a %d-byte marker", h.HeaderLen, width)
		}
	}

	if got, err := writer.UnknownSizeVINTWidth(writer.MaxSizeWidth); err != nil ||
		!bytes.Equal(got, writer.UnknownSizeVINT()) {
		t.Errorf("UnknownSizeVINTWidth(%d) = % X, %v; want UnknownSizeVINT()", writer.MaxSizeWidth, got, err)
	}
	for _, width := range []int{0, -1, writer.MaxSizeWidth + 1} {
		got, err := writer.UnknownSizeVINTWidth(width)
		if got != nil || !errors.Is(err, writer.ErrSizeWidth) {
			t.Errorf("UnknownSizeVINTWidth(%d) = % X, %v; want ErrSizeWidth", width, got, err)
		}
	}
}

func TestEncodeUintInverse(t *testing.T) {
	cases := []struct {
		v     uint64
		width int
	}{
		{0, 1}, {1, 1}, {0x7F, 1}, {0x80, 1}, {0xFF, 1},
		{0x100, 2}, {0xFFFF, 2}, {0x10000, 3},
		{0xFFFFFFFF, 4}, {0x100000000, 5},
		{math.MaxUint64, 8},
	}
	for _, c := range cases {
		got := writer.EncodeUint(c.v)
		if len(got) != c.width {
			t.Errorf("EncodeUint(%d) = % X (%d bytes), want %d bytes", c.v, got, len(got), c.width)
		}
		back, err := parser.DecodeUint(got)
		if err != nil || back != c.v {
			t.Errorf("DecodeUint(EncodeUint(%d)) = %d, %v", c.v, back, err)
		}
	}
}

func TestEncodeIntInverse(t *testing.T) {
	cases := []struct {
		v     int64
		width int
		want  []byte
	}{
		{0, 1, []byte{0x00}},
		{1, 1, []byte{0x01}},
		{-1, 1, []byte{0xFF}},
		{127, 1, []byte{0x7F}},
		{-128, 1, []byte{0x80}},
		{128, 2, []byte{0x00, 0x80}},
		{-129, 2, []byte{0xFF, 0x7F}},
		{-32768, 2, []byte{0x80, 0x00}},
		{math.MaxInt64, 8, []byte{0x7F, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF}},
		{math.MinInt64, 8, []byte{0x80, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00}},
	}
	for _, c := range cases {
		got := writer.EncodeInt(c.v)
		if len(got) != c.width || !bytes.Equal(got, c.want) {
			t.Errorf("EncodeInt(%d) = % X, want % X", c.v, got, c.want)
		}
		back, err := parser.DecodeInt(got)
		if err != nil || back != c.v {
			t.Errorf("DecodeInt(EncodeInt(%d)) = %d, %v", c.v, back, err)
		}
	}
}

func TestEncodeFloatInverse(t *testing.T) {
	values := []float64{0, 1, -1, 0.5, -0.5, 1.5, 3.4028234663852886e+38, -1e-45, 1e300, -1e300}
	for _, v := range values {
		b64, err := writer.EncodeFloat(v, writer.Float64)
		if err != nil {
			t.Fatalf("EncodeFloat(%v, Float64) error = %v", v, err)
		}
		if len(b64) != 8 {
			t.Errorf("EncodeFloat(%v, Float64) = % X, want 8 bytes", v, b64)
		}
		back, err := parser.DecodeFloat(b64)
		if err != nil || back != v {
			t.Errorf("DecodeFloat(EncodeFloat(%v, Float64)) = %v, %v", v, back, err)
		}

		b32, err := writer.EncodeFloat(v, writer.Float32)
		if err != nil {
			t.Fatalf("EncodeFloat(%v, Float32) error = %v", v, err)
		}
		if len(b32) != 4 {
			t.Errorf("EncodeFloat(%v, Float32) = % X, want 4 bytes", v, b32)
		}
		// Float32 rounds first, so the inverse property is over the rounded value.
		back, err = parser.DecodeFloat(b32)
		if want := float64(float32(v)); err != nil || back != want {
			t.Errorf("DecodeFloat(EncodeFloat(%v, Float32)) = %v, %v; want %v", v, back, err, want)
		}
	}

	_, err := writer.EncodeFloat(1, writer.FloatSize(16))
	var fe *writer.FloatSizeError
	if !errors.As(err, &fe) || !errors.Is(err, writer.ErrFloatSize) {
		t.Errorf("EncodeFloat(1, 16) error = %v, want *FloatSizeError", err)
	}
	if got := writer.Float32.String(); got != "float32" {
		t.Errorf("Float32.String() = %q, want %q", got, "float32")
	}
}

// stringValues are the values the string encoders must round-trip exactly. The
// multi-byte one is written with escapes on purpose and covers a 2-, a 3- and a
// 4-byte UTF-8 sequence.
var stringValues = []string{
	"", "a", "matroska", "A_PCM/INT/LIT", "caf\u00e9 \u20ac \U0001F3AC", "0.5 1.25",
	"trailing space ", "\x01\x02 low bytes", "\x7F",
}

// TestEncodeStringInverse asserts the exact-inverse property over a table: every
// accepted value decodes back to itself, byte for byte, through both the primitive
// and the two typed Writer methods.
func TestEncodeStringInverse(t *testing.T) {
	for _, s := range stringValues {
		got, err := writer.EncodeString(s)
		if err != nil {
			t.Errorf("EncodeString(%q) error = %v, want nil", s, err)
			continue
		}
		if string(got) != s {
			t.Errorf("EncodeString(%q) = %q, want the bytes of s", s, got)
		}
		if back := parser.DecodeString(got); back != s {
			t.Errorf("DecodeString(EncodeString(%q)) = %q, want the value unchanged", s, back)
		}

		// The same property through the Writer, for both string types: the leaf's
		// payload is the value and decodes back to it.
		for _, c := range []struct {
			method string
			write  func(*writer.Writer) error
		}{
			{"String", func(w *writer.Writer) error { return w.String(0x81, s) }},
			{"UTF8", func(w *writer.Writer) error { return w.UTF8(0x81, s) }},
		} {
			var buf bytes.Buffer
			w := writer.New(&buf)
			if err := c.write(w); err != nil {
				t.Errorf("Writer.%s(%q) error = %v, want nil", c.method, s, err)
				continue
			}
			if err := w.Close(); err != nil {
				t.Fatalf("Close: %v", err)
			}
			payload := buf.Bytes()[2:] // 1-byte ID, 1-byte size: every value here is short
			if back := parser.DecodeString(payload); back != s {
				t.Errorf("DecodeString(Writer.%s(%q) payload) = %q, want the value unchanged", c.method, s, back)
			}
		}
	}
}

// TestEncodeStringRejectsNUL pins the rule that makes the inverse exact: a NUL byte
// inside a string value is refused, because a reader stops at the first NUL and the
// value could not come back unchanged.
func TestEncodeStringRejectsNUL(t *testing.T) {
	withNUL := []struct {
		s     string
		index int
	}{
		{"a\x00b", 1},
		{"\x00", 0},
		{"trailing\x00", 8},
		{"caf\u00e9\x00\u20ac", 5},
	}
	for _, c := range withNUL {
		got, err := writer.EncodeString(c.s)
		var ne *writer.StringNULError
		if !errors.As(err, &ne) || !errors.Is(err, writer.ErrStringNUL) {
			t.Errorf("EncodeString(%q) error = %v, want *StringNULError", c.s, err)
			continue
		}
		if ne.Index != c.index {
			t.Errorf("EncodeString(%q) NUL index = %d, want %d", c.s, ne.Index, c.index)
		}
		if got != nil {
			t.Errorf("EncodeString(%q) = %q, want no payload alongside the error", c.s, got)
		}
		if ne.Error() == "" {
			t.Error("StringNULError.Error() is empty")
		}

		// Both typed Writer methods surface it, and write nothing at all.
		for _, tc := range []struct {
			method string
			write  func(*writer.Writer) error
		}{
			{"String", func(w *writer.Writer) error { return w.String(0x81, c.s) }},
			{"UTF8", func(w *writer.Writer) error { return w.UTF8(0x81, c.s) }},
		} {
			var buf bytes.Buffer
			w := writer.New(&buf)
			err := tc.write(w)
			if !errors.As(err, &ne) || !errors.Is(err, writer.ErrStringNUL) {
				t.Errorf("Writer.%s(%q) error = %v, want *StringNULError", tc.method, c.s, err)
			}
			if buf.Len() != 0 {
				t.Errorf("Writer.%s(%q) wrote % X, want nothing", tc.method, c.s, buf.Bytes())
			}
			// The rejection is a validation error, so the Writer stays usable.
			if err := tc.write(w); !errors.Is(err, writer.ErrStringNUL) {
				t.Errorf("Writer.%s: a rejected string poisoned the Writer: %v", tc.method, err)
			}
			if err := w.String(0x81, "ok"); err != nil {
				t.Errorf("Writer.%s: a good string after a rejected one = %v", tc.method, err)
			}
		}
	}

	// A value whose only zero bytes would be trailing padding is still a NUL in the
	// value, and is refused: padding is the reader's business, not the value's.
	if _, err := writer.EncodeString("abc\x00\x00"); !errors.Is(err, writer.ErrStringNUL) {
		t.Errorf("EncodeString of a zero-padded value = %v, want ErrStringNUL", err)
	}
	// Arbitrary bytes remain writable as a binary value.
	var buf bytes.Buffer
	w := writer.New(&buf)
	if err := w.Binary(0x81, []byte("a\x00b")); err != nil {
		t.Errorf("Binary with a NUL = %v, want nil (a binary value is opaque)", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if want := []byte{0x81, 0x83, 'a', 0x00, 'b'}; !bytes.Equal(buf.Bytes(), want) {
		t.Errorf("Binary with a NUL = % X, want % X", buf.Bytes(), want)
	}
}

func TestEncodeDateInverse(t *testing.T) {
	cases := []struct {
		name string
		t    time.Time
	}{
		{"the 2001 epoch", testEpoch},
		{"one nanosecond after the epoch", testEpoch.Add(1)},
		{"a date before the epoch", time.Date(1999, time.December, 31, 23, 59, 59, 0, time.UTC)},
		{"one nanosecond before the epoch", testEpoch.Add(-1)},
		{"a recent date", time.Date(2026, time.July, 25, 12, 34, 56, 789000000, time.UTC)},
	}
	for _, c := range cases {
		got, err := writer.EncodeDate(c.t)
		if err != nil {
			t.Fatalf("%s: EncodeDate error = %v", c.name, err)
		}
		if len(got) != 8 {
			t.Fatalf("%s: EncodeDate = % X, want 8 bytes", c.name, got)
		}
		ns, err := parser.DecodeInt(got)
		if err != nil {
			t.Fatalf("%s: DecodeInt = %v", c.name, err)
		}
		// This is exactly how tree decodes a date.
		if back := testEpoch.Add(time.Duration(ns)); !back.Equal(c.t) {
			t.Errorf("%s: date round trip = %v, want %v", c.name, back, c.t)
		}
		if c.t.Before(testEpoch) && ns >= 0 {
			t.Errorf("%s: nanoseconds = %d, want a negative count before the epoch", c.name, ns)
		}
	}
	if got, err := writer.EncodeDate(testEpoch); err != nil || !bytes.Equal(got, make([]byte, 8)) {
		t.Errorf("EncodeDate(epoch) = % X, %v; want eight zero bytes", got, err)
	}
}

// TestEncodeDateRejectsOutOfRange pins the reason EncodeDate reports an error at
// all: the payload is signed nanoseconds since 2001, so 64 bits reach only about
// ±292 years. time.Time.Sub saturates instead of overflowing, which would encode a
// far date as a nearer one and read back as a different instant, so a value that
// does not fit is refused.
func TestEncodeDateRejectsOutOfRange(t *testing.T) {
	for _, c := range []struct {
		name string
		t    time.Time
	}{
		{"far future", time.Date(2501, time.January, 1, 0, 0, 0, 0, time.UTC)},
		{"far past", time.Date(1500, time.January, 1, 0, 0, 0, 0, time.UTC)},
		{"maximum representable time", time.Unix(1<<62, 0).UTC()},
	} {
		got, err := writer.EncodeDate(c.t)
		if err == nil {
			t.Fatalf("%s: EncodeDate = % X, want an error", c.name, got)
		}
		if got != nil {
			t.Errorf("%s: EncodeDate returned % X alongside the error, want nil", c.name, got)
		}
		if !errors.Is(err, writer.ErrDateRange) {
			t.Errorf("%s: error = %v, want ErrDateRange", c.name, err)
		}
		var rangeErr *writer.DateRangeError
		if !errors.As(err, &rangeErr) {
			t.Fatalf("%s: error = %T, want *writer.DateRangeError", c.name, err)
		}
		if !rangeErr.Time.Equal(c.t) {
			t.Errorf("%s: error carries %v, want %v", c.name, rangeErr.Time, c.t)
		}
	}

	// The boundary case: a date the encoder accepts must still round trip, so the
	// rejection cannot be an over-eager range check.
	edge := testEpoch.Add(time.Duration(math.MaxInt64))
	payload, err := writer.EncodeDate(edge)
	if err != nil {
		t.Fatalf("EncodeDate at the maximum offset: %v", err)
	}
	ns, err := parser.DecodeInt(payload)
	if err != nil {
		t.Fatalf("DecodeInt: %v", err)
	}
	if ns != math.MaxInt64 {
		t.Errorf("nanoseconds = %d, want %d", ns, int64(math.MaxInt64))
	}
}
