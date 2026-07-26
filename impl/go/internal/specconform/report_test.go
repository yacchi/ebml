package specconform

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/yacchi/ebml/impl/go/parser"
)

func TestSeverityString(t *testing.T) {
	tests := []struct {
		severity Severity
		want     string
	}{
		{SeverityMismatch, "MISMATCH"},
		{SeverityGap, "GAP"},
		{SeverityNote, "note"},
		{SeverityNote + 1, "Severity(3)"},
	}
	for _, tc := range tests {
		if got := tc.severity.String(); got != tc.want {
			t.Errorf("Severity(%d).String() = %q, want %q", int(tc.severity), got, tc.want)
		}
	}
}

// TestMismatchesCountsOnlyDefects pins the distinction the severities exist for:
// a gap is coverage work and a note is a declared divergence, so neither may
// make a run look like it found a defect.
func TestMismatchesCountsOnlyDefects(t *testing.T) {
	r := &Report{Findings: []Finding{
		{Severity: SeverityMismatch, Check: "identity"},
		{Severity: SeverityGap, Check: "containment"},
		{Severity: SeverityNote, Check: "value-type"},
		{Severity: SeverityMismatch, Check: "identity"},
	}}
	if got := r.Mismatches(); got != 2 {
		t.Errorf("Mismatches = %d, want 2", got)
	}
	if got := (&Report{}).Mismatches(); got != 0 {
		t.Errorf("Mismatches on an empty report = %d, want 0", got)
	}
}

// sampleReport carries one finding of each severity, one with an element name
// and one without, plus an unchecked facet -- every branch WriteText has.
func sampleReport() *Report {
	return &Report{
		Sources:    []string{"ebml.xml", "ebml_matroska.xml"},
		Registered: 100,
		Declared:   120,
		Covered:    95,
		Missing: []SchemaElement{
			{ID: 0x4489, Name: "Duration", Type: "float", Parent: `\Segment\Info`},
		},
		Findings: []Finding{
			{Severity: SeverityMismatch, Check: "identity", Element: "Segment", Detail: "id differs"},
			{Severity: SeverityGap, Check: "containment", Detail: "no element named"},
			{Severity: SeverityNote, Check: "value-type", Element: "SimpleBlock", Detail: "declared divergence"},
		},
		Unchecked: []Facet{
			{Name: "cardinality", Elements: 42, Missing: "minOccurs/maxOccurs enforcement"},
		},
	}
}

