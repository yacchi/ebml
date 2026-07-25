package kvs

import (
	"encoding/hex"
	"io"
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

// WithBufferSize sets the read chunk size (default 64 KiB). A value <= 0 panics.
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
	source      io.Reader
	assembler   *fragment.Assembler
	buffer      []byte
	queue       []*fragment.Fragment
	history     map[string]map[string]string
	inheritTags bool
	sourceDone  bool
	finalized   bool
	sticky      error
	returnedEOF bool
}

// NewReader reads a raw KVS GetMedia stream — concatenated unknown-size
// Matroska Segments — from r.
func NewReader(r io.Reader, opts ...Option) *Reader {
	o := options{bufferSize: 64 * 1024, inheritTags: true}
	for _, opt := range opts {
		if opt != nil {
			opt(&o)
		}
	}
	return &Reader{
		source:      r,
		assembler:   fragment.New(o.assemblerOptions...),
		buffer:      make([]byte, o.bufferSize),
		history:     make(map[string]map[string]string),
		inheritTags: o.inheritTags,
	}
}

// Next returns the next completed fragment and its metadata. It returns io.EOF,
// and only io.EOF, when the stream ended cleanly.
func (r *Reader) Next() (*fragment.Fragment, Metadata, error) {
	var zero Metadata
	if r.returnedEOF {
		return nil, zero, io.EOF
	}
	for len(r.queue) == 0 {
		if r.sticky != nil {
			return nil, zero, r.sticky
		}
		if r.sourceDone {
			r.returnedEOF = true
			return nil, zero, io.EOF
		}
		n, err := r.source.Read(r.buffer)
		if n > 0 {
			frags, ferr := r.assembler.Feed(r.buffer[:n])
			if ferr != nil {
				r.sticky = ferr
				return nil, zero, ferr
			}
			r.queue = append(r.queue, frags...)
		}
		if err != nil {
			if err != io.EOF {
				r.sticky = err
				return nil, zero, err
			}
			r.sourceDone = true
			if !r.finalized {
				r.finalized = true
				frags, ferr := r.assembler.Finalize()
				if ferr != nil {
					r.sticky = ferr
					return nil, zero, ferr
				}
				r.queue = append(r.queue, frags...)
			}
		}
	}
	f := r.queue[0]
	r.queue = r.queue[1:]
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
