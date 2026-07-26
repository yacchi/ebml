// Command core-scan is the smallest complete consumer of the reading core: a pull
// loop over a parser.Cursor with a type switch over the three node kinds. It reads
// raw EBML on stdin and prints the document's DocType and every Cluster Timestamp,
// retaining nothing and materialising no other payload -- not one PCM byte.
package main

import (
	"errors"
	"fmt"
	"io"
	"os"

	"github.com/yacchi/ebml/impl/go/matroska"
	"github.com/yacchi/ebml/impl/go/parser"
)

// scan pulls every element of r and prints the two values it is interested in.
//
// The loop IS the API. Next reports an element header or a master end; the type
// switch says which, and it is exhaustive -- the node interface is closed over
// exactly those three. What a consumer wants from an element it states on the node
// it is holding, and the following Next carries that decision out: here only two
// leaves are asked for their payload, so every other payload is skipped without
// ever being held in memory.
func scan(r io.Reader, w io.Writer) error {
	// The classifier is required: without it a Cluster would be read as one opaque
	// leaf. The boundary rule is what a stream of concatenated unknown-size
	// Segments needs -- one ends where the next top-level element begins.
	c := parser.NewCursor(matroska.KindForElementID, parser.WithBoundary(
		func(open, next parser.ElementID) bool {
			return next == matroska.IDEBML || next == matroska.IDSegment
		}))
	src := &chunks{r: r, buf: make([]byte, 4096)}

	for {
		node, err := c.Next()
		if err != nil {
			// The three outcomes a consumer must tell apart: more input is due, the
			// stream is over, or these bytes cannot be read as EBML.
			switch {
			case isNeedMoreData(err):
				chunk, err := src.next()
				if err != nil {
					return err
				}
				if chunk == nil {
					// Only the caller knows the input is over; saying so is what
					// closes the trailing unknown-size Segment.
					if err := c.Finalize(); err != nil {
						return err
					}
					continue
				}
				c.Feed(chunk)
				continue
			case errors.Is(err, io.EOF):
				return nil
			default:
				return err // structural: parser.IsStructural(err) is true
			}
		}

		switch n := node.(type) {
		case *parser.MasterNode:
			// Nothing to decide: a master nobody touches is descended into, which
			// is what puts the leaves below within reach.
		case *parser.LeafNode:
			if n.ID() != matroska.IDDocType && n.ID() != matroska.IDTimestamp {
				continue // untouched, so its payload is never materialised
			}
			payload, err := readPayload(c, src, n)
			if err != nil {
				return err
			}
			if n.ID() == matroska.IDDocType {
				fmt.Fprintf(w, "DocType=%s\n", parser.DecodeString(payload))
				continue
			}
			v, err := parser.DecodeUint(payload)
			if err != nil {
				return err
			}
			fmt.Fprintf(w, "Timestamp=%d\n", v)
		case *parser.EndNode:
			// The master's extent is settled. A known-size Cluster reaches this
			// point as soon as its declared bytes are consumed, without waiting for
			// the unknown-size Segment around it to close.
		}
	}
}

// readPayload takes a leaf's payload, feeding more input while its bytes are still
// outstanding: a leaf is reported on its HEADER, so its bytes need not have arrived
// yet, and the node stays valid across Feed for exactly this retry.
func readPayload(c *parser.Cursor, src *chunks, leaf *parser.LeafNode) ([]byte, error) {
	for {
		payload, err := leaf.Payload()
		if err == nil {
			return payload, nil
		}
		if !isNeedMoreData(err) {
			return nil, err
		}
		chunk, err := src.next()
		if err != nil {
			return nil, err
		}
		if chunk == nil {
			// The input ended inside this element, which Finalize is what states.
			if err := c.Finalize(); err != nil {
				return nil, err
			}
			return nil, fmt.Errorf("input ended inside element %s at offset %d", leaf.ID(), leaf.Offset())
		}
		c.Feed(chunk)
	}
}

// chunks hands out the input in fixed-size pieces. A nil chunk means the input is
// over, which is the one thing a cursor cannot work out for itself.
type chunks struct {
	r   io.Reader
	buf []byte
}

func (s *chunks) next() ([]byte, error) {
	for {
		n, err := s.r.Read(s.buf)
		if n > 0 {
			return s.buf[:n], nil // Feed copies, so the buffer can be reused
		}
		if errors.Is(err, io.EOF) {
			return nil, nil
		}
		if err != nil {
			return nil, err
		}
	}
}

func isNeedMoreData(err error) bool {
	var needMore parser.NeedMoreData
	return errors.As(err, &needMore)
}

func main() {
	if err := scan(os.Stdin, os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, "core-scan:", err)
		os.Exit(1)
	}
}
