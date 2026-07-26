package fragment

import (
	"errors"
	"fmt"
	"io"
	"iter"
)

// DefaultReadSize is the chunk size NewReader reads with: 64 KiB.
//
// It has no effect on what is assembled. Feed is split-invariant, so the sequence
// of Fragments and their contents are identical however the input is chunked; the
// size governs read granularity only, which is why NewReader does not take it as
// an option and NewReaderSize exists for the caller who must set it.
const DefaultReadSize = 64 * 1024

// Reader assembles Fragments from a byte source it OWNS, which is the other half
// of this package's entry surface: Assembler is for a consumer that pushes bytes
// with Feed, and Reader is for one that would rather hand over the source.
//
// The two differ in nothing but who supplies the bytes. Reader drives the same
// Assembler, takes the same Options, and so recovers from the same errors in the
// same way -- WithResync and WithSkipContentErrors are neither weakened nor
// unavailable here.
//
// WHAT THIS LAYER OWNS is the end-of-input rule: the Fragments that completed
// before a failure are delivered BEFORE the failure is reported, including the
// Cluster that a stream cut mid-element left open, which arrives marked
// Truncated. Assembler.Feed and Assembler.Finalize both state that rule for a
// caller that pushes bytes; a caller that hands over the source would otherwise
// have to restate it, and getting it wrong silently loses the media that had
// already decoded.
//
// A Reader is not safe for concurrent use.
type Reader struct {
	source io.Reader
	asm    *Assembler
	buf    []byte

	// queue holds the Fragments a single Feed or Finalize completed, waiting to be
	// delivered one at a time.
	queue []*Fragment

	sourceDone bool
	finalized  bool
	sticky     error // latched failure, reported once the queue runs out
}

// NewReader returns a Reader over r, reading in DefaultReadSize chunks. The
// options are the Assembler's own and mean exactly what they mean there.
func NewReader(r io.Reader, opts ...Option) *Reader {
	return NewReaderSize(r, DefaultReadSize, opts...)
}

// NewReaderSize is NewReader with an explicit read chunk size, for a caller whose
// source has a granularity of its own. A size <= 0 uses DefaultReadSize. It
// changes nothing about the Fragments produced -- see DefaultReadSize.
func NewReaderSize(r io.Reader, size int, opts ...Option) *Reader {
	if size <= 0 {
		size = DefaultReadSize
	}
	return &Reader{
		source: r,
		asm:    New(opts...),
		buf:    make([]byte, size),
	}
}

// Fragments iterates the Fragments the source completes, reading from it whenever
// the assembler needs input. It is the whole reading surface here, for the reason
// stream.Stream.Nodes is there: this layer OWNS the source, so it answers
// need-more-data itself and only two outcomes ever reach the consumer, which is
// exactly when the host language's iterator is a correct spelling of the pull
// rather than a lossy one. There is deliberately no exported Next.
//
// The end of the input ends the iteration. Every other failure is yielded once, as
// the final pair, with a nil Fragment -- AFTER every Fragment that completed
// before it, so a dropped connection costs the caller only the bytes that never
// arrived and no separate Err call can lose the diagnosis.
//
//	for f, err := range rd.Fragments() {
//	    if err != nil {
//	        return err
//	    }
//	    ...
//	}
//
// Breaking out of the loop leaves the Reader exactly where it stopped, so ranging
// again resumes with the following Fragment. A failure is latched, as it is in the
// Assembler, so ranging again after one reports the same failure.
func (rd *Reader) Fragments() iter.Seq2[*Fragment, error] {
	return func(yield func(*Fragment, error) bool) {
		for {
			f, err := rd.next()
			if errors.Is(err, io.EOF) {
				return
			}
			if err != nil {
				yield(nil, err)
				return
			}
			if !yield(f, nil) {
				return
			}
		}
	}
}

// next reports the next assembled Fragment, or io.EOF once the whole input has
// been reported. It is unexported so that Fragments stays the only reading
// surface -- see its doc.
func (rd *Reader) next() (*Fragment, error) {
	for len(rd.queue) == 0 {
		// The queue is empty, so a latched failure is now the honest answer: nothing
		// that completed before it is still waiting.
		if rd.sticky != nil {
			return nil, rd.sticky
		}
		if rd.sourceDone {
			return nil, io.EOF
		}
		if err := rd.fill(); err != nil {
			rd.sticky = err
		}
	}
	f := rd.queue[0]
	rd.queue = rd.queue[1:]
	return f, nil
}

// fill pushes the next chunk of input into the assembler, or finalizes it once the
// source is exhausted -- which is where a stream that ended inside an element is
// reported as truncated, together with the Cluster it left open.
//
// It queues whatever completed BEFORE returning an error, which is the whole point
// of this layer: the error waits for the queue to run out.
func (rd *Reader) fill() error {
	n, err := rd.source.Read(rd.buf)
	if n > 0 {
		frags, ferr := rd.asm.Feed(rd.buf[:n])
		rd.queue = append(rd.queue, frags...)
		if ferr != nil {
			return ferr
		}
	}
	if err == nil {
		return nil
	}
	if !errors.Is(err, io.EOF) {
		return fmt.Errorf("read input: %w", err)
	}
	rd.sourceDone = true
	if rd.finalized {
		return nil
	}
	rd.finalized = true
	frags, ferr := rd.asm.Finalize()
	rd.queue = append(rd.queue, frags...)
	return ferr
}
