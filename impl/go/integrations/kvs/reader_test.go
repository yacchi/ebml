package kvs

import (
	"bytes"
	"errors"
	"io"
	"slices"
	"testing"

	"github.com/yacchi/ebml/impl/go/ext/fragment"
	"github.com/yacchi/ebml/impl/go/internal/ebmltest"
	"github.com/yacchi/ebml/impl/go/internal/kvsgen"
	"github.com/yacchi/ebml/impl/go/matroska"
	"github.com/yacchi/ebml/impl/go/parser"
)

func fixture(t *testing.T, name string) []byte {
	t.Helper()
	for _, f := range kvsgen.BuildAll() {
		if f.Name == name {
			return f.Data
		}
	}
	t.Fatalf("fixture %q not found", name)
	return nil
}

func TestReaderOverCorpus(t *testing.T) {
	r := NewReader(bytes.NewReader(fixture(t, "topology_basic")))
	count := 0
	for {
		_, m, err := r.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		count++
		if m.FragmentNumber == "" || m.ProducerTimestamp.IsZero() {
			t.Fatalf("fragment %d lacks required metadata: %#v", count, m)
		}
	}
	if count != 1 {
		t.Fatalf("got %d fragments, want 1", count)
	}
}

func TestReaderSplitInvariant(t *testing.T) {
	raw := fixture(t, "topology_basic")
	all := readNumbers(NewReader(bytes.NewReader(raw)))
	one := readNumbers(NewReader(oneByteReader{r: bytes.NewReader(raw)}))
	if len(all) != len(one) {
		t.Fatalf("fragment counts differ: %d and %d", len(all), len(one))
	}
	for i := range all {
		if all[i] != one[i] {
			t.Fatalf("fragment %d differs: %q and %q", i, all[i], one[i])
		}
	}
}

// Q3 in KVS-CONSUMER-FEEDBACK.md asked how the metadata GetMedia writes AFTER a
// Cluster reaches a consumer, and the answer has CHANGED: Metadata carries it. A
// fragment is delivered once its Segment-level metadata has settled, and this
// package tells ext/fragment when that is -- MetadataComplete releases on the
// continuation token, which GetMedia writes last -- so the whole of a chunk's
// metadata is in one Metadata and no consumer has to hold fragments to assemble it.
//
// The earlier answer -- that Metadata is a snapshot at Cluster close, with later
// tags reachable only through ext/scope -- was retired by field measurement: a
// consumer reading tags at delivery lost identity keys silently, and the loss was
// read-size dependent, so it appeared only in production.
//
// The read size is deliberately the one that cuts just past the pre-Cluster Tags,
// which is what used to make this observable: the delivery point no longer depends
// on it.
func TestReaderConnectRealShapeMetadataScope(t *testing.T) {
	raw := fixture(t, "connect_real_shape")
	tagsID := []byte{0x12, 0x54, 0xc3, 0x67}
	offset := 0
	for i := 0; i < 4; i++ {
		next := bytes.Index(raw[offset:], tagsID)
		if next < 0 {
			t.Fatal("fixture does not contain the expected post-Cluster Tags element")
		}
		offset += next + len(tagsID)
	}
	r := NewReader(bytes.NewReader(raw), WithBufferSize(offset+1))
	_, m, err := r.Next()
	if err != nil {
		t.Fatal(err)
	}
	if m.FragmentNumber != "connect-real-0" {
		t.Fatalf("FragmentNumber = %q, want connect-real-0", m.FragmentNumber)
	}
	if m.ProducerTimestamp.IsZero() || m.ServerTimestamp.IsZero() {
		t.Fatalf("timestamps = producer %v, server %v; want both populated", m.ProducerTimestamp, m.ServerTimestamp)
	}
	// Written after the Cluster, and present because delivery waited for it.
	if m.ContinuationToken == "" {
		t.Fatal("ContinuationToken is empty; the post-Cluster metadata was not settled before delivery")
	}
	if _, ok := m.Tags[TagMillisBehindNow]; !ok {
		t.Fatalf("Tags is missing %s, also written after the Cluster", TagMillisBehindNow)
	}
}

