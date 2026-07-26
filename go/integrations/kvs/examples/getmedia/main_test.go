package main

import (
	"bytes"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/yacchi/ebml/integrations/kvs"
)

// loadFixture decodes a committed fixtures/kvs/<name>.ebml.hex file (comment
// lines beginning with '#' stripped) into the raw EBML byte stream.
func loadFixture(t *testing.T, name string) []byte {
	t.Helper()
	path := filepath.Join("..", "..", "..", "..", "..", "fixtures", "kvs", name+".ebml.hex")
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

// TestRunSmoke feeds a committed KVS fixture through the program's core function
// and checks the report surfaces the fragment's metadata and per-track PCM count.
func TestRunSmoke(t *testing.T) {
	raw := loadFixture(t, "topology_basic")

	var out bytes.Buffer
	if err := run(bytes.NewReader(raw), &out); err != nil {
		t.Fatalf("run: %v", err)
	}

	got := out.String()
	for _, want := range []string{
		"fragment 1",
		"fragment_number:   91343852333181000000000000000000000000000000001",
		"contact_id:        00000000-0000-4000-8000-000000000001",
		"cluster_timestamp: 0",
		"sampling_frequency=8000 channels=1 bit_depth=16",
		"pcm_bytes=96",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("report missing %q\n--- got ---\n%s", want, got)
		}
	}
}

func TestTaglessFragmentsInheritMatchingUUIDTags(t *testing.T) {
	raw := loadFixture(t, "tagless_consecutive")
	var out bytes.Buffer
	if err := run(bytes.NewReader(raw), &out); err != nil {
		t.Fatal(err)
	}

	got := out.String()
	for _, fragment := range []string{"fragment 2", "fragment 3"} {
		start := strings.Index(got, fragment)
		if start < 0 {
			t.Fatalf("missing %s", fragment)
		}
		end := strings.Index(got[start+len(fragment):], "fragment ")
		if end < 0 {
			end = len(got) - start - len(fragment)
		}
		part := got[start : start+len(fragment)+end]
		for _, want := range []string{
			"segment_uuid:",
			"fragment_number:   c-0",
			"contact_id:        00000000-0000-4000-8000-000000000001",
			"producer_time:",
		} {
			if !strings.Contains(part, want) {
				t.Errorf("%s missing %q:\n%s", fragment, want, part)
			}
		}
	}
}

func TestPartialTagsInheritMissingIdentityKeys(t *testing.T) {
	raw := loadFixture(t, "partial_tags")
	var out bytes.Buffer
	if err := run(bytes.NewReader(raw), &out); err != nil {
		t.Fatal(err)
	}
	got := out.String()
	start := strings.Index(got, "fragment 2")
	if start < 0 {
		t.Fatal("missing fragment 2")
	}
	part := got[start:]
	for _, want := range []string{
		"fragment_number:   partial-1",
		"contact_id:        00000000-0000-4000-8000-000000000001",
	} {
		if !strings.Contains(part, want) {
			t.Fatalf("partial fragment missing %q:\n%s", want, part)
		}
	}
}

// TestParseProducerTimestamp covers the inline decimal-seconds parsing.
func TestParseProducerTimestamp(t *testing.T) {
	tm, err := kvs.ParseTimestamp("1000000000.512")
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if tm.Unix() != 1000000000 || tm.Nanosecond() != 512000000 {
		t.Fatalf("got unix=%d nsec=%d", tm.Unix(), tm.Nanosecond())
	}
	if _, err := kvs.ParseTimestamp("not-a-number"); err == nil {
		t.Fatal("expected error for non-numeric timestamp")
	}
}
