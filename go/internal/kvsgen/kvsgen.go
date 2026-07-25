// Package kvsgen builds minimal, 100% synthetic Matroska/EBML byte streams that
// reproduce the shape of Amazon Connect KVS GetMedia fragments, from scratch.
//
// Every byte is produced by package writer, the library's only EBML encoder: the
// generator owns the SHAPE of a fragment and nothing about the encoding, so a
// fixture is also a test of the writer, and the committed corpus is what proves the
// two agree.
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
	"bytes"

	"github.com/yacchi/ebml/matroska"
	"github.com/yacchi/ebml/parser"
	"github.com/yacchi/ebml/writer"
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

// ---- Stream construction ----

// stream builds one synthetic byte stream through a writer.Writer. It exists only
// to keep the fixture builders declarative: every method is one writer call whose
// error cannot happen here.
type stream struct {
	buf bytes.Buffer
	w   *writer.Writer
}

func newStream() *stream {
	s := &stream{}
	s.w = writer.New(&s.buf)
	return s
}

// must stops the generator on a writer error. The IDs here are constants from the
// registry and the payloads are tens of bytes, so every error the writer can report
// means this generator is wrong — a programmer error, not an input condition.
func must(err error) {
	if err != nil {
		panic("kvsgen: " + err.Error())
	}
}

// bytes finishes the document and returns it. It is called once, at the end of a
// fixture builder.
func (s *stream) bytes() []byte {
	must(s.w.Close())
	return s.buf.Bytes()
}

// master writes a KNOWN-size master over whatever body writes, which may be nil for
// an empty master. Its size VINT is minimal, since the writer buffers the subtree.
func (s *stream) master(id parser.ElementID, body func()) {
	must(s.w.StartMaster(id, writer.Buffered()))
	if body != nil {
		body()
	}
	must(s.w.EndMaster())
}

// unknownMaster writes a master with the 8-byte UNKNOWN-size marker, which is what
// a real KVS Segment carries: nothing in the bytes says where it ends.
func (s *stream) unknownMaster(id parser.ElementID, body func()) {
	must(s.w.StartMaster(id, writer.UnknownSize()))
	if body != nil {
		body()
	}
	must(s.w.EndMaster())
}

func (s *stream) binary(id parser.ElementID, payload []byte) { must(s.w.Binary(id, payload)) }

func (s *stream) uint(id parser.ElementID, v uint64) { must(s.w.Uint(id, v)) }

func (s *stream) str(id parser.ElementID, v string) { must(s.w.String(id, v)) }

func (s *stream) float(id parser.ElementID, v float64) { must(s.w.Float(id, v, writer.Float64)) }

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

func (s *stream) simpleTag(name, value string) {
	s.master(matroska.IDSimpleTag, func() {
		s.str(matroska.IDTagName, name)
		s.str(matroska.IDTagString, value)
	})
}

