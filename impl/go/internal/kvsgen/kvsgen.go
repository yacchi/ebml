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
//	  Cluster (UNKNOWN-size) { Timestamp, SimpleBlock+ }
//
// Real KVS Clusters are unknown-size. Their closure comes from the RFC 9559
// deny-only child rule: the first registered element that cannot be a Cluster
// child closes the Cluster, while the enclosing Segment remains unknown-size.
//
// Two track shapes exist here on purpose. The generic one above is well-formed:
// it carries an Audio master and a CodecID that matches its payload, which is
// what a fixture about scaling or unknown elements should be built from. The
// Amazon Connect PROFILE (connectFragment and the connect* builders) is what the
// field actually sends — Info with Title and MuxingApp = WritingApp, TrackEntry
// with NO Audio master and a codec declaration that contradicts the payload, one
// tag group per Tags element, and an absolute epoch Cluster Timestamp. Fixtures
// that model Connect are built from the profile; degrading every fixture to it
// would cost the corpus its well-formed cases.
package kvsgen

import (
	"bytes"

	"github.com/yacchi/ebml/impl/go/internal/ebmltest"
	"github.com/yacchi/ebml/impl/go/matroska"
	"github.com/yacchi/ebml/impl/go/parser"
	"github.com/yacchi/ebml/impl/go/writer"
)

// ---- Fake, synthetic identifiers (never real customer data) ----

const (
	fakeContactA     = "00000000-0000-4000-8000-000000000001"
	fakeContactB     = "00000000-0000-4000-8000-000000000002"
	fakeContactC     = "00000000-0000-4000-8000-000000000003"
	fakeInstance     = "00000000-0000-4000-8000-0000000000aa"
	fakeContinuation = "0000000000000000000000000000000000000000000000000000000000000000"
	fakeCodecID      = "A_PCM/INT/LIT"
)

// The Amazon Connect PROFILE: the values a real GetMedia capture carries, as
// distinct from the well-formed generic track the other fixtures use.
//
// connectCodecID and connectCodecPrivate are the field's own contradiction. A
// capture declares AAC with a two-byte AudioSpecificConfig while the SimpleBlock
// payload is 8 kHz 16-bit little-endian L16 PCM, which the MimeType tag states
// correctly. The declaration is simply wrong, so a consumer that dispatches a
// decoder on CodecID decodes noise; the corpus models the wrong declaration on
// purpose, because a consumer must be able to ignore it.
const (
	connectCodecID  = "A_AAC"
	connectMimeType = "audio/L16;rate=8000;channels=1"
	// The modelled fact is the SHAPE of the producer's self-identification: Info
	// carries a Title, and MuxingApp and WritingApp are BOTH present and EQUAL to
	// one another -- so a consumer that expects them to differ, or that expects a
	// Title only on a file rather than on a live stream, is wrong on both counts.
	//
	// The VALUES are deliberately synthetic. A real producer's name and build
	// string identify somebody's deployment rather than the format, and a fixture
	// that carried them would invite exactly the matching this library says not to
	// do: the shape is the contract, the vendor string never is.
	connectTitle = "Synthetic KVS Producer"
	connectApp   = "Synthetic KVS Producer 0.0.0 TEST 0.0"
)

// connectCodecPrivate is the 2-byte CodecPrivate that accompanies connectCodecID.
// Read as an AAC AudioSpecificConfig it decodes to AAC-LC, 48 kHz, stereo, which
// the payload is not either -- it is 8 kHz mono L16 PCM. The whole declaration is
// inert and the fixture proves a consumer can carry on regardless.
var connectCodecPrivate = []byte{0x11, 0x90}

// Track names are structural channel identifiers, not PII: Amazon Connect names
// one track per direction of the call.
const (
	trackNameFromCustomer = "AUDIO_FROM_CUSTOMER"
	trackNameToCustomer   = "AUDIO_TO_CUSTOMER"
)

// The AUDIO_*_CUSTOMER tag VALUES are these constants whatever the Tracks element
// says the numbering is. They therefore identify nothing: TrackEntry Name is the
// only authoritative direction mapping.
const (
	audioTagValueToCustomer   = "1"
	audioTagValueFromCustomer = "2"
)

// connectTrackUID returns the synthetic TrackUID for a track NAME. In the field
// the UID travels with the NAME rather than with the number, which is why
// track_order_swapped can renumber the two tracks without renaming them.
func connectTrackUID(name string) uint64 {
	switch name {
	case trackNameFromCustomer:
		return 0xA001
	case trackNameToCustomer:
		return 0xA002
	}
	panic("kvsgen: no synthetic TrackUID for track name " + name)
}

