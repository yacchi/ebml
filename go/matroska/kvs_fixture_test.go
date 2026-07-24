package matroska_test

import (
	"bytes"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/yacchi/ebml-reader/internal/ebmltrace"
	"github.com/yacchi/ebml-reader/internal/kvsgen"
	"github.com/yacchi/ebml-reader/matroska"
	"github.com/yacchi/ebml-reader/parser"
)

func loadKVSHex(t *testing.T, name string) []byte {
	t.Helper()
	path := filepath.Join("..", "..", "fixtures", "kvs", name+".ebml.hex")
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
	path := filepath.Join("..", "..", "golden", "kvs", name+".jsonl")
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read golden %s: %v", name, err)
	}
	return bytes.TrimSpace(b)
}

// TestKVSFixturesAreSplitInvariant runs every synthetic KVS fixture through all
// three split patterns (one_byte / fibonacci / random) with the KVS classifier
// and asserts each reproduces the committed golden exactly — proving the cursor
// parses the KVS Segment/Cluster topology identically regardless of chunking.
func TestKVSFixturesAreSplitInvariant(t *testing.T) {
	t.Parallel()

	for _, fx := range kvsgen.BuildAll() {
		fx := fx
		t.Run(fx.Name, func(t *testing.T) {
			t.Parallel()

			raw := loadKVSHex(t, fx.Name)

			// The committed .hex must equal what the generator builds.
			if !bytes.Equal(raw, fx.Data) {
				t.Fatalf("committed fixture %s.ebml.hex does not match generator output (%d vs %d bytes)",
					fx.Name, len(raw), len(fx.Data))
			}

			golden := loadKVSGolden(t, fx.Name)

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
