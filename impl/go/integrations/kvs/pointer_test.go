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
	pinType[string](m.FragmentNumber)
	pinType[string](m.ContinuationToken)
	pinType[time.Time](m.ProducerTimestamp)
	pinType[time.Time](m.ServerTimestamp)
	pinType[time.Duration](m.MillisBehindNow)
	pinType[map[string]string](m.Tags)
	pinType[func() error](m.Err)
	pinType[func(string) (time.Time, error)](kvs.ParseTimestamp)

	// ext/fragment: "MetadataComplete, the KVS-specific WithMetadataComplete
	// predicate". The conversion is the claim: it must remain assignable to the
	// option the core package takes.
	pinType[func(*fragment.Fragment, parser.ElementID) bool](kvs.MetadataComplete)
	pinType[fragment.Option](fragment.WithMetadataComplete(kvs.MetadataComplete))

	// ext/fragment: "ClusterTime and the wall-clock reading of StartTime, EndTime
	// and BlockTime, with the epoch basis a KVS Cluster.Timestamp actually uses."
	pinType[func(*fragment.Fragment) time.Time](kvs.ClusterTime)
	pinType[func(*fragment.Fragment) time.Time](kvs.StartTime)
	pinType[func(*fragment.Fragment) time.Time](kvs.EndTime)
	pinType[func(*fragment.Fragment, *parser.SimpleBlock) time.Time](kvs.BlockTime)

	// ext/fragment and ext/tags: "Reader.Next, which REPORTS the in-band
	// StreamError carried by AWS_KINESISVIDEO_ERROR_CODE / _ERROR_ID."
	r := kvs.NewReader(strings.NewReader(""))
	pinType[func() (*fragment.Fragment, kvs.Metadata, error)](r.Next)
	pinType[error](&kvs.StreamError{})
	if kvs.TagErrorCode != "AWS_KINESISVIDEO_ERROR_CODE" || kvs.TagErrorID != "AWS_KINESISVIDEO_ERROR_ID" {
		t.Errorf("the error tag names the core package docs spell out have changed: %q / %q",
			kvs.TagErrorCode, kvs.TagErrorID)
	}

	// ext/tags: "Reader's per-key tag inheritance ... switched off by
	// WithoutTagInheritance."
	pinType[kvs.Option](kvs.WithoutTagInheritance())

	// The empty input is a clean end of stream, which keeps this test about the
	// names rather than about behaviour.
	if _, _, err := r.Next(); err != io.EOF {
		t.Fatalf("Next on empty input = %v, want io.EOF", err)
	}
}

// pinType fails to COMPILE when its argument is not assignable to T, which is
// the whole of what this file asserts. It replaces the `var _ T = x` spelling of
// the same assertion: staticcheck reads that as a redundant type declaration
// (QF1011) precisely because the compiler can infer what the line exists to
// state, so the assertion is moved somewhere the type is load-bearing.
func pinType[T any](T) {}
