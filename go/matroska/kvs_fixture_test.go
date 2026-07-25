package matroska_test

import (
	"bytes"
	"encoding/hex"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/yacchi/ebml/internal/ebmltrace"
	"github.com/yacchi/ebml/internal/kvsgen"
	"github.com/yacchi/ebml/matroska"
	"github.com/yacchi/ebml/parser"
)

// kvsFixturePath and kvsGoldenPath are the committed locations of one corpus case.
func kvsFixturePath(name string) string {
	return filepath.Join("..", "..", "fixtures", "kvs", name+".ebml.hex")
}

func kvsGoldenPath(name string) string {
	return filepath.Join("..", "..", "golden", "kvs", name+".jsonl")
}

// discoverKVSFixtures returns the name of every committed fixture, found by globbing
// the corpus directory rather than by consulting a list. A list — here or in the
// generator — would silently exempt a fixture nobody remembered to add to it.
func discoverKVSFixtures(t *testing.T) []string {
	t.Helper()
	paths, err := filepath.Glob(kvsFixturePath("*"))
	if err != nil {
		t.Fatalf("glob fixtures/kvs: %v", err)
	}
	if len(paths) == 0 {
		t.Fatal("no fixtures found under fixtures/kvs: run `go run ./cmd/ebml genkvs`")
	}
	names := make([]string, 0, len(paths))
	for _, p := range paths {
		names = append(names, strings.TrimSuffix(filepath.Base(p), ".ebml.hex"))
	}
	sort.Strings(names)
	return names
}

// generatedKVSFixtures indexes the generator's cases by name.
func generatedKVSFixtures() map[string]kvsgen.Fixture {
	out := make(map[string]kvsgen.Fixture)
	for _, fx := range kvsgen.BuildAll() {
		out[fx.Name] = fx
	}
	return out
}

func loadKVSHex(t *testing.T, name string) []byte {
	t.Helper()
	path := kvsFixturePath(name)
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read fixture %s: %v", name, err)
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
		t.Fatalf("decode hex fixture %s: %v", name, err)
	}
	return raw
}

func loadKVSGolden(t *testing.T, name string) []byte {
	t.Helper()
	path := kvsGoldenPath(name)
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read golden %s: %v", name, err)
	}
	return bytes.TrimSpace(b)
}

// TestKVSCorpusMatchesGenerator cross-checks the corpus in the direction the
// per-fixture test cannot: every case the generator builds must have a committed
// fixture AND a committed golden. The opposite direction — a committed fixture with
// no generator counterpart, which nothing would exercise — is caught by
// TestKVSFixturesAreSplitInvariant, which iterates the directory itself.
func TestKVSCorpusMatchesGenerator(t *testing.T) {
	t.Parallel()

	committed := make(map[string]bool)
	for _, name := range discoverKVSFixtures(t) {
		committed[name] = true
	}
	for _, fx := range kvsgen.BuildAll() {
		if !committed[fx.Name] {
			t.Errorf("generator case %q has no committed fixture %s: run `go run ./cmd/ebml genkvs`",
				fx.Name, kvsFixturePath(fx.Name))
		}
		if _, err := os.Stat(kvsGoldenPath(fx.Name)); err != nil {
			t.Errorf("generator case %q has no committed golden %s: run `go run ./cmd/ebml genkvs` (%v)",
				fx.Name, kvsGoldenPath(fx.Name), err)
		}
	}
}

