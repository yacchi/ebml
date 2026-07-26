package fragment_test

import (
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/yacchi/ebml/ext/fragment"
	"github.com/yacchi/ebml/ext/tags"
	"github.com/yacchi/ebml/matroska"
)

// Example_streamingAssembly shows the whole loop: construct an assembler, push
// arbitrary byte chunks of a KVS GetMedia stream, receive completed *Fragment
// values, and read metadata (ContactId) plus per-track PCM byte counts.
//
// It loads the committed topology_basic fixture and feeds it in fixed 64-byte
// chunks to demonstrate that chunk boundaries do not matter.
//
// It is also where this package states how a fragment's tags are read, since it
// offers no tag accessor of its own and its doc comments name no other package:
// Segment is an ordinary retained element, so ext/tags reads it. Saying that in
// runnable, output-checked code rather than in prose is deliberate -- a comment
// describing a sibling package's API is a dependency nothing recompiles.
func Example_streamingAssembly() {
	raw := loadFixtureBytes("topology_basic")

	a := fragment.New()

	var fragments []*fragment.Fragment
	const chunk = 64
	for i := 0; i < len(raw); i += chunk {
		end := i + chunk
		if end > len(raw) {
			end = len(raw)
		}
		frags, err := a.Feed(raw[i:end])
		if err != nil {
			panic(err)
		}
		fragments = append(fragments, frags...)
	}
	tail, err := a.Finalize()
	if err != nil {
		panic(err)
	}
	fragments = append(fragments, tail...)

	for _, f := range fragments {
		contactID, _ := tags.Read(f.Segment).Get(tags.Target{}, "ContactId")
		fmt.Printf("ContactId=%s start=%s\n", contactID, f.StartTime())
		for _, track := range f.Tracks() {
			number, err := track.Find(matroska.IDTrackNumber).AsUint()
			if err != nil {
				continue
			}
			fmt.Printf("track %d %s: %d PCM bytes\n",
				number, track.Find(matroska.IDName).AsString(), len(f.TrackPCM(number)))
		}
	}
	// Output:
	// ContactId=00000000-0000-4000-8000-000000000001 start=0s
	// track 1 AUDIO_FROM_CUSTOMER: 96 PCM bytes
	// track 2 AUDIO_TO_CUSTOMER: 0 PCM bytes
}

// loadFixtureBytes decodes a committed .ebml.hex fixture (comment lines removed).
func loadFixtureBytes(name string) []byte {
	path := filepath.Join("..", "..", "..", "fixtures", "kvs", name+".ebml.hex")
	b, err := os.ReadFile(path)
	if err != nil {
		panic(err)
	}
	var sb strings.Builder
	for _, ln := range strings.Split(string(b), "\n") {
		ln = strings.TrimSpace(ln)
		if ln == "" || strings.HasPrefix(ln, "#") {
			continue
		}
		for _, tok := range strings.Fields(ln) {
			sb.WriteString(tok)
		}
	}
	raw, err := hex.DecodeString(sb.String())
	if err != nil {
		panic(err)
	}
	return raw
}
