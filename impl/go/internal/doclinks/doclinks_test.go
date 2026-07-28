// Package doclinks pins the repository's documentation index against the files
// it claims to index.
//
// Two standing rules cannot be checked by a compiler or a linter and were both
// stale within one change of the documentation growing: a design note is listed
// in docs/README.md and in CLAUDE.md's table in the SAME change that adds it,
// and a relative link in any of those documents points at something that exists.
// A rotting index is worse than none, because the reader who follows it stops
// trusting the ones that are still right.
//
// It lives in the Go module because that is where the only test runner is; it
// asserts nothing about Go code.
//
// ONE CAVEAT WORTH KNOWING LOCALLY. Every file it reads is OUTSIDE this module,
// so the go command does not treat them as inputs: editing a document does not
// invalidate a cached PASS, and `go test ./...` can report ok for an index that
// has just gone stale. CI runs cold and catches it; locally, verify a
// documentation change with `go test -count=1 ./internal/doclinks/...`.
package doclinks

import (
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"
)

// indexed are the documents that point at other documents. A relative link in
// any of them must resolve.
var indexed = []string{
	"README.md",
	"llms.txt",
	"CLAUDE.md",
	"docs/README.md",
	"spec/SPEC.md",
}

func repoRoot(t *testing.T) string {
	t.Helper()
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(thisFile), "../../../.."))
}

// markdownLink matches [text](target). Reference-style links are not used in
// this repository; if one appears, this test will simply not see it, which is
// why a new document is added to indexed rather than relied on being scanned.
var markdownLink = regexp.MustCompile(`\[[^\]]*\]\(([^)]+)\)`)

// TestRelativeLinksResolve walks the indexing documents and the design notes,
// and requires every relative link target to exist on disk.
func TestRelativeLinksResolve(t *testing.T) {
	root := repoRoot(t)

	notes, err := filepath.Glob(filepath.Join(root, "docs/design-rules/*.md"))
	if err != nil {
		t.Fatalf("glob design-rules: %v", err)
	}
	docs := append([]string{}, indexed...)
	for _, n := range notes {
		rel, err := filepath.Rel(root, n)
		if err != nil {
			t.Fatalf("rel %s: %v", n, err)
		}
		docs = append(docs, filepath.ToSlash(rel))
	}

	for _, doc := range docs {
		body, err := os.ReadFile(filepath.Join(root, doc))
		if err != nil {
			t.Errorf("read %s: %v", doc, err)
			continue
		}
		for _, m := range markdownLink.FindAllStringSubmatch(string(body), -1) {
			target := m[1]
			switch {
			case strings.HasPrefix(target, "http://"), strings.HasPrefix(target, "https://"):
				continue
			case strings.HasPrefix(target, "#"):
				// Same-document anchor; nothing on disk to check.
				continue
			case strings.HasPrefix(target, "mailto:"):
				continue
			}
			// An anchor on another document names a heading, which this test does
			// not resolve: the file existing is what it pins.
			if i := strings.IndexByte(target, '#'); i >= 0 {
				target = target[:i]
			}
			if target == "" {
				continue
			}
			// plans/ is deliberately not committed, so a citation of one is written
			// in backticks rather than as a link. A link to it would be dead in a
			// clone, which is what this catches.
			resolved := filepath.Join(root, filepath.Dir(doc), filepath.FromSlash(target))
			if _, err := os.Stat(resolved); err != nil {
				t.Errorf("%s links to %q, which does not exist (%v)", doc, m[1], err)
			}
		}
	}
}

// TestEveryDesignNoteIsIndexed pins CLAUDE.md's rule that a new design note is
// listed in docs/README.md in the same change, and the table in CLAUDE.md that
// a reader reaches first.
func TestEveryDesignNoteIsIndexed(t *testing.T) {
	root := repoRoot(t)

	notes, err := filepath.Glob(filepath.Join(root, "docs/design-rules/*.md"))
	if err != nil {
		t.Fatalf("glob design-rules: %v", err)
	}
	if len(notes) == 0 {
		t.Fatal("no design notes found; this test is looking in the wrong place")
	}

	for _, index := range []struct{ path, prefix string }{
		{"docs/README.md", "design-rules/"},
		{"CLAUDE.md", "docs/design-rules/"},
		{"llms.txt", "docs/design-rules/"},
	} {
		body, err := os.ReadFile(filepath.Join(root, index.path))
		if err != nil {
			t.Errorf("read %s: %v", index.path, err)
			continue
		}
		for _, n := range notes {
			want := index.prefix + filepath.Base(n)
			if !strings.Contains(string(body), want) {
				t.Errorf("%s does not link %s; a new design note is indexed in the same change that adds it",
					index.path, want)
			}
		}
	}
}
