package parser

import "fmt"

type BlockLacing uint8

const (
	LacingNone BlockLacing = iota
	LacingXiph
	LacingFixed
	LacingEBML
)

// SimpleBlock is the decoded payload of a Matroska SimpleBlock: the track it
// belongs to, its timecode relative to the enclosing Cluster, the three flags
// Matroska defines for it, the declared lacing and the frames that lacing carves
// the payload into.
//
// ParseSimpleBlock and Append are duals: one reads the payload layout, the other
// writes it. Both deal in payload BYTES and never in an element ID, so neither
// gives package parser element knowledge, and the encoded bytes are what a caller
// hands to writer.Leaf as one binary leaf.
type SimpleBlock struct {
	TrackNumber uint64
	Timecode    int16
	Keyframe    bool
	Invisible   bool
	Discardable bool
	Lacing      BlockLacing
	Frames      [][]byte
}

// maxLacedFrames is the largest frame count a laced block can declare: the count
// travels as one byte holding "frames - 1".
const maxLacedFrames = 256

func parseBlockVINT(b []byte) (value uint64, n int, err error) {
	if len(b) == 0 {
		return 0, 0, fmt.Errorf("truncated VINT")
	}
	n = vintLen(b[0])
	if n == 0 || n > 8 {
		return 0, 0, fmt.Errorf("invalid VINT")
	}
	if len(b) < n {
		return 0, 0, fmt.Errorf("truncated VINT: need %d bytes, have %d", n, len(b))
	}
	value = uint64(b[0] & byte(0xff>>n))
	for i := 1; i < n; i++ {
		value = value<<8 | uint64(b[i])
	}
	return value, n, nil
}

// ParseSimpleBlock decodes a Matroska SimpleBlock payload.
//
// The returned Frames ALIAS b: nothing is copied, so a caller decoding the view
// LeafNode.Payload hands out inherits that view's lifetime and must copy the frames
// it keeps past the next Next.
func ParseSimpleBlock(b []byte) (*SimpleBlock, error) {
	track, n, err := parseBlockVINT(b)
	if err != nil {
		return nil, err
	}
	if track == 0 {
		return nil, fmt.Errorf("SimpleBlock track number must be non-zero")
	}
	b = b[n:]
	if len(b) < 2 {
		return nil, fmt.Errorf("truncated SimpleBlock timecode")
	}
	timecode, err := DecodeInt(b[:2])
	if err != nil {
		return nil, err
	}
	b = b[2:]
	if len(b) < 1 {
		return nil, fmt.Errorf("truncated SimpleBlock flags")
	}
	flags := b[0]
	b = b[1:]

	// Reserved flag bits are accepted for liberal Matroska compatibility.
	block := &SimpleBlock{
		TrackNumber: track,
		Timecode:    int16(timecode),
		Keyframe:    flags&0x80 != 0,
		Invisible:   flags&0x08 != 0,
		Discardable: flags&0x01 != 0,
		Lacing:      BlockLacing((flags >> 1) & 0x03),
	}
	if block.Lacing == LacingNone {
		block.Frames = [][]byte{b}
		return block, nil
	}
	if len(b) < 1 {
		return nil, fmt.Errorf("truncated lacing frame count")
	}
	frameCount := int(b[0]) + 1
	b = b[1:]

	sizes := make([]int, frameCount-1)
	switch block.Lacing {
	case LacingXiph:
		for i := range sizes {
			size := 0
			for {
				if len(b) == 0 {
					return nil, fmt.Errorf("truncated Xiph frame size")
				}
				v := b[0]
				b = b[1:]
				size += int(v)
				if v != 0xff {
					break
				}
			}
			sizes[i] = size
		}
	case LacingFixed:
		if len(b)%frameCount != 0 {
			return nil, fmt.Errorf("fixed lacing payload is not evenly divisible")
		}
		size := len(b) / frameCount
		for i := range sizes {
			sizes[i] = size
		}
	case LacingEBML:
		if len(sizes) > 0 {
			first, consumed, err := parseBlockVINT(b)
			if err != nil {
				return nil, fmt.Errorf("EBML first frame size: %w", err)
			}
			b = b[consumed:]
			if first > uint64(len(b)) {
				return nil, fmt.Errorf("EBML frame size exceeds payload")
			}
			sizes[0] = int(first)
			for i := 1; i < len(sizes); i++ {
				raw, consumed, err := parseBlockVINT(b)
				if err != nil {
					return nil, fmt.Errorf("EBML frame size delta: %w", err)
				}
				b = b[consumed:]
				bits := uint(7*consumed - 1)
				bias := int64((uint64(1) << bits) - 1)
				delta := int64(raw) - bias
				next := int64(sizes[i-1]) + delta
				if next < 0 {
					return nil, fmt.Errorf("negative EBML frame size")
				}
				sizes[i] = int(next)
			}
		}
	}

	remaining := len(b)
	total := 0
	for _, size := range sizes {
		if size > remaining-total {
			return nil, fmt.Errorf("frame size exceeds payload")
		}
		total += size
	}
	sizes = append(sizes, remaining-total)
	frames := make([][]byte, frameCount)
	for i, size := range sizes {
		frames[i] = b[:size]
		b = b[size:]
	}
	block.Frames = frames
	return block, nil
}

