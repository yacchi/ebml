package kvs

import (
	"fmt"
	"time"
)

// ---- A PutMedia producer recipe ----
//
// Everything above reads a KVS stream. A producer WRITES one, through
// go/writer, and the two ends do not want the same document shape. This is that
// difference, stated once, because it was re-derived from first principles by a
// consumer who then found the reasoning existed nowhere.
//
// THE SIZE STRATEGY.
//
//	Segment   unknown-size (writer.UnknownSize)   -- same as a real capture
//	Cluster   known-size   (writer.Buffered)      -- DIFFERENT from a real capture
//
// A Segment has no locatable end while the stream is live, so it is unknown-size
// on both sides and there is nothing to decide.
//
// The Cluster is the interesting one, and the reason is the caller's buffering
// policy rather than an AWS format requirement. writer.Buffered holds the
// Cluster's payload and emits it whole at EndMaster, which is useful when the
// caller deliberately wants one writer call per fragment. An unknown-size
// Cluster streams its children as they are written and is valid PutMedia input
// too. HTTP/TCP write boundaries do not define KVS fragments: PutMedia maps each
// MKV Cluster to one fragment, independently of how many writes carried it.
//
// Producers in the field emit UNKNOWN-SIZE Clusters -- which is why the fixture
// corpus models that shape throughout. Both are correct for their own purpose,
// and neither side may assume the other's: a reader that requires a known-size
// Cluster breaks on the field's own output, and this library's reading path
// deliberately requires nothing of the kind.
//
// WHAT Cluster.Timestamp MEANS is not a property of the document. It is decided
// by the x-amzn-fragment-timecode-type header the producer sends, which is why
// FragmentTimecodeTypeAbsolute and FragmentTimecodeTypeRelative are named here:
// the value is chosen by the caller's own PutMedia request, and this package
// holds no transport, but the choice changes what the caller must write into the
// element. Getting it wrong is not a parse failure -- it produces a document that
// reads perfectly and means something else, which is the single most expensive
// mistake recorded against this integration.

// FragmentTimecodeTypeAbsolute and FragmentTimecodeTypeRelative are the two values
// of the x-amzn-fragment-timecode-type header a PutMedia request carries. They are
// spelled here because each one decides what Cluster.Timestamp must contain:
//
//   - ABSOLUTE: Cluster.Timestamp is a wall-clock instant, counted in
//     TimestampScale ticks from the UNIX EPOCH. It is what the fixture corpus
//     models and what ClusterTime reads back. Use ClusterTimestamp to build it.
//   - RELATIVE: Cluster.Timestamp is measured from the
//     x-amzn-producer-start-timestamp of the PutMedia REQUEST. The service adds
//     that request header to the fragment timecode. A request may carry several
//     Clusters, so their timestamps continue to advance and MUST NOT reset to
//     zero at each fragment. No helper is needed for the elapsed tick count.
//
// Reading a RELATIVE stream with the wall-clock accessors in this package yields
// times near the Unix epoch, faithfully, because that is what the bytes say. The
// mismatch is not detectable from the document alone; only the request that
// created it says which convention was used.
const (
	FragmentTimecodeTypeAbsolute = "ABSOLUTE"
	FragmentTimecodeTypeRelative = "RELATIVE"
)

// ClusterTimestamp converts a wall-clock instant into the value a PutMedia
// producer writes as Cluster.Timestamp under FragmentTimecodeTypeAbsolute: the
// number of timestampScale-nanosecond ticks since the Unix epoch. It is the
// inverse of ClusterTime, and exists for the same reason that function does --
// Matroska does not fix the origin of a Segment's timeline, so the epoch basis is
// knowledge about the producer and belongs with the producer's other conventions.
//
// timestampScale is the value the document declares in Info.TimestampScale, in
// nanoseconds; pass fragment.DefaultTimestampScale (1 ms) unless the producer
// declares something else, and pass the SAME value it declares. A zero scale is
// rejected rather than defaulted, because silently assuming a scale the document
// does not state is how a timeline ends up off by a factor of a million.
//
// This function performs the Matroska time conversion only. It does not validate
// the separate PutMedia service constraints on TimestampScale or on the document
// that carries it.
//
// A time before the Unix epoch is rejected: Cluster.Timestamp is unsigned in
// Matroska, so there is no encoding for it and truncating to zero would place the
// fragment at an instant it does not name.
func ClusterTimestamp(t time.Time, timestampScale uint64) (uint64, error) {
	if timestampScale == 0 {
		return 0, fmt.Errorf("kvs: cluster timestamp for %s: TimestampScale is zero", t.UTC().Format(time.RFC3339Nano))
	}
	nanos := t.UnixNano()
	if nanos < 0 {
		return 0, fmt.Errorf("kvs: cluster timestamp for %s: before the Unix epoch", t.UTC().Format(time.RFC3339Nano))
	}
	return uint64(nanos) / timestampScale, nil
}
