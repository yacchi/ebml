package kvs

import (
	"bytes"
	"errors"
	"io"
	"testing"

	"github.com/yacchi/ebml/internal/ebmltest"
	"github.com/yacchi/ebml/internal/kvsgen"
	"github.com/yacchi/ebml/matroska"
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

// Q3 in KVS-CONSUMER-FEEDBACK.md is settled: Metadata describes the fragment as
// it stood when its Cluster closed, so it carries the tags written before the
// Cluster and not those written after. No notification of later metadata is
// provided; a consumer that needs the continuation token reads it from
// ext/scope, whose finished scope arrives before the next fragment's payload.
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
	if m.ContinuationToken != "" {
		t.Fatalf("ContinuationToken = %q, want empty", m.ContinuationToken)
	}
	if m.MillisBehindNow != 0 {
		t.Fatalf("MillisBehindNow = %v, want zero", m.MillisBehindNow)
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
	if first != sentinel || second != sentinel {
		t.Fatalf("errors = %v and %v, want same sentinel", first, second)
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