// Append encodes b as SimpleBlock payload bytes, appends them to dst and returns
// the extended slice -- the exact inverse of ParseSimpleBlock. Appending is what
// lets a caller build into a buffer it reuses across blocks and hand the result to
// writer.Leaf, which writes the element ID and size around it. Nothing else in the
// block reaches EBML: the layout encoded here lives entirely inside one binary
// leaf's payload, so package writer remains the only EBML encoder.
//
// ROUND-TRIP GUARANTEE. For a block that came from ParseSimpleBlock, Append
// reproduces the original payload BYTE FOR BYTE whenever that payload was
// canonically encoded -- every length VINT minimal-width, and no reserved flag bit
// set. Every block in this repository's fixture corpus is, and a test asserts it.
// The two ways a foreign payload can differ are both visible in the decoded value:
// ParseSimpleBlock accepts a non-minimal length VINT and tolerates the reserved
// flag bits (0x70) without retaining either, so Append re-emits minimal VINTs and
// zero reserved bits. For a LACED block the guarantee is therefore stated the other
// way round: Append writes the CANONICAL encoding of the value, which
// ParseSimpleBlock reads back as a block EQUAL to b -- same track, timecode, flags,
// lacing and frames.
//
// It refuses what ParseSimpleBlock refuses, and anything the declared lacing cannot
// represent, rather than emitting a payload that will not read back:
//
//   - track number 0, which no SimpleBlock may carry, or one too large for an
//     8-byte VINT;
//   - a lacing value outside LacingNone..LacingEBML;
//   - no frames at all, or more than 256, which is the largest count the one-byte
//     laced frame count can state;
//   - LacingNone with more than one frame, the one frame being the whole payload;
//   - LacingFixed with frames of unequal size, which the encoding cannot express
//     since it states no size at all;
//   - LacingEBML with a frame size, or a size delta, outside VINT range.
//
// On error nothing is appended: dst is returned with its original contents, and the
// error says which of the above it was.
func (b *SimpleBlock) Append(dst []byte) ([]byte, error) {
	if b == nil {
		return dst, fmt.Errorf("SimpleBlock is nil")
	}
	if b.TrackNumber == 0 {
		return dst, fmt.Errorf("SimpleBlock track number must be non-zero")
	}
	if b.Lacing > LacingEBML {
		return dst, fmt.Errorf("unknown SimpleBlock lacing %d", uint8(b.Lacing))
	}
	if len(b.Frames) == 0 {
		return dst, fmt.Errorf("SimpleBlock carries no frame")
	}
	if b.Lacing == LacingNone && len(b.Frames) != 1 {
		return dst, fmt.Errorf("lacing none cannot represent %d frames", len(b.Frames))
	}
	if len(b.Frames) > maxLacedFrames {
		return dst, fmt.Errorf("laced frame count %d exceeds %d", len(b.Frames), maxLacedFrames)
	}

	out, err := appendBlockVINT(dst, b.TrackNumber)
	if err != nil {
		return dst, fmt.Errorf("SimpleBlock track number: %w", err)
	}
	out = append(out, byte(uint16(b.Timecode)>>8), byte(uint16(b.Timecode)))

	// Reserved flag bits are written as zero: ParseSimpleBlock tolerates them but
	// does not retain them, so there is nothing to reproduce.
	flags := byte(b.Lacing) << 1
	if b.Keyframe {
		flags |= 0x80
	}
	if b.Invisible {
		flags |= 0x08
	}
	if b.Discardable {
		flags |= 0x01
	}
	out = append(out, flags)

	if b.Lacing == LacingNone {
		return append(out, b.Frames[0]...), nil
	}
	out = append(out, byte(len(b.Frames)-1))

	// Only the frames BEFORE the last one carry a size: the last one is whatever
	// payload remains, which is what makes the sizes a lacing and not a list.
	switch b.Lacing {
	case LacingXiph:
		for _, frame := range b.Frames[:len(b.Frames)-1] {
			size := len(frame)
			for size >= 0xff {
				out = append(out, 0xff)
				size -= 0xff
			}
			out = append(out, byte(size))
		}
	case LacingFixed:
		size := len(b.Frames[0])
		for i, frame := range b.Frames {
			if len(frame) != size {
				return dst, fmt.Errorf("fixed lacing needs equal frame sizes: frame 0 is %d bytes, frame %d is %d", size, i, len(frame))
			}
		}
	case LacingEBML:
		for i := 0; i < len(b.Frames)-1; i++ {
			size := int64(len(b.Frames[i]))
			if i == 0 {
				out, err = appendBlockVINT(out, uint64(size))
				if err != nil {
					return dst, fmt.Errorf("EBML lacing first frame size: %w", err)
				}
				continue
			}
			out, err = appendLacingDelta(out, size-int64(len(b.Frames[i-1])))
			if err != nil {
				return dst, fmt.Errorf("EBML lacing frame size delta: %w", err)
			}
		}
	}
	for _, frame := range b.Frames {
		out = append(out, frame...)
	}
	return out, nil
}

