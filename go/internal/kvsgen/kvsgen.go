// Package kvsgen builds minimal, 100% synthetic Matroska/EBML byte streams that
// reproduce the shape of Amazon Connect KVS GetMedia fragments, from scratch.
//
// DATA SAFETY: every value here is fabricated. ContactId/InstanceId are fake
// RFC-4122-shaped UUIDs, PCM is a deterministic counter pattern, SegmentUUIDs and
// tokens are synthetic. Nothing is copied or derived from any real capture.
//
// Fragment shape (one per Segment):
//
//	EBML (known-size header)
//	Segment (UNKNOWN-size)
//	  Info?      { SegmentUUID, TimestampScale }
//	  Tracks     { TrackEntry { TrackNumber, TrackType, CodecID, Name } x2 }
//	  Tags?      { Tag { Targets, SimpleTag* (ProducerTimestamp, FragmentNumber,
//	                     ContinuationToken, ContactId?, InstanceId) } }
//	  Cluster (KNOWN-size) { Timestamp, SimpleBlock+ }
//
// The Cluster is known-size (closes via end_master the moment its bytes are
// consumed) while the Segment is unknown-size (closed only by the next top-level
// element or by EOF) — the tail-fix property the KVS reader needs.
package kvsgen

import (
	"encoding/binary"
	"math"

	"github.com/yacchi/ebml-reader/matroska"
	"github.com/yacchi/ebml-reader/parser"
)

// ---- Fake, synthetic identifiers (never real customer data) ----

const (
	fakeContactA     = "00000000-0000-4000-8000-000000000001"
	fakeContactB     = "00000000-0000-4000-8000-000000000002"
	fakeInstance     = "00000000-0000-4000-8000-0000000000aa"
	fakeContinuation = "0000000000000000000000000000000000000000000000000000000000000000"
	fakeCodecID      = "A_PCM/INT/LIT"
)

// defaultTimestampScale is the Matroska default TimestampScale in nanoseconds.
const defaultTimestampScale uint64 = 1000000

// Element IDs no registry knows, used by the unknown_elements fixture: a
// vendor-shaped leaf and a vendor-shaped master. Both are valid EBML IDs that the
// RFC 9559 table does not define, so the standard classifier reads either as one
// opaque binary leaf until a consumer registers them.
const (
	idUnregisteredLeaf   parser.ElementID = 0xEE
	idUnregisteredMaster parser.ElementID = 0x4FFF
)

// ---- Low-level EBML encoders ----

// encodeID returns the element ID bytes (big-endian, leading zero bytes trimmed);
// the ID value already carries its EBML length-marker bits.
func encodeID(id parser.ElementID) []byte {
	b := []byte{byte(id >> 24), byte(id >> 16), byte(id >> 8), byte(id)}
	start := 0
	for start < 3 && b[start] == 0 {
		start++
	}
	return append([]byte(nil), b[start:]...)
}

// encodeSize returns a minimal known-size EBML VINT for n (never all-ones, so it
// is never mistaken for unknown-size).
func encodeSize(n uint64) []byte {
	for l := 1; l <= 8; l++ {
		max := uint64(1)<<(7*uint(l)) - 1
		if n < max {
			v := n | (uint64(1) << (7 * uint(l)))
			out := make([]byte, l)
			for i := l - 1; i >= 0; i-- {
				out[i] = byte(v)
				v >>= 8
			}
			return out
		}
	}
	panic("kvsgen: size too large to encode")
}

// unknownSize is the 8-byte unknown-size VINT (0x01FFFFFFFFFFFFFF).
func unknownSize() []byte {
	return []byte{0x01, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF}
}

// encodeUint returns a minimal big-endian unsigned integer (at least 1 byte).
func encodeUint(v uint64) []byte {
	if v == 0 {
		return []byte{0x00}
	}

	n := 0
	for x := v; x > 0; x >>= 8 {
		n++
	}
	out := make([]byte, n)
	for i := n - 1; i >= 0; i-- {
		out[i] = byte(v)
		v >>= 8
	}
	return out
}

func encodeFloat(v float64) []byte {
	out := make([]byte, 8)
	binary.BigEndian.PutUint64(out, math.Float64bits(v))
	return out
}

func elem(id parser.ElementID, payload []byte) []byte {
	out := encodeID(id)
	out = append(out, encodeSize(uint64(len(payload)))...)
	return append(out, payload...)
}

func elemUnknown(id parser.ElementID, payload []byte) []byte {
	out := encodeID(id)
	out = append(out, unknownSize()...)
	return append(out, payload...)
}

func concat(parts ...[]byte) []byte {
	var out []byte
	for _, p := range parts {
		out = append(out, p...)
	}
	return out
}

// ---- Synthetic PCM ----

// pcm returns n bytes of a deterministic counter pattern (fake 8kHz/16bit audio).
func pcm(n int, seed byte) []byte {
	out := make([]byte, n)
	for i := range out {
		out[i] = seed + byte(i)
	}
	return out
}

// pcmWithEBMLMagic returns synthetic PCM that CONTAINS the 4 EBML magic bytes
// (0x1A 0x45 0xDF 0xA3). A byte-scanning splitter would false-trigger here; a
// size-driven cursor must not.
func pcmWithEBMLMagic(n int) []byte {
	out := pcm(n, 0x40)
	copy(out[8:], []byte{0x1A, 0x45, 0xDF, 0xA3})
	return out
}

// ---- Matroska element builders ----

