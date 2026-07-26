package stream_test

import (
	"bytes"
	"errors"
	"io"
	"testing"

	"github.com/yacchi/ebml/impl/go/internal/ebmltest"
	"github.com/yacchi/ebml/impl/go/matroska"
	"github.com/yacchi/ebml/impl/go/parser"
	"github.com/yacchi/ebml/impl/go/stream"
)

type nodeShape struct {
	id     parser.ElementID
	kind   parser.Kind
	depth  int
	offset int64
	end    int64
}

func TestNodesNeverYieldNeedMoreData(t *testing.T) {
	raw := testDocument()
	want := collectNodes(t, stream.New(bytes.NewReader(raw), matroska.KindForElementID))
	gotStream := stream.New(&chunkReader{r: bytes.NewReader(raw), size: 1}, matroska.KindForElementID)
	got := collectNodes(t, gotStream)
	if len(got) != len(want) {
		t.Fatalf("node count = %d, want %d", len(got), len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("node %d = %+v, want %+v", i, got[i], want[i])
		}
	}
}

func TestPayloadNeverReportsNeedMoreData(t *testing.T) {
	raw := ebmltest.Encode(
		ebmltest.String(matroska.IDDocType, "webm"),
		ebmltest.Leaf(matroska.IDMuxingApp, []byte("mux")),
	)
	s := stream.New(&chunkReader{r: bytes.NewReader(raw), size: 1}, matroska.KindForElementID)
	var values [][]byte
	for node, err := range s.Nodes() {
		if err != nil {
			t.Fatal(err)
		}
		leaf, ok := node.(*parser.LeafNode)
		if !ok {
			continue
		}
		payload, err := s.Payload(leaf)
		if err != nil {
			var needMore parser.NeedMoreData
			if errors.As(err, &needMore) {
				t.Fatalf("Payload leaked NeedMoreData: %v", err)
			}
			t.Fatal(err)
		}
		values = append(values, bytes.Clone(payload))
	}
	if got, want := values, [][]byte{[]byte("webm"), []byte("mux")}; !equalBytes(got, want) {
		t.Fatalf("payloads = %q, want %q", got, want)
	}
}

func TestPayloadAcrossChunkBoundary(t *testing.T) {
	want := bytes.Repeat([]byte("payload"), 20)
	raw := ebmltest.Encode(ebmltest.Leaf(matroska.IDCodecPrivate, want))
	s := stream.New(&chunkReader{r: bytes.NewReader(raw), size: 3}, matroska.KindForElementID)
	node := firstNode(t, s)
	payload, err := s.Payload(node.(*parser.LeafNode))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(payload, want) {
		t.Fatalf("payload = %q, want %q", payload, want)
	}
}

func TestTruncatedInputIsStructural(t *testing.T) {
	raw := ebmltest.Encode(ebmltest.Leaf(matroska.IDCodecPrivate, []byte("truncated")))
	raw = raw[:len(raw)-2]
	s := stream.New(bytes.NewReader(raw), matroska.KindForElementID)
	node := firstNode(t, s)
	_, err := s.Payload(node.(*parser.LeafNode))
	if err == nil || !parser.IsStructural(err) {
		t.Fatalf("error = %v, want structural error", err)
	}
	var needMore parser.NeedMoreData
	if errors.As(err, &needMore) {
		t.Fatalf("truncated input returned NeedMoreData: %v", err)
	}
}

// TestTruncatedInputEndsIterationWithError is the same failure seen through the
// iterator: it arrives as the final pair, never as a silently short iteration.
// That is the whole reason Nodes is an iter.Seq2 and not an iter.Seq plus an Err
// method a consumer can forget to call.
func TestTruncatedInputEndsIterationWithError(t *testing.T) {
	raw := ebmltest.Encode(ebmltest.Master(matroska.IDEBML,
		ebmltest.Uint(matroska.IDEBMLVersion, 1),
	))
	raw = raw[:len(raw)-1]
	s := stream.New(bytes.NewReader(raw), matroska.KindForElementID)
	var last error
	var nodes int
	for node, err := range s.Nodes() {
		if err != nil {
			if node != nil {
				t.Fatalf("node = %v alongside error %v, want a nil node", node, err)
			}
			last = err
			continue
		}
		nodes++
	}
	if last == nil || !parser.IsStructural(last) {
		t.Fatalf("final error = %v, want structural error", last)
	}
	if nodes == 0 {
		t.Fatal("no node was yielded before the truncation")
	}
}

// TestExhaustedStreamYieldsNothing is what replaced the old "a terminal Next keeps
// reporting io.EOF": the end of the input is the end of the iteration, and it stays
// the end however many times the stream is ranged.
func TestExhaustedStreamYieldsNothing(t *testing.T) {
	s := stream.New(bytes.NewReader(testDocument()), matroska.KindForElementID)
	if len(collectNodes(t, s)) == 0 {
		t.Fatal("first pass yielded no node")
	}
	for pass := range 2 {
		for node, err := range s.Nodes() {
			t.Fatalf("pass %d after exhaustion yielded (%v, %v), want nothing", pass, node, err)
		}
	}
}

