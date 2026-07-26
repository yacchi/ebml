package parser_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/yacchi/ebml/impl/go/parser"
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
// to parser.KindClassifier and accepted as New's first argument without a cast.
//
// The classifier used to be supplied through an optional Option, backed by a
// built-in default; both are gone. It is now a required positional argument of
// New and NewCursor, so "forgot the classifier" is a compile error instead of a
// stream silently read with the wrong structure -- which is why this file has no
// negative test for the omitted case: it cannot be written.
var _ parser.KindClassifier = callerClassifier

func TestNewUsesCallerClassifier(t *testing.T) {
	p := parser.New(callerClassifier)
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

// TestNewRejectsNilClassifier pins the loud-failure contract: a nil classifier is
// a programmer error at construction time, so the constructors panic instead of
// substituting a default that would misread the stream.
func TestNewRejectsNilClassifier(t *testing.T) {
	for _, tt := range []struct {
		name string
		call func()
	}{
		{"New", func() { parser.New(nil) }},
		{"NewCursor", func() { parser.NewCursor(nil) }},
	} {
		t.Run(tt.name, func(t *testing.T) {
			defer func() {
				r := recover()
				if r == nil {
					t.Fatalf("%s(nil) did not panic", tt.name)
				}
				msg, ok := r.(string)
				if !ok {
					t.Fatalf("%s(nil) panicked with %T, want string", tt.name, r)
				}
				if !strings.Contains(msg, "must not be nil") {
					t.Errorf("%s(nil) panic message = %q, want it to mention a nil classifier", tt.name, msg)
				}
			}()
			tt.call()
		})
	}
}

// closeMasterFixture builds "Segment(unknown size){ Cluster(size 3){ 0x81 0x00 } }"
// -- a known-size master with one 2-byte child inside it, so the cursor can be
// parked both before and at the Cluster's declared end.
func closeMasterFixture() []byte {
	return []byte{
		0x18, 0x53, 0x80, 0x67, 0xFF, // Segment, unknown size
		0x1F, 0x43, 0xB6, 0x75, 0x83, // Cluster, size 3
		0xEC, 0x81, 0x00, // Void, size 1, payload 0x00
	}
}

// closeMasterClassifier classifies just the elements of closeMasterFixture.
func closeMasterClassifier(id parser.ElementID) parser.Kind {
	switch id {
	case 0x18538067, 0x1F43B675: // Segment, Cluster
		return parser.KindMaster
	default:
		return parser.KindBinary
	}
}

// enterCloseMasterFixture feeds the fixture and enters both masters, leaving the
// cursor at the Cluster's first child with the Cluster innermost on the stack.
func enterCloseMasterFixture(t *testing.T) *parser.Parser {
	t.Helper()
	p := parser.New(closeMasterClassifier)
	p.Feed(closeMasterFixture())
	for i := 0; i < 2; i++ {
		if _, err := p.ConsumeHeader(); err != nil {
			t.Fatalf("ConsumeHeader() error = %v", err)
		}
		if err := p.EnterMaster(); err != nil {
			t.Fatalf("EnterMaster() error = %v", err)
		}
	}
	if p.Depth() != 2 {
		t.Fatalf("Depth() = %d, want 2", p.Depth())
	}
	return p
}

// TestCloseMasterRejectsKnownSizeBeforeDeclaredEnd is the boundary-preservation
// contract: explicit close may not discard a boundary the stream declares, or the
// master's remaining payload would be read as elements of its parent.
func TestCloseMasterRejectsKnownSizeBeforeDeclaredEnd(t *testing.T) {
	p := enterCloseMasterFixture(t)

	offsetBefore := p.Offset()
	err := p.CloseMaster()
	if err == nil {
		t.Fatal("CloseMaster() on a known-size master short of its end returned nil, want an error")
	}
	if !errors.Is(err, parser.ErrPrematureClose) {
		t.Fatalf("CloseMaster() error = %v, want it to match ErrPrematureClose", err)
	}
	var pce parser.PrematureCloseError
	if !errors.As(err, &pce) {
		t.Fatalf("CloseMaster() error = %T, want parser.PrematureCloseError", err)
	}
	if pce.ID != 0x1F43B675 {
		t.Errorf("PrematureCloseError.ID = %s, want 0x1F43B675 (Cluster)", pce.ID)
	}
	if pce.Offset != offsetBefore {
		t.Errorf("PrematureCloseError.Offset = %d, want %d", pce.Offset, offsetBefore)
	}
	if pce.DeclaredEnd != 13 {
		t.Errorf("PrematureCloseError.DeclaredEnd = %d, want 13", pce.DeclaredEnd)
	}

	// The rejection must leave the cursor untouched, so the caller can keep
	// reading the master's remaining children.
	if p.Depth() != 2 {
		t.Errorf("Depth() after rejected CloseMaster() = %d, want 2", p.Depth())
	}
	if p.Offset() != offsetBefore {
		t.Errorf("Offset() after rejected CloseMaster() = %d, want %d", p.Offset(), offsetBefore)
	}
	if _, err := p.ConsumeHeader(); err != nil {
		t.Fatalf("ConsumeHeader() after rejected CloseMaster() error = %v", err)
	}
}

// TestCloseMasterAllowsKnownSizeAtDeclaredEnd covers the other half of the
// precondition: once the declared end is reached there is no boundary left to
// discard, so the same call succeeds.
func TestCloseMasterAllowsKnownSizeAtDeclaredEnd(t *testing.T) {
	p := enterCloseMasterFixture(t)

	// Consume the Cluster's only child, which lands the cursor exactly on the
	// Cluster's declared end.
	if _, err := p.ConsumeHeader(); err != nil {
		t.Fatalf("ConsumeHeader() error = %v", err)
	}
	if err := p.SkipPayload(); err != nil {
		t.Fatalf("SkipPayload() error = %v", err)
	}
	if p.Offset() != 13 {
		t.Fatalf("Offset() = %d, want 13 (the Cluster's declared end)", p.Offset())
	}

	if err := p.CloseMaster(); err != nil {
		t.Fatalf("CloseMaster() at the declared end error = %v", err)
	}
	if p.Depth() != 1 {
		t.Fatalf("Depth() = %d, want 1 (only the Segment left open)", p.Depth())
	}
}

// TestCloseMasterClosesUnknownSizeMaster is the case explicit close exists for:
// an unknown-size master has no declared end, so a consumer's own boundary rule
// is the only thing that can close it.
func TestCloseMasterClosesUnknownSizeMaster(t *testing.T) {
	p := enterCloseMasterFixture(t)

	// Reach the Cluster's declared end and leave it the ordinary way, so only the
	// unknown-size Segment stays open.
	if _, err := p.ConsumeHeader(); err != nil {
		t.Fatalf("ConsumeHeader() error = %v", err)
	}
	if err := p.SkipPayload(); err != nil {
		t.Fatalf("SkipPayload() error = %v", err)
	}
	if err := p.LeaveMaster(); err != nil {
		t.Fatalf("LeaveMaster() error = %v", err)
	}

	if err := p.CloseMaster(); err != nil {
		t.Fatalf("CloseMaster() on the unknown-size Segment error = %v", err)
	}
	if p.Depth() != 0 {
		t.Fatalf("Depth() = %d, want 0", p.Depth())
	}
}
