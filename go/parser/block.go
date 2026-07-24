package parser

import "fmt"

type BlockLacing uint8

const (
	LacingNone BlockLacing = iota
	LacingXiph
	LacingFixed
	LacingEBML
)

type SimpleBlock struct {
	TrackNumber uint64
	Timecode    int16
	Keyframe    bool
	Invisible   bool
	Discardable bool
	Lacing      BlockLacing
	Frames      [][]byte
}

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
