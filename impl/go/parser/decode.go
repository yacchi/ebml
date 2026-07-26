package parser

import (
	"encoding/binary"
	"fmt"
	"math"
	"strings"
)

func DecodeUint(b []byte) (uint64, error) {
	if len(b) > 8 {
		return 0, fmt.Errorf("unsigned integer is %d bytes; maximum is 8", len(b))
	}
	var value uint64
	for _, v := range b {
		value = value<<8 | uint64(v)
	}
	return value, nil
}

func DecodeInt(b []byte) (int64, error) {
	if len(b) > 8 {
		return 0, fmt.Errorf("signed integer is %d bytes; maximum is 8", len(b))
	}
	if len(b) == 0 {
		return 0, nil
	}
	value, _ := DecodeUint(b)
	if b[0]&0x80 != 0 && len(b) < 8 {
		value |= ^uint64(0) << uint(8*len(b))
	}
	return int64(value), nil
}

func DecodeFloat(b []byte) (float64, error) {
	switch len(b) {
	case 0:
		return 0, nil
	case 4:
		return float64(math.Float32frombits(binary.BigEndian.Uint32(b))), nil
	case 8:
		return math.Float64frombits(binary.BigEndian.Uint64(b)), nil
	default:
		return 0, fmt.Errorf("float is %d bytes; expected 0, 4, or 8", len(b))
	}
}

func DecodeString(b []byte) string {
	if i := strings.IndexByte(string(b), 0); i >= 0 {
		b = b[:i]
	}
	return string(b)
}
