package parser

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"math/rand"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type logEvent struct {
	Step      int    `json:"step"`
	Op        string `json:"op"`
	Offset    int64  `json:"offset"`
	Depth     int    `json:"depth"`
	ID        string `json:"id,omitempty"`
	Size      *int64 `json:"size,omitempty"`
	Kind      string `json:"kind,omitempty"`
	HeaderLen int    `json:"header_len,omitempty"`
}

func i64p(v int64) *int64 { return &v }

func loadHexFixture(t *testing.T, rel string) []byte {
	t.Helper()
	path := filepath.Join("..", "..", rel)
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	lines := strings.Split(string(b), "\n")
	var sb strings.Builder
	for _, ln := range lines {
		ln = strings.TrimSpace(ln)
		if ln == "" || strings.HasPrefix(ln, "#") {
			continue
		}
		for _, tok := range strings.Fields(ln) {
			sb.WriteString(tok)
		}
	}
	raw, err := hex.DecodeString(sb.String())
	if err != nil {
		t.Fatalf("decode hex fixture: %v", err)
	}
	return raw
}

func loadGolden(t *testing.T, rel string) []byte {
	t.Helper()
	path := filepath.Join("..", "..", rel)
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read golden: %v", err)
	}
	return bytes.TrimSpace(b)
}

func splitOneByte(b []byte) [][]byte {
	out := make([][]byte, 0, len(b))
	for i := 0; i < len(b); i++ {
		out = append(out, b[i:i+1])
	}
	return out
}

func splitFibonacci(b []byte) [][]byte {
	var out [][]byte
	a, c := 1, 2
	for i := 0; i < len(b); {
		n := a
		if n < 1 {
			n = 1
		}
		if i+n > len(b) {
			n = len(b) - i
		}
		out = append(out, b[i:i+n])
		i += n
		a, c = c, a+c
	}
	return out
}

func splitRandom(b []byte, seed int64, maxChunk int) [][]byte {
	r := rand.New(rand.NewSource(seed))
	var out [][]byte
	for i := 0; i < len(b); {
		n := 1 + r.Intn(maxChunk)
		if i+n > len(b) {
			n = len(b) - i
		}
		out = append(out, b[i:i+n])
		i += n
	}
	return out
}

func scanAll(t *testing.T, p *Parser, events *[]logEvent, step *int, needMoreCount *int) {
	t.Helper()
	for {
		// If we have a pending current element (after consume_header), we must
		// finish it (enter_master or skip_payload) before we can peek again.
		if p.current != nil {
			switch p.current.kind {
			case KindMaster:
				enterOffset := p.Offset()
				*step++
				*events = append(*events, logEvent{
					Step:   *step,
					Op:     "enter",
					Offset: enterOffset,
					Depth:  p.Depth(),
				})
				if err := p.EnterMaster(); err != nil {
					t.Fatalf("enter_master: %v", err)
				}
				continue
			default:
				skipOffset := p.Offset()
				if err := p.SkipPayload(); err != nil {
					if _, ok := err.(NeedMoreData); ok {
						*needMoreCount += 1
						return
					}
					t.Fatalf("skip_payload: %v", err)
				}
				*step++
				*events = append(*events, logEvent{
					Step:   *step,
					Op:     "skip",
					Offset: skipOffset,
					Depth:  p.Depth(),
				})
				continue
			}
		}

		h, err := p.Peek()
		if err != nil {
			if _, ok := err.(NeedMoreData); ok {
				*needMoreCount += 1
				return
			}
			t.Fatalf("peek: %v", err)
		}

		if h.Kind == KindEndMaster {
			*step++
			*events = append(*events, logEvent{
				Step:   *step,
				Op:     "peek",
				Offset: p.Offset(),
				Depth:  p.Depth(),
				Kind:   string(KindEndMaster),
			})

			*step++
			*events = append(*events, logEvent{
				Step:   *step,
				Op:     "leave",
				Offset: p.Offset(),
				Depth:  p.Depth(),
			})

			if err := p.LeaveMaster(); err != nil {
				t.Fatalf("leave_master: %v", err)
			}
			continue
		}

		*step++
		*events = append(*events, logEvent{
			Step:      *step,
			Op:        "peek",
			Offset:    p.Offset(),
			Depth:     p.Depth(),
			ID:        FormatID(h.ID),
			Size:      i64p(h.Size),
			Kind:      string(h.Kind),
			HeaderLen: h.HeaderLen,
		})

		consumeOffset := p.Offset()
		if _, err := p.ConsumeHeader(); err != nil {
			if _, ok := err.(NeedMoreData); ok {
				t.Fatalf("consume unexpectedly needed more data at offset=%d: %v", consumeOffset, err)
			}
			t.Fatalf("consume: %v", err)
		}
		*step++
		*events = append(*events, logEvent{
			Step:   *step,
			Op:     "consume",
			Offset: consumeOffset,
			Depth:  p.Depth(),
		})
	}
}

func TestTinyGoldenIsSplitInvariant(t *testing.T) {
	fixture := loadHexFixture(t, "fixtures/tiny.ebml.hex")
	golden := loadGolden(t, "golden/tiny.jsonl")

	type tc struct {
		name   string
		chunks [][]byte
	}
	cases := []tc{
		{name: "one_byte", chunks: splitOneByte(fixture)},
		{name: "fibonacci", chunks: splitFibonacci(fixture)},
		{name: "random", chunks: splitRandom(fixture, 12345, 7)},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			p := New()
			var events []logEvent
			step := 0
			needMore := 0

			for _, ch := range c.chunks {
				p.Feed(ch)
				scanAll(t, p, &events, &step, &needMore)
			}

			// Drain one last time.
			scanAll(t, p, &events, &step, &needMore)

			// EOF: close remaining unknown-size masters.
			closed, err := p.FinalizeEOF()
			if err != nil {
				t.Fatalf("finalize_eof: %v", err)
			}
			for _, c := range closed {
				step++
				events = append(events, logEvent{
					Step:   step,
					Op:     "leave",
					Offset: p.Offset(),
					Depth:  c.Depth,
				})
			}

			if p.available() != 0 {
				t.Fatalf("expected all bytes consumed, remaining=%d", p.available())
			}
			if p.Depth() != 0 {
				t.Fatalf("expected stack empty, depth=%d", p.Depth())
			}

			if c.name == "one_byte" && needMore == 0 {
				t.Fatalf("expected NeedMoreData to occur for one_byte split")
			}

			var out bytes.Buffer
			enc := json.NewEncoder(&out)
			enc.SetEscapeHTML(false)
			for _, ev := range events {
				if err := enc.Encode(ev); err != nil {
					t.Fatalf("encode event: %v", err)
				}
			}

			got := bytes.TrimSpace(out.Bytes())

			if !bytes.Equal(got, golden) {
				t.Fatalf("golden mismatch\n--- got ---\n%s\n--- want ---\n%s\n", got, golden)
			}
		})
	}
}
