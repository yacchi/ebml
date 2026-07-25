package main

import (
	"bytes"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestScanFixture(t *testing.T) {
	b, err := os.ReadFile(filepath.Join("..", "..", "..", "fixtures", "tiny.ebml.hex"))
	if err != nil {
		t.Fatal(err)
	}
	var text strings.Builder
	for _, line := range strings.Split(string(b), "\n") {
		if !strings.HasPrefix(strings.TrimSpace(line), "#") {
			text.WriteString(strings.Join(strings.Fields(line), ""))
		}
	}
	raw, err := hex.DecodeString(text.String())
	if err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err := scan(bytes.NewReader(raw), &out); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "DocType=webm") {
		t.Fatalf("output %q", out.String())
	}
}
