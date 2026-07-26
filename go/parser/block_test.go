package parser

import (
	"bytes"
	"errors"
	"io"
	"io/fs"
	"path/filepath"
	"strings"
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
			// Every input here is canonically encoded -- minimal length VINTs, no
			// reserved flag bit -- so Append must reproduce it byte for byte, one
			// case per lacing type.
			enc, err := got.Append(nil)
			if err != nil {
				t.Fatalf("Append() error = %v", err)
			}
			if !bytes.Equal(enc, tt.in) {
				t.Fatalf("Append() = %x, want the input %x", enc, tt.in)
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

// TestSimpleBlockAppendMatchesCorpusBytes is the byte-exactness conformance check
// for the encoder: every SimpleBlock in the committed fixture corpus is decoded and
// re-encoded, and the result must equal the original payload byte for byte. It is
// the block-level counterpart of the parse-then-marshal round trip package
// tree runs over the same fixtures.
func TestSimpleBlockAppendMatchesCorpusBytes(t *testing.T) {
	var paths []string
	root := filepath.Join("..", "..", "fixtures")
	if err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() && strings.HasSuffix(path, ".ebml.hex") {
			paths = append(paths, path)
		}
		return nil
	}); err != nil {
		t.Fatalf("walk %s: %v", root, err)
	}
	if len(paths) == 0 {
		t.Fatalf("no fixtures found under %s", root)
	}

	blocks := 0
	for _, path := range paths {
		raw, err := loadFuzzHex(path)
		if err != nil {
			t.Fatalf("load %s: %v", path, err)
		}
		// Every SimpleBlock sits inside a known-size Cluster and is complete in the
		// bytes fed, so all of them are reported before the cursor asks for more
		// input for the trailing unknown-size Segment.
		c := NewCursor(testKindClassifier, WithBoundary(func(open, next ElementID) bool {
			return next == 0x1A45DFA3 || next == 0x18538067
		}))
		c.Feed(raw)
		for {
			node, err := c.Next()
			if err != nil {
				var needMore NeedMoreData
				if errors.As(err, &needMore) || errors.Is(err, io.EOF) {
					break
				}
				t.Fatalf("%s: Next: %v", path, err)
			}
			leaf, ok := node.(*LeafNode)
			if !ok || leaf.ID() != 0xA3 { // SimpleBlock
				continue
			}
			payload, err := leaf.Payload()
			if err != nil {
				t.Fatalf("%s: Payload at offset %d: %v", path, leaf.Offset(), err)
			}
			want := bytes.Clone(payload)
			block, err := ParseSimpleBlock(want)
			if err != nil {
				t.Fatalf("%s: ParseSimpleBlock at offset %d: %v", path, leaf.Offset(), err)
			}
			got, err := block.Append(nil)
			if err != nil {
				t.Fatalf("%s: Append of the block at offset %d: %v", path, leaf.Offset(), err)
			}
			if !bytes.Equal(got, want) {
				t.Fatalf("%s: block at offset %d re-encoded as %x, want the original %x",
					path, leaf.Offset(), got, want)
			}
			blocks++
		}
	}
	if blocks == 0 {
		t.Fatal("no SimpleBlock found in the corpus: the round trip proved nothing")
	}
	t.Logf("re-encoded %d SimpleBlocks from %d fixtures byte for byte", blocks, len(paths))
}

