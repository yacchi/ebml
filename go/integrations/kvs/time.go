package kvs

import (
	"time"

	"github.com/yacchi/ebml/ext/fragment"
	"github.com/yacchi/ebml/parser"
)

// ---- Wall-clock time for a KVS fragment ----
//
// Matroska does not fix the origin of a Segment's timeline, so ext/fragment's
// BlockTime, StartTime and EndTime are durations measured from whatever origin the
// stream chose, and this package cannot be spared the difference: KVS writes an
// EPOCH-BASED Cluster.Timestamp, which makes those durations offsets from the Unix
// epoch -- one real capture's StartTime read 493626h52m49.32s, about 56 years. That
// is not a bug in either layer. Which origin a stream uses is knowledge about the
// producer, so naming it belongs here, exactly as naming its tags does, and the four
// functions below are the same four accessors read as wall-clock time.
//
// They are for a KVS stream. Given a file whose first Cluster starts at zero they
// return times near the Unix epoch, faithfully, because that is what the bytes say.

// ClusterTime returns the fragment's Cluster.Timestamp as wall-clock time in UTC:
// the instant the Cluster's own timeline position names. It is the value a consumer
// usually wants for "when this fragment starts" at Cluster granularity, and it needs
// no block to be present.
//
// It returns the zero Time for a nil fragment. A fragment whose Cluster declared no
// Timestamp reads as 0 ticks, which is the epoch itself, not a zero Time -- an absent
// Timestamp and a Timestamp of zero are the same statement in Matroska.
func ClusterTime(f *fragment.Fragment) time.Time {
	if f == nil {
		return time.Time{}
	}
	return time.Unix(0, int64(f.ClusterTimestamp())*int64(f.TimestampScale())).UTC()
}

// BlockTime returns the wall-clock time in UTC of one block of the fragment,
// (ClusterTimestamp + block.Timecode) * TimestampScale nanoseconds after the epoch.
// A block's timecode is signed, so a block may legitimately fall before its
// Cluster's own time.
//
// It returns the zero Time for a nil fragment or a nil block.
func BlockTime(f *fragment.Fragment, b *parser.SimpleBlock) time.Time {
	if f == nil || b == nil {
		return time.Time{}
	}
	return time.Unix(0, int64(f.BlockTime(b))).UTC()
}

// StartTime returns the wall-clock time in UTC of the fragment's FIRST block in
// stream order, and the zero Time when it carries none -- so IsZero distinguishes
// "no media" from a time near the epoch, the same way Metadata's timestamps do.
func StartTime(f *fragment.Fragment) time.Time {
	if f == nil || len(f.Blocks) == 0 {
		return time.Time{}
	}
	return BlockTime(f, f.Blocks[0])
}

// EndTime returns the wall-clock time in UTC of the fragment's LAST block in stream
// order, and the zero Time when it carries none.
//
// It is that block's START time and EXCLUDES the block's own duration, which
// Matroska does not state for a SimpleBlock: how long the last block lasts follows
// from the codec and the frame count, which only the consumer knows. EndTime.Sub of
// StartTime therefore spans the block starts and is shorter than the media the
// fragment carries.
func EndTime(f *fragment.Fragment) time.Time {
	if f == nil || len(f.Blocks) == 0 {
		return time.Time{}
	}
	return BlockTime(f, f.Blocks[len(f.Blocks)-1])
}
