package kvs_test

import (
	"io"
	"strings"
	"testing"
	"time"

	"github.com/yacchi/ebml/impl/go/ext/fragment"
	"github.com/yacchi/ebml/impl/go/integrations/kvs"
	"github.com/yacchi/ebml/impl/go/parser"
)

// The package docs of ext/fragment and ext/tags point a consumer here by NAME,
// because this module is not resolvable from the core one and a reader who never
// learns it exists hand-rolls it instead -- which is what happened, and what
// plans/KVS-CONSUMER-FEEDBACK-ROUND4.md F8 records the cost of.
//
// Those pointers are prose in a module that cannot import this one, so nothing in
// the core module can check them. This test is the other half: it is compiled in
// the module that CAN see both, and it fails to build the moment a symbol either
// pointer names stops existing or changes shape. Renaming anything here without
// rewriting those two comments is the drift this pins, per CLAUDE.md's rule that a
// cross-package claim is either compiler-checked or deleted.
//
// It asserts existence and signature only. What each one DOES is tested by the
// tests named beside it.
func TestNamesTheCorePackageDocsPointAt(t *testing.T) {
	// ext/fragment: "Metadata, the typed per-fragment view of the AWS tags, and
	// ParseTimestamp for the format their timestamps are written in."
	var m kvs.Metadata
	var _ string = m.FragmentNumber
	var _ string = m.ContinuationToken
	var _ time.Time = m.ProducerTimestamp
	var _ time.Time = m.ServerTimestamp
	var _ time.Duration = m.MillisBehindNow
	var _ map[string]string = m.Tags
	var _ func() error = m.Err
	var _ func(string) (time.Time, error) = kvs.ParseTimestamp

	// ext/fragment: "MetadataComplete, the KVS-specific WithMetadataComplete
	// predicate". The conversion is the claim: it must remain assignable to the
	// option the core package takes.
	var complete func(*fragment.Fragment, parser.ElementID) bool = kvs.MetadataComplete
	var _ fragment.Option = fragment.WithMetadataComplete(complete)

	// ext/fragment: "ClusterTime and the wall-clock reading of StartTime, EndTime
	// and BlockTime, with the epoch basis a KVS Cluster.Timestamp actually uses."
	var _ func(*fragment.Fragment) time.Time = kvs.ClusterTime
	var _ func(*fragment.Fragment) time.Time = kvs.StartTime
	var _ func(*fragment.Fragment) time.Time = kvs.EndTime
	var _ func(*fragment.Fragment, *parser.SimpleBlock) time.Time = kvs.BlockTime

	// ext/fragment and ext/tags: "Reader.Next, which REPORTS the in-band
	// StreamError carried by AWS_KINESISVIDEO_ERROR_CODE / _ERROR_ID."
	r := kvs.NewReader(strings.NewReader(""))
	var _ func() (*fragment.Fragment, kvs.Metadata, error) = r.Next
	var _ error = &kvs.StreamError{}
	if kvs.TagErrorCode != "AWS_KINESISVIDEO_ERROR_CODE" || kvs.TagErrorID != "AWS_KINESISVIDEO_ERROR_ID" {
		t.Errorf("the error tag names the core package docs spell out have changed: %q / %q",
			kvs.TagErrorCode, kvs.TagErrorID)
	}

	// ext/tags: "Reader's per-key tag inheritance ... switched off by
	// WithoutTagInheritance."
	var _ kvs.Option = kvs.WithoutTagInheritance()

	// The empty input is a clean end of stream, which keeps this test about the
	// names rather than about behaviour.
	if _, _, err := r.Next(); err != io.EOF {
		t.Fatalf("Next on empty input = %v, want io.EOF", err)
	}
}
