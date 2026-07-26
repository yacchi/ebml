package kvs

import (
	"bytes"
	"errors"
	"io"
	"testing"
	"time"

	"github.com/yacchi/ebml/impl/go/internal/ebmltest"
	"github.com/yacchi/ebml/impl/go/matroska"
)

// --- 1. Metadata.Err() typed-nil trap -------------------------------------

func TestInvariantErrTypedNil(t *testing.T) {
	m := Metadata{Tags: map[string]string{}}
	if m.Err() != nil {
		t.Fatalf("Err() = %#v, want untyped nil (m.Err() != nil check)", m.Err())
	}
	// Also check via the Reader path with a real fragment carrying no Tags at all.
	raw := testSegment(0x30, "no-error", "")
	r := NewReader(bytes.NewReader(raw))
	_, meta, err := r.Next()
	if err != nil {
		t.Fatal(err)
	}
	if meta.Err() != nil {
		t.Fatalf("Reader-produced Err() = %#v, want nil", meta.Err())
	}
}

// --- 2(g). Mutating returned Metadata.Tags must not corrupt reader history --

func TestInvariantTagsMapNotAliasedWithHistory(t *testing.T) {
	raw := ebmltest.Concat(testSegment(0x40, "one", "orig-contact"), testSegment2(0x40, "two"))
	r := NewReader(bytes.NewReader(raw))

	_, first, err := r.Next()
	if err != nil {
		t.Fatal(err)
	}
	// Poison the map the caller was handed back.
	first.Tags["ContactId"] = "POISONED"
	first.Tags["Injected"] = "evil"

	_, second, err := r.Next()
	if err != nil {
		t.Fatal(err)
	}
	if second.Tags["ContactId"] != "orig-contact" {
		t.Fatalf("second.Tags[ContactId] = %q, want %q (internal history corrupted by external mutation of previous Metadata.Tags)",
			second.Tags["ContactId"], "orig-contact")
	}
	if _, ok := second.Tags["Injected"]; ok {
		t.Fatalf("second.Tags contains %q=%q injected via mutation of a previously returned Metadata.Tags map",
			"Injected", second.Tags["Injected"])
	}
}

// --- 3. Typed fields derive from effective (inherited) tags ---------------

func TestInvariantTypedFieldsReflectInheritedTags(t *testing.T) {
	simpleTag := func(name, value string) ebmltest.Node {
		return ebmltest.Master(matroska.IDSimpleTag,
			ebmltest.UTF8(matroska.IDTagName, name),
			ebmltest.UTF8(matroska.IDTagString, value))
	}
	seg := func(seed byte, tags ...ebmltest.Node) []byte {
		cluster := ebmltest.Master(matroska.IDCluster, ebmltest.Uint(matroska.IDTimestamp, 0),
			ebmltest.Leaf(matroska.IDSimpleBlock, []byte{0x81, 0, 0, 0, 0}))
		children := []ebmltest.Node{
			ebmltest.Master(matroska.IDInfo, ebmltest.Leaf(matroska.IDSegmentUUID, bytes.Repeat([]byte{seed}, 16))),
		}
		if len(tags) > 0 {
			children = append(children, ebmltest.Master(matroska.IDTags, ebmltest.Master(matroska.IDTag, tags...)))
		}
		children = append(children, cluster)
		return ebmltest.Encode(
			ebmltest.Master(matroska.IDEBML, ebmltest.Uint(matroska.IDEBMLVersion, 1)),
			ebmltest.UnknownMaster(matroska.IDSegment, children...),
		)
	}

	frag1 := seg(0x50, simpleTag(TagFragmentNumber, "frag-one"))
	// frag2 carries no FRAGMENT_NUMBER tag at all - must be inherited.
	frag2 := seg(0x50)

	raw := ebmltest.Concat(frag1, frag2)
	r := NewReader(bytes.NewReader(raw))

	_, first, err := r.Next()
	if err != nil {
		t.Fatal(err)
	}
	if first.FragmentNumber != "frag-one" {
		t.Fatalf("first.FragmentNumber = %q, want %q", first.FragmentNumber, "frag-one")
	}

	_, second, err := r.Next()
	if err != nil {
		t.Fatal(err)
	}
	if second.FragmentNumber != "frag-one" {
		t.Fatalf("second (typed) FragmentNumber = %q, want inherited %q", second.FragmentNumber, "frag-one")
	}
}

// --- 2(b). Same key present on both fragments: fragment 2's value wins ----

func TestInvariantInheritanceOverwriteTakesLatestValue(t *testing.T) {
	raw := ebmltest.Concat(testSegment(0x60, "num-1", "contact-A"), testSegment(0x60, "num-2", "contact-B"))
	r := NewReader(bytes.NewReader(raw))

	if _, _, err := r.Next(); err != nil {
		t.Fatal(err)
	}
	_, second, err := r.Next()
	if err != nil {
		t.Fatal(err)
	}
	if second.Tags["ContactId"] != "contact-B" {
		t.Fatalf("second.Tags[ContactId] = %q, want fragment 2's own value %q", second.Tags["ContactId"], "contact-B")
	}
	if second.FragmentNumber != "num-2" {
		t.Fatalf("second.FragmentNumber = %q, want %q", second.FragmentNumber, "num-2")
	}
}

