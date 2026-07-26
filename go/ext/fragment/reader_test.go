package fragment_test

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"testing"

	"github.com/yacchi/ebml/ext/fragment"
	"github.com/yacchi/ebml/matroska"
	"github.com/yacchi/ebml/parser"
)

// ---- The source-owning entry point ----
//
// Reader must produce EXACTLY what a caller pushing the same bytes through
// Assembler.Feed produces, and must deliver a failure only after the Fragments
// that completed before it. Those are the two properties a consumer would
// otherwise have to reimplement in its own read loop.

// collect ranges a Reader to exhaustion and returns the Fragments and the final
// error, mirroring how a consumer writes the loop.
func collect(rd *fragment.Reader) ([]*fragment.Fragment, error) {
	var (
		frags []*fragment.Fragment
		final error
	)
	for f, err := range rd.Fragments() {
		if err != nil {
			final = err
			break
		}
		frags = append(frags, f)
	}
	return frags, final
}

// errAfter is a source that delivers body and then fails with a non-EOF error,
// which is what a dropped connection looks like to the layer above.
type errAfter struct {
	body []byte
	err  error
	at   int
}

func (e *errAfter) Read(p []byte) (int, error) {
	if e.at >= len(e.body) {
		return 0, e.err
	}
	n := copy(p, e.body[e.at:])
	e.at += n
	return n, nil
}

func TestReaderMatchesFeed(t *testing.T) {
	raw := loadHex(t, "multi_segment")
	// The baseline is Feed AND Finalize: a trailing unknown-size Cluster completes
	// only when the input ends, which is precisely the half a Reader takes over.
	a := fragment.New()
	want, err := a.Feed(raw)
	if err != nil {
		t.Fatalf("Feed: %v", err)
	}
	tail, err := a.Finalize()
	if err != nil {
		t.Fatalf("Finalize: %v", err)
	}
	want = append(want, tail...)
	if len(want) == 0 {
		t.Fatal("fixture completed no fragment; the comparison would be vacuous")
	}
	// Every read size must give the same answer: the Reader inherits Feed's
	// split-invariance because it changes nothing but who supplies the bytes.
	for _, size := range []int{1, 3, 17, 512, fragment.DefaultReadSize} {
		t.Run(fmt.Sprintf("read=%d", size), func(t *testing.T) {
			got, err := collect(fragment.NewReaderSize(bytes.NewReader(raw), size))
			if err != nil {
				t.Fatalf("Fragments: %v", err)
			}
			if len(got) != len(want) {
				t.Fatalf("got %d fragments, want %d", len(got), len(want))
			}
			for i := range got {
				if len(got[i].Blocks) != len(want[i].Blocks) {
					t.Errorf("fragment %d: %d blocks, want %d", i, len(got[i].Blocks), len(want[i].Blocks))
				}
				gotTag, _ := fragTag(got[i], "AWS_KINESISVIDEO_FRAGMENT_NUMBER")
				wantTag, _ := fragTag(want[i], "AWS_KINESISVIDEO_FRAGMENT_NUMBER")
				if gotTag != wantTag {
					t.Errorf("fragment %d: tag %q, want %q", i, gotTag, wantTag)
				}
			}
		})
	}
}

// TestReaderCleanEndYieldsNoError pins the arity: the end of the input ends the
// iteration instead of producing a value, so a clean stream never yields an error
// -- there is no io.EOF to filter out.
func TestReaderCleanEndYieldsNoError(t *testing.T) {
	rd := fragment.NewReader(bytes.NewReader(loadHex(t, "topology_basic")))
	frags, err := collect(rd)
	if err != nil {
		t.Fatalf("clean stream yielded %v; the end of input must end the iteration", err)
	}
	if len(frags) == 0 {
		t.Fatal("clean stream yielded no fragment")
	}
	// Ranging an exhausted Reader yields nothing, as ranging an exhausted Stream does.
	if again, err := collect(rd); len(again) != 0 || err != nil {
		t.Fatalf("re-ranging an exhausted Reader = %d fragments, %v; want none", len(again), err)
	}
}

