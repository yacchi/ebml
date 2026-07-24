package parser_test

import (
	"testing"

	"github.com/yacchi/ebml-reader/parser"
)

func TestElementIDString(t *testing.T) {
	for _, tt := range []struct {
		id   parser.ElementID
		want string
	}{
		{0xA3, "0xA3"},             // SimpleBlock (1-byte ID)
		{0x4286, "0x4286"},         // EBMLVersion (2-byte ID)
		{0x2AD7B1, "0x2AD7B1"},     // TimestampScale (3-byte ID)
		{0x1F43B675, "0x1F43B675"}, // Cluster (4-byte ID)
	} {
		if got := tt.id.String(); got != tt.want {
			t.Errorf("ElementID(%d).String() = %q, want %q", uint32(tt.id), got, tt.want)
		}
	}
}

// callerClassifier stands in for a classifier written outside package parser,
// spelled exactly as a caller would spell it.
func callerClassifier(id parser.ElementID) parser.Kind {
	if id == 0x18538067 { // Segment
		return parser.KindMaster
	}
	return parser.KindBinary
}

// Compile-level check: a plain func(parser.ElementID) parser.Kind is assignable
// to parser.KindClassifier and accepted by WithKindClassifier without a cast.
var (
	_ parser.KindClassifier = callerClassifier
	_ parser.Option         = parser.WithKindClassifier(callerClassifier)
)

func TestWithKindClassifierUsesCallerClassifier(t *testing.T) {
	p := parser.New(parser.WithKindClassifier(callerClassifier))
	p.Feed([]byte{0x18, 0x53, 0x80, 0x67, 0xFF}) // Segment, unknown size

	h, err := p.Peek()
	if err != nil {
		t.Fatalf("Peek() error = %v", err)
	}
	if h.ID != 0x18538067 {
		t.Fatalf("Peek() ID = %s, want 0x18538067", h.ID)
	}
	if h.Kind != parser.KindMaster {
		t.Fatalf("Peek() Kind = %q, want %q", h.Kind, parser.KindMaster)
	}
	if h.Size != parser.UnknownSize {
		t.Fatalf("Peek() Size = %d, want UnknownSize", h.Size)
	}
}
