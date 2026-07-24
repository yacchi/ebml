// Package fragment is a per-fragment assembly layer over the streaming EBML
// cursor in package parser. It consumes the continuous Amazon Kinesis Video
// Streams (KVS) GetMedia byte stream — concatenated unknown-size Matroska
// Segments, one per KVS fragment — and emits one Fragment value per Segment's
// Cluster.
//
// The defining property (inherited from the cursor) is early emission: a
// Fragment is produced the moment its known-size Cluster is fully consumed,
// without waiting for the enclosing unknown-size Segment to close, for the next
// Segment's header to arrive, or for connection EOF. This removes both the
// boundary-wait and the end-of-stream tail latency that a whole-document parser
// suffers on a live GetMedia body.
//
// Inside a Segment the Tags and Tracks elements arrive before the Cluster; the
// assembler buffers them and attaches them to each Fragment as the Cluster
// completes. A Segment may contain multiple Clusters (each yields its own
// Fragment, sharing that Segment's Tags/Tracks) or no Tags at all (the Fragment
// then carries an empty tag map). Each Segment stands alone: no state leaks
// across Segment boundaries, so mid-stream metadata changes and dropped
// fragments need no special handling.
package fragment

import (
	"strconv"
	"strings"
	"time"

	"github.com/yacchi/ebml-reader/parser"
)

// AWS KVS tag names carried in a fragment's Tags element (Tag -> SimpleTag ->
// TagName/TagString). These are the well-known keys; arbitrary custom tags such
// as ContactId / InstanceId are reachable via Fragment.Tag.
const (
	TagProducerTimestamp = "AWS_KINESISVIDEO_PRODUCER_TIMESTAMP"
	TagFragmentNumber    = "AWS_KINESISVIDEO_FRAGMENT_NUMBER"
	TagContinuationToken = "AWS_KINESISVIDEO_CONTINUATION_TOKEN"
)

// Track is one decoded Matroska TrackEntry. Only the fields present in the KVS
// fragment shape are decoded; absent fields keep their zero value.
type Track struct {
	Number  uint64 // TrackNumber
	Type    uint64 // TrackType (2 = audio)
	CodecID string // CodecID, e.g. "A_PCM/INT/LIT"
}

// Fragment is one assembled KVS fragment: the metadata buffered from its
// Segment plus the contents of a single completed Cluster.
type Fragment struct {
	// Tags maps TagName -> TagString for every SimpleTag in the Segment's Tags
	// element. It is non-nil but empty for a tagless Segment.
	Tags map[string]string

	// Tracks lists the Segment's TrackEntry elements in stream order.
	Tracks []Track

	// ClusterTimestamp is the Cluster's Timestamp (0xE7) value.
	ClusterTimestamp uint64

	// Blocks holds every SimpleBlock in the Cluster, decoded via
	// parser.ParseSimpleBlock, in stream order.
	Blocks []*parser.SimpleBlock
}

// Tag returns the TagString for the given TagName and whether it was present.
func (f *Fragment) Tag(name string) (string, bool) {
	v, ok := f.Tags[name]
	return v, ok
}

// FragmentNumber returns AWS_KINESISVIDEO_FRAGMENT_NUMBER and whether present.
func (f *Fragment) FragmentNumber() (string, bool) {
	return f.Tag(TagFragmentNumber)
}

// ContinuationToken returns AWS_KINESISVIDEO_CONTINUATION_TOKEN and whether present.
func (f *Fragment) ContinuationToken() (string, bool) {
	return f.Tag(TagContinuationToken)
}

// ProducerTimestamp parses AWS_KINESISVIDEO_PRODUCER_TIMESTAMP, a decimal
// seconds-since-epoch string that may carry a fractional part (e.g.
// "1700000000.512"), into a UTC time.Time. The bool is false when the tag is
// absent or cannot be parsed.
func (f *Fragment) ProducerTimestamp() (time.Time, bool) {
	s, ok := f.Tag(TagProducerTimestamp)
	if !ok {
		return time.Time{}, false
	}
	return parseDecimalSeconds(s)
}

// TrackPCM returns the concatenated frame bytes (PCM payload) of every
// SimpleBlock whose TrackNumber matches trackNumber, in stream order. It returns
// nil when no block matches.
func (f *Fragment) TrackPCM(trackNumber uint64) []byte {
	var out []byte
	for _, b := range f.Blocks {
		if b.TrackNumber != trackNumber {
			continue
		}
		for _, frame := range b.Frames {
			out = append(out, frame...)
		}
	}
	return out
}

// parseDecimalSeconds converts a decimal seconds string into a UTC time.Time.
// The integer part is whole seconds; up to 9 fractional digits become
// nanoseconds (extra digits are truncated).
func parseDecimalSeconds(s string) (time.Time, bool) {
	intPart, fracPart := s, ""
	if i := strings.IndexByte(s, '.'); i >= 0 {
		intPart, fracPart = s[:i], s[i+1:]
	}
	sec, err := strconv.ParseInt(intPart, 10, 64)
	if err != nil {
		return time.Time{}, false
	}
	var nsec int64
	if fracPart != "" {
		if len(fracPart) > 9 {
			fracPart = fracPart[:9]
		}
		f, err := strconv.ParseInt(fracPart, 10, 64)
		if err != nil {
			return time.Time{}, false
		}
		for i := len(fracPart); i < 9; i++ {
			f *= 10
		}
		nsec = f
	}
	return time.Unix(sec, nsec).UTC(), true
}
