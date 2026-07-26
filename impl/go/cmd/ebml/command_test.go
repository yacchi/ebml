package main

import (
	"bytes"
	"encoding/xml"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/yacchi/ebml/impl/go/internal/ebmltest"
	"github.com/yacchi/ebml/impl/go/matroska"
)

// writeTemp writes b to a file in a per-test directory and returns its path.
func writeTemp(t *testing.T, name string, b []byte) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, b, 0o600); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
	return path
}

// tinyHex is the commented-hex fixture the CLI's --hex flag exists to read.
func tinyHex(t *testing.T) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "..", "..", "..", "fixtures", "tiny.ebml.hex"))
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	return b
}

// TestRunHelpExitsZero separates the two ways the top-level usage is reached:
// an explicit help request succeeds and prints to stdout, while a missing or
// unknown command fails and prints to stderr. A tool whose help text goes to
// stderr cannot be piped, so the destination is part of the behaviour.
func TestRunHelpExitsZero(t *testing.T) {
	for _, arg := range []string{"-h", "--help", "help"} {
		var stdout, stderr bytes.Buffer
		if code := run([]string{arg}, nil, &stdout, &stderr); code != 0 {
			t.Errorf("run(%q) = %d, want 0", arg, code)
		}
		if !strings.Contains(stdout.String(), "Usage: ebml <command>") {
			t.Errorf("run(%q) did not print usage to stdout: %q", arg, stdout.String())
		}
		if stderr.Len() != 0 {
			t.Errorf("run(%q) wrote to stderr: %q", arg, stderr.String())
		}
	}
}

func TestRunUnknownCommandPrintsUsageToStderr(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if code := run([]string{"bogus"}, nil, &stdout, &stderr); code != 2 {
		t.Fatalf("run = %d, want 2", code)
	}
	if !strings.Contains(stderr.String(), `unknown command "bogus"`) ||
		!strings.Contains(stderr.String(), "Usage: ebml <command>") {
		t.Errorf("stderr = %q", stderr.String())
	}
	if stdout.Len() != 0 {
		t.Errorf("a failing invocation wrote to stdout: %q", stdout.String())
	}
}

// TestCommandsReadFileArgument covers the FILE positional for both subcommands,
// which is the path a shell user takes and the one the tests above bypass by
// calling runDump/runXML directly.
func TestCommandsReadFileArgument(t *testing.T) {
	path := writeTemp(t, "tiny.ebml.hex", tinyHex(t))
	for _, cmd := range []string{"dump", "xml"} {
		t.Run(cmd, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			if code := run([]string{cmd, "--hex", path}, nil, &stdout, &stderr); code != 0 {
				t.Fatalf("run = %d, want 0 (stderr=%q)", code, stderr.String())
			}
			if !strings.Contains(stdout.String(), "DocType") {
				t.Errorf("%s output missing DocType:\n%s", cmd, stdout.String())
			}
		})
	}
}

// TestCommandsReadStdin pins both spellings of stdin: an absent FILE and "-".
func TestCommandsReadStdin(t *testing.T) {
	hex := tinyHex(t)
	for _, cmd := range []string{"dump", "xml"} {
		for _, args := range [][]string{{cmd, "--hex"}, {cmd, "--hex", "-"}} {
			var stdout, stderr bytes.Buffer
			if code := run(args, bytes.NewReader(hex), &stdout, &stderr); code != 0 {
				t.Fatalf("run(%v) = %d, want 0 (stderr=%q)", args, code, stderr.String())
			}
			if !strings.Contains(stdout.String(), "DocType") {
				t.Errorf("run(%v) output missing DocType:\n%s", args, stdout.String())
			}
		}
	}
}

// TestCommandFailuresExitNonZero enumerates the failure exits both subcommands
// share. An unparseable flag is a usage error (2); everything the command was
// asked to do but could not is a run failure (1).
func TestCommandFailuresExitNonZero(t *testing.T) {
	path := writeTemp(t, "tiny.ebml.hex", tinyHex(t))
	badHex := writeTemp(t, "bad.hex", []byte("# not hex\nzzzz\n"))

	tests := []struct {
		name     string
		args     []string
		wantCode int
		wantErr  string
	}{
		{"unknown flag", []string{"-nosuchflag"}, 2, "flag provided but not defined"},
		{"two FILE arguments", []string{path, path}, 1, "expected at most one FILE argument"},
		{"missing file", []string{filepath.Join(t.TempDir(), "absent")}, 1, "ebml: "},
		{"undecodable hex", []string{"--hex", badHex}, 1, "decode hex input"},
		{"raw input that is not EBML", []string{path}, 1, "at offset"},
	}
	for _, cmd := range []string{"dump", "xml"} {
		for _, tc := range tests {
			t.Run(cmd+"/"+tc.name, func(t *testing.T) {
				var stdout, stderr bytes.Buffer
				code := run(append([]string{cmd}, tc.args...), nil, &stdout, &stderr)
				if code != tc.wantCode {
					t.Fatalf("run = %d, want %d (stderr=%q)", code, tc.wantCode, stderr.String())
				}
				if !strings.Contains(stderr.String(), tc.wantErr) {
					t.Errorf("stderr = %q, want it to mention %q", stderr.String(), tc.wantErr)
				}
			})
		}
	}
}

