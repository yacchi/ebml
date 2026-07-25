// Package fragment assembles a continuous Matroska byte stream into one Fragment
// per completed Cluster.
//
// It is an optional convenience: see package ext for the policy that applies to
// every package under ext/. In short, this package is built only on the exported
// API of parser (it pulls events from a parser.Cursor), matroska and ext/tree; it
// is deliberately outside the cross-language contract that spec/SPEC.md defines,
// and other-language ports are not expected to provide an equivalent.
//
// # Early emission
//
// The defining property, inherited from the cursor, is early emission: a Fragment
// is produced the moment its known-size Cluster is fully consumed, without
// waiting for the enclosing unknown-size Segment to close, for the next Segment's
// header to arrive, or for connection EOF. That removes both the boundary-wait
// and the end-of-stream tail latency a whole-document parser incurs on a live
// stream. Segment boundaries are found structurally -- the enclosing unknown-size
// Segment is closed when the next top-level element's header appears -- never by
// scanning the bytes for the EBML magic, which can occur inside PCM.
//
// # What a Fragment retains
//
// A Fragment holds two retained element trees: the Segment-level metadata that
// had completed before this Cluster, and the Cluster itself with its block
// payloads decoded into Blocks rather than duplicated in the tree. Retention is
// generic: every element is retained by ID, including elements no registry knows,
// so there is no allowlist to extend when a stream carries something new.
//
// # Two ways to read a value
//
// Values and Value are loose, extractive lookups: they return every element with
// an ID anywhere in the fragment, at any depth, ignoring containment. The Segment
// and Cluster trees are the strict, structural view, where Find states an exact
// path. They compose, because a loose lookup returns nodes -- Path, Parent and
// Ancestor tighten a loose result afterwards. See package ext/tree for the two
// modes in full.
//
// On top of both sit the accessors that add something the trees cannot state on
// their own -- a spec default, a unit conversion or a derivation: Tag, Tags,
// Tracks, Track, TrackByName, TimestampScale, ClusterTimestamp, BlockTime,
// StartTime, EndTime and TrackPCM.
//
// For an Amazon KVS GetMedia consumer, see go/kvs/examples/getmedia.
package fragment

import (
	"time"

	"github.com/yacchi/ebml/ext/tree"
	"github.com/yacchi/ebml/matroska"
	"github.com/yacchi/ebml/parser"
)

// DefaultTimestampScale is the Matroska default TimestampScale in nanoseconds,
// which applies when a Segment declares no Info>TimestampScale.
const DefaultTimestampScale uint64 = 1_000_000

// Fragment is one assembled Matroska fragment: a completed Cluster plus the
// Segment-level metadata that preceded it.
//
// Every Fragment of one Segment shares that Segment's tree, so reading metadata
// from two Fragments of a multi-Cluster Segment reads the same nodes rather than
// two copies. Nothing at all is shared across Segments.
type Fragment struct {
	// Segment is the enclosing Segment element, retaining the Segment-level
	// subtrees that had COMPLETED before this Cluster -- Info, Tracks, Tags,
	// SeekHead, whatever the stream carried -- as its children, in stream order.
	// Every child is a finished subtree: an element still being read when this
	// Cluster completed cannot be one of them.
	//
	// It is a view of a streaming Segment, not a document. Metadata is never
	// re-attributed: nothing is added to this Fragment on account of a later
	// Cluster, and the Fragment is never re-emitted. The node itself, though, is
	// the one every Fragment of this Segment shares, so a stream that places
	// further Segment-level metadata AFTER this Cluster extends it as it
	// continues. Matroska and KVS put the metadata describing a Cluster before it,
	// so a Fragment consumed as it arrives -- which is what this package is for --
	// sees a stable view; a consumer that hoards Fragments and inspects them much
	// later should copy what it needs when it receives them.
	Segment *tree.Element

	// Cluster is the completed Cluster element with its children retained, except
	// that SimpleBlock payloads are not: they are decoded into Blocks instead of
	// being held twice. Those SimpleBlock nodes are retained with Truncated set,
	// so their extent stays readable while their bytes are not.
	//
	// It is a root of its own: Cluster.Parent() is nil, deliberately, because
	// attaching it to Segment would accumulate every Cluster of a long
	// multi-Cluster Segment in a tree that every Fragment of it shares.
	Cluster *tree.Element

	// Blocks holds every SimpleBlock of the Cluster, decoded with
	// parser.ParseSimpleBlock, in stream order.
	//
	// A block that will not decode ends assembly by default. With
	// WithSkipContentErrors set it is instead absent from this slice and was
	// reported to that option's notify, while the Cluster tree still carries the
	// element -- so a fragment whose Blocks cover less than its Cluster's extent is
	// exactly the case that notify announced.
	Blocks []*parser.SimpleBlock
}