// --- 2(d). Fragment with no SegmentUUID neither inherits nor pollutes -----

func TestInvariantNoSegmentUUIDNeitherInheritsNorPollutes(t *testing.T) {
	simpleTag := func(name, value string) ebmltest.Node {
		return ebmltest.Master(matroska.IDSimpleTag,
			ebmltest.UTF8(matroska.IDTagName, name),
			ebmltest.UTF8(matroska.IDTagString, value))
	}
	// Fragment with a SegmentUUID and a ContactId tag.
	withUUID := ebmltest.Encode(
		ebmltest.Master(matroska.IDEBML, ebmltest.Uint(matroska.IDEBMLVersion, 1)),
		ebmltest.UnknownMaster(matroska.IDSegment,
			ebmltest.Master(matroska.IDInfo, ebmltest.Leaf(matroska.IDSegmentUUID, bytes.Repeat([]byte{0x70}, 16))),
			ebmltest.Master(matroska.IDTags, ebmltest.Master(matroska.IDTag,
				simpleTag(TagFragmentNumber, "with-uuid"), simpleTag("ContactId", "contact-X"))),
			ebmltest.Master(matroska.IDCluster, ebmltest.Uint(matroska.IDTimestamp, 0),
				ebmltest.Leaf(matroska.IDSimpleBlock, []byte{0x81, 0, 0, 0, 0})),
		),
	)
	// Fragment with NO SegmentUUID and no ContactId of its own.
	noUUID := ebmltest.Encode(
		ebmltest.Master(matroska.IDEBML, ebmltest.Uint(matroska.IDEBMLVersion, 1)),
		ebmltest.UnknownMaster(matroska.IDSegment,
			ebmltest.Master(matroska.IDTags, ebmltest.Master(matroska.IDTag,
				simpleTag(TagFragmentNumber, "no-uuid"))),
			ebmltest.Master(matroska.IDCluster, ebmltest.Uint(matroska.IDTimestamp, 0),
				ebmltest.Leaf(matroska.IDSimpleBlock, []byte{0x81, 0, 0, 0, 0})),
		),
	)

	raw := ebmltest.Concat(withUUID, noUUID)
	r := NewReader(bytes.NewReader(raw))

	if _, _, err := r.Next(); err != nil {
		t.Fatal(err)
	}
	_, second, err := r.Next()
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := second.Tags["ContactId"]; ok {
		t.Fatalf("fragment with no SegmentUUID inherited ContactId=%q, want absent", second.Tags["ContactId"])
	}
	if second.FragmentNumber != "no-uuid" {
		t.Fatalf("second.FragmentNumber = %q, want %q", second.FragmentNumber, "no-uuid")
	}
}

// --- 4. Reader error/EOF state machine: sticky error, no further reads ----

type countingErrorReader struct {
	err   error
	count int
}

func (r *countingErrorReader) Read([]byte) (int, error) {
	r.count++
	return 0, r.err
}

func TestInvariantStickyErrorDoesNotReadAgain(t *testing.T) {
	sentinel := errors.New("boom")
	src := &countingErrorReader{err: sentinel}
	r := NewReader(src)

	_, _, err1 := r.Next()
	_, _, err2 := r.Next()
	_, _, err3 := r.Next()

	// The source's error is wrapped by the layer that owns the read -- as
	// stream.Stream wraps its own -- so it is matched with errors.Is, never by
	// identity or message text.
	if !errors.Is(err1, sentinel) || !errors.Is(err2, sentinel) || !errors.Is(err3, sentinel) {
		t.Fatalf("errors = %v, %v, %v; want the source's error every time", err1, err2, err3)
	}
	if src.count != 1 {
		t.Fatalf("underlying Read called %d times, want exactly 1 (subsequent Next calls must not read again)", src.count)
	}
}

// --- 4. io.EOF stays io.EOF after first return, and tail fragments from
// Finalize are delivered BEFORE io.EOF, not lost. -------------------------

// tailOnlyReader hands out a full stream but only reveals it needs Finalize
// to flush by returning the last bytes together with io.EOF, exactly as a
// live GetMedia body ending mid-cluster would behave for an unknown-size
// Segment: the assembler only emits the last fragment on Finalize.
func TestInvariantEOFStableAndTailDeliveredBeforeEOF(t *testing.T) {
	raw := fixture(t, "topology_basic")
	r := NewReader(bytes.NewReader(raw))

	var got []string
	for {
		_, m, err := r.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		got = append(got, m.FragmentNumber)
	}
	if len(got) == 0 {
		t.Fatal("no fragments delivered; tail fragment (flushed only by Finalize) appears lost")
	}
	// EOF must stay stable.
	for i := 0; i < 3; i++ {
		if _, _, err := r.Next(); err != io.EOF {
			t.Fatalf("call %d after EOF returned %v, want io.EOF", i, err)
		}
	}
}