// TestKVSFixturesAreSplitInvariant runs every COMMITTED KVS fixture — discovered by
// globbing the corpus directory, so no committed fixture can escape coverage —
// through all three split patterns (one_byte / fibonacci / random) with the KVS
// classifier, and asserts each reproduces the committed golden exactly, proving the
// cursor parses the KVS Segment/Cluster topology identically regardless of chunking.
// Each fixture's bytes must also equal what the generator builds for that name.
func TestKVSFixturesAreSplitInvariant(t *testing.T) {
	t.Parallel()

	generated := generatedKVSFixtures()

	for _, name := range discoverKVSFixtures(t) {
		name := name
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			fx, ok := generated[name]
			if !ok {
				t.Fatalf("committed fixture %s has no counterpart in kvsgen.BuildAll(), so nothing regenerates or verifies it: add the generator case or delete the fixture",
					kvsFixturePath(name))
			}

			raw := loadKVSHex(t, name)

			// The committed .hex must equal what the generator builds.
			if !bytes.Equal(raw, fx.Data) {
				t.Fatalf("committed fixture %s.ebml.hex does not match generator output (%d vs %d bytes)",
					name, len(raw), len(fx.Data))
			}

			golden := loadKVSGolden(t, name)

			splits := []struct {
				name   string
				chunks [][]byte
			}{
				{"one_byte", ebmltrace.SplitOneByte(raw)},
				{"fibonacci", ebmltrace.SplitFibonacci(raw)},
				{"random", ebmltrace.SplitRandom(raw, 12345, 7)},
			}

			for _, sp := range splits {
				events, needMore, err := ebmltrace.Trace(sp.chunks, matroska.KindForElementID)
				if err != nil {
					t.Fatalf("[%s] trace: %v", sp.name, err)
				}
				got, err := ebmltrace.MarshalJSONL(events)
				if err != nil {
					t.Fatalf("[%s] marshal: %v", sp.name, err)
				}
				if !bytes.Equal(got, golden) {
					t.Fatalf("[%s] golden mismatch\n--- got ---\n%s\n--- want ---\n%s",
						sp.name, got, golden)
				}
				if sp.name == "one_byte" && needMore == 0 {
					t.Fatalf("[one_byte] expected NeedMoreData to occur")
				}
			}
		})
	}
}

// TestFalseEBMLMagicInPCMIsNotMisSplit is the key property test: a SimpleBlock
// whose PCM payload embeds the 4 EBML magic bytes must be read as ONE binary leaf
// of its declared size, and the stream must contain exactly one EBML header (the
// real fragment header) — no spurious Segment/EBML split inside the audio.
func TestFalseEBMLMagicInPCMIsNotMisSplit(t *testing.T) {
	t.Parallel()

	const name = "false_ebml_magic_in_pcm"
	raw := loadKVSHex(t, name)

	// Sanity: the fixture really does embed the EBML magic bytes at least twice
	// (the real fragment header, plus the copy planted inside a SimpleBlock's PCM).
	if bytes.Count(raw, []byte{0x1A, 0x45, 0xDF, 0xA3}) < 2 {
		t.Fatalf("fixture must contain the EBML magic at least twice (header + embedded in PCM)")
	}

	events, _, err := ebmltrace.Trace(ebmltrace.SplitFibonacci(raw), matroska.KindForElementID)
	if err != nil {
		t.Fatalf("trace: %v", err)
	}

	ebmlPeeks := 0
	var simpleBlockSizes []int64
	for _, ev := range events {
		if ev.Op != "peek" || ev.ID == "" {
			continue
		}
		switch ev.ID {
		case parser.FormatID(matroska.IDEBML):
			ebmlPeeks++
		case parser.FormatID(matroska.IDSimpleBlock):
			if ev.Size != nil {
				simpleBlockSizes = append(simpleBlockSizes, *ev.Size)
			}
		}
	}

	if ebmlPeeks != 1 {
		t.Fatalf("expected exactly 1 EBML header peek (no spurious split in PCM), got %d", ebmlPeeks)
	}
	// Three SimpleBlocks: 28, 52 (the magic-carrying one: 1+2+1+48), 28.
	wantSizes := []int64{28, 52, 28}
	if len(simpleBlockSizes) != len(wantSizes) {
		t.Fatalf("expected %d SimpleBlock leaves, got %d (%v)", len(wantSizes), len(simpleBlockSizes), simpleBlockSizes)
	}
	for i, want := range wantSizes {
		if simpleBlockSizes[i] != want {
			t.Fatalf("SimpleBlock %d: expected leaf size %d, got %d", i, want, simpleBlockSizes[i])
		}
	}
}
