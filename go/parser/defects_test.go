package parser

import (
	"errors"
	"testing"
)

func TestFinalizeEOFRejectsTruncatedHeader(t *testing.T) {
	p := New()
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
	p := New()
	p.Feed([]byte{0xec, 0x82, 0x01})
	if _, err := p.ConsumeHeader(); err != nil {
		t.Fatalf("ConsumeHeader() error = %v", err)
	}
	if _, err := p.FinalizeEOF(); !errors.Is(err, ErrTruncated) {
		t.Fatalf("FinalizeEOF() error = %v, want ErrTruncated", err)
	}
}

func TestFinalizeEOFCleanFixture(t *testing.T) {
	p := New()
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
			p := New(WithKindClassifier(testKindClassifier))
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

func TestParseSimpleBlockRejectsZeroTrackNumber(t *testing.T) {
	if _, err := ParseSimpleBlock([]byte{0x80, 0, 0, 0}); err == nil {
		t.Fatal("ParseSimpleBlock() accepted zero track number")
	}
}