// TestWriteTextSummarisesNotesUnlessVerbose pins the one behavioural difference
// between the two modes. A declared divergence is not news on every run, so the
// default states how many there are; -v is what makes them readable.
func TestWriteTextSummarisesNotesUnlessVerbose(t *testing.T) {
	var quiet bytes.Buffer
	sampleReport().WriteText(&quiet, false)
	got := quiet.String()

	for _, want := range []string{
		"schemas: ebml.xml, ebml_matroska.xml",
		"registry: 100 elements, 95 of them declared by a schema",
		"schema:   120 elements, 119 of them registered (1 missing)",
		// Checks are grouped and sorted, so a report reads check by check.
		"\ncontainment\n",
		"\nidentity\n",
		"MISMATCH Segment: id differs",
		// A finding with no element name omits the colon rather than printing one.
		"GAP      no element named",
		"1 note(s) suppressed; pass -v to list them",
		"not checked -- the schema declares these",
		"cardinality",
		"minOccurs/maxOccurs enforcement",
		"1 mismatch(es)",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("report missing %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "declared divergence") {
		t.Errorf("the non-verbose report listed a note:\n%s", got)
	}

	var verbose bytes.Buffer
	sampleReport().WriteText(&verbose, true)
	got = verbose.String()
	if !strings.Contains(got, "note     SimpleBlock: declared divergence") {
		t.Errorf("the verbose report did not list the note:\n%s", got)
	}
	if strings.Contains(got, "suppressed") {
		t.Errorf("the verbose report still claimed to suppress notes:\n%s", got)
	}
}

// TestWriteTextOmitsTheUncheckedSectionWhenEmpty keeps the scope statement from
// becoming noise: a checker that verifies everything has nothing to disclaim.
func TestWriteTextOmitsTheUncheckedSectionWhenEmpty(t *testing.T) {
	r := sampleReport()
	r.Unchecked = nil
	var out bytes.Buffer
	r.WriteText(&out, false)
	if strings.Contains(out.String(), "not checked") {
		t.Errorf("empty Unchecked still produced a section:\n%s", out.String())
	}
}

// TestWriteMissingGroupsByParent pins the grouping, which is what makes the
// output a worklist rather than a list: the registry is extended one parent's
// containment list at a time.
func TestWriteMissingGroupsByParent(t *testing.T) {
	r := &Report{Missing: []SchemaElement{
		{ID: 0x4489, Name: "Duration", Type: "float", Parent: `\Segment\Info`},
		{ID: 0x2AD7B1, Name: "TimestampScale", Type: "uinteger", Parent: `\Segment\Info`},
		{ID: 0xBF, Name: "CRC-32", Type: "binary", Global: true},
	}}
	var out bytes.Buffer
	r.WriteMissing(&out)
	got := out.String()

	for _, want := range []string{
		"3 schema element(s) the registry does not know:",
		`\Segment\Info`,
		"0x4489     Duration",
		"0x2AD7B1   TimestampScale",
		// A global element has no single parent and is grouped as such.
		"(global)",
		"0xBF       CRC-32",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("missing report lacks %q:\n%s", want, got)
		}
	}
	// The shared parent is printed once, not once per element.
	if n := strings.Count(got, `\Segment\Info`); n != 1 {
		t.Errorf("parent group printed %d times, want 1:\n%s", n, got)
	}
}

func TestWriteMissingStatesCompleteCoverage(t *testing.T) {
	var out bytes.Buffer
	(&Report{}).WriteMissing(&out)
	if !strings.Contains(out.String(), "the registry declares every element the loaded schemas do") {
		t.Errorf("empty worklist did not say so: %q", out.String())
	}
}

const miniSchema = `<EBMLSchema docType="matroska" version="4">
  <element name="Segment" path="\Segment" id="0x18538067" type="master" unknownsizeallowed="1"/>
  <element name="Info" path="\Segment\Info" id="0x1549A966" type="master"/>
</EBMLSchema>`

func TestLoadSchemaReadsFromDisk(t *testing.T) {
	path := filepath.Join(t.TempDir(), "mini.xml")
	if err := os.WriteFile(path, []byte(miniSchema), 0o600); err != nil {
		t.Fatalf("write schema: %v", err)
	}
	s, err := LoadSchema(path)
	if err != nil {
		t.Fatalf("LoadSchema: %v", err)
	}
	if s.Source != path {
		t.Errorf("Source = %q, want %q", s.Source, path)
	}
	if s.DocType != "matroska" || s.Version != "4" {
		t.Errorf("docType/version = %q/%q, want matroska/4", s.DocType, s.Version)
	}
	if len(s.Elements) != 2 {
		t.Fatalf("parsed %d elements, want 2", len(s.Elements))
	}
	if s.Elements[0].ID != parser.ElementID(0x18538067) || !s.Elements[0].UnknownSizeAllowed {
		t.Errorf("Segment parsed as %+v", s.Elements[0])
	}
	if s.Elements[1].Parent != `\Segment` {
		t.Errorf("Info parent = %q, want %q", s.Elements[1].Parent, `\Segment`)
	}
}

func TestLoadSchemaReportsAMissingFile(t *testing.T) {
	if _, err := LoadSchema(filepath.Join(t.TempDir(), "absent.xml")); err == nil {
		t.Fatal("LoadSchema succeeded on a missing file")
	}
}
