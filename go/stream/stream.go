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
package stream

import (
	"errors"
	"fmt"
	"io"

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

// Next reports the next node. It never reports NeedMoreData: it reads until the
// cursor is satisfied, and reports io.EOF once the whole input has been reported.
func (s *Stream) Next() (parser.Node, error) {
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
// The bytes are the cursor's own view: valid only until the next Next, never to
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