// TestBreakResumesAtEveryNode pins the documented resumption rule, exhaustively:
// for every node index in turn it breaks out of the range there, resumes with a
// fresh range, and requires the concatenated sequence to equal the uninterrupted
// one. Breaking out of a range-over-func loop must therefore leave the stream
// exactly where it stopped, including the decision left on the node it broke on.
//
// This is the rigour the deleted parser.Cursor.Nodes test used to carry; the
// property belongs to whichever layer actually offers an iterator, and since
// stream is now the only one, it is tested here.
func TestBreakResumesAtEveryNode(t *testing.T) {
	all := collectNodes(t, stream.New(bytes.NewReader(testDocument()), matroska.KindForElementID))
	if len(all) < 2 {
		t.Fatalf("test document has %d nodes, too few to break within", len(all))
	}

	for k := range all {
		s := stream.New(bytes.NewReader(testDocument()), matroska.KindForElementID)
		var seen []nodeShape
		for node, err := range s.Nodes() {
			if err != nil {
				t.Fatal(err)
			}
			seen = append(seen, shape(node))
			if len(seen) == k+1 {
				break
			}
		}
		for node, err := range s.Nodes() {
			if err != nil {
				t.Fatal(err)
			}
			seen = append(seen, shape(node))
		}

		if len(seen) != len(all) {
			t.Fatalf("breaking after node %d yielded %d nodes, want %d", k, len(seen), len(all))
		}
		for i := range all {
			if seen[i] != all[i] {
				t.Fatalf("breaking after node %d, node %d = %+v, want %+v", k, i, seen[i], all[i])
			}
		}
	}
}

func TestSourceErrorPropagates(t *testing.T) {
	sentinel := errors.New("source failed")
	s := stream.New(&errorReader{
		r:   bytes.NewReader(testDocument()),
		err: sentinel,
	}, matroska.KindForElementID)
	var last error
	for _, err := range s.Nodes() {
		if err != nil {
			last = err
		}
	}
	if !errors.Is(last, sentinel) {
		t.Fatalf("error = %v, want %v", last, sentinel)
	}
}

func TestSkippedMasterCostsNoPayloadRead(t *testing.T) {
	raw := ebmltest.Encode(ebmltest.Master(matroska.IDCluster,
		ebmltest.Leaf(matroska.IDCodecPrivate, bytes.Repeat([]byte{0xA5}, 32*1024)),
	))
	reader := &countReader{r: bytes.NewReader(raw)}
	s := stream.New(reader, matroska.KindForElementID)
	var nodes int
	for node, err := range s.Nodes() {
		if err != nil {
			t.Fatal(err)
		}
		nodes++
		master, ok := node.(*parser.MasterNode)
		if !ok {
			t.Fatalf("node %d = %T, want *parser.MasterNode", nodes, node)
		}
		master.Skip()
	}
	if nodes != 1 {
		t.Fatalf("node count = %d, want 1 (the skipped master)", nodes)
	}
	if reader.reads != 2 {
		t.Fatalf("reader reads = %d, want 2 (data plus terminal EOF); skipped payload was not materialised", reader.reads)
	}
}

func testDocument() []byte {
	return ebmltest.Encode(
		ebmltest.Master(matroska.IDEBML,
			ebmltest.Uint(matroska.IDEBMLVersion, 1),
			ebmltest.String(matroska.IDDocType, "webm"),
		),
		ebmltest.Master(matroska.IDInfo,
			ebmltest.Uint(matroska.IDTimestampScale, 1000000),
		),
	)
}

func collectNodes(t *testing.T, s *stream.Stream) []nodeShape {
	t.Helper()
	var nodes []nodeShape
	for node, err := range s.Nodes() {
		if err != nil {
			var needMore parser.NeedMoreData
			if errors.As(err, &needMore) {
				t.Fatalf("Nodes leaked NeedMoreData: %v", err)
			}
			t.Fatal(err)
		}
		nodes = append(nodes, shape(node))
	}
	return nodes
}

// firstNode takes just the first node and leaves the stream standing on it, which
// is the break-and-hold shape a single-node test needs.
func firstNode(t *testing.T, s *stream.Stream) parser.Node {
	t.Helper()
	for node, err := range s.Nodes() {
		if err != nil {
			t.Fatal(err)
		}
		return node
	}
	t.Fatal("stream yielded no node")
	return nil
}

func shape(node parser.Node) nodeShape {
	return nodeShape{
		id:     node.ID(),
		kind:   node.Kind(),
		depth:  node.Depth(),
		offset: node.Offset(),
		end:    node.End(),
	}
}

func equalBytes(a, b [][]byte) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if !bytes.Equal(a[i], b[i]) {
			return false
		}
	}
	return true
}

type chunkReader struct {
	r    io.Reader
	size int
}

func (r *chunkReader) Read(p []byte) (int, error) {
	if len(p) > r.size {
		p = p[:r.size]
	}
	return r.r.Read(p)
}

type countReader struct {
	r     io.Reader
	reads int
}

func (r *countReader) Read(p []byte) (int, error) {
	r.reads++
	return r.r.Read(p)
}

type errorReader struct {
	r   io.Reader
	err error
}

func (r *errorReader) Read(p []byte) (int, error) {
	if r.r != nil {
		n, _ := r.r.Read(p)
		r.r = nil
		if n > 0 {
			return n, nil
		}
	}
	return 0, r.err
}