// defaultTimestampScale is the Matroska default TimestampScale in nanoseconds.
const defaultTimestampScale uint64 = 1000000

// Element IDs no registry knows, used by the unknown_elements fixture: a
// vendor-shaped leaf and a vendor-shaped master. Both are valid EBML IDs that the
// RFC 9559 table does not define, so the standard classifier reads either as one
// opaque binary leaf until a consumer registers them. They come from
// internal/ebmltest, which is the single place this repository reserves
// unassigned IDs, and internal/specconform checks them against the published
// schema so this fixture's premise cannot quietly stop being true.
const (
	idUnregisteredLeaf   = ebmltest.UnassignedLeafID
	idUnregisteredMaster = ebmltest.UnassignedMasterID
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

func (s *stream) utf8(id parser.ElementID, v string) { must(s.w.UTF8(id, v)) }

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

// connectInfo writes the Segment Info an Amazon Connect capture carries: a
// SegmentUUID, the default TimestampScale, and the producing SDK's Title,
// MuxingApp and WritingApp, the last two being the same string. Nothing here is
// optional in the field, so a consumer that assumes Info holds only the two
// values the generic fixtures carry meets three more elements it must skip.
func (s *stream) connectInfo(uid []byte) {
	s.master(matroska.IDInfo, func() {
		s.binary(matroska.IDSegmentUUID, uid)
		s.uint(matroska.IDTimestampScale, defaultTimestampScale)
		s.utf8(matroska.IDTitle, connectTitle)
		s.utf8(matroska.IDMuxingApp, connectApp)
		s.utf8(matroska.IDWritingApp, connectApp)
	})
}

// connectTrackEntry writes one TrackEntry in the Amazon Connect profile: NO Audio
// master element at all, and the misdeclared codec. A consumer that reads sampling
// frequency, channel count or bit depth out of the track finds nothing to read;
// the MimeType tag is where the capture states them.
func (s *stream) connectTrackEntry(number uint64, name string) {
	s.master(matroska.IDTrackEntry, func() {
		s.uint(matroska.IDTrackNumber, number)
		s.uint(matroska.IDTrackUID, connectTrackUID(name))
		s.uint(matroska.IDTrackType, 2) // 2 = audio
		s.str(matroska.IDCodecID, connectCodecID)
		s.binary(matroska.IDCodecPrivate, connectCodecPrivate)
		s.str(matroska.IDName, name)
	})
}

// connectTracks writes the two Connect tracks IN THE GIVEN ORDER, numbering them
// from 1. The order is a per-capture fact, not a constant.
func (s *stream) connectTracks(names ...string) {
	s.master(matroska.IDTracks, func() {
		for i, name := range names {
			s.connectTrackEntry(uint64(i+1), name)
		}
	})
}

// trackEntry writes one audio TrackEntry with a Name and standard audio
// parameters. It is the WELL-FORMED track the generic fixtures want, and is
// deliberately not the Connect profile above: a fixture about unknown elements or
// timestamp scaling should not also be carrying a broken codec declaration. The
// names (AUDIO_FROM_CUSTOMER / AUDIO_TO_CUSTOMER) are generic Amazon Connect
// track-name conventions — structural channel identifiers, not PII.
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
		s.trackEntry(1, trackNameFromCustomer, audioParams)
		s.trackEntry(2, trackNameToCustomer, audioParams)
	})
}

type tagPair struct {
	name, value string
}

// connectIdentityTags writes the identity Tags element a Connect capture repeats
// before and after its Cluster. The AUDIO_*_CUSTOMER values are the constants,
// which is what makes them useless as a mapping.
func (s *stream) connectIdentityTags(contactID string) {
	pairs := []tagPair{
		{"ContactId", contactID},
		{"InstanceId", fakeInstance},
		{"MimeType", connectMimeType},
		{"AUDIO_TO_CUSTOMER", audioTagValueToCustomer},
		{"AUDIO_FROM_CUSTOMER", audioTagValueFromCustomer},
	}
	s.tagsWithPairs(pairs...)
}

// connectFragmentTags writes the fragment-identifying Tags element, which precedes
// the Cluster.
func (s *stream) connectFragmentTags(fragmentNumber, producerTS string) {
	s.tagsWithPairs(
		tagPair{"AWS_KINESISVIDEO_FRAGMENT_NUMBER", fragmentNumber},
		tagPair{"AWS_KINESISVIDEO_SERVER_TIMESTAMP", producerTS},
		tagPair{"AWS_KINESISVIDEO_PRODUCER_TIMESTAMP", producerTS},
	)
}

