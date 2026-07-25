// Command genfixtures regenerates the repository's synthetic fixture corpus.
//
// It is an internal build tool, not part of the published CLI: it writes into
// the repository tree (fixtures/kvs, golden/kvs) and is deliberately placed
// under internal/ so it can be neither imported nor `go install`ed.
//
//	go run ./internal/kvsgen/genfixtures
package main

import (
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/yacchi/ebml/internal/ebmltrace"
	"github.com/yacchi/ebml/internal/kvsgen"
	"github.com/yacchi/ebml/matroska"
)

// generateKVS writes the synthetic KVS fixture corpus under root:
//
//	fixtures/kvs/<name>.ebml.hex   commented hex, in the tiny.ebml.hex format
//	golden/kvs/<name>.jsonl        cursor event log (via the Matroska classifier)
//	fixtures/kvs/README.json       manifest of cases + structural facts
//
// All fixtures are 100% synthetic (see package kvsgen). Progress is written to
// log (nil discards it).
func generateKVS(root string, log io.Writer) error {
	if log == nil {
		log = io.Discard
	}
	fixturesDir := filepath.Join(root, "fixtures", "kvs")
	goldenDir := filepath.Join(root, "golden", "kvs")
	for _, d := range []string{fixturesDir, goldenDir} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			return fmt.Errorf("mkdir %s: %w", d, err)
		}
	}

	fixtures := kvsgen.BuildAll()
	manifest := map[string]any{
		"generated_by": "go run ./internal/kvsgen/genfixtures",
		"data_safety":  "100% synthetic: fake UUID ContactId/InstanceId, counter-pattern PCM, synthetic tokens. No real Amazon Connect capture data.",
		"schema":       "each .ebml.hex is a synthetic MKV stream; each golden/kvs/<name>.jsonl is the cursor event log (KVS classifier) and is split-invariant.",
		"fixtures":     map[string]kvsgen.Facts{},
	}
	facts := manifest["fixtures"].(map[string]kvsgen.Facts)

	for _, f := range fixtures {
		f.Facts.Bytes = len(f.Data)

		hexPath := filepath.Join(fixturesDir, f.Name+".ebml.hex")
		if err := os.WriteFile(hexPath, []byte(renderHex(f)), 0o644); err != nil {
			return fmt.Errorf("write %s: %w", hexPath, err)
		}

		events, _, err := ebmltrace.Trace(ebmltrace.Whole(f.Data), matroska.KindForElementID)
		if err != nil {
			return fmt.Errorf("trace %s: %w", f.Name, err)
		}
		jsonl, err := ebmltrace.MarshalJSONL(events)
		if err != nil {
			return fmt.Errorf("marshal golden %s: %w", f.Name, err)
		}
		goldenPath := filepath.Join(goldenDir, f.Name+".jsonl")
		if err := os.WriteFile(goldenPath, append(jsonl, '\n'), 0o644); err != nil {
			return fmt.Errorf("write %s: %w", goldenPath, err)
		}

		facts[f.Name] = f.Facts
		fmt.Fprintf(log, "generated %-28s %5d bytes, %3d events\n", f.Name, len(f.Data), len(events))
	}

	mb, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal manifest: %w", err)
	}
	manifestPath := filepath.Join(fixturesDir, "README.json")
	if err := os.WriteFile(manifestPath, append(mb, '\n'), 0o644); err != nil {
		return fmt.Errorf("write manifest: %w", err)
	}
	fmt.Fprintf(log, "wrote manifest %s\n", manifestPath)
	return nil
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

func main() {
	fs := flag.NewFlagSet("genfixtures", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	root := fs.String("root", "..", "repository root (parent of fixtures/ and golden/)")
	fs.Usage = func() {
		fmt.Fprintln(os.Stderr, "Usage: go run ./internal/kvsgen/genfixtures [flags]")
		fmt.Fprintln(os.Stderr, "  Regenerate fixtures/kvs, golden/kvs and README.json. Run from the go/ directory.")
		fs.PrintDefaults()
	}
	if err := fs.Parse(os.Args[1:]); err != nil {
		os.Exit(2)
	}
	if err := generateKVS(*root, os.Stdout); err != nil {
		fmt.Fprintf(os.Stderr, "genfixtures: %v\n", err)
		os.Exit(1)
	}
}
