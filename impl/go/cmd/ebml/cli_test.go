package main

import (
	"bytes"
	"encoding/xml"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// loadFixture reads a commented-hex fixture and returns the decoded EBML bytes.
func loadFixture(t *testing.T, rel string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "..", "..", "..", "fixtures", rel))
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

// TestDumpMaxBinaryZeroPrintsSizeOnly pins the deliberate use of the cursor's lazy
// default: with --max-binary 0 no byte of a binary or block leaf would be printed,
// so the dump states its size and never asks the cursor for the payload at all.
func TestDumpMaxBinaryZeroPrintsSizeOnly(t *testing.T) {
	raw := loadFixture(t, "kvs/topology_basic.ebml.hex")
	var out bytes.Buffer
	if err := runDump(bytes.NewReader(raw), &out, dumpOptions{maxBinary: 0}); err != nil {
		t.Fatalf("runDump: %v", err)
	}
	got := out.String()
	for _, want := range []string{
		"SimpleBlock (0xA3) [type block, offset 589, size 36] = binary 36 bytes\n",
		// A scalar leaf is still read: its value is what the dump prints.
		`DocType (0x4282) [type string, offset 13, size 8] = "matroska"`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("dump output missing %q\n%s", want, got)
		}
	}
	// "binary N bytes: <hex>" is the materialised form; nothing may print it here.
	if strings.Contains(got, "bytes: ") {
		t.Errorf("--max-binary 0 printed payload bytes:\n%s", got)
	}
	if strings.Contains(got, "track=") {
		t.Errorf("--max-binary 0 decoded a block payload it was not going to print:\n%s", got)
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

func TestUnknownCommandExits2(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if code := run([]string{"bogus"}, nil, &stdout, &stderr); code != 2 {
		t.Fatalf("expected exit 2 for unknown command, got %d", code)
	}
	if code := run(nil, nil, &stdout, &stderr); code != 2 {
		t.Fatalf("expected exit 2 for no args, got %d", code)
	}
}

// TestDumpClosesUnknownSizeCluster is the CLI's half of the round 2 F4 fix. The
// command used to carry its own copy of the boundary rule, answering only about
// EBML and Segment, so on the shape a live GetMedia stream actually sends -- an
// unknown-size Cluster with Segment-level Tags after it -- every trailing Tags
// element was rendered as a child of the Cluster. Sharing matroska.StreamBoundary
// is what keeps the CLI and ext/fragment from reading the same bytes differently.
func TestDumpClosesUnknownSizeCluster(t *testing.T) {
	raw := loadFixture(t, "kvs/connect_real_shape.ebml.hex")
	var out bytes.Buffer
	if err := runDump(bytes.NewReader(raw), &out, dumpOptions{maxBinary: 0}); err != nil {
		t.Fatalf("runDump: %v", err)
	}

	// Segment children are indented once; a Cluster child twice. All four Tags
	// elements of this fixture are Segment children -- two before the Cluster and
	// two after it -- so none may appear at the deeper indent.
	var segmentTags, clusterTags int
	var clusterLine string
	for _, line := range strings.Split(out.String(), "\n") {
		switch {
		case strings.HasPrefix(line, "  Tags ("):
			segmentTags++
		case strings.HasPrefix(line, "    Tags ("):
			clusterTags++
		case strings.HasPrefix(line, "  Cluster ("):
			clusterLine = line
		}
	}
	if segmentTags != 4 || clusterTags != 0 {
		t.Errorf("Tags elements: %d as Segment children, %d as Cluster children; want 4 and 0\n%s",
			segmentTags, clusterTags, out.String())
	}
	// What this asserts is the SIZE, not the offset. The offset is a byte position
	// in a generated fixture, so pinning it here would fail this CLI test for any
	// change to the Connect profile's Info or Tracks -- neither of which this test
	// is about.
	if !strings.HasPrefix(clusterLine, "  Cluster (0x1F43B675) [offset ") ||
		!strings.HasSuffix(clusterLine, ", size unknown]") {
		t.Errorf("dump did not report an unknown-size Cluster as a Segment child\n%s", out.String())
	}
}