func (s *stream) ebmlHeader() {
	s.master(matroska.IDEBML, func() {
		s.uint(matroska.IDEBMLVersion, 1)
		s.uint(matroska.IDEBMLReadVersion, 1)
		s.str(matroska.IDDocType, "matroska")
	})
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

// info writes a Segment Info element carrying a synthetic SegmentUUID and the given
// TimestampScale in nanoseconds (defaultTimestampScale is the Matroska default; a
// different value makes a fixture exercise the scaling path).
func (s *stream) info(uid []byte, timestampScale uint64) {
	s.master(matroska.IDInfo, func() {
		s.binary(matroska.IDSegmentUUID, uid)
		s.uint(matroska.IDTimestampScale, timestampScale)
	})
}

// trackEntry writes one audio TrackEntry with a Name and standard audio
// parameters. The names
// (AUDIO_FROM_CUSTOMER / AUDIO_TO_CUSTOMER) are generic Amazon Connect track-name
// conventions — structural channel identifiers, not PII.
func (s *stream) trackEntry(number uint64, name string, audioParams bool) {
	s.master(matroska.IDTrackEntry, func() {
		s.uint(matroska.IDTrackNumber, number)
		s.uint(matroska.IDTrackType, 2) // 2 = audio
		s.str(matroska.IDCodecID, fakeCodecID)
		s.str(matroska.IDName, name)
		if audioParams {
			s.master(matroska.IDAudio, func() {
				s.float(matroska.IDSamplingFrequency, 8000)
				s.uint(matroska.IDChannels, 1)
				s.uint(matroska.IDBitDepth, 16)
			})
		}
	})
}

func (s *stream) tracks(audioParams bool) {
	s.master(matroska.IDTracks, func() {
		s.trackEntry(1, "AUDIO_FROM_CUSTOMER", audioParams)
		s.trackEntry(2, "AUDIO_TO_CUSTOMER", audioParams)
	})
}

// tags writes a Tags element. If contactID is "" the ContactId SimpleTag is omitted
// (tagless-by-contact fragment). omitIdentity omits both identity tags while
// retaining the ordinary fragment metadata.
func (s *stream) tags(producerTS, fragmentNumber, contactID string) {
	s.tagsWithIdentity(producerTS, fragmentNumber, contactID, false)
}

func (s *stream) tagsWithIdentity(producerTS, fragmentNumber, contactID string, omitIdentity bool) {
	s.master(matroska.IDTags, func() {
		s.master(matroska.IDTag, func() {
			s.master(matroska.IDTargets, nil) // empty Targets master
			s.simpleTag("AWS_KINESISVIDEO_PRODUCER_TIMESTAMP", producerTS)
			s.simpleTag("AWS_KINESISVIDEO_FRAGMENT_NUMBER", fragmentNumber)
			s.simpleTag("AWS_KINESISVIDEO_CONTINUATION_TOKEN", fakeContinuation)
			if !omitIdentity {
				if contactID != "" {
					s.simpleTag("ContactId", contactID)
				}
				s.simpleTag("InstanceId", fakeInstance)
			}
		})
	})
}

// block is one synthetic SimpleBlock: a relative timecode, which may be negative,
// and the PCM it carries.
type block struct {
	timecode int16
	audio    []byte
}

// simpleBlock writes a SimpleBlock leaf: track VINT (=1), int16 BE relative
// timecode, flags byte, then PCM. The whole thing is one binary leaf — the block
// structure lives inside the payload, not in EBML.
func (s *stream) simpleBlock(b block) {
	s.simpleBlockOnTrack(1, b)
}

func (s *stream) simpleBlockOnTrack(trackNumber uint64, b block) {
	content := []byte{0x80 | byte(trackNumber)} // track number as a 1-byte VINT
	content = append(content, byte(uint16(b.timecode)>>8), byte(uint16(b.timecode)))
	content = append(content, 0x00) // flags
	content = append(content, b.audio...)
	s.binary(matroska.IDSimpleBlock, content)
}

// cluster writes a KNOWN-size Cluster with a Timestamp and blocks.
func (s *stream) cluster(clusterTS uint64, blocks ...block) {
	s.master(matroska.IDCluster, func() {
		s.uint(matroska.IDTimestamp, clusterTS)
		for _, b := range blocks {
			s.simpleBlock(b)
		}
	})
}

func (s *stream) twoTrackCluster(clusterTS uint64, blocks ...struct {
	track uint64
	block
}) {
	s.master(matroska.IDCluster, func() {
		s.uint(matroska.IDTimestamp, clusterTS)
		for _, b := range blocks {
			s.simpleBlockOnTrack(b.track, b.block)
		}
	})
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
	omitIdentity   bool
	clusterTS      uint64
	blocks         []block
}

// fragment writes one EBML-header + unknown-size Segment fragment.
func (s *stream) fragment(o fragOpts) {
	s.ebmlHeader()
	s.unknownMaster(matroska.IDSegment, func() {
		if o.includeInfo {
			scale := o.timestampScale
			if scale == 0 {
				scale = defaultTimestampScale
			}
			s.info(segmentUID(o.uidSeed), scale)
		}
		s.tracks(o.audioParams)
		if !o.omitTags {
			s.tagsWithIdentity(o.producerTS, o.fragmentNumber, o.contactID, o.omitIdentity)
		}
		s.cluster(o.clusterTS, o.blocks...)
	})
}
