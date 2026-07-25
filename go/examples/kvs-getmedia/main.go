// Command kvs-getmedia is a runnable, end-to-end demonstration that this library
// parses an Amazon Kinesis Video Streams (KVS) GetMedia byte stream in pure Go —
// the capability AWS otherwise ships officially only as the Java
// amazon-kinesis-video-streams-parser-library.
//
// A live KVS GetMedia response is a single continuous HTTP body of concatenated
// unknown-size Matroska Segments, one per KVS fragment. Each Segment carries its
// Tracks + Tags (AWS metadata) and exactly one known-size Cluster of SimpleBlocks
// (for Amazon Connect: 8 kHz / 16-bit PCM, one mono track per channel). This
// program feeds that raw stream through fragment.Assembler and prints, for every
// emitted Fragment:
//
//   - AWS_KINESISVIDEO_FRAGMENT_NUMBER
//   - AWS_KINESISVIDEO_PRODUCER_TIMESTAMP, parsed inline into a time.Time
//   - ContactId (when present)
//   - the track list
//   - the Cluster timestamp and the fragment's first block time
//   - per-track decoded PCM byte counts
//
// The library exposes AWS tags only through the generic Fragment.Tag accessor;
// interpreting a specific tag (here, parsing the decimal-seconds producer
// timestamp) is the consumer's job — this program shows exactly how.
//
// Usage (run from the go/ directory):
//
//	go run ./examples/kvs-getmedia path/to/stream.mkv   # from a file
//	some-getmedia-source | go run ./examples/kvs-getmedia # or from stdin
package main

import (
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/yacchi/ebml/ext/fragment"
	"github.com/yacchi/ebml/matroska"
)

// Well-known AWS KVS tag names carried in each fragment's Tags element. They are
// ordinary SimpleTag entries, read through the library's generic Fragment.Tag.
const (
	tagFragmentNumber    = "AWS_KINESISVIDEO_FRAGMENT_NUMBER"
	tagContinuationToken = "AWS_KINESISVIDEO_CONTINUATION_TOKEN"
	tagProducerTimestamp = "AWS_KINESISVIDEO_PRODUCER_TIMESTAMP"
	tagContactID         = "ContactId"
)

func main() {
	if err := run(inputReader(), os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, "kvs-getmedia:", err)
		os.Exit(1)
	}
}

// inputReader returns the raw EBML stream source: the file named by the first
// argument, or stdin when no argument is given.
func inputReader() io.Reader {
	if len(os.Args) > 1 && os.Args[1] != "-" {
		f, err := os.Open(os.Args[1])
		if err != nil {
			fmt.Fprintln(os.Stderr, "kvs-getmedia:", err)
			os.Exit(1)
		}
		// Left open for process lifetime; the OS reclaims it on exit.
		return f
	}
	return os.Stdin
}

// run is the testable core: it reads a raw KVS GetMedia EBML stream from r in
// arbitrary chunks, assembles fragments incrementally, and writes a human report
// to w. Chunk boundaries are irrelevant — the assembler is split-invariant — so a
// modest fixed read buffer is fine.
func run(r io.Reader, w io.Writer) error {
	a := fragment.New()
	buf := make([]byte, 4096)
	n := 0
	var previousTags map[string]string
	var previousUUID string

	report := func(frags []*fragment.Fragment) {
		for _, f := range frags {
			n++
			tags, uuid := effectiveTags(f, previousTags, previousUUID)
			printFragment(w, n, f, tags)
			if len(tags) > 0 {
				previousTags, previousUUID = tags, uuid
			}
		}
	}

	for {
		read, err := r.Read(buf)
		if read > 0 {
			frags, ferr := a.Feed(buf[:read])
			if ferr != nil {
				return ferr
			}
			report(frags)
		}
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}
	}

	tail, err := a.Finalize()
	if err != nil {
		return err
	}
	report(tail)

	if n == 0 {
		fmt.Fprintln(w, "no fragments found in input")
	}
	return nil
}

// printFragment renders one assembled fragment.
func effectiveTags(f *fragment.Fragment, previous map[string]string, previousUUID string) (map[string]string, string) {
	tags := f.Tags()
	uuid := ""
	if value := f.Value(matroska.IDSegmentUUID); value != nil {
		uuid = fmt.Sprintf("%x", value.Bytes())
	}
	if len(tags) == 0 && uuid != "" && uuid == previousUUID {
		// KVS consumers may inherit tagless metadata; this policy belongs here,
		// not in the generic fragment assembler.
		tags = previous
	}
	return tags, uuid
}