// TestOpenInputClosesTheFileItOpened pins the close func contract: it is always
// safe to call, including on the paths that opened nothing.
func TestOpenInputClosesTheFileItOpened(t *testing.T) {
	path := writeTemp(t, "in", []byte("payload"))

	r, closeFn, err := openInput([]string{path}, nil)
	if err != nil {
		t.Fatalf("openInput: %v", err)
	}
	got, err := io.ReadAll(r)
	if err != nil || string(got) != "payload" {
		t.Fatalf("read = (%q, %v)", got, err)
	}
	closeFn()

	stdin := strings.NewReader("from stdin")
	for _, args := range [][]string{nil, {"-"}} {
		r, closeFn, err := openInput(args, stdin)
		if err != nil {
			t.Fatalf("openInput(%v): %v", args, err)
		}
		if r != io.Reader(stdin) {
			t.Errorf("openInput(%v) did not return stdin", args)
		}
		closeFn()
	}

	// The error paths still hand back a callable close func.
	for _, args := range [][]string{{"a", "b"}, {filepath.Join(t.TempDir(), "absent")}} {
		_, closeFn, err := openInput(args, nil)
		if err == nil {
			t.Errorf("openInput(%v) succeeded, want an error", args)
		}
		closeFn()
	}
}

func TestSourceReaderPassesRawInputThrough(t *testing.T) {
	raw := []byte{0x1A, 0x45, 0xDF, 0xA3}
	in := bytes.NewReader(raw)
	got, err := sourceReader(in, false)
	if err != nil {
		t.Fatalf("sourceReader: %v", err)
	}
	if got != io.Reader(in) {
		t.Errorf("raw input was not passed through unchanged")
	}
}

func TestSourceReaderRejectsUndecodableHex(t *testing.T) {
	_, err := sourceReader(strings.NewReader("# comment\nzz\n"), true)
	if err == nil {
		t.Fatal("expected an error on undecodable hex")
	}
	if !strings.Contains(err.Error(), "decode hex input") {
		t.Errorf("error = %v, want it to name the decode step", err)
	}
}

// unregisteredDocument is an EBML document holding an element the registry does
// not name. No fixture carries one -- a fixture models what the field sends --
// so the unknown-element rendering has to be built here.
func unregisteredDocument() []byte {
	return ebmltest.Encode(
		ebmltest.Master(matroska.IDEBML,
			ebmltest.String(matroska.IDDocType, "matroska"),
		),
		ebmltest.Master(matroska.IDSegment,
			ebmltest.Leaf(unregisteredID, []byte{0xDE, 0xAD, 0xBE, 0xEF}),
		),
	)
}

// TestDumpRendersUnregisteredElement pins both halves of the unknown-element
// path: with a byte budget the payload is shown as hex, and with none the dump
// keeps the cursor's skipping default and states the size alone.
func TestDumpRendersUnregisteredElement(t *testing.T) {
	raw := unregisteredDocument()

	var out bytes.Buffer
	if err := runDump(bytes.NewReader(raw), &out, dumpOptions{maxBinary: 16}); err != nil {
		t.Fatalf("runDump: %v", err)
	}
	for _, want := range []string{"type unknown", "binary 4 bytes: deadbeef"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("dump missing %q:\n%s", want, out.String())
		}
	}

	out.Reset()
	if err := runDump(bytes.NewReader(raw), &out, dumpOptions{maxBinary: 0}); err != nil {
		t.Fatalf("runDump: %v", err)
	}
	if !strings.Contains(out.String(), "= binary 4 bytes\n") {
		t.Errorf("dump did not state the size alone:\n%s", out.String())
	}
	if strings.Contains(out.String(), "deadbeef") {
		t.Errorf("--max-binary 0 materialised a payload it was not going to print:\n%s", out.String())
	}

	// A truncated hex rendering is marked as such.
	out.Reset()
	if err := runDump(bytes.NewReader(raw), &out, dumpOptions{maxBinary: 2}); err != nil {
		t.Fatalf("runDump: %v", err)
	}
	if !strings.Contains(out.String(), "binary 4 bytes: dead...") {
		t.Errorf("dump did not mark the rendering as truncated:\n%s", out.String())
	}
}