// Values returns every element with the given ID anywhere in this fragment's
// scope -- at any depth, under any parent, in stream order -- and nil when there
// is none. It searches the Segment tree and then the Cluster tree, so
// Segment-level metadata precedes Cluster contents.
//
// Scope is what this fragment had seen by the time its Cluster completed, not a
// whole-document view: elements of an earlier or later Segment are never in it,
// and neither is Segment-level metadata that the stream places after this
// Cluster.
//
// Containment is deliberately ignored: an ID that legitimately occurs under
// several parents yields all of its occurrences in one slice. That costs no
// precision, because the result is nodes and not bare scalars -- every returned
// element still carries Path, Parent and Ancestor, so a caller can start loose
// and tighten only where the document shape actually carries meaning:
//
//	for _, name := range frag.Values(matroska.IDName) {
//	    if entry := name.Ancestor(matroska.IDTrackEntry); entry.Exists() {
//	        // a track name, not some other element's Name
//	    }
//	}
func (f *Fragment) Values(id parser.ElementID) []*tree.Element {
	if f == nil {
		return nil
	}
	var out []*tree.Element
	for _, root := range []*tree.Element{f.Segment, f.Cluster} {
		root.Walk(func(node *tree.Element) bool {
			if node.ID == id {
				out = append(out, node)
			}
			return true
		})
	}
	return out
}

// Value returns the first element with the given ID in this fragment's scope, or
// nil when there is none. The scope and the looseness are those documented on
// Values.
//
// A nil result is safe to use: every ext/tree accessor is nil-safe, so
// f.Value(id).Bytes() and f.Value(id).AsUint() never panic on a miss.
func (f *Fragment) Value(id parser.ElementID) *tree.Element {
	for _, node := range f.Values(id) {
		return node
	}
	return nil
}

// Tag returns the TagString of the first SimpleTag whose TagName is name, and
// whether such a tag was present. See Tags for how the pairs are collected.
func (f *Fragment) Tag(name string) (string, bool) {
	if f == nil {
		return "", false
	}
	if name == "" {
		return "", false
	}
	for _, st := range f.Segment.Descendants(matroska.IDSimpleTag) {
		if st.Find(matroska.IDTagName).AsString() == name {
			return st.Find(matroska.IDTagString).AsString(), true
		}
	}
	return "", false
}

// Tags flattens the Segment's SimpleTag elements into a TagName -> TagString map.
// The map is built per call and owned by the caller, never aliasing the
// assembler's state; it is non-nil and empty for a Segment that carries no Tags.
//
// Collection is generic and depth-agnostic: every SimpleTag under the Segment
// counts, wherever it sits, including one nested inside another SimpleTag. A tag
// with an empty TagName is skipped, and a repeated TagName keeps its first
// occurrence, which is what Tag reports too. Interpreting a specific tag --
// naming conventions, value encoding -- is left to the caller, which is how AWS
// KVS metadata (AWS_KINESISVIDEO_FRAGMENT_NUMBER and friends) is read without
// this package knowing anything about it.
func (f *Fragment) Tags() map[string]string {
	out := make(map[string]string)
	if f == nil {
		return out
	}
	for _, st := range f.Segment.Descendants(matroska.IDSimpleTag) {
		name := st.Find(matroska.IDTagName).AsString()
		if name == "" {
			continue
		}
		if _, seen := out[name]; seen {
			continue
		}
		out[name] = st.Find(matroska.IDTagString).AsString()
	}
	return out
}

// Tracks returns the Segment's TrackEntry elements in stream order, and nil when
// the Segment declared no Tracks. This is strict access: only the TrackEntry
// children of a Tracks element count, so an element that merely looks like one
// elsewhere in the Segment is not a track.
func (f *Fragment) Tracks() []*tree.Element {
	if f == nil {
		return nil
	}
	return f.Segment.FindAll(matroska.IDTracks, matroska.IDTrackEntry)
}

