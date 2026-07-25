// Package kvs interprets the metadata Amazon Kinesis Video Streams (KVS)
// carries in a Matroska fragment stream, on top of the core library's
// ext/fragment. It reads bytes a caller already obtained -- there is no
// GetMedia API wrapper here and no AWS SDK dependency at all.
//
// This package is not affiliated with, endorsed by, or sponsored by Amazon Web
// Services; the service name appears here descriptively only.
package kvs

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

// The SimpleTag names GetMedia adds to every chunk it returns, and the two it
// adds only when the stream stops on an error. They are ordinary Matroska tags:
// ext/fragment reads them without knowing any of this, and naming them is what
// this package is for.
const (
	TagContinuationToken = "AWS_KINESISVIDEO_CONTINUATION_TOKEN"
	TagMillisBehindNow   = "AWS_KINESISVIDEO_MILLIS_BEHIND_NOW"
	TagFragmentNumber    = "AWS_KINESISVIDEO_FRAGMENT_NUMBER"
	TagServerTimestamp   = "AWS_KINESISVIDEO_SERVER_TIMESTAMP"
	TagProducerTimestamp = "AWS_KINESISVIDEO_PRODUCER_TIMESTAMP"
	TagErrorCode         = "AWS_KINESISVIDEO_ERROR_CODE"
	TagErrorID           = "AWS_KINESISVIDEO_ERROR_ID"
)

// Metadata is the typed view of one fragment's AWS tags. Every field is the
// zero value when its tag is absent; a fragment carrying no Tags element at all
// yields a zero Metadata with a non-nil, possibly empty, Tags map.
type Metadata struct {
	FragmentNumber    string
	ProducerTimestamp time.Time
	ServerTimestamp   time.Time
	ContinuationToken string
	MillisBehindNow   time.Duration
	Tags              map[string]string
}

// StreamError is the in-band failure GetMedia reports through
// AWS_KINESISVIDEO_ERROR_CODE and AWS_KINESISVIDEO_ERROR_ID.
type StreamError struct {
	Code string
	ID   int
}

func (e *StreamError) Error() string {
	if e.ID == 0 {
		return fmt.Sprintf("kvs: stream error: %s", e.Code)
	}
	return fmt.Sprintf("kvs: stream error %d: %s", e.ID, e.Code)
}

var errorDescriptions = map[int]string{
	3002: "Error writing to the stream",
	4000: "Requested fragment is not found",
	4500: "Access denied for the stream's KMS key",
	4501: "Stream's KMS key is disabled",
	4502: "Validation error on the stream's KMS key",
	4503: "KMS key specified in the stream is unavailable",
	4504: "Invalid usage of the KMS key specified in the stream",
	4505: "Invalid state of the KMS key specified in the stream",
	4506: "Unable to find the KMS key specified in the stream",
	5000: "Internal error",
}

// DescribeErrorID returns the meaning AWS documents for a GetMedia error ID,
// or "" for an ID that is not documented.
func DescribeErrorID(id int) string {
	return errorDescriptions[id]
}

// Err reports the in-band GetMedia error this fragment carries, or nil.
// It returns non-nil whenever AWS_KINESISVIDEO_ERROR_CODE is present, even
// if AWS_KINESISVIDEO_ERROR_ID is missing.
func (m Metadata) Err() error {
	code, ok := m.Tags[TagErrorCode]
	if !ok {
		return nil
	}
	id, _ := strconv.Atoi(m.Tags[TagErrorID])
	return &StreamError{Code: code, ID: id}
}

// ParseTimestamp converts an AWS_KINESISVIDEO_PRODUCER_TIMESTAMP or
// AWS_KINESISVIDEO_SERVER_TIMESTAMP value — a decimal seconds-since-epoch
// string with an optional fractional part — into a UTC time.Time. Up to 9
// fractional digits are honored as nanoseconds; extra digits are truncated.
//
// Both halves must be unsigned decimal digits. A negative value is rejected
// rather than interpreted: KVS times an event since the Unix epoch and never
// emits one, and reading "-1.5" as a signed second plus an unsigned fraction
// would yield -0.5s, so accepting the sign would mean returning a wrong time
// for the one input that could carry it.
func ParseTimestamp(s string) (time.Time, error) {
	intPart, fracPart := s, ""
	if i := strings.IndexByte(s, '.'); i >= 0 {
		intPart, fracPart = s[:i], s[i+1:]
	}
	sec, err := strconv.ParseUint(intPart, 10, 63)
	if err != nil {
		return time.Time{}, fmt.Errorf("kvs: timestamp %q: %w", s, err)
	}
	var nsec uint64
	if fracPart != "" {
		if len(fracPart) > 9 {
			fracPart = fracPart[:9]
		}
		frac, err := strconv.ParseUint(fracPart, 10, 64)
		if err != nil {
			return time.Time{}, fmt.Errorf("kvs: timestamp %q: %w", s, err)
		}
		for i := len(fracPart); i < 9; i++ {
			frac *= 10
		}
		nsec = frac
	}
	return time.Unix(int64(sec), int64(nsec)).UTC(), nil
}
