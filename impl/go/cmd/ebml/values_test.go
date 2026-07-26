package main

import (
	"strings"
	"testing"

	"github.com/yacchi/ebml/impl/go/matroska"
	"github.com/yacchi/ebml/impl/go/parser"
)

// unregisteredID is a syntactically valid one-byte element ID the Matroska
// registry does not name. The CLI has to render such an element without knowing
// anything about it, which is a shape the fixtures cannot supply on purpose.
const unregisteredID parser.ElementID = 0x81

// TestScalarValueCoversEveryRegistryScalarType walks one case per branch of
// scalarValue: each decodable type, the types that are deliberately not scalars,
// and the decode failures the callers turn into a binary rendering.
func TestScalarValueCoversEveryRegistryScalarType(t *testing.T) {
	tests := []struct {
		name    string
		id      parser.ElementID
		payload []byte
		want    string
		wantOK  bool
	}{
		{"uint", matroska.IDTrackNumber, []byte{0x01, 0x02}, "258", true},
		{"int is signed", matroska.IDTrackOffset, []byte{0xFF}, "-1", true},
		{"float32", matroska.IDDuration, []byte{0x3F, 0xC0, 0x00, 0x00}, "1.5", true},
		{"float64", matroska.IDDuration, []byte{0x3F, 0xF8, 0, 0, 0, 0, 0, 0}, "1.5", true},
		{"string", matroska.IDDocType, []byte("webm"), "webm", true},
		{"date is the EBML epoch plus nanoseconds", matroska.IDDateUTC,
			[]byte{0, 0, 0, 0, 0, 0, 0, 0}, "2001-01-01T00:00:00Z", true},

		// Not scalars: the caller renders these itself.
		{"binary", matroska.IDVoid, []byte{0xAA}, "", false},
		{"unregistered id", unregisteredID, []byte{0xAA}, "", false},

		// Decode failures fall back the same way, rather than surfacing an error.
		{"overlong uint", matroska.IDTrackNumber, make([]byte, 9), "", false},
		{"overlong int", matroska.IDTrackOffset, make([]byte, 9), "", false},
		{"float of an impossible width", matroska.IDDuration, []byte{0x01, 0x02, 0x03}, "", false},
		{"date of an impossible width", matroska.IDDateUTC, []byte{0x01, 0x02, 0x03}, "", false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := scalarValue(tc.id, tc.payload)
			if ok != tc.wantOK || got != tc.want {
				t.Errorf("scalarValue = (%q, %v), want (%q, %v)", got, ok, tc.want, tc.wantOK)
			}
		})
	}
}

// TestBlockSummaryReportsFlagsAndLacing pins the one-line block rendering across
// the flag combinations and every lacing the parser can produce. The blocks are
// built with (*parser.SimpleBlock).Append so the bytes are the ones the library
// itself writes, not a hand-rolled guess at the layout.
func TestBlockSummaryReportsFlagsAndLacing(t *testing.T) {
	tests := []struct {
		name  string
		block parser.SimpleBlock
		want  []string
	}{
		{
			name: "no flags renders a dash",
			block: parser.SimpleBlock{
				TrackNumber: 1, Timecode: 7,
				Frames: [][]byte{{0xAA, 0xBB}},
			},
			want: []string{"track=1", "timecode=7", "flags=-", "lacing=none", "frames=1", "sizes=[2]"},
		},
		{
			name: "every flag is listed",
			block: parser.SimpleBlock{
				TrackNumber: 2, Timecode: -3,
				Keyframe: true, Invisible: true, Discardable: true,
				Frames: [][]byte{{0xAA}},
			},
			want: []string{"track=2", "timecode=-3", "flags=key,invisible,discardable"},
		},
		{
			name: "xiph lacing",
			block: parser.SimpleBlock{
				TrackNumber: 1, Lacing: parser.LacingXiph,
				Frames: [][]byte{{0xAA}, {0xBB, 0xCC}},
			},
			want: []string{"lacing=xiph", "frames=2", "sizes=[1 2]"},
		},
		{
			name: "fixed lacing",
			block: parser.SimpleBlock{
				TrackNumber: 1, Lacing: parser.LacingFixed,
				Frames: [][]byte{{0xAA}, {0xBB}},
			},
			want: []string{"lacing=fixed", "frames=2", "sizes=[1 1]"},
		},
		{
			name: "ebml lacing",
			block: parser.SimpleBlock{
				TrackNumber: 1, Lacing: parser.LacingEBML,
				Frames: [][]byte{{0xAA}, {0xBB, 0xCC}},
			},
			want: []string{"lacing=ebml", "frames=2", "sizes=[1 2]"},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			payload, err := tc.block.Append(nil)
			if err != nil {
				t.Fatalf("Append: %v", err)
			}
			got, ok := blockSummary(payload)
			if !ok {
				t.Fatalf("blockSummary reported a block it wrote itself as unparseable")
			}
			for _, want := range tc.want {
				if !strings.Contains(got, want) {
					t.Errorf("summary %q missing %q", got, want)
				}
			}
		})
	}
}

// TestBlockSummaryRejectsNonBlock is the branch the callers rely on to switch to
// a binary rendering: a payload that is not a block must not be summarised.
func TestBlockSummaryRejectsNonBlock(t *testing.T) {
	for _, payload := range [][]byte{nil, {0x00}, {0xAA}} {
		if got, ok := blockSummary(payload); ok {
			t.Errorf("blockSummary(%x) = (%q, true), want ok=false", payload, got)
		}
	}
}

// TestLacingNameNamesEveryLacing includes the out-of-range value, which the
// parser never produces but which the CLI must still name rather than print
// blank.
func TestLacingNameNamesEveryLacing(t *testing.T) {
	tests := []struct {
		lacing parser.BlockLacing
		want   string
	}{
		{parser.LacingNone, "none"},
		{parser.LacingXiph, "xiph"},
		{parser.LacingFixed, "fixed"},
		{parser.LacingEBML, "ebml"},
		{parser.LacingEBML + 1, "unknown"},
	}
	for _, tc := range tests {
		if got := lacingName(tc.lacing); got != tc.want {
			t.Errorf("lacingName(%d) = %q, want %q", tc.lacing, got, tc.want)
		}
	}
}

func TestHexBytesTruncates(t *testing.T) {
	tests := []struct {
		name          string
		b             []byte
		maxBinary     int
		wantText      string
		wantTruncated bool
	}{
		{"under the limit", []byte{0xAA, 0xBB}, 4, "aabb", false},
		{"exactly the limit", []byte{0xAA, 0xBB}, 2, "aabb", false},
		{"over the limit", []byte{0xAA, 0xBB, 0xCC}, 2, "aabb", true},
		{"zero limit with bytes reports truncation", []byte{0xAA}, 0, "", true},
		{"zero limit with no bytes does not", nil, 0, "", false},
		{"negative limit behaves as zero", []byte{0xAA}, -1, "", true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			text, truncated := hexBytes(tc.b, tc.maxBinary)
			if text != tc.wantText || truncated != tc.wantTruncated {
				t.Errorf("hexBytes = (%q, %v), want (%q, %v)",
					text, truncated, tc.wantText, tc.wantTruncated)
			}
		})
	}
}

func TestSizeTextNamesUnknownSize(t *testing.T) {
	if got := sizeText(-1); got != "unknown" {
		t.Errorf("sizeText(-1) = %q, want %q", got, "unknown")
	}
	if got := sizeText(0); got != "0" {
		t.Errorf("sizeText(0) = %q, want %q", got, "0")
	}
}
