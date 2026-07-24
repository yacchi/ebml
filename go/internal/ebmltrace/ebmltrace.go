// Package ebmltrace drives the streaming EBML cursor over a byte stream and
// records the exact peek/consume/enter/skip/leave event sequence as JSONL,
// matching the golden schema used by the parser tests.
//
// It is shared by the KVS fixture generator (to emit golden files) and by the
// KVS split-invariance test (to verify every chunking reproduces the golden),
// so both use one implementation of "run the cursor".
//
// Beyond the README's basic loop, Trace adds one KVS-specific rule: an
// unknown-size master (a KVS Segment) is closed when the cursor peeks a new
// top-level element (EBML header or Segment) — the boundary between concatenated
// GetMedia fragments. This close is driven purely by element structure (via the
// public CloseMaster API), never by scanning the bytes for the EBML magic, which
// is what lets PCM payloads containing the magic bytes parse without a spurious
// split.
package ebmltrace

import (
	"bytes"
	"encoding/json"
	"math/rand"

	"github.com/yacchi/ebml-reader/parser"
)

// Event mirrors the golden JSONL schema (golden/tiny.jsonl).
type Event struct {
	Step      int    `json:"step"`
	Op        string `json:"op"`
	Offset    int64  `json:"offset"`
	Depth     int    `json:"depth"`
	ID        string `json:"id,omitempty"`
	Size      *int64 `json:"size,omitempty"`
	Kind      string `json:"kind,omitempty"`
	HeaderLen int    `json:"header_len,omitempty"`
}

// frame mirrors the parser's master stack so Trace knows whether the current
// top master is unknown-size (only unknown-size masters get boundary-closed).
type frame struct {
	id      uint32
	unknown bool
}

// Trace feeds the given chunks into a fresh cursor (with classifier) and returns
// the ordered event log plus the number of NeedMoreData occurrences. The event
// log is split-invariant: identical for any chunking of the same stream.
func Trace(chunks [][]byte, classifier parser.KindClassifier) ([]Event, int, error) {
	p := parser.New(parser.WithKindClassifier(classifier))

	var events []Event
	var mstack []frame
	step := 0
	needMore := 0
	pendingSkip := false // a non-master header was consumed; payload not yet skipped

	drain := func() error {
		for {
			if pendingSkip {
				off := p.Offset()
				if err := p.SkipPayload(); err != nil {
					if _, ok := err.(parser.NeedMoreData); ok {
						needMore++
						return nil
					}
					return err
				}
				step++
				events = append(events, Event{Step: step, Op: "skip", Offset: off, Depth: p.Depth()})
				pendingSkip = false
				continue
			}

			h, err := p.Peek()
			if err != nil {
				if _, ok := err.(parser.NeedMoreData); ok {
					needMore++
					return nil
				}
				return err
			}

			// Known-size master reached its end.
			if h.Kind == parser.KindEndMaster {
				step++
				events = append(events, Event{Step: step, Op: "peek", Offset: p.Offset(), Depth: p.Depth(), Kind: string(parser.KindEndMaster)})
				step++
				events = append(events, Event{Step: step, Op: "leave", Offset: p.Offset(), Depth: p.Depth()})
				if err := p.LeaveMaster(); err != nil {
					return err
				}
				mstack = mstack[:len(mstack)-1]
				continue
			}

			// Unknown-size master (KVS Segment) boundary: a new top-level element
			// begins the next fragment. Close the open Segment explicitly.
			if len(mstack) > 0 && mstack[len(mstack)-1].unknown && parser.IsTopLevelElementID(h.ID) {
				step++
				events = append(events, Event{Step: step, Op: "leave", Offset: p.Offset(), Depth: p.Depth()})
				if err := p.CloseMaster(); err != nil {
					return err
				}
				mstack = mstack[:len(mstack)-1]
				continue
			}

			// Normal element: peek -> consume -> enter/skip.
			step++
			size := h.Size
			events = append(events, Event{
				Step:      step,
				Op:        "peek",
				Offset:    p.Offset(),
				Depth:     p.Depth(),
				ID:        parser.FormatID(h.ID),
				Size:      &size,
				Kind:      string(h.Kind),
				HeaderLen: h.HeaderLen,
			})

			consumeOff := p.Offset()
			if _, err := p.ConsumeHeader(); err != nil {
				return err
			}
			step++
			events = append(events, Event{Step: step, Op: "consume", Offset: consumeOff, Depth: p.Depth()})

			if h.Kind == parser.KindMaster {
				step++
				events = append(events, Event{Step: step, Op: "enter", Offset: p.Offset(), Depth: p.Depth()})
				if err := p.EnterMaster(); err != nil {
					return err
				}
				mstack = append(mstack, frame{id: h.ID, unknown: h.Size < 0})
			} else {
				pendingSkip = true
			}
		}
	}

	for _, ch := range chunks {
		p.Feed(ch)
		if err := drain(); err != nil {
			return nil, needMore, err
		}
	}
	if err := drain(); err != nil {
		return nil, needMore, err
	}

	closed, err := p.FinalizeEOF()
	if err != nil {
		return nil, needMore, err
	}
	for _, c := range closed {
		step++
		events = append(events, Event{Step: step, Op: "leave", Offset: p.Offset(), Depth: c.Depth})
	}

	return events, needMore, nil
}

// MarshalJSONL encodes events as newline-delimited JSON, trimmed, matching the
// golden file layout (no HTML escaping, one object per line, no trailing spaces).
func MarshalJSONL(events []Event) ([]byte, error) {
	var out bytes.Buffer
	enc := json.NewEncoder(&out)
	enc.SetEscapeHTML(false)
	for _, ev := range events {
		if err := enc.Encode(ev); err != nil {
			return nil, err
		}
	}
	return bytes.TrimSpace(out.Bytes()), nil
}

// SplitOneByte splits b into one-byte chunks.
func SplitOneByte(b []byte) [][]byte {
	out := make([][]byte, 0, len(b))
	for i := 0; i < len(b); i++ {
		out = append(out, b[i:i+1])
	}
	return out
}

// SplitFibonacci splits b into chunks of Fibonacci-growing sizes.
func SplitFibonacci(b []byte) [][]byte {
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

// SplitRandom splits b into random-sized chunks (1..maxChunk) using seed.
func SplitRandom(b []byte, seed int64, maxChunk int) [][]byte {
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

// Whole returns b as a single chunk (used for golden generation).
func Whole(b []byte) [][]byte {
	if len(b) == 0 {
		return nil
	}
	return [][]byte{b}
}
