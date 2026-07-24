package main

import (
	"flag"
	"fmt"
	"io"
	"strings"

	"github.com/yacchi/ebml-reader/matroska"
)

type dumpOptions struct {
	maxBinary int
	hex       bool
}

// dumpVisitor prints a human-readable indented element tree.
type dumpVisitor struct {
	out       io.Writer
	maxBinary int
}

func (d *dumpVisitor) header(e element) string {
	details := fmt.Sprintf("offset %d, size %s", e.offset, sizeText(e.size))
	if e.typ != matroska.TypeMaster {
		details = fmt.Sprintf("type %s, %s", e.typ.String(), details)
	}
	return fmt.Sprintf("%s%s [%s]",
		strings.Repeat("  ", e.depth), matroska.Describe(e.id), details)
}

func (d *dumpVisitor) startMaster(e element) {
	fmt.Fprintln(d.out, d.header(e))
}

func (d *dumpVisitor) endMaster() {}

func (d *dumpVisitor) leaf(e element, payload []byte) {
	line := d.header(e)
	switch {
	case e.typ == matroska.TypeBlock:
		if s, ok := blockSummary(payload); ok {
			fmt.Fprintf(d.out, "%s = %s\n", line, s)
			return
		}
		fmt.Fprintf(d.out, "%s = %s\n", line, d.binaryValue(payload))
	case e.typ == matroska.TypeBinary || !e.known:
		fmt.Fprintf(d.out, "%s = %s\n", line, d.binaryValue(payload))
	default:
		if s, ok := scalarValue(e, payload); ok {
			if e.typ == matroska.TypeString || e.typ == matroska.TypeUTF8 {
				fmt.Fprintf(d.out, "%s = %q\n", line, s)
			} else {
				fmt.Fprintf(d.out, "%s = %s\n", line, s)
			}
			return
		}
		fmt.Fprintf(d.out, "%s = %s\n", line, d.binaryValue(payload))
	}
}

func (d *dumpVisitor) binaryValue(payload []byte) string {
	text, truncated := hexBytes(payload, d.maxBinary)
	if text == "" {
		return fmt.Sprintf("binary %d bytes", len(payload))
	}
	if truncated {
		return fmt.Sprintf("binary %d bytes: %s...", len(payload), text)
	}
	return fmt.Sprintf("binary %d bytes: %s", len(payload), text)
}

func runDump(in io.Reader, out io.Writer, opt dumpOptions) error {
	src, err := sourceReader(in, opt.hex)
	if err != nil {
		return err
	}
	v := &dumpVisitor{out: out, maxBinary: opt.maxBinary}
	return walk(src, v)
}

func dumpCommand(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("dump", flag.ContinueOnError)
	fs.SetOutput(stderr)
	maxBinary := fs.Int("max-binary", 16, "max bytes of a binary leaf to show as hex (0 = size only)")
	asHex := fs.Bool("hex", false, "read the commented-hex fixture format (# comments + hex bytes) instead of raw EBML")
	fs.Usage = func() {
		fmt.Fprintln(stderr, "Usage: ebml-reader dump [flags] [FILE]")
		fmt.Fprintln(stderr, "  Print an indented EBML element tree. FILE absent or \"-\" reads stdin.")
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		return 2
	}
	in, closeFn, err := openInput(fs.Args(), stdin)
	if err != nil {
		fmt.Fprintf(stderr, "ebml-reader: %v\n", err)
		return 1
	}
	defer closeFn()
	if err := runDump(in, stdout, dumpOptions{maxBinary: *maxBinary, hex: *asHex}); err != nil {
		fmt.Fprintf(stderr, "ebml-reader: %v\n", err)
		return 1
	}
	return 0
}