// TestSimpleBlockAppendSemanticRoundTrip is the guarantee stated for a LACED block:
// Append writes the canonical encoding of the value and ParseSimpleBlock reads back
// an EQUAL block. Each lacing type is covered, including an EBML lacing whose second
// size delta is negative and sizes wide enough to need a multi-byte VINT.
func TestSimpleBlockAppendSemanticRoundTrip(t *testing.T) {
	big := bytes.Repeat([]byte{0x5a}, 300)
	tests := []struct {
		name string
		want *SimpleBlock
	}{
		{
			name: "none",
			want: &SimpleBlock{TrackNumber: 1, Timecode: -3, Keyframe: true, Frames: [][]byte{{1, 2, 3}}},
		},
		{
			name: "none empty frame",
			want: &SimpleBlock{TrackNumber: 2, Frames: [][]byte{{}}},
		},
		{
			name: "none wide track number",
			want: &SimpleBlock{TrackNumber: 0x3FFF, Frames: [][]byte{{9}}},
		},
		{
			name: "xiph",
			want: &SimpleBlock{TrackNumber: 1, Lacing: LacingXiph, Invisible: true,
				Frames: [][]byte{big, {1}, {2, 3}}},
		},
		{
			name: "xiph size exactly 255",
			want: &SimpleBlock{TrackNumber: 1, Lacing: LacingXiph,
				Frames: [][]byte{bytes.Repeat([]byte{7}, 255), {8}}},
		},
		{
			name: "fixed",
			want: &SimpleBlock{TrackNumber: 3, Lacing: LacingFixed, Discardable: true,
				Frames: [][]byte{{1, 2}, {3, 4}, {5, 6}}},
		},
		{
			name: "fixed single frame",
			want: &SimpleBlock{TrackNumber: 1, Lacing: LacingFixed, Frames: [][]byte{{1, 2}}},
		},
		{
			name: "ebml negative delta",
			want: &SimpleBlock{TrackNumber: 1, Timecode: 7, Lacing: LacingEBML,
				Frames: [][]byte{{1, 2, 3}, {4}, {5, 6}}},
		},
		{
			name: "ebml wide first size and negative delta",
			want: &SimpleBlock{TrackNumber: 1, Lacing: LacingEBML,
				Frames: [][]byte{big, {1}, big, {2}}},
		},
		{
			name: "ebml single frame",
			want: &SimpleBlock{TrackNumber: 1, Lacing: LacingEBML, Frames: [][]byte{{1, 2}}},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// A non-empty dst proves the append contract: the prefix survives and
			// the block goes after it, so a caller can build into a reused buffer.
			prefix := []byte{0xde, 0xad}
			buf, err := tt.want.Append(prefix)
			if err != nil {
				t.Fatalf("Append() error = %v", err)
			}
			if !bytes.Equal(buf[:len(prefix)], prefix) {
				t.Fatalf("Append() overwrote dst: %x", buf[:len(prefix)])
			}
			got, err := ParseSimpleBlock(buf[len(prefix):])
			if err != nil {
				t.Fatalf("ParseSimpleBlock(Append()) error = %v", err)
			}
			if got.TrackNumber != tt.want.TrackNumber || got.Timecode != tt.want.Timecode ||
				got.Keyframe != tt.want.Keyframe || got.Invisible != tt.want.Invisible ||
				got.Discardable != tt.want.Discardable || got.Lacing != tt.want.Lacing ||
				!equalFrames(got.Frames, tt.want.Frames) {
				t.Fatalf("round trip = %#v, want %#v", got, tt.want)
			}
			// Canonical means stable: re-encoding what was read back yields the
			// same bytes again.
			again, err := got.Append(nil)
			if err != nil {
				t.Fatalf("second Append() error = %v", err)
			}
			if !bytes.Equal(again, buf[len(prefix):]) {
				t.Fatalf("second Append() = %x, want %x", again, buf[len(prefix):])
			}
		})
	}
}

// TestSimpleBlockAppendRejects covers what the encoder refuses: what the parser
// refuses too, and what the declared lacing cannot represent. In every case dst
// must come back untouched, so a rejected block leaves no partial payload behind.
func TestSimpleBlockAppendRejects(t *testing.T) {
	tests := []struct {
		name string
		in   *SimpleBlock
	}{
		{"nil block", nil},
		{"track number zero", &SimpleBlock{TrackNumber: 0, Frames: [][]byte{{1}}}},
		{"track number too large", &SimpleBlock{TrackNumber: 1<<56 - 1, Frames: [][]byte{{1}}}},
		{"unknown lacing", &SimpleBlock{TrackNumber: 1, Lacing: LacingEBML + 1, Frames: [][]byte{{1}}}},
		{"no frames", &SimpleBlock{TrackNumber: 1, Frames: nil}},
		{"no frames laced", &SimpleBlock{TrackNumber: 1, Lacing: LacingXiph, Frames: [][]byte{}}},
		{"lacing none with two frames", &SimpleBlock{TrackNumber: 1, Frames: [][]byte{{1}, {2}}}},
		{"fixed unequal sizes", &SimpleBlock{TrackNumber: 1, Lacing: LacingFixed,
			Frames: [][]byte{{1, 2}, {3}}}},
		{"frame count over 256", &SimpleBlock{TrackNumber: 1, Lacing: LacingXiph,
			Frames: make([][]byte, 257)}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dst := []byte{0x01, 0x02}
			got, err := tt.in.Append(dst)
			if err == nil {
				t.Fatalf("Append() returned nil error and %x", got)
			}
			if !bytes.Equal(got, dst) {
				t.Fatalf("Append() = %x on failure, want dst %x unchanged", got, dst)
			}
		})
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
