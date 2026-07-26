package kvs

import (
	"encoding/hex"
	"io"
	"iter"
	"maps"
	"strconv"
	"time"

	"github.com/yacchi/ebml/ext/fragment"
	"github.com/yacchi/ebml/matroska"
)

type options struct {
	bufferSize       int
	assemblerOptions []fragment.Option
	inheritTags      bool
}

// Option configures a Reader.
type Option func(*options)

// WithBufferSize sets the read chunk size, which defaults to
// fragment.DefaultReadSize. A value <= 0 panics. It changes read granularity
// only: assembly is split-invariant, so the fragments are the same either way.
func WithBufferSize(n int) Option {
	if n <= 0 {
		panic("kvs: buffer size must be positive")
	}
	return func(o *options) { o.bufferSize = n }
}

// WithAssemblerOptions passes options through to the underlying
// ext/fragment.Assembler.
func WithAssemblerOptions(opts ...fragment.Option) Option {
	return func(o *options) { o.assemblerOptions = append(o.assemblerOptions, opts...) }
}

// WithoutTagInheritance disables per-key tag inheritance.
func WithoutTagInheritance() Option {
	return func(o *options) { o.inheritTags = false }
}

// Reader pulls one KVS fragment at a time from a raw GetMedia byte stream.
type Reader struct {
	next        func() (*fragment.Fragment, error, bool)
	stop        func()
	history     map[string]map[string]string
	inheritTags bool
	sticky      error
	returnedEOF bool
}

// NewReader reads a raw KVS GetMedia stream — concatenated unknown-size
// Matroska Segments — from r.
func NewReader(r io.Reader, opts ...Option) *Reader {
	// A zero bufferSize means "unset": NewReaderSize supplies the default, so the
	// number lives in exactly one place.
	o := options{inheritTags: true}
	for _, opt := range opts {
		if opt != nil {
			opt(&o)
		}
	}
	// The read loop, the EOF finalization and the "queued fragments first, then the
	// error" ordering all belong to ext/fragment.Reader; this package adds the KVS
	// reading of the tags and nothing else. iter.Pull2 is what turns that layer's
	// iterator back into the Next this package's API is spelled with -- kvs.Next
	// carries metadata alongside the fragment, which a range loop cannot.
	src := fragment.NewReaderSize(r, o.bufferSize, o.assemblerOptions...)
	next, stop := iter.Pull2(src.Fragments())
	return &Reader{
		next:        next,
		stop:        stop,
		history:     make(map[string]map[string]string),
		inheritTags: o.inheritTags,
	}
}

// Next returns the next completed fragment and its metadata. It returns io.EOF,
// and only io.EOF, when the stream ended cleanly.
//
// A FAILURE NEVER DISCARDS THE FRAGMENTS THAT PRECEDED IT -- including, at EOF,
// the Cluster that a stream cut mid-element left open, which arrives marked
// fragment.Fragment.Truncated. That ordering is ext/fragment.Reader's, not
// restated here: the fragments are delivered by the calls that follow and the
// error is reported once they run out, so a dropped GetMedia connection costs the
// caller only the bytes that never arrived. Once reported the error is sticky.
func (r *Reader) Next() (*fragment.Fragment, Metadata, error) {
	var zero Metadata
	if r.returnedEOF {
		return nil, zero, io.EOF
	}
	if r.sticky != nil {
		return nil, zero, r.sticky
	}
	f, err, ok := r.next()
	if !ok {
		// The iterator ended, which is the clean end of the input: everything it had
		// assembled has already been handed over.
		r.stop()
		r.returnedEOF = true
		return nil, zero, io.EOF
	}
	if err != nil {
		r.sticky = err
		r.stop()
		return nil, zero, err
	}
	return f, r.metadata(f), nil
}

func (r *Reader) metadata(f *fragment.Fragment) Metadata {
	tags := f.Tags()
	if r.inheritTags {
		tags = r.effectiveTags(f, tags)
	}
	m := Metadata{Tags: tags}
	m.FragmentNumber = tags[TagFragmentNumber]
	m.ContinuationToken = tags[TagContinuationToken]
	if millis, err := strconv.ParseInt(tags[TagMillisBehindNow], 10, 64); err == nil {
		m.MillisBehindNow = time.Duration(millis) * time.Millisecond
	}
	if t, err := ParseTimestamp(tags[TagProducerTimestamp]); err == nil {
		m.ProducerTimestamp = t
	}
	if t, err := ParseTimestamp(tags[TagServerTimestamp]); err == nil {
		m.ServerTimestamp = t
	}
	return m
}

func (r *Reader) effectiveTags(f *fragment.Fragment, tags map[string]string) map[string]string {
	value := f.Value(matroska.IDSegmentUUID)
	if value == nil {
		return tags
	}
	uuid := hex.EncodeToString(value.Bytes())
	effective := make(map[string]string, len(r.history[uuid])+len(tags))
	for name, tag := range r.history[uuid] {
		effective[name] = tag
	}
	for name, tag := range tags {
		effective[name] = tag
	}
	// The caller owns the map it is handed, so the history keeps one of its own.
	// Sharing a single map would let a consumer that edits Metadata.Tags rewrite
	// what every later fragment of the same Segment inherits.
	r.history[uuid] = maps.Clone(effective)
	return effective
}
