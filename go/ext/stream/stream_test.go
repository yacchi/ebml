package stream_test

import (
	"bytes"
	"errors"
	"io"
	"testing"

	"github.com/yacchi/ebml/ext/stream"
	"github.com/yacchi/ebml/internal/ebmltest"
	"github.com/yacchi/ebml/matroska"
	"github.com/yacchi/ebml/parser"
)

type nodeShape struct {
	id     parser.ElementID
	kind   parser.Kind
	depth  int
	offset int64
	end    int64
}

func TestNextNeverReportsNeedMoreData(t *testing.T) {
	raw := testDocument()
	want := collectNodes(t, stream.New(bytes.NewReader(raw), matroska.KindForElementID))
	gotStream := stream.New(&chunkReader{r: bytes.NewReader(raw), size: 1}, matroska.KindForElementID)
	var got []nodeShape
	for {
		node, err := gotStream.Next()
		if err != nil {
			var needMore parser.NeedMoreData
			if errors.As(err, &needMore) {
				t.Fatalf("Next leaked NeedMoreData: %v", err)
			}
			if errors.Is(err, io.EOF) {
				break
			}
			t.Fatal(err)
		}
		got = append(got, shape(node))
	}
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
	for {
		node, err := s.Next()
		if errors.Is(err, io.EOF) {
			break
		}
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
	node, err := s.Next()
	if err != nil {
		t.Fatal(err)
	}
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
	node, err := s.Next()
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.Payload(node.(*parser.LeafNode))
	if err == nil || !parser.IsStructural(err) {
		t.Fatalf("error = %v, want structural error", err)
	}
	var needMore parser.NeedMoreData
	if errors.As(err, &needMore) {
		t.Fatalf("truncated input returned NeedMoreData: %v", err)
	}
}

func TestEOFAfterFullInput(t *testing.T) {
	s := stream.New(bytes.NewReader(testDocument()), matroska.KindForElementID)
	for {
		_, err := s.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.Next(); !errors.Is(err, io.EOF) {
		t.Fatalf("second terminal Next error = %v, want io.EOF", err)
	}
	if _, err := s.Next(); !errors.Is(err, io.EOF) {
		t.Fatalf("third terminal Next error = %v, want io.EOF", err)
	}
}

func TestSourceErrorPropagates(t *testing.T) {
	sentinel := errors.New("source failed")
	s := stream.New(&errorReader{
		r:   bytes.NewReader(testDocument()),
		err: sentinel,
	}, matroska.KindForElementID)
	for {
		_, err := s.Next()
		if err != nil {
			if !errors.Is(err, sentinel) {
				t.Fatalf("error = %v, want %v", err, sentinel)
			}
			return
		}
	}
}

func TestSkippedMasterCostsNoPayloadRead(t *testing.T) {
	raw := ebmltest.Encode(ebmltest.Master(matroska.IDCluster,
		ebmltest.Leaf(matroska.IDCodecPrivate, bytes.Repeat([]byte{0xA5}, 32*1024)),
	))
	reader := &countReader{r: bytes.NewReader(raw)}
	s := stream.New(reader, matroska.KindForElementID)
	node, err := s.Next()
	if err != nil {
		t.Fatal(err)
	}
	node.(*parser.MasterNode).Skip()
	if _, err := s.Next(); !errors.Is(err, io.EOF) {
		t.Fatalf("after skipped master: %v", err)
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
	for {
		node, err := s.Next()
		if errors.Is(err, io.EOF) {
			return nodes
		}
		if err != nil {
			t.Fatal(err)
		}
		nodes = append(nodes, shape(node))
	}
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
