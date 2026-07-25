package main

import (
	"errors"
	"fmt"
	"io"

	"github.com/yacchi/ebml/matroska"
	"github.com/yacchi/ebml/parser"
)

// stream drives a parser.Cursor over a blocking io.Reader. It exists because the
// cursor's central distinction -- "the next chunk is due" versus "the input is
// over" -- collapses for input a command can simply read: the answer to
// NeedMoreData is another Read, and the answer to a Read at EOF is Finalize. So a
// command's own loop stays a plain pull loop and sees only elements, io.EOF once,
// and a structural failure.
//
// Every node it hands out obeys the cursor's rule: a node is valid only until the
// next Next call.
type stream struct {
	c   *parser.Cursor
	r   io.Reader
	buf []byte
	// drained means the reader reported EOF and the cursor has been finalized, so
	// no further input can arrive.
	drained bool
}

// newStream reads raw EBML from r, classifying elements through the standard
// Matroska registry and closing unknown-size masters with matroska's own
// StreamBoundary -- the same policy ext/fragment is built on, not a copy of it.
// A KVS GetMedia body needs both halves of that rule: a Segment ends where the
// next top-level element begins, and an unknown-size Cluster ends at the first
// element that cannot be its child. Without the second half the CLI renders a
// live stream's trailing Tags as children of its Cluster.
func newStream(r io.Reader) *stream {
	return &stream{
		c:   parser.NewCursor(matroska.KindForElementID, parser.WithBoundary(matroska.StreamBoundary)),
		r:   r,
		buf: make([]byte, 64*1024),
	}
}

// Next reports the next element header or master end, reading more input for as
// long as the cursor asks for it. It reports io.EOF once the whole input has been
// reported.
func (s *stream) Next() (parser.Node, error) {
	for {
		node, err := s.c.Next()
		if err == nil {
			return node, nil
		}
		if !isNeedMoreData(err) {
			return nil, err // io.EOF, or a structural failure
		}
		if err := s.fill(); err != nil {
			return nil, err
		}
	}
}

// Payload delivers a leaf's payload, reading more input while its bytes are still
// outstanding. The node stays valid across those reads, which is what lets the
// decision taken on the header stand until the bytes are there.
//
// The bytes are the cursor's own, valid only until the next Next: the commands format
// them on the spot, so nothing here needs a copy.
func (s *stream) Payload(leaf *parser.LeafNode) ([]byte, error) {
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

// Offset reports the absolute offset the cursor has reached, so a failure can be
// located in the input.
func (s *stream) Offset() int64 { return s.c.Offset() }

// fill pushes the next chunk of input into the cursor, or declares the input over
// once the reader is exhausted -- which is where a stream ending inside an element
// is reported as truncated.
func (s *stream) fill() error {
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
