package main

import (
	"encoding/hex"
	"fmt"
	"strings"
	"time"

	"github.com/yacchi/ebml-reader/matroska"
	"github.com/yacchi/ebml-reader/parser"
)

// ebmlEpoch is the EBML date origin: 2001-01-01T00:00:00 UTC.
var ebmlEpoch = time.Date(2001, 1, 1, 0, 0, 0, 0, time.UTC)

// hexBytes renders up to maxBinary bytes of b as space-free lowercase hex. When
// maxBinary is 0 no bytes are shown. truncated reports whether bytes were cut.
func hexBytes(b []byte, maxBinary int) (text string, truncated bool) {
	if maxBinary <= 0 {
		return "", len(b) > 0
	}
	n := len(b)
	if n > maxBinary {
		n = maxBinary
		truncated = true
	}
	return hex.EncodeToString(b[:n]), truncated
}

// scalarValue decodes a registry scalar leaf to its textual representation.
// Binary, block and unknown types return ("", false); callers render those
// specially. Decode failures fall back to a binary rendering by returning false.
func scalarValue(e element, payload []byte) (string, bool) {
	switch e.typ {
	case matroska.TypeUint:
		v, err := parser.DecodeUint(payload)
		if err != nil {
			return "", false
		}
		return fmt.Sprintf("%d", v), true
	case matroska.TypeInt:
		v, err := parser.DecodeInt(payload)
		if err != nil {
			return "", false
		}
		return fmt.Sprintf("%d", v), true
	case matroska.TypeFloat:
		v, err := parser.DecodeFloat(payload)
		if err != nil {
			return "", false
		}
		return fmt.Sprintf("%g", v), true
	case matroska.TypeString, matroska.TypeUTF8:
		return parser.DecodeString(payload), true
	case matroska.TypeDate:
		v, err := parser.DecodeInt(payload)
		if err != nil {
			return "", false
		}
		return ebmlEpoch.Add(time.Duration(v)).Format(time.RFC3339Nano), true
	default:
		return "", false
	}
}

// blockSummary renders a one-line SimpleBlock/Block summary, or ok=false if the
// payload does not parse as a block (caller falls back to a binary rendering).
func blockSummary(payload []byte) (string, bool) {
	blk, err := parser.ParseSimpleBlock(payload)
	if err != nil {
		return "", false
	}
	var flags []string
	if blk.Keyframe {
		flags = append(flags, "key")
	}
	if blk.Invisible {
		flags = append(flags, "invisible")
	}
	if blk.Discardable {
		flags = append(flags, "discardable")
	}
	flagStr := "-"
	if len(flags) > 0 {
		flagStr = strings.Join(flags, ",")
	}
	sizes := make([]int, len(blk.Frames))
	for i, f := range blk.Frames {
		sizes[i] = len(f)
	}
	return fmt.Sprintf("track=%d timecode=%d flags=%s lacing=%s frames=%d sizes=%v",
		blk.TrackNumber, blk.Timecode, flagStr, lacingName(blk.Lacing), len(blk.Frames), sizes), true
}

func lacingName(l parser.BlockLacing) string {
	switch l {
	case parser.LacingNone:
		return "none"
	case parser.LacingXiph:
		return "xiph"
	case parser.LacingFixed:
		return "fixed"
	case parser.LacingEBML:
		return "ebml"
	default:
		return "unknown"
	}
}

func sizeText(size int64) string {
	if size < 0 {
		return "unknown"
	}
	return fmt.Sprintf("%d", size)
}
