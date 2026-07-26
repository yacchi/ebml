package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// miniSchema is a PARTIAL schema, and every test here is written knowing that a
// partial schema can never produce a clean run: the checker reports a registered
// element no loaded schema declares as a mismatch, which is what makes it able to
// catch an ID the registry invented. Only the complete CC-BY documents -- which
// this repository never vendors -- can exit 0, so that path belongs to the
// conformance workflow and not to a unit test. What is testable here is the
// command around the check: its flags, its input failures, and its exit codes.
const miniSchema = `<EBMLSchema docType="matroska" version="4">
  <element name="DocType" path="\EBML\DocType" id="0x4282" type="string"/>
  <element name="DocTypeVersion" path="\EBML\DocTypeVersion" id="0x4287" type="uinteger"/>
</EBMLSchema>`

func writeSchema(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "schema.xml")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("write schema: %v", err)
	}
	return path
}

func TestSchemaPathsAccumulates(t *testing.T) {
	var p schemaPaths
	for _, v := range []string{"a.xml", "b.xml"} {
		if err := p.Set(v); err != nil {
			t.Fatalf("Set(%q): %v", v, err)
		}
	}
	if len(p) != 2 || p[0] != "a.xml" || p[1] != "b.xml" {
		t.Errorf("paths = %v, want [a.xml b.xml]", p)
	}
	if got := p.String(); !strings.Contains(got, "a.xml") || !strings.Contains(got, "b.xml") {
		t.Errorf("String() = %q", got)
	}
}

// TestRunWritesTheReportToStdout keeps the report on stdout and nothing but
// diagnostics on stderr, so `checkschema ... | less` shows the report.
func TestRunWritesTheReportToStdout(t *testing.T) {
	path := writeSchema(t, miniSchema)
	var stdout, stderr bytes.Buffer
	run([]string{"-schema", path}, &stdout, &stderr)
	for _, want := range []string{"schemas: ", "registry: ", "schema:   ", "mismatch(es)"} {
		if !strings.Contains(stdout.String(), want) {
			t.Errorf("report missing %q:\n%s", want, stdout.String())
		}
	}
	if stderr.Len() != 0 {
		t.Errorf("a successful parse wrote to stderr: %q", stderr.String())
	}
}

// TestRunListsMissingOnRequest pins -missing as the coverage worklist: an
// element a schema declares and the registry does not know is coverage, not a
// defect, so it appears under the worklist heading and never as a MISMATCH.
func TestRunListsMissingOnRequest(t *testing.T) {
	path := writeSchema(t, `<EBMLSchema docType="matroska" version="4">
  <element name="DocType" path="\EBML\DocType" id="0x4282" type="string"/>
  <element name="NoSuchElement" path="\EBML\NoSuchElement" id="0x4EEE" type="binary"/>
</EBMLSchema>`)

	var withFlag, without bytes.Buffer
	run([]string{"-schema", path, "-missing", "-v"}, &withFlag, new(bytes.Buffer))
	run([]string{"-schema", path}, &without, new(bytes.Buffer))

	if !strings.Contains(withFlag.String(), "schema element(s) the registry does not know") ||
		!strings.Contains(withFlag.String(), "NoSuchElement") {
		t.Errorf("-missing did not list the unregistered element:\n%s", withFlag.String())
	}
	if strings.Contains(without.String(), "schema element(s) the registry does not know") {
		t.Errorf("the worklist appeared without -missing:\n%s", without.String())
	}
	if strings.Contains(withFlag.String(), "MISMATCH NoSuchElement") {
		t.Errorf("an unregistered element was reported as a defect:\n%s", withFlag.String())
	}
}

func TestRunRejectsBadInvocations(t *testing.T) {
	tests := []struct {
		name    string
		args    []string
		wantErr string
	}{
		{"no schema", nil, "at least one -schema is required"},
		{"unknown flag", []string{"-nosuchflag"}, "flag provided but not defined"},
		{"missing file", []string{"-schema", filepath.Join(t.TempDir(), "absent.xml")}, "checkschema: "},
		{"unparseable schema", []string{"-schema", writeSchema(t, "not xml at all")}, "checkschema: "},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			if code := run(tc.args, &stdout, &stderr); code != 2 {
				t.Fatalf("run = %d, want 2 (stderr=%q)", code, stderr.String())
			}
			if !strings.Contains(stderr.String(), tc.wantErr) {
				t.Errorf("stderr = %q, want it to mention %q", stderr.String(), tc.wantErr)
			}
			if stdout.Len() != 0 {
				t.Errorf("a failing invocation wrote a report to stdout: %q", stdout.String())
			}
		})
	}
}

// TestRunExitsOneOnAContradiction is the exit code CI reads: the registry
// stating something a schema contradicts is a defect, not coverage.
func TestRunExitsOneOnAContradiction(t *testing.T) {
	// The registry types DocType as a string; the schema here calls it a uinteger.
	path := writeSchema(t, `<EBMLSchema docType="matroska" version="4">
  <element name="DocType" path="\EBML\DocType" id="0x4282" type="uinteger"/>
</EBMLSchema>`)

	var stdout, stderr bytes.Buffer
	if code := run([]string{"-schema", path}, &stdout, &stderr); code != 1 {
		t.Fatalf("run = %d, want 1\nstdout:\n%s", code, stdout.String())
	}
	if !strings.Contains(stdout.String(), "MISMATCH") {
		t.Errorf("report did not name the mismatch:\n%s", stdout.String())
	}
}
