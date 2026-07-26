// Package stream drives a parser cursor from a byte source it owns, answering
// NeedMoreData so that nothing above it ever sees that flow-control error.
//
// It is CORE, not convenience. io.Reader is Go's spelling of a byte source, but
// the contract it stands for is not Go's: keep supplying bytes and parsing
// proceeds, and when the source is exhausted the input is finalized so a stream
// that ended inside an element is reported as truncated. Every port needs that
// contract, and getting its end-of-input half wrong is observable -- a reader
// that skipped Finalize would silently accept a truncated document. Only the
// spelling of the source is language-specific; SPEC.md states the contract and
// leaves each language to name its own source type.
//
// The cursor's own Feed/Next split stays exactly as it is: a consumer that wants
// to push bytes itself drives parser.Cursor directly and answers NeedMoreData
// itself. This package is the answer for a consumer that would rather hand over
// the source.
//
// # Why this layer is an iterator and the cursor is not
//
// A pull has THREE outcomes -- an event, need-more-data, and end of input -- and
// an iterator protocol carries two, value and done. parser.Cursor is fed by its
// caller, so need-more-data has nowhere to go there and Cursor.Next stays an
// explicit call. This package OWNS the source, so it answers need-more-data by
// reading and only two outcomes ever reach the consumer. That is exactly the
// condition under which the host language's iterator is a CORRECT spelling of
// the contract rather than a lossy one, so Nodes is the whole reading surface
// here: there is deliberately no exported Next, because a second spelling of the
// same pull is where the three-outcome collapse creeps back in.
// docs/pull-shape-across-languages.md states the same split for other languages.
package stream

import (
	"errors"
	"fmt"
	"io"
	"iter"

	"github.com/yacchi/ebml/parser"
)

// Stream drives a cursor over an io.Reader it owns. It exists to answer
// NeedMoreData -- the one thing a consumer cannot do for the cursor without
// holding the input source -- so nothing above it ever sees that error.
type Stream struct {
	c       *parser.Cursor
	r       io.Reader
	buf     []byte
	drained bool
}

// New returns a Stream that classifies elements with classifier and applies opts
// to its cursor.
func New(r io.Reader, classifier parser.KindClassifier, opts ...parser.Option) *Stream {
	return &Stream{
		c:   parser.NewCursor(classifier, opts...),
		r:   r,
		buf: make([]byte, 64*1024),
	}
}

// Nodes iterates the stream's nodes, reading from the source whenever the cursor
// needs input. It never yields NeedMoreData: the end of the input ends the
// iteration instead of producing a value, and any other failure is yielded once,
// as the final pair, with a nil node.
//
//	for node, err := range s.Nodes() {
//	    if err != nil {
//	        return err
//	    }
//	    ...
//	}
//
// Flow control is unchanged: the decision on a node (MasterNode.Descend/Skip,
// LeafNode.Payload/Skip, or this Stream's Payload) is taken in the loop body,
// before the range asks for the following node, and a node is valid only for the
// iteration that delivered it.
//
// Breaking out of the loop leaves the stream exactly where it stopped, so ranging
// again resumes with the following node: the loop that broke has seen the node it
// broke on, and the decision left on that node is carried out when iteration
// resumes. Ranging a stream whose input has already ended yields nothing.
func (s *Stream) Nodes() iter.Seq2[parser.Node, error] {
	return func(yield func(parser.Node, error) bool) {
		for {
			node, err := s.next()
			if errors.Is(err, io.EOF) {
				return
			}
			if err != nil {
				yield(nil, err)
				return
			}
			if !yield(node, nil) {
				return
			}
		}
	}
}

// next reports the next node, or io.EOF once the whole input has been reported.
// It never reports NeedMoreData: it reads until the cursor is satisfied. It is
// unexported so that Nodes stays the only reading surface -- see the package doc.
func (s *Stream) next() (parser.Node, error) {
	for {
		node, err := s.c.Next()
		if err == nil {
			return node, nil
		}
		if !isNeedMoreData(err) {
			return nil, err
		}
		if err := s.fill(); err != nil {
			return nil, err
		}
	}
}

// Payload delivers a leaf's payload, reading more input while its bytes are
// outstanding. The node remains valid across those reads, allowing the decision
// taken on its header to stand until its bytes are available.
//
// The bytes are the cursor's own view: valid only until the next node, never to
// be modified, and copied by whoever retains them.
func (s *Stream) Payload(leaf *parser.LeafNode) ([]byte, error) {
	for {
		payload, err := leaf.Payload()
		if err == nil {
			return payload, nil
		}
		if !isNeedMoreData(err) {
			return nil, err
		}
		if err := s.fill(); err != nil {
			return nil, err
		}
	}
}

// Offset reports the absolute offset the cursor has reached.
func (s *Stream) Offset() int64 { return s.c.Offset() }

// fill pushes the next chunk of input into the cursor, or declares the input over
// once the reader is exhausted -- which is where a stream ending inside an element
// is reported as truncated.
func (s *Stream) fill() error {
	if s.drained {
		return io.EOF
	}
	for {
		n, err := s.r.Read(s.buf)
		if n > 0 {
			s.c.Feed(s.buf[:n])
			return nil
		}
		if errors.Is(err, io.EOF) {
			s.drained = true
			return s.c.Finalize()
		}
		if err != nil {
			return fmt.Errorf("read input: %w", err)
		}
	}
}

func isNeedMoreData(err error) bool {
	var needMore parser.NeedMoreData
	return errors.As(err, &needMore)
}