// TestReaderCallerOverridesMetadataCompletion keeps NewReader's KVS default from
// being a decision the caller cannot revisit: an explicit predicate wins, including
// the always-true one that restores emission at the Cluster's end.
//
// The read size is what makes the override OBSERVABLE, and that is the point rather
// than an artifact: Fragment.Tags is a live view of a Segment tree that keeps
// growing, so an eagerly emitted fragment shows whatever had been parsed by the time
// the caller looked -- with one large read even the post-Cluster tags are already
// in. That read-size dependence is exactly why waiting is the default; a consumer
// asking for eager emission is asking for the snapshot and its caveats.
func TestReaderCallerOverridesMetadataCompletion(t *testing.T) {
	raw := fixture(t, "connect_real_shape")
	tagsID := []byte{0x12, 0x54, 0xc3, 0x67}
	offset := 0
	for i := 0; i < 4; i++ {
		next := bytes.Index(raw[offset:], tagsID)
		if next < 0 {
			t.Fatal("fixture does not contain the expected post-Cluster Tags element")
		}
		offset += next + len(tagsID)
	}
	r := NewReader(bytes.NewReader(raw), WithBufferSize(offset+1), WithAssemblerOptions(
		fragment.WithMetadataComplete(
			func(*fragment.Fragment, parser.ElementID) bool { return true },
		),
	))
	_, m, err := r.Next()
	if err != nil {
		t.Fatal(err)
	}
	if m.FragmentNumber != "connect-real-0" {
		t.Fatalf("FragmentNumber = %q, want connect-real-0", m.FragmentNumber)
	}
	if m.ContinuationToken != "" {
		t.Fatalf("ContinuationToken = %q; eager emission must not see post-Cluster tags", m.ContinuationToken)
	}
}

func readNumbers(r *Reader) []string {
	var out []string
	for {
		_, m, err := r.Next()
		if err == io.EOF {
			return out
		}
		if err != nil {
			panic(err)
		}
		out = append(out, m.FragmentNumber)
	}
}

func TestTagInheritancePartial(t *testing.T) {
	r := NewReader(bytes.NewReader(fixture(t, "partial_tags")))
	_, first, err := r.Next()
	if err != nil {
		t.Fatal(err)
	}
	_, second, err := r.Next()
	if err != nil {
		t.Fatal(err)
	}
	if first.Tags["ContactId"] == "" || second.Tags["ContactId"] != first.Tags["ContactId"] {
		t.Fatalf("inherited ContactId = %q, want %q", second.Tags["ContactId"], first.Tags["ContactId"])
	}
	r = NewReader(bytes.NewReader(fixture(t, "partial_tags")), WithoutTagInheritance())
	_, _, _ = r.Next()
	_, second, err = r.Next()
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := second.Tags["ContactId"]; ok {
		t.Fatal("ContactId inherited with WithoutTagInheritance")
	}
}

func TestInheritanceDoesNotCrossSegmentUUID(t *testing.T) {
	raw := ebmltest.Concat(testSegment(0x10, "first", "contact-a"), testSegment(0x20, "second", ""))
	r := NewReader(bytes.NewReader(raw))
	if _, _, err := r.Next(); err != nil {
		t.Fatal(err)
	}
	_, m, err := r.Next()
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := m.Tags["ContactId"]; ok {
		t.Fatal("ContactId crossed SegmentUUID boundary")
	}
}

func TestReaderSourceErrorIsSticky(t *testing.T) {
	sentinel := errors.New("source failed")
	r := NewReader(errorReader{err: sentinel})
	_, _, first := r.Next()
	_, _, second := r.Next()
	// Wrapped by the layer that owns the read, so matched with errors.Is.
	if !errors.Is(first, sentinel) || !errors.Is(second, sentinel) {
		t.Fatalf("errors = %v and %v, want the same source error both times", first, second)
	}
}

type oneByteReader struct{ r io.Reader }

func (r oneByteReader) Read(p []byte) (int, error) { return r.r.Read(p[:1]) }

type errorReader struct{ err error }

func (r errorReader) Read([]byte) (int, error) { return 0, r.err }

