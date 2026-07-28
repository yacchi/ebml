package main

import (
	"crypto/sha256"
	"encoding/hex"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/yacchi/ebml/impl/go/internal/kvsgen"
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

// TestCorpusIsDescribedWhereItIsClaimed pins the two prose homes that state what
// the corpus IS against what kvsgen actually builds. Both were stale within one
// change of the corpus growing, and CLAUDE.md's own rule is that a claim is
// either compiler-checked or deleted -- these two cannot be deleted, since a
// reader has to be told what the corpus covers, so they are checked here.
//
// The root README enumerates every fixture BY NAME, which pins completeness and
// not merely a count; CLAUDE.md states the count, which is the number a reader
// reaches for first.
func TestCorpusIsDescribedWhereItIsClaimed(t *testing.T) {
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	repoRoot := filepath.Clean(filepath.Join(filepath.Dir(thisFile), "../../../../.."))

	fixtures := kvsgen.BuildAll()

	readme, err := os.ReadFile(filepath.Join(repoRoot, "README.md"))
	if err != nil {
		t.Fatalf("read README.md: %v", err)
	}
	for _, f := range fixtures {
		if !strings.Contains(string(readme), "`"+f.Name+"`") {
			t.Errorf("fixture %q is not named in README.md; the corpus description there is now incomplete", f.Name)
		}
	}

	claude, err := os.ReadFile(filepath.Join(repoRoot, "CLAUDE.md"))
	if err != nil {
		t.Fatalf("read CLAUDE.md: %v", err)
	}
	stated := regexp.MustCompile(`the corpus is (\d+) fixtures`).FindSubmatch(claude)
	if stated == nil {
		t.Fatal(`CLAUDE.md no longer says "the corpus is N fixtures"; update this test with the sentence that replaced it`)
	}
	want := strconv.Itoa(len(fixtures))
	if string(stated[1]) != want {
		t.Errorf("CLAUDE.md says the corpus is %s fixtures; kvsgen builds %s", stated[1], want)
	}
}
