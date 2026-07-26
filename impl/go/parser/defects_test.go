package parser

import (
	"errors"
	"testing"
)

func TestFinalizeEOFRejectsTruncatedHeader(t *testing.T) {
	p := New(testKindClassifier)
	p.Feed([]byte{0x1a})
	_, err := p.FinalizeEOF()
	if !errors.Is(err, ErrTruncated) {
		t.Fatalf("FinalizeEOF() error = %v, want ErrTruncated", err)
	}
	var truncated TruncatedError
	if !errors.As(err, &truncated) {
		t.Fatalf("FinalizeEOF() error = %T, want TruncatedError", err)
	}
}

func TestFinalizeEOFRejectsTruncatedLeafPayload(t *testing.T) {
	p := New(testKindClassifier)
	p.Feed([]byte{0xec, 0x82, 0x01})
	if _, err := p.ConsumeHeader(); err != nil {
		t.Fatalf("ConsumeHeader() error = %v", err)
	}
	if _, err := p.FinalizeEOF(); !errors.Is(err, ErrTruncated) {
		t.Fatalf("FinalizeEOF() error = %v, want ErrTruncated", err)
	}
}

func TestFinalizeEOFCleanFixture(t *testing.T) {
	p := New(testKindClassifier)
	p.Feed(loadHexFixture(t, "fixtures/tiny.ebml.hex"))
	var events []logEvent
	step, needMore := 0, 0
	scanAll(t, p, &events, &step, &needMore)
	if _, err := p.FinalizeEOF(); err != nil {
		t.Fatalf("FinalizeEOF() error = %v", err)
	}
}

func drainDefectTest(p *Parser) error {
	for {
		if p.current != nil {
			if p.current.kind == KindMaster {
				if err := p.EnterMaster(); err != nil {
					return err
				}
			} else if err := p.SkipPayload(); err != nil {
				var needMore NeedMoreData
				if errors.As(err, &needMore) {
					return nil
				}
				return err
			}
			continue
		}
		h, err := p.Peek()
		if err != nil {
			var needMore NeedMoreData
			if errors.As(err, &needMore) {
				return nil
			}
			return err
		}
		if h.Kind == KindEndMaster {
			if err := p.LeaveMaster(); err != nil {
				return err
			}
			continue
		}
		if _, err := p.ConsumeHeader(); err != nil {
			return err
		}
	}
}

func TestKnownSizeChildOverflowIsSplitInvariant(t *testing.T) {
	data := []byte{0x1f, 0x43, 0xb6, 0x75, 0x83, 0xe7, 0x82, 0x00, 0x01}
	for _, split := range []bool{false, true} {
		name := "whole"
		if split {
			name = "one_byte"
		}
		t.Run(name, func(t *testing.T) {
			p := New(testKindClassifier)
			var err error
			if split {
				for _, b := range data {
					p.Feed([]byte{b})
					err = drainDefectTest(p)
					if errors.Is(err, ErrElementOverflowsParent) {
						break
					}
					if err != nil {
						t.Fatalf("drain error = %v", err)
					}
				}
			} else {
				p.Feed(data)
				err = drainDefectTest(p)
			}
			if !errors.Is(err, ErrElementOverflowsParent) {
				t.Fatalf("error = %v, want ErrElementOverflowsParent", err)
			}
		})
	}
}

// TestUnknownSizeLeafIsTyped pins the diagnosis of the one input EBML forbids
// outright: an unknown size on an element the classifier does not treat as a
// master. It is reported on the header, by Peek, with the ID and the absolute
// offset, so a consumer can detect exactly this case -- typically an
// unregistered vendor master -- instead of reading a generic message.
func TestUnknownSizeLeafIsTyped(t *testing.T) {
	p := New(testKindClassifier)
	// A Void leaf with a known (empty) payload, then a Void leaf declaring
	// unknown size at offset 2.
	p.Feed([]byte{0xec, 0x80, 0xec, 0xff})
	if _, err := p.ConsumeHeader(); err != nil {
		t.Fatalf("ConsumeHeader() error = %v", err)
	}
	if err := p.SkipPayload(); err != nil {
		t.Fatalf("SkipPayload() error = %v", err)
	}

	_, err := p.Peek()
	if !errors.Is(err, ErrUnknownSizeLeaf) {
		t.Fatalf("Peek() error = %v, want ErrUnknownSizeLeaf", err)
	}
	var bad UnknownSizeLeafError
	if !errors.As(err, &bad) {
		t.Fatalf("Peek() error = %T, want UnknownSizeLeafError", err)
	}
	if bad.ID != 0xEC || bad.Offset != 2 || bad.Kind != KindBinary {
		t.Errorf("error = %+v, want ID 0xEC at offset 2 classified as binary", bad)
	}
	if bad.Error() == "" {
		t.Error("UnknownSizeLeafError.Error() is empty")
	}
	// The cursor never accepted the element, so no payload operation can run on
	// it and the diagnosis is stable while the stream stays where it is.
	if _, err := p.ConsumeHeader(); !errors.Is(err, ErrUnknownSizeLeaf) {
		t.Errorf("ConsumeHeader() error = %v, want ErrUnknownSizeLeaf", err)
	}
}

// TestUnknownSizeMasterIsAccepted is the counterpart: the unknown size is
// legitimate for a master, which is how a KVS Segment is read.
func TestUnknownSizeMasterIsAccepted(t *testing.T) {
	p := New(testKindClassifier)
	p.Feed([]byte{0x18, 0x53, 0x80, 0x67, 0xff})
	h, err := p.Peek()
	if err != nil {
		t.Fatalf("Peek() error = %v", err)
	}
	if h.Kind != KindMaster || h.Size != UnknownSize {
		t.Fatalf("Peek() = %v, want an unknown-size master", h)
	}
}

func TestParseSimpleBlockRejectsZeroTrackNumber(t *testing.T) {
	if _, err := ParseSimpleBlock([]byte{0x80, 0, 0, 0}); err == nil {
		t.Fatal("ParseSimpleBlock() accepted zero track number")
	}
}