func testSegment(seed byte, number, contact string) []byte {
	simpleTag := func(name, value string) ebmltest.Node {
		return ebmltest.Master(matroska.IDSimpleTag,
			ebmltest.UTF8(matroska.IDTagName, name),
			ebmltest.UTF8(matroska.IDTagString, value))
	}
	tags := []ebmltest.Node{simpleTag(TagFragmentNumber, number)}
	if contact != "" {
		tags = append(tags, simpleTag("ContactId", contact))
	}
	cluster := ebmltest.Master(matroska.IDCluster, ebmltest.Uint(matroska.IDTimestamp, 0),
		ebmltest.Leaf(matroska.IDSimpleBlock, []byte{0x81, 0, 0, 0, 0}))
	return ebmltest.Encode(
		ebmltest.Master(matroska.IDEBML, ebmltest.Uint(matroska.IDEBMLVersion, 1)),
		ebmltest.UnknownMaster(matroska.IDSegment,
			ebmltest.Master(matroska.IDInfo, ebmltest.Leaf(matroska.IDSegmentUUID, bytes.Repeat([]byte{seed}, 16))),
			ebmltest.Master(matroska.IDTags, ebmltest.Master(matroska.IDTag, tags...)),
			cluster,
		),
	)
}

// truncatedTailStream is two complete KVS documents followed by one cut inside a
// SimpleBlock payload, which is what a dropped GetMedia connection looks like: the
// blocks before the cut decoded, the bytes after it never arrived.
func truncatedTailStream(t *testing.T) []byte {
	t.Helper()
	simpleTag := func(name, value string) ebmltest.Node {
		return ebmltest.Master(matroska.IDSimpleTag,
			ebmltest.UTF8(matroska.IDTagName, name),
			ebmltest.UTF8(matroska.IDTagString, value))
	}
	doc := func(seed byte, number string) []byte {
		return ebmltest.Encode(
			ebmltest.Master(matroska.IDEBML, ebmltest.Uint(matroska.IDEBMLVersion, 1)),
			ebmltest.UnknownMaster(matroska.IDSegment,
				ebmltest.Master(matroska.IDInfo,
					ebmltest.Leaf(matroska.IDSegmentUUID, bytes.Repeat([]byte{seed}, 16))),
				ebmltest.Master(matroska.IDTags,
					ebmltest.Master(matroska.IDTag, simpleTag(TagFragmentNumber, number))),
				// Unknown-size, because that is the only Cluster shape KVS sends.
				ebmltest.UnknownMaster(matroska.IDCluster,
					ebmltest.Uint(matroska.IDTimestamp, 0),
					ebmltest.Leaf(matroska.IDSimpleBlock, []byte{0x81, 0, 0, 0, 0x11, 0x22}),
					ebmltest.Leaf(matroska.IDSimpleBlock, []byte{0x81, 0, 10, 0, 0x33, 0x44}),
				),
			),
		)
	}
	var raw []byte
	raw = append(raw, doc(1, "100")...)
	raw = append(raw, doc(2, "101")...)
	cut := doc(3, "102")
	return append(raw, cut[:len(cut)-3]...)
}

// TestReaderDeliversSalvagedTailBeforeError pins the Reader's half of the salvage
// contract: the assembler returns its truncation error together with the Cluster
// the cut left open, and the Reader must hand that fragment over before reporting
// the error rather than dropping it. The error is not softened -- it is reported
// once the queue runs out, and stays sticky.
func TestReaderDeliversSalvagedTailBeforeError(t *testing.T) {
	r := NewReader(bytes.NewReader(truncatedTailStream(t)))

	var numbers []string
	var truncatedSeen int
	var err error
	for {
		f, m, e := r.Next()
		if e != nil {
			err = e
			break
		}
		numbers = append(numbers, m.FragmentNumber)
		if f.Truncated {
			truncatedSeen++
			if len(f.Blocks) != 1 {
				t.Errorf("salvaged fragment carries %d blocks, want the 1 that decoded", len(f.Blocks))
			}
		}
	}

	if err == nil || err == io.EOF {
		t.Fatalf("Next ended with %v, want the truncation reported", err)
	}
	if want := []string{"100", "101", "102"}; !slices.Equal(numbers, want) {
		t.Fatalf("delivered fragments %v, want %v including the salvaged one", numbers, want)
	}
	if truncatedSeen != 1 {
		t.Fatalf("%d fragments marked Truncated, want exactly the last one", truncatedSeen)
	}
	if _, _, e := r.Next(); e != err {
		t.Fatalf("Next after the truncation = %v, want the sticky %v", e, err)
	}
}

