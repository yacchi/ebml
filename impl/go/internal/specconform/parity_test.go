package specconform

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"
)

// The two READMEs publish a specification-parity table: how many elements the
// registry knows, how many the schemas declare, and how many elements each
// unimplemented capability would have to answer for. Those numbers are computed
// by this package, and a number copied into prose is exactly the claim this
// repository does not leave unchecked -- the corpus, the doc index and the node
// method set are all pinned for the same reason.
//
// It cannot run in the ordinary suite: the CC-BY schemas are never vendored, so
// there is nothing to compare against unless someone fetched them. It therefore
// SKIPS without the cache and runs in the conformance workflow, which fetches
// the pinned schemas before calling it.

func repoRoot(t *testing.T) string {
	t.Helper()
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(thisFile), "../../../.."))
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(b)
}

// tableRowContaining returns the first MARKDOWN TABLE ROW of doc holding token.
// A count is checked against the row it belongs to rather than against the whole
// file -- otherwise "133" appearing anywhere would satisfy a WebM assertion --
// and only table rows are considered, because the prose around them legitimately
// names the same capabilities without restating their counts.
func tableRowContaining(doc, token string) (string, bool) {
	for line := range strings.SplitSeq(doc, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "|") && strings.Contains(line, token) {
			return line, true
		}
	}
	return "", false
}

func wantCountOnRow(t *testing.T, docName, doc, token string, want int) {
	t.Helper()
	line, ok := tableRowContaining(doc, token)
	if !ok {
		t.Errorf("%s has no table row mentioning %q; the parity table no longer covers it", docName, token)
		return
	}
	if !regexp.MustCompile(`\b` + fmt.Sprint(want) + `\b`).MatchString(line) {
		t.Errorf("%s row for %q does not state %d:\n  %s", docName, token, want, strings.TrimSpace(line))
	}
}

// TestParityTablesMatchTheSchemas is the gate on those published numbers.
func TestParityTablesMatchTheSchemas(t *testing.T) {
	root := repoRoot(t)
	paths := []string{
		filepath.Join(root, ".spec-cache/ebml.xml"),
		filepath.Join(root, ".spec-cache/ebml_matroska.xml"),
	}
	var schemas []*Schema
	for _, p := range paths {
		if _, err := os.Stat(p); err != nil {
			t.Skipf("no schema cache at %s; fetch the schemas (see the conformance-check skill) to run this", p)
		}
		s, err := LoadSchema(p)
		if err != nil {
			t.Fatalf("LoadSchema(%s): %v", p, err)
		}
		schemas = append(schemas, s)
	}
	report := Check(schemas...)

	if got := report.Mismatches(); got != 0 {
		t.Fatalf("%d mismatch(es) against the schemas; fix those before trusting any published number", got)
	}

	// The counts each unimplemented capability would have to answer for, keyed by
	// the token that identifies its row in the prose. A capability that gains an
	// implementation loses its row, and this map is where that is noticed.
	rowToken := map[string]string{
		"maxOccurs/minOccurs": "maxOccurs",
		"minver":              "`minver`",
		"default":             "`default`",
		"range":               "`range`",
		"restriction/enum":    "restriction",
		"length":              "`length`",
		"recurring":           "`recurring`",
		"webmproject.org":     "WebM profile marker",
	}

	for _, doc := range []struct{ name, body string }{
		{"README.md", readFile(t, filepath.Join(root, "README.md"))},
		{"impl/go/README.md", readFile(t, filepath.Join(root, "impl/go/README.md"))},
	} {
		wantCountOnRow(t, doc.name, doc.body, "of the 273", report.Declared)
		wantCountOnRow(t, doc.name, doc.body, "of the 273", report.Registered)

		for _, facet := range report.Unchecked {
			token, ok := rowToken[facet.Name]
			if !ok {
				t.Errorf("the checker reports a capability %q that no parity row names", facet.Name)
				continue
			}
			wantCountOnRow(t, doc.name, doc.body, token, facet.Elements)
		}
	}
}

// TestConformanceWorkflowPinsTheDocumentedSchemas keeps the commits the parity
// tables cite and the commits CI actually checks from drifting apart. It needs
// no schema cache, so it runs everywhere: a README naming one revision while the
// gate runs another is a claim about a measurement nobody took.
func TestConformanceWorkflowPinsTheDocumentedSchemas(t *testing.T) {
	root := repoRoot(t)
	workflow := readFile(t, filepath.Join(root, ".github/workflows/conformance.yml"))

	refs := map[string]string{}
	for _, key := range []string{"EBML_SPEC_REF", "MATROSKA_SPEC_REF"} {
		m := regexp.MustCompile(key + `:\s*(\S+)`).FindStringSubmatch(workflow)
		if m == nil {
			t.Fatalf("conformance.yml no longer sets %s; this test needs updating with whatever replaced it", key)
		}
		refs[key] = m[1]
	}

	for _, name := range []string{"README.md", "impl/go/README.md"} {
		body := readFile(t, filepath.Join(root, name))
		for key, ref := range refs {
			if !strings.Contains(body, ref) {
				t.Errorf("%s cites no schema revision %s (%s), so its measured numbers name a revision CI does not check",
					name, ref, key)
			}
		}
	}
}