// --- 5. ParseTimestamp boundary/edge cases ---------------------------------

func TestInvariantParseTimestampEdgeCases(t *testing.T) {
	// Negative value.
	if got, err := ParseTimestamp("-5"); err == nil {
		t.Errorf("ParseTimestamp(%q) = %v, %v; want error for negative input (or document why not)", "-5", got, err)
	}
	// Bare dot.
	if got, err := ParseTimestamp("."); err == nil {
		t.Errorf("ParseTimestamp(\".\") = %v, want error", got)
	}
	// Trailing dot, empty fraction.
	if got, err := ParseTimestamp("1700000000."); err != nil {
		t.Errorf("ParseTimestamp(\"1700000000.\") = err %v, want success with 0 nsec", err)
	} else if got.Unix() != 1700000000 || got.Nanosecond() != 0 {
		t.Errorf("ParseTimestamp(\"1700000000.\") = unix=%d nsec=%d, want unix=1700000000 nsec=0", got.Unix(), got.Nanosecond())
	}
	// Short fraction must be scaled, not left as raw nanoseconds.
	got, err := ParseTimestamp("1.5")
	if err != nil {
		t.Fatalf("ParseTimestamp(\"1.5\") error: %v", err)
	}
	if got.Nanosecond() != 500000000 {
		t.Errorf("ParseTimestamp(\"1.5\").Nanosecond() = %d, want 500000000 (0.5s), not 5ns", got.Nanosecond())
	}
	if !got.Equal(time.Unix(1, 500000000).UTC()) {
		t.Errorf("ParseTimestamp(\"1.5\") = %v, want %v", got, time.Unix(1, 500000000).UTC())
	}
}

// --- 6. Split invariance at a 7-byte chunk size, comparing full Metadata --

type nByteReader struct {
	r io.Reader
	n int
}

func (r nByteReader) Read(p []byte) (int, error) {
	if len(p) > r.n {
		p = p[:r.n]
	}
	return r.r.Read(p)
}

func TestInvariantSplitInvariantSevenByteChunks(t *testing.T) {
	raw := fixture(t, "topology_basic")

	full := readAllMetadata(t, NewReader(bytes.NewReader(raw)))
	seven := readAllMetadata(t, NewReader(nByteReader{r: bytes.NewReader(raw), n: 7}))

	if len(full) != len(seven) {
		t.Fatalf("fragment counts differ: full=%d seven=%d", len(full), len(seven))
	}
	for i := range full {
		if full[i].FragmentNumber != seven[i].FragmentNumber ||
			!full[i].ProducerTimestamp.Equal(seven[i].ProducerTimestamp) ||
			full[i].ContinuationToken != seven[i].ContinuationToken ||
			full[i].MillisBehindNow != seven[i].MillisBehindNow ||
			len(full[i].Tags) != len(seven[i].Tags) {
			t.Fatalf("fragment %d Metadata differs between full-read and 7-byte-chunk reads:\nfull=%#v\nseven=%#v", i, full[i], seven[i])
		}
	}
}

func readAllMetadata(t *testing.T, r *Reader) []Metadata {
	t.Helper()
	var out []Metadata
	for {
		_, m, err := r.Next()
		if err == io.EOF {
			return out
		}
		if err != nil {
			t.Fatalf("Next: %v", err)
		}
		out = append(out, m)
	}
}

// testSegment2 mirrors testSegment but only sets the fragment number tag,
// leaving ContactId absent so inheritance can be exercised.
func testSegment2(seed byte, number string) []byte {
	simpleTag := func(name, value string) ebmltest.Node {
		return ebmltest.Master(matroska.IDSimpleTag,
			ebmltest.UTF8(matroska.IDTagName, name),
			ebmltest.UTF8(matroska.IDTagString, value))
	}
	cluster := ebmltest.Master(matroska.IDCluster, ebmltest.Uint(matroska.IDTimestamp, 0),
		ebmltest.Leaf(matroska.IDSimpleBlock, []byte{0x81, 0, 0, 0, 0}))
	return ebmltest.Encode(
		ebmltest.Master(matroska.IDEBML, ebmltest.Uint(matroska.IDEBMLVersion, 1)),
		ebmltest.UnknownMaster(matroska.IDSegment,
			ebmltest.Master(matroska.IDInfo, ebmltest.Leaf(matroska.IDSegmentUUID, bytes.Repeat([]byte{seed}, 16))),
			ebmltest.Master(matroska.IDTags, ebmltest.Master(matroska.IDTag, simpleTag(TagFragmentNumber, number))),
			cluster,
		),
	)
}
