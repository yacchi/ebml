// Command ebml is a small CLI over the streaming EBML/Matroska cursor.
//
// Subcommands:
//
//	dump [flags] [FILE]   print a human-readable indented element tree
//	xml  [flags] [FILE]   emit the element tree as well-formed XML
//
// dump and xml read RAW EBML bytes from FILE ("-" or absent means stdin) and feed
// them through the cursor incrementally, so a continuous KVS GetMedia stream of
// concatenated unknown-size Segments is handled without buffering the whole
// input. The commented-hex fixture format under fixtures/ is a test source, not a
// runtime input; pass --hex to decode it.
package main

import (
	"bytes"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"strings"
)

func usage(w io.Writer) {
	fmt.Fprintln(w, "Usage: ebml <command> [flags] [FILE]")
	fmt.Fprintln(w, "")
	fmt.Fprintln(w, "Commands:")
	fmt.Fprintln(w, "  dump     print a human-readable indented EBML element tree")
	fmt.Fprintln(w, "  xml      emit the EBML element tree as well-formed XML")
	fmt.Fprintln(w, "")
	fmt.Fprintln(w, "Run 'ebml <command> -h' for command-specific flags.")
}

func main() {
	os.Exit(run(os.Args[1:], os.Stdin, os.Stdout, os.Stderr))
}

func run(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		usage(stderr)
		return 2
	}
	switch args[0] {
	case "dump":
		return dumpCommand(args[1:], stdin, stdout, stderr)
	case "xml":
		return xmlCommand(args[1:], stdin, stdout, stderr)
	case "-h", "--help", "help":
		usage(stdout)
		return 0
	default:
		fmt.Fprintf(stderr, "ebml: unknown command %q\n\n", args[0])
		usage(stderr)
		return 2
	}
}

// openInput resolves the positional FILE argument. An absent argument or "-"
// selects stdin. The returned close func is always safe to call.
func openInput(args []string, stdin io.Reader) (io.Reader, func(), error) {
	if len(args) == 0 || args[0] == "-" {
		return stdin, func() {}, nil
	}
	if len(args) > 1 {
		return nil, func() {}, fmt.Errorf("expected at most one FILE argument, got %d", len(args))
	}
	f, err := os.Open(args[0])
	if err != nil {
		return nil, func() {}, err
	}
	return f, func() { f.Close() }, nil
}

// sourceReader returns r unchanged for raw EBML input. When asHex is set it
// reads all of r, strips the commented-hex fixture format (lines beginning with
// "#" and all whitespace) and returns a reader over the decoded bytes.
func sourceReader(r io.Reader, asHex bool) (io.Reader, error) {
	if !asHex {
		return r, nil
	}
	raw, err := io.ReadAll(r)
	if err != nil {
		return nil, err
	}
	var sb strings.Builder
	for _, ln := range strings.Split(string(raw), "\n") {
		ln = strings.TrimSpace(ln)
		if ln == "" || strings.HasPrefix(ln, "#") {
			continue
		}
		for _, tok := range strings.Fields(ln) {
			sb.WriteString(tok)
		}
	}
	decoded, err := hex.DecodeString(sb.String())
	if err != nil {
		return nil, fmt.Errorf("decode hex input: %w", err)
	}
	return bytes.NewReader(decoded), nil
}
