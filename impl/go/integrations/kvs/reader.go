package kvs

import (
	"encoding/hex"
	"io"
	"iter"
	"maps"
	"strconv"
	"time"

	"github.com/yacchi/ebml/impl/go/ext/fragment"
	"github.com/yacchi/ebml/impl/go/ext/tags"
	"github.com/yacchi/ebml/impl/go/matroska"
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
//
// The same Reader reads a FINITE stream of the same shape, which is what
// GetMediaForFragmentList returns for a fragment list: nothing here assumes the
// input continues, and the end of the input closes the document that was open,
// exactly as the end of a live capture does. Only the source differs.
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
	// MetadataComplete comes FIRST so a caller's own WithMetadataComplete overrides
	// it: this package knows the KVS layout, and passing that knowledge on is what it
	// is for, but it is not a decision the caller may not revisit.
	asmOpts := append([]fragment.Option{fragment.WithMetadataComplete(MetadataComplete)},
		o.assemblerOptions...)
	src := fragment.NewReaderSize(r, o.bufferSize, asmOpts...)
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
// AN IN-BAND FAILURE IS REPORTED, NEVER MERELY AVAILABLE. When a fragment carries
// AWS_KINESISVIDEO_ERROR_CODE, Next hands that fragment over first and then
// returns the *StreamError, which is sticky; the stream is not read further,
// because GetMedia sends nothing after the error it reports. There is no option
// to disable this and none to enable it: a stream KVS stopped on an error must
// never be able to read as a short clean end, which is what "the caller may call
// Metadata.Err" amounted to. Metadata.Err still answers for the fragment itself,
// for a caller that wants the failure alongside the document that carried it.
//
// The one shape this cannot report is an error KVS sends with no Cluster of its
// own: fragments are what this Reader delivers, so error tags arriving in a
// document that assembles no fragment are not seen here.
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
	m := r.metadata(f)
	if streamErr := m.Err(); streamErr != nil {
		// GetMedia has reported a failure in-band and this document is the last one
		// it will send, so the pull is stopped here. The error is LATCHED rather than
		// returned with the fragment: a caller that leaves the loop on a non-nil
		// error has then already received every fragment, including this one, which
		// is the ordering the truncated tail uses for the same reason.
		r.sticky = streamErr
		r.stop()
	}
	return f, m, nil
}

func (r *Reader) metadata(f *fragment.Fragment) Metadata {
	pairs := tags.Read(f.Segment).All(tags.Target{})
	if r.inheritTags {
		pairs = r.effectiveTags(f, pairs)
	}
	m := Metadata{Tags: pairs}
	m.FragmentNumber = pairs[TagFragmentNumber]
	m.ContinuationToken = pairs[TagContinuationToken]
	if millis, err := strconv.ParseInt(pairs[TagMillisBehindNow], 10, 64); err == nil {
		m.MillisBehindNow = time.Duration(millis) * time.Millisecond
	}
	if t, err := ParseTimestamp(pairs[TagProducerTimestamp]); err == nil {
		m.ProducerTimestamp = t
	}
	if t, err := ParseTimestamp(pairs[TagServerTimestamp]); err == nil {
		m.ServerTimestamp = t
	}
	return m
}

func (r *Reader) effectiveTags(f *fragment.Fragment, pairs map[string]string) map[string]string {
	value := f.Value(matroska.IDSegmentUUID)
	if value == nil {
		return pairs
	}
	uuid := hex.EncodeToString(value.Bytes())
	effective := make(map[string]string, len(r.history[uuid])+len(pairs))
	for name, tag := range r.history[uuid] {
		effective[name] = tag
	}
	for name, tag := range pairs {
		effective[name] = tag
	}
	// The caller owns the map it is handed, so the history keeps one of its own.
	// Sharing a single map would let a consumer that edits Metadata.Tags rewrite
	// what every later fragment of the same Segment inherits.
	r.history[uuid] = maps.Clone(effective)
	return effective
}
