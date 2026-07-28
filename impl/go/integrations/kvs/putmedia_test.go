package kvs_test

import (
	"bytes"
	"io"
	"testing"
	"time"

	"github.com/yacchi/ebml/impl/go/ext/fragment"
	"github.com/yacchi/ebml/impl/go/integrations/kvs"
	"github.com/yacchi/ebml/impl/go/matroska"
	"github.com/yacchi/ebml/impl/go/parser"
	"github.com/yacchi/ebml/impl/go/writer"
)

// putMediaFragment writes ONE fragment in the shape putmedia.go describes: an
// unknown-size Segment holding a KNOWN-SIZE Cluster, with Cluster.Timestamp
// carrying an absolute instant.
//
// The profile's whole content is a pair of writer.SizeStrategy choices and an
// epoch basis, so this is the profile: the doc comment states it in prose and
// this function is what makes that prose fail to compile if writer.Buffered,
// writer.UnknownSize or ClusterTimestamp stop meaning what it says.
func putMediaFragment(t *testing.T, at time.Time, scale uint64) []byte {
	t.Helper()

	clusterTS, err := kvs.ClusterTimestamp(at, scale)
	if err != nil {
		t.Fatalf("ClusterTimestamp: %v", err)
	}

	var buf bytes.Buffer
	w := writer.New(&buf)
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatalf("write: %v", err)
		}
	}

	must(w.StartMaster(matroska.IDEBML, writer.Buffered()))
	must(w.Uint(matroska.IDEBMLVersion, 1))
	must(w.String(matroska.IDDocType, "matroska"))
	must(w.Uint(matroska.IDDocTypeVersion, 2))
	must(w.Uint(matroska.IDDocTypeReadVersion, 2))
	must(w.EndMaster())

	// The Segment has no locatable end while the stream is live.
	must(w.StartMaster(matroska.IDSegment, writer.UnknownSize()))
	must(w.StartMaster(matroska.IDInfo, writer.Buffered()))
	must(w.Uint(matroska.IDTimestampScale, scale))
	must(w.EndMaster())

	// The Cluster is KNOWN-size, which is the profile's one real decision: its
	// bytes reach the sink as a single write at EndMaster.
	must(w.StartMaster(matroska.IDCluster, writer.Buffered()))
	must(w.Uint(matroska.IDTimestamp, clusterTS))
	must(w.Binary(matroska.IDSimpleBlock, []byte{0x81, 0x00, 0x00, 0x80, 0x01, 0x02}))
	must(w.EndMaster())

	must(w.EndMaster()) // Segment
	must(w.Close())
	return buf.Bytes()
}

// TestPutMediaProfileRoundTrips writes the profile and reads it back through this
// package's own Reader, so the two directions are checked against each other
// rather than against a restatement of either.
func TestPutMediaProfileRoundTrips(t *testing.T) {
	const scale = fragment.DefaultTimestampScale
	// A whole number of milliseconds, so the scale loses nothing and any
	// difference on the way back is a real one.
	at := time.Date(2026, 7, 29, 10, 30, 0, 0, time.UTC)

	raw := putMediaFragment(t, at, scale)

	r := kvs.NewReader(bytes.NewReader(raw))
	f, _, err := r.Next()
	if err != nil {
		t.Fatalf("Next: %v", err)
	}
	if got := kvs.ClusterTime(f); !got.Equal(at) {
		t.Errorf("ClusterTime = %s, want %s: the write-side epoch basis and the read-side one disagree",
			got, at)
	}
	if _, _, err := r.Next(); err != io.EOF {
		t.Errorf("second Next = %v, want io.EOF", err)
	}
}

// TestPutMediaClusterIsKnownSize pins the half of the profile a round trip cannot
// see: the Cluster's size is DECLARED, not left unknown. Both shapes parse, which
// is exactly why nothing else would catch a regression here.
func TestPutMediaClusterIsKnownSize(t *testing.T) {
	raw := putMediaFragment(t, time.Date(2026, 7, 29, 10, 30, 0, 0, time.UTC), fragment.DefaultTimestampScale)

	cur := parser.NewCursor(matroska.KindForElementID, parser.WithBoundary(matroska.StreamBoundary))
	cur.Feed(raw)
	var sawCluster bool
	for {
		node, err := cur.Next()
		if err == io.EOF {
			break
		}
		if _, ok := err.(parser.NeedMoreData); ok {
			break
		}
		if err != nil {
			t.Fatalf("Next: %v", err)
		}
		master, ok := node.(*parser.MasterNode)
		if !ok || master.ID() != matroska.IDCluster {
			continue
		}
		sawCluster = true
		if master.Size() == parser.UnknownSize {
			t.Error("the PutMedia profile declares the Cluster's size; it was written unknown-size")
		}
		master.Descend()
	}
	if !sawCluster {
		t.Fatal("no Cluster in the written document")
	}
}

// TestClusterTimestampRejectsWhatItCannotEncode covers the two inputs that would
// otherwise produce a document naming an instant nobody chose.
func TestClusterTimestampRejectsWhatItCannotEncode(t *testing.T) {
	if _, err := kvs.ClusterTimestamp(time.Unix(0, 0), 0); err == nil {
		t.Error("a zero TimestampScale must be rejected, never defaulted")
	}
	if _, err := kvs.ClusterTimestamp(time.Unix(-1, 0), fragment.DefaultTimestampScale); err == nil {
		t.Error("a time before the Unix epoch has no unsigned encoding and must be rejected")
	}
}

// TestClusterTimestampInvertsClusterTime states the relationship the doc claims,
// over a scale that is not the default, so a hard-coded millisecond would show.
func TestClusterTimestampInvertsClusterTime(t *testing.T) {
	const scale uint64 = 100_000 // 0.1 ms per tick
	at := time.Date(2026, 1, 2, 3, 4, 5, 600_000_000, time.UTC)

	ticks, err := kvs.ClusterTimestamp(at, scale)
	if err != nil {
		t.Fatalf("ClusterTimestamp: %v", err)
	}
	if want := uint64(at.UnixNano()) / scale; ticks != want {
		t.Fatalf("ClusterTimestamp = %d, want %d", ticks, want)
	}
	if got := time.Unix(0, int64(ticks)*int64(scale)).UTC(); !got.Equal(at) {
		t.Errorf("round trip = %s, want %s", got, at)
	}
}
