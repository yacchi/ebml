package main

import (
	"crypto/sha256"
	"encoding/hex"
	"io"
	"os"
	"path/filepath"
	"sort"
	"testing"
)

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

func TestGenfixturesIdempotent(t *testing.T) {
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
		t.Fatalf("genfixtures is not idempotent: %s != %s", first, second)
	}
}