// appendBlockVINT appends v as a block-internal VINT in the minimal width that
// holds it: the same leading-marker-bit encoding an element size uses, but for a
// length that lives INSIDE a leaf payload rather than in an element header.
//
// The all-ones value of a width is never emitted -- that pattern is EBML's
// unknown-size marker, which no block length may be mistaken for -- so a value
// needing it moves to the next width up.
func appendBlockVINT(dst []byte, v uint64) ([]byte, error) {
	for width := 1; width <= 8; width++ {
		if v <= maxBlockVINT(width) {
			return appendBlockVINTWidth(dst, v, width), nil
		}
	}
	return dst, fmt.Errorf("value %d does not fit an 8-byte VINT", v)
}

// appendLacingDelta appends an EBML-lacing frame size delta, which is signed: the
// value is stored biased by the midpoint of its width, exactly as ParseSimpleBlock
// removes that bias. The minimal width is chosen from the delta's MAGNITUDE, since
// the bias makes the representable range symmetric.
func appendLacingDelta(dst []byte, delta int64) ([]byte, error) {
	for width := 1; width <= 8; width++ {
		bias := int64(1)<<uint(7*width-1) - 1
		if delta < -bias || delta > bias {
			continue
		}
		// The width must be the one the bias was taken from, so the raw value is
		// written at that width and not at the narrowest width that holds it.
		return appendBlockVINTWidth(dst, uint64(delta+bias), width), nil
	}
	return dst, fmt.Errorf("delta %d does not fit an 8-byte signed VINT", delta)
}

// maxBlockVINT returns the largest value a width-byte VINT may hold, the all-ones
// unknown-size pattern excluded.
func maxBlockVINT(width int) uint64 {
	return uint64(1)<<uint(7*width) - 2
}

func appendBlockVINTWidth(dst []byte, v uint64, width int) []byte {
	raw := v | uint64(1)<<uint(7*width)
	for shift := (width - 1) * 8; shift >= 0; shift -= 8 {
		dst = append(dst, byte(raw>>uint(shift)))
	}
	return dst
}
