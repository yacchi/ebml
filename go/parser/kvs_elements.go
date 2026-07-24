package parser

// KVS / Matroska element IDs used by the Amazon Connect KVS GetMedia fragment
// shape. These extend the six EBML-header IDs in elements.go so the streaming
// cursor can classify a real per-fragment Segment { Info?, Tracks, Tags, Cluster }.
//
// IDs are the canonical Matroska element IDs (cross-checked against
// github.com/yacchi/go-mkvparse). Each ID value already carries its EBML
// length-marker bits, matching the encoding produced by parseElementID.
const (
	// Masters
	ElementIDSeekHead   uint32 = 0x114D9B74
	ElementIDInfo       uint32 = 0x1549A966
	ElementIDTracks     uint32 = 0x1654AE6B
	ElementIDTrackEntry uint32 = 0xAE
	ElementIDCluster    uint32 = 0x1F43B675
	ElementIDTags       uint32 = 0x1254C367
	ElementIDTag        uint32 = 0x7373
	ElementIDTargets    uint32 = 0x63C0
	ElementIDSimpleTag  uint32 = 0x67C8

	// Uint leaves
	ElementIDTimestampScale uint32 = 0x2AD7B1
	ElementIDTrackNumber    uint32 = 0xD7
	ElementIDTrackType      uint32 = 0x83
	ElementIDTimestamp      uint32 = 0xE7 // a.k.a. Cluster Timecode

	// String leaves (no dedicated string Kind; classified as KindBinary, which is
	// leaf-skippable via SkipPayload).
	ElementIDCodecID   uint32 = 0x86
	ElementIDTagName   uint32 = 0x45A3
	ElementIDTagString uint32 = 0x4487

	// Binary leaves
	ElementIDSimpleBlock uint32 = 0xA3
	ElementIDTagBinary   uint32 = 0x4485
)

// KVSNameForElementID returns a human-readable name for KVS/Matroska element
// IDs, falling back to the EBML-header names in elements.go.
func KVSNameForElementID(id uint32) string {
	switch id {
	case ElementIDSeekHead:
		return "SeekHead"
	case ElementIDInfo:
		return "Info"
	case ElementIDTracks:
		return "Tracks"
	case ElementIDTrackEntry:
		return "TrackEntry"
	case ElementIDCluster:
		return "Cluster"
	case ElementIDTags:
		return "Tags"
	case ElementIDTag:
		return "Tag"
	case ElementIDTargets:
		return "Targets"
	case ElementIDSimpleTag:
		return "SimpleTag"
	case ElementIDTimestampScale:
		return "TimestampScale"
	case ElementIDTrackNumber:
		return "TrackNumber"
	case ElementIDTrackType:
		return "TrackType"
	case ElementIDTimestamp:
		return "Timestamp"
	case ElementIDCodecID:
		return "CodecID"
	case ElementIDTagName:
		return "TagName"
	case ElementIDTagString:
		return "TagString"
	case ElementIDSimpleBlock:
		return "SimpleBlock"
	case ElementIDTagBinary:
		return "TagBinary"
	case ElementIDCRC32:
		return "CRC-32"
	case ElementIDVoid:
		return "Void"
	default:
		return NameForElementID(id)
	}
}

// KVSKindForElementID classifies KVS/Matroska element IDs into cursor Kinds.
// Master IDs return KindMaster; uint leaves return KindUint; string and binary
// leaves return KindBinary (leaf, skippable by size). The six EBML-header IDs
// keep working by delegating to KindForElementID. Unknown IDs default to the
// existing default (KindBinary), so an unrecognized leaf is still size-skippable.
func KVSKindForElementID(id uint32) Kind {
	switch id {
	case ElementIDSeekHead,
		ElementIDInfo,
		ElementIDTracks,
		ElementIDTrackEntry,
		ElementIDCluster,
		ElementIDTags,
		ElementIDTag,
		ElementIDTargets,
		ElementIDSimpleTag:
		return KindMaster
	case ElementIDTimestampScale,
		ElementIDTrackNumber,
		ElementIDTrackType,
		ElementIDTimestamp:
		return KindUint
	case ElementIDCodecID,
		ElementIDTagName,
		ElementIDTagString,
		ElementIDSimpleBlock,
		ElementIDTagBinary,
		ElementIDCRC32,
		ElementIDVoid:
		return KindBinary
	default:
		// EBML-header IDs (EBML, Segment, EBMLVersion, ...) and any unknown ID.
		return KindForElementID(id)
	}
}

// IsTopLevelElementID reports whether id begins a new top-level (level-0)
// element in a concatenated KVS GetMedia stream: an EBML header or a Segment.
// The streaming cursor uses this to detect the boundary of an unknown-size
// Segment (which is closed only by such a sibling, or by EOF) — it never scans
// the byte stream for the EBML magic, so PCM payloads that happen to contain
// the magic bytes cannot cause a spurious split.
func IsTopLevelElementID(id uint32) bool {
	return id == ElementIDEBML || id == ElementIDSegment
}
