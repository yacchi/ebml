package specconform

import (
	"strings"
	"testing"

	"github.com/yacchi/ebml/matroska"
)

// The official schema documents are not vendored -- see the package doc -- so
// every test here builds the fragment it needs inline. That keeps `go test
// ./...` free of both a network dependency and a CC-BY-4.0 file, and it lets a
// test state one invariant at a time instead of asserting against 273 elements.

func schemaFor(t *testing.T, body string) *Schema {
	t.Helper()
	doc := `<EBMLSchema xmlns="urn:ietf:rfc:8794" docType="matroska" version="4">` + body + `</EBMLSchema>`
	schema, err := ParseSchema("inline", []byte(doc))
	if err != nil {
		t.Fatalf("ParseSchema: %v", err)
	}
	return schema
}

func findings(report *Report, check, element string) []Finding {
	var matched []Finding
	for _, f := range report.Findings {
		if f.Check == check && f.Element == element {
			matched = append(matched, f)
		}
	}
	return matched
}

func TestParsePathForms(t *testing.T) {
	schema := schemaFor(t, `
		<element name="Segment" path="\Segment" id="0x18538067" type="master" unknownsizeallowed="1"/>
		<element name="Cluster" path="\Segment\Cluster" id="0x1F43B675" type="master"/>
		<element name="ChapterAtom" path="\Segment\Chapters\EditionEntry\+ChapterAtom" id="0xB6" type="master"/>
		<element name="Void" path="\(-\)Void" id="0xEC" type="binary"/>
		<element name="CRC-32" path="\(1-\)CRC-32" id="0xBF" type="binary" length="4"/>
	`)

	want := map[string]struct {
		parent      string
		global      bool
		minLevel    int
		recursive   bool
		unknownSize bool
	}{
		"Segment":     {parent: `\`, unknownSize: true},
		"Cluster":     {parent: `\Segment`},
		"ChapterAtom": {parent: `\Segment\Chapters\EditionEntry`, recursive: true},
		"Void":        {global: true, minLevel: -1},
		"CRC-32":      {global: true, minLevel: 1},
	}
	if len(schema.Elements) != len(want) {
		t.Fatalf("parsed %d elements, want %d", len(schema.Elements), len(want))
	}
	for _, e := range schema.Elements {
		expected, ok := want[e.Name]
		if !ok {
			t.Fatalf("unexpected element %q", e.Name)
		}
		if e.Parent != expected.parent {
			t.Errorf("%s: parent = %q, want %q", e.Name, e.Parent, expected.parent)
		}
		if e.Global != expected.global {
			t.Errorf("%s: global = %v, want %v", e.Name, e.Global, expected.global)
		}
		if e.Global && e.GlobalMinLevel != expected.minLevel {
			t.Errorf("%s: global min level = %d, want %d", e.Name, e.GlobalMinLevel, expected.minLevel)
		}
		if e.Recursive != expected.recursive {
			t.Errorf("%s: recursive = %v, want %v", e.Name, e.Recursive, expected.recursive)
		}
		if e.UnknownSizeAllowed != expected.unknownSize {
			t.Errorf("%s: unknownsizeallowed = %v, want %v", e.Name, e.UnknownSizeAllowed, expected.unknownSize)
		}
	}
}

func TestParsePathRejectsNameMismatch(t *testing.T) {
	doc := `<EBMLSchema docType="matroska" version="4">` +
		`<element name="Cluster" path="\Segment\Timestamp" id="0x1F43B675" type="master"/>` +
		`</EBMLSchema>`
	if _, err := ParseSchema("inline", []byte(doc)); err == nil {
		t.Fatal("expected an error for a path whose leaf is not the element name")
	}
}

// A recursive element is a child of its declared parent AND of itself; a
// containment check that missed the self-edge would report a false gap for
// every nested ChapterAtom child.
func TestChildrenIncludesRecursion(t *testing.T) {
	schema := schemaFor(t, `
		<element name="ChapterAtom" path="\Segment\Chapters\EditionEntry\+ChapterAtom" id="0xB6" type="master"/>
		<element name="ChapterTimeStart" path="\Segment\Chapters\EditionEntry\+ChapterAtom\ChapterTimeStart" id="0x91" type="uinteger"/>
	`)
	children := schema.Children(`\Segment\Chapters\EditionEntry\+ChapterAtom`)
	if len(children) != 2 {
		t.Fatalf("got %d children, want 2 (ChapterAtom itself and ChapterTimeStart)", len(children))
	}
	names := children[0].Name + "," + children[1].Name
	if names != "ChapterAtom,ChapterTimeStart" {
		t.Errorf("children = %s", names)
	}
}

func TestCheckReportsRenamedElement(t *testing.T) {
	schema := schemaFor(t, `
		<element name="Timecode" path="\Segment\Cluster\Timecode" id="0xE7" type="uinteger"/>
	`)
	report := Check(schema)
	found := findings(report, "identity", "Timestamp")
	if len(found) != 1 || !strings.Contains(found[0].Detail, "Timecode") {
		t.Fatalf("expected an identity mismatch naming Timecode, got %v", found)
	}
	if found[0].Severity != SeverityMismatch {
		t.Errorf("severity = %s, want MISMATCH", found[0].Severity)
	}
}

func TestCheckReportsValueTypeMismatch(t *testing.T) {
	schema := schemaFor(t, `
		<element name="Duration" path="\Segment\Info\Duration" id="0x4489" type="uinteger"/>
	`)
	found := findings(Check(schema), "value-type", "Duration")
	if len(found) != 1 || found[0].Severity != SeverityMismatch {
		t.Fatalf("expected a value-type mismatch for Duration, got %v", found)
	}
}

// TypeBlock is a declared refinement of the schema's binary type, so it must
// report as a note and never as a defect.
func TestCheckAcceptsDeclaredBlockRefinement(t *testing.T) {
	schema := schemaFor(t, `
		<element name="SimpleBlock" path="\Segment\Cluster\SimpleBlock" id="0xA3" type="binary"/>
	`)
	found := findings(Check(schema), "value-type", "SimpleBlock")
	if len(found) != 1 || found[0].Severity != SeverityNote {
		t.Fatalf("expected a note for SimpleBlock, got %v", found)
	}
}

// The invariant that matters most: a child the schema declares for a master
// whose child list is documented as COMPLETE is safe only while the element is
// unregistered. Registered and omitted, it would end the master early.
func TestCheckReportsRegisteredChildMissingFromCompleteList(t *testing.T) {
	schema := schemaFor(t, `
		<element name="Cluster" path="\Segment\Cluster" id="0x1F43B675" type="master" unknownsizeallowed="1"/>
		<element name="Timestamp" path="\Segment\Cluster\Timestamp" id="0xE7" type="uinteger"/>
		<element name="Position" path="\Segment\Cluster\Position" id="0xA7" type="uinteger"/>
		<element name="PrevSize" path="\Segment\Cluster\PrevSize" id="0xAB" type="uinteger"/>
		<element name="SimpleBlock" path="\Segment\Cluster\SimpleBlock" id="0xA3" type="binary"/>
		<element name="BlockGroup" path="\Segment\Cluster\BlockGroup" id="0xA0" type="master"/>
		<element name="Tags" path="\Segment\Cluster\Tags" id="0x1254C367" type="master"/>
	`)
	found := findings(Check(schema), "containment", "Cluster")
	if len(found) != 1 || found[0].Severity != SeverityMismatch {
		t.Fatalf("expected one containment mismatch for Cluster, got %v", found)
	}
	if !strings.Contains(found[0].Detail, "Tags") {
		t.Errorf("detail does not name the offending child: %s", found[0].Detail)
	}
}

// The same list is checked in the other direction: a child this library lists
// that the schema does not declare is equally a defect.
func TestCheckReportsChildAbsentFromSchema(t *testing.T) {
	schema := schemaFor(t, `
		<element name="Cluster" path="\Segment\Cluster" id="0x1F43B675" type="master"/>
		<element name="Timestamp" path="\Segment\Cluster\Timestamp" id="0xE7" type="uinteger"/>
	`)
	var absent []Finding
	for _, f := range findings(Check(schema), "containment", "Cluster") {
		if strings.Contains(f.Detail, "the schema does not") {
			absent = append(absent, f)
		}
	}
	// Position, PrevSize, SimpleBlock and BlockGroup are listed by the registry
	// and absent from this fragment.
	if len(absent) != 4 {
		t.Fatalf("expected 4 findings for children absent from the schema, got %d: %v", len(absent), absent)
	}
	for _, f := range absent {
		if f.Severity != SeverityMismatch {
			t.Errorf("severity = %s, want MISMATCH: %s", f.Severity, f.Detail)
		}
	}
}

// clusterSchema is the Cluster subtree the registry lists as complete, plus
// whatever extra child a test wants to add to it.
func clusterSchema(t *testing.T, extra string) *Schema {
	t.Helper()
	return schemaFor(t, `
		<element name="Cluster" path="\Segment\Cluster" id="0x1F43B675" type="master" unknownsizeallowed="1"/>
		<element name="Timestamp" path="\Segment\Cluster\Timestamp" id="0xE7" type="uinteger"/>
		<element name="Position" path="\Segment\Cluster\Position" id="0xA7" type="uinteger"/>
		<element name="PrevSize" path="\Segment\Cluster\PrevSize" id="0xAB" type="uinteger"/>
		<element name="SimpleBlock" path="\Segment\Cluster\SimpleBlock" id="0xA3" type="binary"/>
		<element name="BlockGroup" path="\Segment\Cluster\BlockGroup" id="0xA0" type="master"/>
	`+extra)
}

// Being unregistered is not on its own a good enough reason to leave a child
// out of a list documented as COMPLETE. Only a child the schema marks REMOVED
// is a deliberate omission; a child the schema still declares current is a
// coverage hole, and reporting it as a note would hide exactly the case this
// check exists for.
func TestCheckReportsCurrentChildMissingFromCompleteList(t *testing.T) {
	current := clusterSchema(t, `
		<element name="SomeCurrentChild" path="\Segment\Cluster\SomeCurrentChild" id="0x4FFD" type="binary"/>
	`)
	found := findings(Check(current), "containment", "Cluster")
	if len(found) != 1 || found[0].Severity != SeverityGap {
		t.Fatalf("expected one containment GAP for Cluster, got %v", found)
	}
	if !strings.Contains(found[0].Detail, "SomeCurrentChild") {
		t.Errorf("detail does not name the child: %s", found[0].Detail)
	}
}

// The same child, marked removed before the schema's own version, is the
// deliberate case and reports as a note.
func TestCheckAcceptsRemovedChildMissingFromCompleteList(t *testing.T) {
	removed := clusterSchema(t, `
		<element name="SomeRemovedChild" path="\Segment\Cluster\SomeRemovedChild" id="0x4FFD" type="binary" minver="0" maxver="0"/>
	`)
	found := findings(Check(removed), "containment", "Cluster")
	if len(found) != 1 || found[0].Severity != SeverityNote {
		t.Fatalf("expected one containment note for Cluster, got %v", found)
	}
	if !strings.Contains(found[0].Detail, "removed after version 0") {
		t.Errorf("detail does not state why the omission is deliberate: %s", found[0].Detail)
	}
}

// maxver at or above the schema's own version is not a removal.
func TestRemovedReadsMaxverAgainstTheSchemaVersion(t *testing.T) {
	schema := schemaFor(t, `
		<element name="Current" path="\Segment\Current" id="0x4FFD" type="binary" maxver="4"/>
		<element name="Old" path="\Segment\Old" id="0x4FFC" type="binary" maxver="2"/>
		<element name="Unbounded" path="\Segment\Unbounded" id="0x4FFB" type="binary"/>
	`)
	want := map[string]bool{"Current": false, "Old": true, "Unbounded": false}
	for _, e := range schema.Elements {
		if got := schema.Removed(e); got != want[e.Name] {
			t.Errorf("Removed(%s) = %v, want %v", e.Name, got, want[e.Name])
		}
	}
}

// The schema states recursion twice. If this checker's path parsing and the
// recursive attribute disagree, Children() is wrong and every containment
// verdict resting on it is worthless -- so the disagreement is a mismatch, not
// a curiosity.
func TestCheckReportsRecursionMarkerDisagreement(t *testing.T) {
	schema := schemaFor(t, `
		<element name="ChapterAtom" path="\Segment\Chapters\EditionEntry\+ChapterAtom" id="0xB6" type="master"/>
	`)
	found := findings(Check(schema), "path-consistency", "ChapterAtom")
	if len(found) != 1 || found[0].Severity != SeverityMismatch {
		t.Fatalf("expected a path-consistency mismatch, got %v", found)
	}
}

func TestCheckAcceptsAgreeingRecursionMarkers(t *testing.T) {
	schema := schemaFor(t, `
		<element name="SimpleTag" path="\Segment\Tags\Tag\+SimpleTag" id="0x67C8" type="master" recursive="1"/>
		<element name="Cluster" path="\Segment\Cluster" id="0x1F43B675" type="master"/>
	`)
	if found := findings(Check(schema), "path-consistency", "SimpleTag"); len(found) != 0 {
		t.Fatalf("unexpected findings: %v", found)
	}
	if found := findings(Check(schema), "path-consistency", "Cluster"); len(found) != 0 {
		t.Fatalf("unexpected findings: %v", found)
	}
}

// A report that listed only what it verified would read as "the library
// conforms". It has to say what it never looked at.
func TestReportStatesWhatItDoesNotCheck(t *testing.T) {
	schema := schemaFor(t, `
		<element name="Cluster" path="\Segment\Cluster" id="0x1F43B675" type="master" maxOccurs="1"/>
		<element name="Timestamp" path="\Segment\Cluster\Timestamp" id="0xE7" type="uinteger" default="0" range="not 0"/>
	`)
	report := Check(schema)
	seen := map[string]int{}
	for _, facet := range report.Unchecked {
		seen[facet.Name] = facet.Elements
		if facet.Missing == "" {
			t.Errorf("facet %q does not say what capability it would need", facet.Name)
		}
	}
	if seen["maxOccurs/minOccurs"] != 1 || seen["default"] != 1 || seen["range"] != 1 {
		t.Fatalf("unchecked facets = %v", seen)
	}
}

// A master the schema lets carry an unknown size needs a complete child list,
// because that list is the only thing that can close it.
func TestCheckReportsUnknownSizeMasterWithoutChildList(t *testing.T) {
	schema := schemaFor(t, `
		<element name="Tags" path="\Segment\Tags" id="0x1254C367" type="master" unknownsizeallowed="1"/>
	`)
	found := findings(Check(schema), "unknown-size", "Tags")
	if len(found) != 1 || found[0].Severity != SeverityMismatch {
		t.Fatalf("expected an unknown-size mismatch for Tags, got %v", found)
	}
}

func TestCheckDerivesGlobalElements(t *testing.T) {
	globals := derivedGlobals()
	if len(globals) != 2 || !globals[matroska.IDCRC32] || !globals[matroska.IDVoid] {
		t.Fatalf("derived globals = %v, want exactly CRC-32 and Void", globals)
	}
}

func TestCheckReportsUndeclaredGlobal(t *testing.T) {
	// A schema that declares Void as an ordinary Cluster child contradicts the
	// registry, which never lets Void end a master.
	schema := schemaFor(t, `
		<element name="Void" path="\Segment\Cluster\Void" id="0xEC" type="binary"/>
	`)
	found := findings(Check(schema), "global", "Void")
	if len(found) != 1 || found[0].Severity != SeverityMismatch {
		t.Fatalf("expected a global mismatch for Void, got %v", found)
	}
}

func TestCheckHeaderLimits(t *testing.T) {
	ok := schemaFor(t, `
		<element name="EBMLMaxIDLength" path="\EBML\EBMLMaxIDLength" id="0x42F2" type="uinteger" range="4" default="4"/>
		<element name="EBMLMaxSizeLength" path="\EBML\EBMLMaxSizeLength" id="0x42F3" type="uinteger" range="1-8" default="8"/>
	`)
	for _, f := range findings(Check(ok), "header-limits", "EBMLMaxIDLength") {
		if f.Severity == SeverityMismatch {
			t.Errorf("unexpected mismatch: %s", f.Detail)
		}
	}
	for _, f := range findings(Check(ok), "header-limits", "EBMLMaxSizeLength") {
		if f.Severity == SeverityMismatch {
			t.Errorf("unexpected mismatch: %s", f.Detail)
		}
	}

	narrowed := schemaFor(t, `
		<element name="EBMLMaxSizeLength" path="\EBML\EBMLMaxSizeLength" id="0x42F3" type="uinteger" range="1-4" default="8"/>
	`)
	found := findings(Check(narrowed), "header-limits", "EBMLMaxSizeLength")
	if len(found) != 1 || found[0].Severity != SeverityMismatch {
		t.Fatalf("expected a header-limits mismatch, got %v", found)
	}
}

func TestRangeUpperBound(t *testing.T) {
	cases := []struct {
		spec  string
		bound int
		ok    bool
	}{
		{"4", 4, true},
		{"1-8", 8, true},
		{"<=8", 8, true},
		{">=4", 0, false},
		{"not 0", 0, false},
		{"", 0, false},
	}
	for _, c := range cases {
		bound, ok := rangeUpperBound(c.spec)
		if ok != c.ok || bound != c.bound {
			t.Errorf("rangeUpperBound(%q) = %d, %v; want %d, %v", c.spec, bound, ok, c.bound, c.ok)
		}
	}
}

// Every element the registry knows must be one some schema declares. Run
// against a fragment, the check reports the rest as unknown to the schema --
// which is what makes the full-corpus run meaningful.
func TestCheckReportsRegistryElementsUnknownToSchema(t *testing.T) {
	schema := schemaFor(t, `
		<element name="Cluster" path="\Segment\Cluster" id="0x1F43B675" type="master"/>
	`)
	report := Check(schema)
	if report.Covered != 1 {
		t.Fatalf("covered = %d, want 1", report.Covered)
	}
	unknown := 0
	for _, f := range report.Findings {
		if f.Check == "identity" && strings.Contains(f.Detail, "no loaded schema declares") {
			unknown++
		}
	}
	if want := report.Registered - 1; unknown != want {
		t.Errorf("elements unknown to the schema = %d, want %d", unknown, want)
	}
}

// The corpus plants elements "no registry knows". If a schema turns out to
// define one of those IDs, the fixture documenting that claim is wrong and the
// golden trace recorded from it looks like evidence for it.
func TestCheckReportsSchemaDefinedSentinelID(t *testing.T) {
	schema := schemaFor(t, `
		<element name="SomeFutureElement" path="\Segment\SomeFutureElement" id="0x4FFE" type="binary"/>
	`)
	found := findings(Check(schema), "unassigned", "SomeFutureElement")
	if len(found) != 1 || found[0].Severity != SeverityMismatch {
		t.Fatalf("expected an unassigned mismatch, got %v", found)
	}
}

func TestSentinelIDsAreUnassignedInFragments(t *testing.T) {
	schema := schemaFor(t, `
		<element name="Cluster" path="\Segment\Cluster" id="0x1F43B675" type="master"/>
	`)
	if found := findings(Check(schema), "unassigned", ""); len(found) != 0 {
		t.Fatalf("unexpected findings: %v", found)
	}
}

func TestCheckNotesIncompleteSchemaSet(t *testing.T) {
	report := Check(schemaFor(t, `<element name="Cluster" path="\Segment\Cluster" id="0x1F43B675" type="master"/>`))
	if len(findings(report, "schema-set", "")) != 1 {
		t.Fatal("expected a note that the ebml doc type is missing")
	}
}