// connectTrailingTags writes the Tags element that only ever appears AFTER the
// Cluster, carrying the continuation token the service adds last.
func (s *stream) connectTrailingTags() {
	s.tagsWithPairs(
		tagPair{"AWS_KINESISVIDEO_MILLIS_BEHIND_NOW", "0"},
		tagPair{"AWS_KINESISVIDEO_CONTINUATION_TOKEN", fakeContinuation},
	)
}

func (s *stream) tagsWithPairs(pairs ...tagPair) {
	s.master(matroska.IDTags, func() {
		s.master(matroska.IDTag, func() {
			s.master(matroska.IDTargets, nil)
			for _, pair := range pairs {
				s.simpleTag(pair.name, pair.value)
			}
		})
	})
}

// tags writes a Tags element. If contactID is "" the ContactId SimpleTag is omitted
// (tagless-by-contact fragment). omitIdentity omits both identity tags while
// retaining the ordinary fragment metadata.
func (s *stream) tags(producerTS, fragmentNumber, contactID string) {
	s.tagsWithIdentity(producerTS, fragmentNumber, contactID, false)
}

func (s *stream) tagsWithIdentity(producerTS, fragmentNumber, contactID string, omitIdentity bool) {
	pairs := []tagPair{
		{"AWS_KINESISVIDEO_PRODUCER_TIMESTAMP", producerTS},
		{"AWS_KINESISVIDEO_FRAGMENT_NUMBER", fragmentNumber},
		{"AWS_KINESISVIDEO_CONTINUATION_TOKEN", fakeContinuation},
	}
	if !omitIdentity {
		if contactID != "" {
			pairs = append(pairs, tagPair{"ContactId", contactID})
		}
		pairs = append(pairs, tagPair{"InstanceId", fakeInstance})
	}
	s.tagsWithPairs(pairs...)
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

func (s *stream) unknownCluster(clusterTS uint64, blocks ...block) {
	s.unknownMaster(matroska.IDCluster, func() {
		s.uint(matroska.IDTimestamp, clusterTS)
		for _, b := range blocks {
			s.simpleBlock(b)
		}
	})
}

// trackBlock is one SimpleBlock together with the track it belongs to.
type trackBlock struct {
	track uint64
	block
}

func (s *stream) unknownTwoTrackCluster(clusterTS uint64, blocks ...trackBlock) {
	s.unknownMaster(matroska.IDCluster, func() {
		s.uint(matroska.IDTimestamp, clusterTS)
		for _, b := range blocks {
			s.simpleBlockOnTrack(b.track, b.block)
		}
	})
}

// ---- Fragment builder ----

type clusterSizeStrategy uint8

const (
	unknownClusterSize clusterSizeStrategy = iota
	knownClusterSize
)

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
	clusterSize    clusterSizeStrategy
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
		if o.clusterSize == knownClusterSize {
			s.cluster(o.clusterTS, o.blocks...)
		} else {
			s.unknownCluster(o.clusterTS, o.blocks...)
		}
	})
}

// connectFragOpts describes one fragment in the Amazon Connect PROFILE: the Info,
// Tracks and Tags layout a real GetMedia capture carries, with only the values a
// capture varies left open.
type connectFragOpts struct {
	trackOrder     []string // Tracks order; the first name is track 1
	uidSeed        byte
	contactID      string // "" => the ContactId SimpleTag is omitted
	fragmentNumber string
	producerTS     string
	clusterTS      uint64 // ABSOLUTE epoch milliseconds, as Connect sends it
	blocks         []trackBlock
	omitTags       bool // omit every Tags element (a tagless fragment)
}

// connectFragment writes one fragment in the Connect profile: two Tags elements
// before the Cluster and two after, one tag group per Tags element.
func (s *stream) connectFragment(o connectFragOpts) {
	s.ebmlHeader()
	s.unknownMaster(matroska.IDSegment, func() {
		s.connectInfo(segmentUID(o.uidSeed))
		s.connectTracks(o.trackOrder...)
		if !o.omitTags {
			s.connectIdentityTags(o.contactID)
			s.connectFragmentTags(o.fragmentNumber, o.producerTS)
		}
		s.unknownTwoTrackCluster(o.clusterTS, o.blocks...)
		if !o.omitTags {
			s.connectIdentityTags(o.contactID)
			s.connectTrailingTags()
		}
	})
}
