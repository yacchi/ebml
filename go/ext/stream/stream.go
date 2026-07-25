// Package stream provides Go convenience for driving a parser cursor from an
// io.Reader. It is outside the portable contract in spec/SPEC.md because owning
// an input source is inherently a Go concern.
//
// Stream answers NeedMoreData here, where the source is owned, so consumers
// above it never see that flow-control error.
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
