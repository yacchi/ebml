// Package fragment is a per-fragment assembly layer over the streaming EBML
// cursor in package parser. It consumes a continuous Matroska byte stream —
// concatenated (possibly unknown-size) Segments — and emits one Fragment value
// per completed Cluster.
//
// The defining property (inherited from the cursor) is early emission: a
// Fragment is produced the moment its known-size Cluster is fully consumed,
// without waiting for the enclosing unknown-size Segment to close, for the next
// Segment's header to arrive, or for connection EOF. This removes both the
// boundary-wait and the end-of-stream tail latency that a whole-document parser
// would otherwise incur on a live stream.
//
// Inside a Segment the Tags and Tracks elements arrive before the Cluster; the
// assembler buffers them and attaches them to each Fragment as the Cluster
// completes. A Segment may contain multiple Clusters (each yields its own
// Fragment, sharing that Segment's Tags/Tracks) or no Tags at all (the Fragment
// then carries an empty tag map). Each Segment stands alone: no state leaks
// across Segment boundaries, so mid-stream metadata changes and dropped
// segments need no special handling.
//
// For an Amazon KVS GetMedia consumer, see examples/kvs-getmedia.
package fragment

import (
	"github.com/yacchi/ebml-reader/parser"
)

// Track is one decoded Matroska TrackEntry. Only a small subset of fields is
// decoded; absent fields keep their zero value.
type Track struct {
	Number  uint64 // TrackNumber
	Type    uint64 // TrackType (2 = audio)
	CodecID string // CodecID, e.g. "A_PCM/INT/LIT"
}

// Fragment is one assembled Matroska fragment: the Tags/Tracks metadata
// buffered from its Segment plus the contents of a single completed Cluster.
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
// Every SimpleTag in the Segment's Tags element is read through this generic
// accessor; interpreting a specific tag's meaning (naming conventions, value
// encoding) is left to the caller.
func (f *Fragment) Tag(name string) (string, bool) {
	v, ok := f.Tags[name]
	return v, ok
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
