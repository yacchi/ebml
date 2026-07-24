package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/xml"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// loadFixture reads a commented-hex fixture and returns the decoded EBML bytes.
func loadFixture(t *testing.T, rel string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "..", "..", "fixtures", rel))
	if err != nil {
		t.Fatalf("read fixture %s: %v", rel, err)
	}
	r, err := sourceReader(bytes.NewReader(b), true)
	if err != nil {
		t.Fatalf("decode fixture %s: %v", rel, err)
	}
	raw, err := io.ReadAll(r)
	if err != nil {
		t.Fatalf("read decoded fixture %s: %v", rel, err)
	}
	return raw
}

func TestDumpTiny(t *testing.T) {
	raw := loadFixture(t, "tiny.ebml.hex")
	var out bytes.Buffer
	if err := runDump(bytes.NewReader(raw), &out, dumpOptions{maxBinary: 16}); err != nil {
		t.Fatalf("runDump: %v", err)
	}
	got := out.String()
	for _, want := range []string{
		"EBML (0x1A45DFA3)",
		"DocType (0x4282)",
		`"webm"`,
		"Segment (0x18538067)",
		"Void (0xEC)",
		"type string",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("dump output missing %q\n%s", want, got)
		}
	}
}

func TestDumpKVSTopology(t *testing.T) {
	raw := loadFixture(t, "kvs/topology_basic.ebml.hex")
	var out bytes.Buffer
	if err := runDump(bytes.NewReader(raw), &out, dumpOptions{maxBinary: 8}); err != nil {
		t.Fatalf("runDump: %v", err)
	}
	got := out.String()
	for _, want := range []string{
		"Segment (0x18538067)",
		"Cluster (0x1F43B675)",
		"SimpleBlock (0xA3)",
		"track=",
		"TagString (0x4487)",
		"type block",
		"size unknown",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("dump output missing %q\n%s", want, got)
		}
	}
}

func TestXMLKVSTopology(t *testing.T) {
	raw := loadFixture(t, "kvs/topology_basic.ebml.hex")
	var out bytes.Buffer
	if err := runXML(bytes.NewReader(raw), &out, xmlOptions{maxBinary: 8}); err != nil {
		t.Fatalf("runXML: %v", err)
	}

	// Must parse as well-formed XML.
	dec := xml.NewDecoder(bytes.NewReader(out.Bytes()))
	names := map[string]bool{}
	for {
		tok, err := dec.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("xml does not parse: %v\n%s", err, out.String())
		}
		if se, ok := tok.(xml.StartElement); ok {
			names[se.Name.Local] = true
		}
	}
	for _, want := range []string{"EBMLStream", "Segment", "Cluster", "SimpleBlock", "TagString"} {
		if !names[want] {
			t.Errorf("xml missing element %q\n%s", want, out.String())
		}
	}
}

func TestMalformedInputErrors(t *testing.T) {
	// A lone 0xFF is a valid VINT id of value 0x7f but leaves no size; more
	// pointedly, random bytes that cannot form a coherent element tree must fail.
	garbage := []byte{0x1A, 0x45, 0xDF, 0xA3, 0x84, 0xFF, 0xFF} // EBML header claims size 4, only 2 payload bytes -> truncated
	var out bytes.Buffer
	err := runDump(bytes.NewReader(garbage), &out, dumpOptions{maxBinary: 16})
	if err == nil {
		t.Fatalf("expected error on malformed input, got nil (out=%q)", out.String())
	}
	if !strings.Contains(err.Error(), "offset") {
		t.Errorf("error should mention offset, got: %v", err)
	}
}

func TestXMLMalformedInputIsWellFormed(t *testing.T) {
	garbage := bytes.Repeat([]byte{0xFF}, 32)
	var out bytes.Buffer
	err := runXML(bytes.NewReader(garbage), &out, xmlOptions{maxBinary: 16})
	if err == nil {
		t.Fatalf("expected error on malformed input, got nil (out=%q)", out.String())
	}

	dec := xml.NewDecoder(bytes.NewReader(out.Bytes()))
	for {
		if _, err := dec.Token(); err != nil {
			if err == io.EOF {
				break
			}
			t.Fatalf("xml does not parse: %v\n%s", err, out.String())
		}
	}
}

func hashTree(t *testing.T, dir string) string {
	t.Helper()
	var paths []string
	err := filepath.Walk(dir, func(p string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if !info.IsDir() {
			paths = append(paths, p)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk %s: %v", dir, err)
	}
	sort.Strings(paths)
	h := sha256.New()
	for _, p := range paths {
		rel, _ := filepath.Rel(dir, p)
		h.Write([]byte(rel))
		b, err := os.ReadFile(p)
		if err != nil {
			t.Fatalf("read %s: %v", p, err)
		}
		h.Write(b)
	}
	return hex.EncodeToString(h.Sum(nil))
}

func TestGenkvsIdempotent(t *testing.T) {
	root := t.TempDir()
	if err := generateKVS(root, io.Discard); err != nil {
		t.Fatalf("generateKVS (1): %v", err)
	}
	first := hashTree(t, root)
	if err := generateKVS(root, io.Discard); err != nil {
		t.Fatalf("generateKVS (2): %v", err)
	}
	second := hashTree(t, root)
	if first != second {
		t.Fatalf("genkvs is not idempotent: %s != %s", first, second)
	}
}

func TestUnknownCommandExits2(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if code := run([]string{"bogus"}, nil, &stdout, &stderr); code != 2 {
		t.Fatalf("expected exit 2 for unknown command, got %d", code)
	}
	if code := run(nil, nil, &stdout, &stderr); code != 2 {
		t.Fatalf("expected exit 2 for no args, got %d", code)
	}
}