// TestXMLRendersUnregisteredElement pins the XML side of the same shape: an
// element the registry cannot name is emitted under a placeholder tag and flagged
// with unknown="true", so a consumer can tell it apart from a named element.
func TestXMLRendersUnregisteredElement(t *testing.T) {
	raw := unregisteredDocument()

	var out bytes.Buffer
	if err := runXML(bytes.NewReader(raw), &out, xmlOptions{maxBinary: 2}); err != nil {
		t.Fatalf("runXML: %v", err)
	}
	got := out.String()
	for _, want := range []string{
		`<Unknown `, `unknown="true"`, `encoding="hex"`, `bytes="4"`, `truncated="true"`, `>dead<`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("xml missing %q:\n%s", want, got)
		}
	}

	// With no byte budget the leaf keeps the skipping default: the size is
	// reported, no payload byte is emitted, and the document still parses.
	out.Reset()
	if err := runXML(bytes.NewReader(raw), &out, xmlOptions{maxBinary: 0}); err != nil {
		t.Fatalf("runXML: %v", err)
	}
	got = out.String()
	if !strings.Contains(got, `bytes="4"`) {
		t.Errorf("xml did not report the size:\n%s", got)
	}
	if strings.Contains(got, "dead") {
		t.Errorf("--max-binary 0 emitted payload bytes:\n%s", got)
	}
	if err := xml.Unmarshal([]byte(got), new(struct{})); err != nil {
		t.Errorf("xml does not parse: %v\n%s", err, got)
	}
}

// TestUndecodableBlockFallsBackToBinary pins the fallback both renderers share:
// an element the registry types as a block whose payload does not parse as one
// is rendered as binary rather than dropped or reported as an error. A stream
// carrying a damaged block is exactly when a dump is worth running.
func TestUndecodableBlockFallsBackToBinary(t *testing.T) {
	raw := ebmltest.Encode(
		ebmltest.Master(matroska.IDEBML,
			ebmltest.String(matroska.IDDocType, "matroska"),
		),
		ebmltest.Master(matroska.IDSegment,
			ebmltest.Master(matroska.IDCluster,
				ebmltest.Uint(matroska.IDTimestamp, 0),
				// One byte is a track VINT and nothing else: not a block.
				ebmltest.Leaf(matroska.IDSimpleBlock, []byte{0x81}),
			),
		),
	)

	var dumpOut bytes.Buffer
	if err := runDump(bytes.NewReader(raw), &dumpOut, dumpOptions{maxBinary: 16}); err != nil {
		t.Fatalf("runDump: %v", err)
	}
	if !strings.Contains(dumpOut.String(), "SimpleBlock (0xA3)") ||
		!strings.Contains(dumpOut.String(), "binary 1 bytes: 81") {
		t.Errorf("dump did not fall back to a binary rendering:\n%s", dumpOut.String())
	}
	if strings.Contains(dumpOut.String(), "track=") {
		t.Errorf("dump summarised a payload that is not a block:\n%s", dumpOut.String())
	}

	var xmlOut bytes.Buffer
	if err := runXML(bytes.NewReader(raw), &xmlOut, xmlOptions{maxBinary: 16}); err != nil {
		t.Fatalf("runXML: %v", err)
	}
	if !strings.Contains(xmlOut.String(), `encoding="hex"`) ||
		!strings.Contains(xmlOut.String(), ">81<") {
		t.Errorf("xml did not fall back to a binary rendering:\n%s", xmlOut.String())
	}
	if strings.Contains(xmlOut.String(), "block=") {
		t.Errorf("xml summarised a payload that is not a block:\n%s", xmlOut.String())
	}
}

// TestEmptyBinaryPayloadStatesItsSize covers the rendering of a zero-length
// binary leaf, where there is a byte budget but no byte to spend it on.
func TestEmptyBinaryPayloadStatesItsSize(t *testing.T) {
	raw := ebmltest.Encode(
		ebmltest.Master(matroska.IDEBML,
			ebmltest.String(matroska.IDDocType, "matroska"),
		),
		ebmltest.Master(matroska.IDSegment,
			ebmltest.Leaf(matroska.IDVoid, nil),
		),
	)
	var out bytes.Buffer
	if err := runDump(bytes.NewReader(raw), &out, dumpOptions{maxBinary: 16}); err != nil {
		t.Fatalf("runDump: %v", err)
	}
	if !strings.Contains(out.String(), "= binary 0 bytes\n") {
		t.Errorf("dump did not state the size of an empty payload:\n%s", out.String())
	}
}

// failWriter fails every write, standing in for the closed pipe a CLI meets when
// its output is piped into a reader that exits first.
type failWriter struct{}

func (failWriter) Write([]byte) (int, error) { return 0, io.ErrClosedPipe }

// TestOutputErrorsAreReported pins that a failing destination is reported rather
// than swallowed. The xml path matters most: it keeps its own sticky error so it
// can always close its open elements, and that must not turn into a success.
func TestOutputErrorsAreReported(t *testing.T) {
	raw := tinyHex(t)

	decoded, err := sourceReader(bytes.NewReader(raw), true)
	if err != nil {
		t.Fatalf("sourceReader: %v", err)
	}
	body, err := io.ReadAll(decoded)
	if err != nil {
		t.Fatalf("read decoded fixture: %v", err)
	}

	if err := runXML(bytes.NewReader(body), failWriter{}, xmlOptions{maxBinary: 16}); err == nil {
		t.Error("runXML reported success while writing to a failing destination")
	}
}
