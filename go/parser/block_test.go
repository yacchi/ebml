package parser

import (
	"bytes"
	"testing"
)

func TestParseSimpleBlock(t *testing.T) {
	tests := []struct {
		name string
		in   []byte
		want *SimpleBlock
		err  bool
	}{
		{
			name: "none",
			in:   []byte{0x81, 0x00, 0x64, 0x80, 0x01, 0x02},
			want: &SimpleBlock{TrackNumber: 1, Timecode: 100, Keyframe: true, Lacing: LacingNone, Frames: [][]byte{{1, 2}}},
		},
		{
			name: "negative timecode",
			in:   []byte{0x81, 0xff, 0x9c, 0x00, 0xaa},
			want: &SimpleBlock{TrackNumber: 1, Timecode: -100, Lacing: LacingNone, Frames: [][]byte{{0xaa}}},
		},
		{
			name: "xiph with large frame",
			in:   append([]byte{0x81, 0, 0, 0x02, 0x02, 0xff, 0x2d, 0x01}, append(bytes.Repeat([]byte{1}, 300), []byte{2, 3}...)...),
			want: &SimpleBlock{TrackNumber: 1, Lacing: LacingXiph, Frames: [][]byte{bytes.Repeat([]byte{1}, 300), {2}, {3}}},
		},
		{
			name: "fixed",
			in:   []byte{0x81, 0, 0, 0x04, 0x02, 1, 2, 3, 4, 5, 6},
			want: &SimpleBlock{TrackNumber: 1, Lacing: LacingFixed, Frames: [][]byte{{1, 2}, {3, 4}, {5, 6}}},
		},
		{
			name: "fixed non divisible",
			in:   []byte{0x81, 0, 0, 0x04, 0x02, 1, 2, 3, 4},
			err:  true,
		},
		{
			name: "EBML negative delta",
			in:   []byte{0x81, 0, 0, 0x06, 0x02, 0x83, 0xbd, 0x01, 0x02, 0x03, 0x04, 0x05, 0x06},
			want: &SimpleBlock{TrackNumber: 1, Lacing: LacingEBML, Frames: [][]byte{{1, 2, 3}, {4}, {5, 6}}},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParseSimpleBlock(tt.in)
			if tt.err {
				if err == nil {
					t.Fatal("ParseSimpleBlock() returned nil error")
				}
				return
			}
			if err != nil {
				t.Fatalf("ParseSimpleBlock() error = %v", err)
			}
			if got.TrackNumber != tt.want.TrackNumber || got.Timecode != tt.want.Timecode ||
				got.Keyframe != tt.want.Keyframe || got.Invisible != tt.want.Invisible ||
				got.Discardable != tt.want.Discardable || got.Lacing != tt.want.Lacing ||
				!equalFrames(got.Frames, tt.want.Frames) {
				t.Fatalf("ParseSimpleBlock() = %#v, want %#v", got, tt.want)
			}
		})
	}
}

func TestParseSimpleBlockTruncatedStages(t *testing.T) {
	tests := [][]byte{
		nil,
		{0x81},
		{0x81, 0},
		{0x81, 0, 0},
		{0x81, 0, 0, 0x02},
		{0x81, 0, 0, 0x02, 0x01},
		{0x81, 0, 0, 0x02, 0x01, 0xff},
	}
	for _, in := range tests {
		if got, err := ParseSimpleBlock(in); err == nil || got != nil {
			t.Errorf("ParseSimpleBlock(%x) = %#v, %v; want error and nil", in, got, err)
		}
	}

}

func TestParseSimpleBlockRejectsMalformedLacing(t *testing.T) {
	tests := [][]byte{
		{0x81, 0, 0, 0x02, 0x01, 0xff},
		{0x81, 0, 0, 0x06, 0x01, 0x84},
		{0x81, 0, 0, 0x06, 0x01, 0xC0, 0x00},
		{0x81, 0, 0, 0x06, 0x02, 0x81, 0x00, 0x00},
	}
	for _, in := range tests {
		if _, err := ParseSimpleBlock(in); err == nil {
			t.Errorf("ParseSimpleBlock(%x) returned nil error", in)
		}
	}
}

func equalFrames(a, b [][]byte) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if !bytes.Equal(a[i], b[i]) {
			return false
		}
	}
	return true
}