// Track returns the TrackEntry whose TrackNumber is number, and whether it was
// found.
func (f *Fragment) Track(number uint64) (*tree.Element, bool) {
	for _, entry := range f.Tracks() {
		if n, err := entry.Find(matroska.IDTrackNumber).AsUint(); err == nil && n == number {
			return entry, true
		}
	}
	return nil, false
}

// TrackByName returns the TrackEntry whose Name is name, and whether it was
// found. It is how a stream that identifies its channels by name -- Amazon
// Connect's AUDIO_FROM_CUSTOMER and AUDIO_TO_CUSTOMER -- is read without assuming
// a track numbering.
//
// A TrackEntry that declares no Name element never matches, not even for an empty
// name: an absent element is not an empty one.
func (f *Fragment) TrackByName(name string) (*tree.Element, bool) {
	for _, entry := range f.Tracks() {
		if el := entry.Find(matroska.IDName); el.Exists() && el.AsString() == name {
			return entry, true
		}
	}
	return nil, false
}

// TimestampScale returns the Segment's Info>TimestampScale in nanoseconds,
// falling back to DefaultTimestampScale when the Segment carries no Info, no
// TimestampScale, or a zero one. It is the spec default made explicit, so a
// caller never has to know it.
func (f *Fragment) TimestampScale() uint64 {
	if f == nil {
		return DefaultTimestampScale
	}
	if v, err := f.Segment.Find(matroska.IDInfo, matroska.IDTimestampScale).AsUint(); err == nil && v != 0 {
		return v
	}
	return DefaultTimestampScale
}

// ClusterTimestamp returns the Cluster's Timestamp in TimestampScale units, and 0
// when the Cluster declared none. It is a raw tick count, not a duration:
// BlockTime is what turns ticks into time.
func (f *Fragment) ClusterTimestamp() uint64 {
	if f == nil {
		return 0
	}
	v, err := f.Cluster.Find(matroska.IDTimestamp).AsUint()
	if err != nil {
		return 0
	}
	return v
}

// BlockTime returns the absolute time of a block within its Segment:
//
//	(ClusterTimestamp + block.Timecode) * TimestampScale nanoseconds
//
// BOTH operands are in TimestampScale units -- the Cluster's Timestamp and the
// block's timecode relative to it -- so the sum is scaled exactly once. A block's
// timecode is signed, and a negative one legitimately places the block before its
// Cluster's timestamp, which is why the sum is computed as a signed tick count;
// the result is therefore negative when a Cluster near time zero carries such a
// block. It returns 0 for a nil block.
func (f *Fragment) BlockTime(b *parser.SimpleBlock) time.Duration {
	if f == nil || b == nil {
		return 0
	}
	ticks := int64(f.ClusterTimestamp()) + int64(b.Timecode)
	return time.Duration(ticks) * time.Duration(f.TimestampScale())
}

// StartTime returns the BlockTime of the fragment's FIRST block in stream order,
// and 0 when it carries none. It is a block START time: it says when the first
// block's content begins.
func (f *Fragment) StartTime() time.Duration {
	if f == nil || len(f.Blocks) == 0 {
		return 0
	}
	return f.BlockTime(f.Blocks[0])
}

// EndTime returns the BlockTime of the fragment's LAST block in stream order, and
// 0 when it carries none.
//
// It is that block's START time and deliberately EXCLUDES the block's own
// duration, which Matroska does not state for a SimpleBlock: how long the last
// block lasts follows from the codec and the frame count, which only the consumer
// knows. EndTime - StartTime therefore spans the block starts and is shorter than
// the media the fragment actually carries.
func (f *Fragment) EndTime() time.Duration {
	if f == nil || len(f.Blocks) == 0 {
		return 0
	}
	return f.BlockTime(f.Blocks[len(f.Blocks)-1])
}

// TrackPCM returns the concatenated frame bytes (the PCM payload) of every block
// whose TrackNumber matches trackNumber, in stream order. It returns nil when no
// block matches.
func (f *Fragment) TrackPCM(trackNumber uint64) []byte {
	if f == nil {
		return nil
	}
	var out []byte
	for _, b := range f.Blocks {
		if b == nil || b.TrackNumber != trackNumber {
			continue
		}
		for _, frame := range b.Frames {
			out = append(out, frame...)
		}
	}
	return out
}
