package parser

import (
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func FuzzParser(f *testing.F) {
	paths, err := filepath.Glob(filepath.Join("..", "..", "fixtures", "kvs", "*.ebml.hex"))
	if err != nil {
		f.Fatal(err)
	}
	for _, path := range paths {
		raw, err := loadFuzzHex(path)
		if err != nil {
			f.Fatalf("load fuzz seed %s: %v", path, err)
		}
		if len(raw) > 0 {
			f.Add(raw)
		}
	}
	f.Add([]byte{0x1a, 0x45, 0xdf, 0xa3})

	f.Fuzz(func(t *testing.T, data []byte) {
		driveParser(data, false)
		driveParser(data, true)
	})
}

func loadFuzzHex(path string) ([]byte, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var hexText strings.Builder
	for _, line := range strings.Split(string(raw), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		hexText.WriteString(strings.Join(strings.Fields(line), ""))
	}
	return hex.DecodeString(hexText.String())
}

func driveParser(data []byte, byteByByte bool) {
	p := New(WithKindClassifier(testKindClassifier))
	drain := func() {
		for {
			if p.current != nil {
				if p.current.kind == KindMaster {
					_ = p.EnterMaster()
					continue
				}
				if err := p.SkipPayload(); err != nil {
					var needMore NeedMoreData
					if errors.As(err, &needMore) {
						return
					}
					return
				}
				continue
			}

			h, err := p.Peek()
			if err != nil {
				var needMore NeedMoreData
				if errors.As(err, &needMore) {
					return
				}
				return
			}
			if h.Kind == KindEndMaster {
				_ = p.LeaveMaster()
				continue
			}
			if _, err := p.ConsumeHeader(); err != nil {
				return
			}
		}
	}

	if byteByByte {
		for _, b := range data {
			p.Feed([]byte{b})
			drain()
		}
	} else {
		p.Feed(data)
		drain()
	}
	drain()
	_, _ = p.FinalizeEOF()
}

func TestVINTLengthLimits(t *testing.T) {
	if _, _, err := parseElementID([]byte{0x01}); !errors.Is(err, ErrElementIDTooLong) {
		t.Fatalf("parseElementID() error = %v, want ErrElementIDTooLong", err)
	}
	var idErr VINTLengthError
	if _, _, err := parseElementID([]byte{0x01}); !errors.As(err, &idErr) {
		t.Fatalf("parseElementID() error = %T, want VINTLengthError", err)
	}

	if _, _, err := parseSize([]byte{0x80}); err != nil {
		t.Fatalf("parseSize() with one-byte VINT: %v", err)
	}
	if _, n, err := parseSize([]byte{0x01, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff}); err != nil || n != MaxElementSizeLength {
		t.Fatalf("parseSize() with maximum VINT: n=%d err=%v", n, err)
	}

	// A leading byte of 0x00 has no marker bit within the first 8 bits, so it
	// encodes a 9-byte VINT -- one longer than the 8-byte max for an element
	// size. This must surface as a typed VINTLengthError/ErrElementSizeTooLong,
	// not a generic Invalid.
	if _, _, err := parseSize([]byte{0x00, 0x01, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff}); !errors.Is(err, ErrElementSizeTooLong) {
		t.Fatalf("parseSize() with zero leading byte: error = %v, want ErrElementSizeTooLong", err)
	} else {
		var sizeErr VINTLengthError
		if !errors.As(err, &sizeErr) {
			t.Fatalf("parseSize() with zero leading byte: error = %T, want VINTLengthError", err)
		}
		if sizeErr.What != "element size" {
			t.Fatalf("parseSize() with zero leading byte: What = %q, want %q", sizeErr.What, "element size")
		}
	}
}

// TestHostileSizeVINTViaPeek drives the parser's public Peek() with a hostile
// element-size VINT (element ID 0xA3, size leading byte 0x00 followed by
// continuation bytes) and checks that it surfaces the typed
// VINTLengthError/ErrElementSizeTooLong -- not a generic Invalid -- exactly
// as a caller relying on errors.Is would observe it.
func TestHostileSizeVINTViaPeek(t *testing.T) {
	data := []byte{0xa3, 0x00, 0x01, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff}
	p := New(WithKindClassifier(testKindClassifier))
	p.Feed(data)

	_, err := p.Peek()
	if !errors.Is(err, ErrElementSizeTooLong) {
		t.Fatalf("Peek() error = %v, want ErrElementSizeTooLong", err)
	}
	var sizeErr VINTLengthError
	if !errors.As(err, &sizeErr) {
		t.Fatalf("Peek() error = %T, want VINTLengthError", err)
	}
	if sizeErr.What != "element size" {
		t.Fatalf("Peek() error What = %q, want %q", sizeErr.What, "element size")
	}
}
