// Command genkvs generates the synthetic KVS/Matroska fixture corpus:
//
//	fixtures/kvs/<name>.ebml.hex   commented hex, in the tiny.ebml.hex format
//	golden/kvs/<name>.jsonl        cursor event log (via the KVS classifier)
//	fixtures/kvs/README.json       manifest of cases + structural facts
//
// All fixtures are 100% synthetic (see package kvsgen). Run from the go/ dir:
//
//	go run ./cmd/genkvs            # writes into ../fixtures/kvs and ../golden/kvs
//	go run ./cmd/genkvs -root .    # custom repo root
package main

import (
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/yacchi/ebml-reader/internal/ebmltrace"
	"github.com/yacchi/ebml-reader/internal/kvsgen"
	"github.com/yacchi/ebml-reader/parser"
)

func main() {
	root := flag.String("root", "..", "repository root (parent of fixtures/ and golden/)")
	flag.Parse()

	fixturesDir := filepath.Join(*root, "fixtures", "kvs")
	goldenDir := filepath.Join(*root, "golden", "kvs")
	for _, d := range []string{fixturesDir, goldenDir} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			fail("mkdir %s: %v", d, err)
		}
	}

	fixtures := kvsgen.BuildAll()
	manifest := map[string]any{
		"generated_by": "go run ./cmd/genkvs",
		"data_safety":  "100% synthetic: fake UUID ContactId/InstanceId, counter-pattern PCM, synthetic tokens. No real Amazon Connect capture data.",
		"schema":       "each .ebml.hex is a synthetic MKV stream; each golden/kvs/<name>.jsonl is the cursor event log (KVS classifier) and is split-invariant.",
		"fixtures":     map[string]kvsgen.Facts{},
	}
	facts := manifest["fixtures"].(map[string]kvsgen.Facts)

	for _, f := range fixtures {
		f.Facts.Bytes = len(f.Data)

		hexPath := filepath.Join(fixturesDir, f.Name+".ebml.hex")
		if err := os.WriteFile(hexPath, []byte(renderHex(f)), 0o644); err != nil {
			fail("write %s: %v", hexPath, err)
		}

		events, _, err := ebmltrace.Trace(ebmltrace.Whole(f.Data), parser.KVSKindForElementID)
		if err != nil {
			fail("trace %s: %v", f.Name, err)
		}
		jsonl, err := ebmltrace.MarshalJSONL(events)
		if err != nil {
			fail("marshal golden %s: %v", f.Name, err)
		}
		goldenPath := filepath.Join(goldenDir, f.Name+".jsonl")
		if err := os.WriteFile(goldenPath, append(jsonl, '\n'), 0o644); err != nil {
			fail("write %s: %v", goldenPath, err)
		}

		facts[f.Name] = f.Facts
		fmt.Printf("generated %-28s %5d bytes, %3d events\n", f.Name, len(f.Data), len(events))
	}

	mb, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		fail("marshal manifest: %v", err)
	}
	manifestPath := filepath.Join(fixturesDir, "README.json")
	if err := os.WriteFile(manifestPath, append(mb, '\n'), 0o644); err != nil {
		fail("write manifest: %v", err)
	}
	fmt.Printf("wrote manifest %s\n", manifestPath)
}

// renderHex emits the commented-hex fixture format: "# " comment lines then hex
// wrapped at 16 bytes/line (matching the loader that strips comments/whitespace).
func renderHex(f kvsgen.Fixture) string {
	var b strings.Builder
	for _, line := range strings.Split(f.Comment, "\n") {
		b.WriteString("# ")
		b.WriteString(line)
		b.WriteByte('\n')
	}
	b.WriteString(fmt.Sprintf("# bytes=%d\n", len(f.Data)))
	full := hex.EncodeToString(f.Data)
	for i := 0; i < len(full); i += 32 {
		end := i + 32
		if end > len(full) {
			end = len(full)
		}
		b.WriteString(full[i:end])
		b.WriteByte('\n')
	}
	return b.String()
}

func fail(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "genkvs: "+format+"\n", args...)
	os.Exit(1)
}
