package parser

import (
	"math"
	"testing"
)

func TestDecodeUint(t *testing.T) {
	tests := []struct {
		name string
		in   []byte
		want uint64
		err  bool
	}{
		{"empty", nil, 0, false},
		{"big endian", []byte{0x01, 0x00}, 256, false},
		{"eight bytes", []byte{1, 2, 3, 4, 5, 6, 7, 8}, 0x0102030405060708, false},
		{"too long", make([]byte, 9), 0, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := DecodeUint(tt.in)
			if (err != nil) != tt.err || got != tt.want {
				t.Fatalf("DecodeUint() = %d, %v; want %d, error=%v", got, err, tt.want, tt.err)
			}
		})
	}
}

func TestDecodeInt(t *testing.T) {
	tests := []struct {
		name string
		in   []byte
		want int64
		err  bool
	}{
		{"empty", nil, 0, false},
		{"negative one", []byte{0xff}, -1, false},
		{"positive", []byte{0x00, 0x80}, 128, false},
		{"minimum one byte", []byte{0x80}, -128, false},
		{"too long", make([]byte, 9), 0, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := DecodeInt(tt.in)
			if (err != nil) != tt.err || got != tt.want {
				t.Fatalf("DecodeInt() = %d, %v; want %d, error=%v", got, err, tt.want, tt.err)
			}
		})
	}
}

func TestDecodeFloat(t *testing.T) {
	tests := []struct {
		name string
		in   []byte
		want float64
		err  bool
	}{
		{"empty", nil, 0, false},
		{"float32", []byte{0x3f, 0x80, 0x00, 0x00}, 1, false},
		{"float64", []byte{0x3f, 0xf0, 0, 0, 0, 0, 0, 0}, 1, false},
		{"invalid length", []byte{1, 2}, 0, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := DecodeFloat(tt.in)
			if (err != nil) != tt.err || (!tt.err && got != tt.want) {
				t.Fatalf("DecodeFloat() = %v, %v; want %v, error=%v", got, err, tt.want, tt.err)
			}
		})
	}
}

func TestDecodeString(t *testing.T) {
	if got := DecodeString([]byte("abc\x00\x00")); got != "abc" {
		t.Fatalf("DecodeString() = %q, want %q", got, "abc")
	}
}

func TestDecodeFloatPreservesSpecialValues(t *testing.T) {
	got, err := DecodeFloat([]byte{0x7f, 0xc0, 0, 0})
	if err != nil || !math.IsNaN(got) {
		t.Fatalf("DecodeFloat() = %v, %v; want NaN, nil", got, err)
	}
}
