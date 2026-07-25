// Command getmedia demonstrates the kvs package over an Amazon Kinesis Video
// Streams (KVS) GetMedia byte stream without an AWS SDK.
//
// A live KVS GetMedia response is a single continuous HTTP body of concatenated
// unknown-size Matroska Segments, one per KVS fragment. Each Segment carries its
// Tracks + Tags (AWS metadata) and exactly one known-size Cluster of SimpleBlocks
// (for Amazon Connect: 8 kHz / 16-bit PCM, one mono track per channel). This
// program feeds that raw stream through kvs.Reader and prints, for every
// emitted Fragment:
//
//   - AWS_KINESISVIDEO_FRAGMENT_NUMBER
//   - AWS_KINESISVIDEO_PRODUCER_TIMESTAMP, parsed inline into a time.Time
//   - ContactId (when present)
//   - the track list
//   - the Cluster timestamp and the fragment's first block time
//   - per-track decoded PCM byte counts
//
// Usage (run from the go/kvs/ directory):
//
//	go run ./examples/getmedia path/to/stream.mkv   # from a file
//	some-getmedia-source | go run ./examples/getmedia # or from stdin
package main

import (
	"fmt"
	"io"
	"os"
	"time"

	"github.com/yacchi/ebml/ext/fragment"
	"github.com/yacchi/ebml/kvs"
	"github.com/yacchi/ebml/matroska"
)

const tagContactID = "ContactId"

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
	reader := kvs.NewReader(r)
	n := 0
	for {
		f, metadata, err := reader.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}
		n++
		printFragment(w, n, f, metadata)
	}
	if n == 0 {
		fmt.Fprintln(w, "no fragments found in input")
	}
	return nil
}

// printFragment renders one assembled fragment.
func printFragment(w io.Writer, index int, f *fragment.Fragment, metadata kvs.Metadata) {
	fmt.Fprintf(w, "fragment %d\n", index)

	if metadata.FragmentNumber != "" {
		fmt.Fprintf(w, "  fragment_number:   %s\n", metadata.FragmentNumber)
	}
	if err := metadata.Err(); err != nil {
		fmt.Fprintf(w, "  stream_error:      %s\n", err)
	}
	if token := metadata.ContinuationToken; token != "" {
		fmt.Fprintf(w, "  continuation_token: %s\n", token)
	}

	if ts, ok := metadata.Tags[kvs.TagProducerTimestamp]; ok {
		if !metadata.ProducerTimestamp.IsZero() {
			fmt.Fprintf(w, "  producer_time:     %s (raw %s)\n", metadata.ProducerTimestamp.Format(time.RFC3339Nano), ts)
		} else {
			fmt.Fprintf(w, "  producer_time:     unparseable (%s)\n", ts)
		}
	}

	if cid, ok := metadata.Tags[tagContactID]; ok {
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