func tag(f *fragment.Fragment, tags map[string]string, name string) (string, bool) {
	if value, ok := f.Tag(name); ok {
		return value, true
	}
	value, ok := tags[name]
	return value, ok
}

func printFragment(w io.Writer, index int, f *fragment.Fragment, tags map[string]string) {
	fmt.Fprintf(w, "fragment %d\n", index)

	if fn, ok := tag(f, tags, tagFragmentNumber); ok {
		fmt.Fprintf(w, "  fragment_number:   %s\n", fn)
	}
	if token, ok := tag(f, tags, tagContinuationToken); ok {
		fmt.Fprintf(w, "  continuation_token: %s\n", token)
	}

	// The producer timestamp is a decimal seconds-since-epoch string (e.g.
	// "1700000000.512"). Parsing it into a time.Time is the consumer's
	// responsibility; parseProducerTimestamp below shows how.
	if ts, ok := tag(f, tags, tagProducerTimestamp); ok {
		if t, perr := parseProducerTimestamp(ts); perr == nil {
			fmt.Fprintf(w, "  producer_time:     %s (raw %s)\n", t.UTC().Format(time.RFC3339Nano), ts)
		} else {
			fmt.Fprintf(w, "  producer_time:     unparseable (%s)\n", ts)
		}
	}

	if cid, ok := tag(f, tags, tagContactID); ok {
		fmt.Fprintf(w, "  contact_id:        %s\n", cid)
	}

	if value := f.Value(matroska.IDSegmentUUID); value != nil {
		fmt.Fprintf(w, "  segment_uuid:      %x\n", value.Bytes())
	}
	fmt.Fprintf(w, "  cluster_timestamp: %d (scale %d ns)\n", f.ClusterTimestamp(), f.TimestampScale())
	fmt.Fprintf(w, "  timestamp_scale:   %d ns\n", f.TimestampScale())
	fmt.Fprintf(w, "  start_time:        %s\n", f.StartTime())
	fmt.Fprintf(w, "  end_time:          %s\n", f.EndTime())

	tracks := f.Tracks()
	if len(tracks) == 0 {
		fmt.Fprintln(w, "  tracks:            (none)")
	}
	for _, tr := range tracks {
		number, err := tr.Find(matroska.IDTrackNumber).AsUint()
		if err != nil {
			continue
		}
		trackType, _ := tr.Find(matroska.IDTrackType).AsUint()
		audio := tr.Find(matroska.IDAudio)
		audioParams := ""
		if audio.Exists() {
			if v, err := audio.Find(matroska.IDSamplingFrequency).AsFloat(); err == nil {
				audioParams += fmt.Sprintf(" sampling_frequency=%g", v)
			}
			if v, err := audio.Find(matroska.IDChannels).AsUint(); err == nil {
				audioParams += fmt.Sprintf(" channels=%d", v)
			}
			if v, err := audio.Find(matroska.IDBitDepth).AsUint(); err == nil {
				audioParams += fmt.Sprintf(" bit_depth=%d", v)
			}
		}
		fmt.Fprintf(w, "  track %d %s: type=%d codec=%s%s pcm_bytes=%d\n",
			number, tr.Find(matroska.IDName).AsString(), trackType,
			tr.Find(matroska.IDCodecID).AsString(), audioParams, len(f.TrackPCM(number)))
	}
}

// parseProducerTimestamp converts an AWS_KINESISVIDEO_PRODUCER_TIMESTAMP value —
// a decimal seconds-since-epoch string with an optional fractional part — into a
// UTC time.Time. Up to 9 fractional digits are honored as nanoseconds; extra
// digits are truncated. This parsing lives in the consumer, not the library.
func parseProducerTimestamp(s string) (time.Time, error) {
	intPart, fracPart := s, ""
	if i := strings.IndexByte(s, '.'); i >= 0 {
		intPart, fracPart = s[:i], s[i+1:]
	}
	sec, err := strconv.ParseInt(intPart, 10, 64)
	if err != nil {
		return time.Time{}, fmt.Errorf("producer timestamp %q: %w", s, err)
	}
	var nsec int64
	if fracPart != "" {
		if len(fracPart) > 9 {
			fracPart = fracPart[:9]
		}
		frac, err := strconv.ParseInt(fracPart, 10, 64)
		if err != nil {
			return time.Time{}, fmt.Errorf("producer timestamp %q: %w", s, err)
		}
		for i := len(fracPart); i < 9; i++ {
			frac *= 10
		}
		nsec = frac
	}
	return time.Unix(sec, nsec).UTC(), nil
}