func simpleTag(name, value string) []byte {
	return elem(matroska.IDSimpleTag, concat(
		elem(matroska.IDTagName, []byte(name)),
		elem(matroska.IDTagString, []byte(value)),
	))
}

func ebmlHeader() []byte {
	return elem(matroska.IDEBML, concat(
		elem(matroska.IDEBMLVersion, encodeUint(1)),
		elem(matroska.IDEBMLReadVersion, encodeUint(1)),
		elem(matroska.IDDocType, []byte("matroska")),
	))
}

// segmentUID returns a DETERMINISTIC 16-byte synthetic Segment UID derived from
// seed (never random, so fixtures stay reproducible and split-invariant). It is
// fabricated, not derived from any real KVS capture.
func segmentUID(seed byte) []byte {
	out := make([]byte, 16)
	for i := range out {
		out[i] = seed + byte(i)
	}
	return out
}

// infoElement builds a Segment Info element carrying a synthetic SegmentUUID and
// the given TimestampScale in nanoseconds (defaultTimestampScale is the Matroska
// default; a different value makes a fixture exercise the scaling path).
func infoElement(uid []byte, timestampScale uint64) []byte {
	return elem(matroska.IDInfo, concat(
		elem(matroska.IDSegmentUUID, uid),
		elem(matroska.IDTimestampScale, encodeUint(timestampScale)),
	))
}

// trackEntry builds one audio TrackEntry with a Name and standard audio
// parameters. The names
// (AUDIO_FROM_CUSTOMER / AUDIO_TO_CUSTOMER) are generic Amazon Connect track-name
// conventions — structural channel identifiers, not PII.
func trackEntry(number uint64, name string, audioParams bool) []byte {
	payload := concat(
		elem(matroska.IDTrackNumber, encodeUint(number)),
		elem(matroska.IDTrackType, encodeUint(2)), // 2 = audio
		elem(matroska.IDCodecID, []byte(fakeCodecID)),
		elem(matroska.IDName, []byte(name)),
	)
	if audioParams {
		payload = append(payload, elem(matroska.IDAudio, concat(
			elem(matroska.IDSamplingFrequency, encodeFloat(8000)),
			elem(matroska.IDChannels, encodeUint(1)),
			elem(matroska.IDBitDepth, encodeUint(16)),
		))...)
	}
	return elem(matroska.IDTrackEntry, payload)
}

func tracksElement(audioParams bool) []byte {
	return elem(matroska.IDTracks, concat(
		trackEntry(1, "AUDIO_FROM_CUSTOMER", audioParams),
		trackEntry(2, "AUDIO_TO_CUSTOMER", audioParams),
	))
}

// tagsElement builds a Tags element. If contactID is "" the ContactId SimpleTag
// is omitted (tagless-by-contact fragment).
func tagsElement(producerTS, fragmentNumber, contactID string) []byte {
	tags := concat(
		simpleTag("AWS_KINESISVIDEO_PRODUCER_TIMESTAMP", producerTS),
		simpleTag("AWS_KINESISVIDEO_FRAGMENT_NUMBER", fragmentNumber),
		simpleTag("AWS_KINESISVIDEO_CONTINUATION_TOKEN", fakeContinuation),
	)
	if contactID != "" {
		tags = append(tags, simpleTag("ContactId", contactID)...)
	}
	tags = append(tags, simpleTag("InstanceId", fakeInstance)...)

	tag := elem(matroska.IDTag, concat(
		elem(matroska.IDTargets, nil), // empty Targets master
		tags,
	))
	return elem(matroska.IDTags, tag)
}

// simpleBlock builds a SimpleBlock leaf: track VINT (=1), int16 BE relative
// timecode, flags byte, then PCM. The whole thing is one binary leaf.
func simpleBlock(timecode int16, audio []byte) []byte {
	content := []byte{0x81} // track number 1 as a 1-byte VINT
	content = append(content, byte(uint16(timecode)>>8), byte(uint16(timecode)))
	content = append(content, 0x00) // flags
	content = append(content, audio...)
	return elem(matroska.IDSimpleBlock, content)
}

// clusterElement builds a KNOWN-size Cluster with a Timestamp and blocks.
func clusterElement(clusterTS uint64, blocks ...[]byte) []byte {
	payload := elem(matroska.IDTimestamp, encodeUint(clusterTS))
	for _, b := range blocks {
		payload = append(payload, b...)
	}
	return elem(matroska.IDCluster, payload)
}

// ---- Fragment builder ----

type fragOpts struct {
	producerTS     string
	fragmentNumber string
	contactID      string // "" => no ContactId tag
	omitTags       bool   // omit the entire Tags element
	includeInfo    bool
	uidSeed        byte   // seed for the synthetic SegmentUUID when includeInfo is set
	timestampScale uint64 // 0 => defaultTimestampScale
	audioParams    bool
	clusterTS      uint64
	blocks         [][]byte
}

// fragment builds one EBML-header + unknown-size Segment fragment.
func fragment(o fragOpts) []byte {
	seg := []byte{}
	if o.includeInfo {
		scale := o.timestampScale
		if scale == 0 {
			scale = defaultTimestampScale
		}
		seg = append(seg, infoElement(segmentUID(o.uidSeed), scale)...)
	}
	seg = append(seg, tracksElement(o.audioParams)...)
	if !o.omitTags {
		seg = append(seg, tagsElement(o.producerTS, o.fragmentNumber, o.contactID)...)
	}
	seg = append(seg, clusterElement(o.clusterTS, o.blocks...)...)
	return concat(ebmlHeader(), elemUnknown(matroska.IDSegment, seg))
}