// errorTagStream is a healthy document followed by one carrying the in-band
// failure GetMedia reports through AWS_KINESISVIDEO_ERROR_CODE / _ERROR_ID.
func errorTagStream() []byte {
	simpleTag := func(name, value string) ebmltest.Node {
		return ebmltest.Master(matroska.IDSimpleTag,
			ebmltest.UTF8(matroska.IDTagName, name),
			ebmltest.UTF8(matroska.IDTagString, value))
	}
	doc := func(seed byte, tags ...ebmltest.Node) []byte {
		return ebmltest.Encode(
			ebmltest.Master(matroska.IDEBML, ebmltest.Uint(matroska.IDEBMLVersion, 1)),
			ebmltest.UnknownMaster(matroska.IDSegment,
				ebmltest.Master(matroska.IDInfo,
					ebmltest.Leaf(matroska.IDSegmentUUID, bytes.Repeat([]byte{seed}, 16))),
				ebmltest.Master(matroska.IDTags, ebmltest.Master(matroska.IDTag, tags...)),
				ebmltest.UnknownMaster(matroska.IDCluster,
					ebmltest.Uint(matroska.IDTimestamp, 0),
					ebmltest.Leaf(matroska.IDSimpleBlock, []byte{0x81, 0, 0, 0, 0x11})),
			),
		)
	}
	return ebmltest.Concat(
		doc(1, simpleTag(TagFragmentNumber, "100")),
		doc(2, simpleTag(TagFragmentNumber, "101"),
			simpleTag(TagErrorCode, "ARCHIVAL_ERROR"),
			simpleTag(TagErrorID, "3002")),
	)
}

// TestReaderReportsInBandStreamError pins the half of the contract a consumer
// cannot forget: the in-band error is REPORTED by Next, so a stream KVS stopped
// on an error can never read as a short clean end. It follows the fragment that
// carried it -- the same "the error comes after what completed" ordering the
// truncated tail uses -- so a caller that breaks on the error still received
// every fragment, and it is sticky afterwards.
func TestReaderReportsInBandStreamError(t *testing.T) {
	r := NewReader(bytes.NewReader(errorTagStream()))

	var numbers []string
	var err error
	for {
		_, m, e := r.Next()
		if e != nil {
			err = e
			break
		}
		numbers = append(numbers, m.FragmentNumber)
	}

	if want := []string{"100", "101"}; !slices.Equal(numbers, want) {
		t.Fatalf("delivered fragments %v, want %v -- the error must not swallow the one carrying it", numbers, want)
	}
	var streamErr *StreamError
	if !errors.As(err, &streamErr) {
		t.Fatalf("Next ended with %v, want a *StreamError", err)
	}
	if streamErr.Code != "ARCHIVAL_ERROR" || streamErr.ID != 3002 {
		t.Fatalf("StreamError = %+v, want code ARCHIVAL_ERROR id 3002", streamErr)
	}
	if _, _, e := r.Next(); e != err {
		t.Fatalf("Next after the stream error = %v, want the sticky %v", e, err)
	}
}

// TestReaderStreamErrorFragmentStillCarriesMetadata keeps Metadata.Err working
// for a caller that inspects the fragment itself: reporting through Next is an
// addition, not a replacement.
func TestReaderStreamErrorFragmentStillCarriesMetadata(t *testing.T) {
	r := NewReader(bytes.NewReader(errorTagStream()))
	if _, _, err := r.Next(); err != nil {
		t.Fatal(err)
	}
	f, m, err := r.Next()
	if err != nil {
		t.Fatalf("the fragment carrying the error tags must arrive without an error of its own, got %v", err)
	}
	if f == nil {
		t.Fatal("no fragment delivered for the erroring document")
	}
	if m.Err() == nil {
		t.Fatal("Metadata.Err() = nil on the fragment carrying the error tags")
	}
}