// TestReaderDeliversSalvagedTailBeforeError is the rule this layer owns: the
// Cluster a cut connection left open arrives as an ordinary yielded Fragment, and
// the truncation is the final pair AFTER it.
func TestReaderDeliversSalvagedTailBeforeError(t *testing.T) {
	raw := synFragment(
		synTracks(synTrackEntry(1, "AUDIO_FROM_CUSTOMER")),
		synUnknownCluster(0,
			synSimpleBlock(1, 0, []byte{0x01, 0x02, 0x03, 0x04}),
			synSimpleBlock(1, 20, []byte{0x05, 0x06, 0x07, 0x08}),
			synSimpleBlock(1, 40, []byte{0x09, 0x0A, 0x0B, 0x0C}),
		),
	)
	rd := fragment.NewReaderSize(bytes.NewReader(cutInside(raw, 3)), 8)
	frags, err := collect(rd)
	if !errors.Is(err, parser.ErrTruncated) {
		t.Fatalf("final error = %v, want the truncation still reported", err)
	}
	if len(frags) != 1 {
		t.Fatalf("got %d fragments, want the 1 salvaged Cluster delivered before the error", len(frags))
	}
	if !frags[0].Truncated {
		t.Error("salvaged fragment is not marked Truncated")
	}
	if len(frags[0].Blocks) != 2 {
		t.Errorf("salvaged %d blocks, want the 2 that decoded before the cut", len(frags[0].Blocks))
	}
	// The failure is latched, exactly as it is in the Assembler.
	if _, again := collect(rd); !errors.Is(again, parser.ErrTruncated) {
		t.Fatalf("re-ranging after a failure = %v, want the same latched failure", again)
	}
}

// TestReaderDeliversQueueBeforeAReadError covers the other half of the same rule:
// the failure comes from the SOURCE rather than the bytes, and still waits for the
// fragments that completed.
func TestReaderDeliversQueueBeforeAReadError(t *testing.T) {
	sentinel := errors.New("connection reset")
	raw := loadHex(t, "multi_segment")
	rd := fragment.NewReaderSize(&errAfter{body: raw, err: sentinel}, 64)
	frags, err := collect(rd)
	if !errors.Is(err, sentinel) {
		t.Fatalf("final error = %v, want the source's own error to survive wrapping", err)
	}
	if len(frags) == 0 {
		t.Fatal("a read error discarded the fragments that had completed")
	}
}

// TestReaderResumesAfterBreak pins the iterator's resumption rule: breaking out
// leaves the Reader where it stopped, so a consumer may stop and continue -- the
// undelivered fragments are not lost with the loop.
func TestReaderResumesAfterBreak(t *testing.T) {
	raw := loadHex(t, "multi_segment")
	all, err := collect(fragment.NewReader(bytes.NewReader(raw)))
	if err != nil {
		t.Fatalf("baseline: %v", err)
	}
	if len(all) < 2 {
		t.Skipf("fixture has %d fragments; this test needs at least 2", len(all))
	}

	rd := fragment.NewReader(bytes.NewReader(raw))
	var first *fragment.Fragment
	for f, err := range rd.Fragments() {
		if err != nil {
			t.Fatalf("first range: %v", err)
		}
		first = f
		break
	}
	if first == nil {
		t.Fatal("first range yielded nothing")
	}
	rest, err := collect(rd)
	if err != nil {
		t.Fatalf("resumed range: %v", err)
	}
	if got := 1 + len(rest); got != len(all) {
		t.Fatalf("break-and-resume delivered %d fragments, want %d", got, len(all))
	}
}

// TestReaderAppliesAssemblerOptions checks that the options mean the same thing at
// both entry points: the whole reason Reader drives an Assembler rather than
// reimplementing one is that WithResync and friends cannot go missing here.
func TestReaderAppliesAssemblerOptions(t *testing.T) {
	raw := loadHex(t, "multi_segment")
	rd := fragment.NewReader(bytes.NewReader(raw), fragment.WithMaxRetainedPayload(0))
	frags, err := collect(rd)
	if err != nil {
		t.Fatalf("Fragments: %v", err)
	}
	if len(frags) == 0 {
		t.Fatal("no fragment to inspect")
	}
	// With the cap at zero, structure is retained and no metadata payload is: the
	// option reached the Assembler the Reader built.
	var sawElided bool
	for _, el := range frags[0].Segment.Descendants(matroska.IDTagString) {
		if el.Truncated && el.Payload == nil {
			sawElided = true
		}
	}
	if !sawElided {
		t.Error("WithMaxRetainedPayload(0) did not reach the assembler the Reader owns")
	}
}

// TestReaderSizeFallsBackToDefault keeps the constructor total: a nonsensical size
// is not a panic and not a zero-length buffer that could never make progress.
func TestReaderSizeFallsBackToDefault(t *testing.T) {
	for _, size := range []int{0, -1} {
		frags, err := collect(fragment.NewReaderSize(bytes.NewReader(loadHex(t, "topology_basic")), size))
		if err != nil {
			t.Fatalf("size %d: %v", size, err)
		}
		if len(frags) == 0 {
			t.Fatalf("size %d produced no fragment", size)
		}
	}
}

var _ io.Reader = (*errAfter)(nil)
