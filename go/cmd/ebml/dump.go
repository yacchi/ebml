package main

import (
	"flag"
	"fmt"
	"io"
	"strings"

	"github.com/yacchi/ebml/matroska"
	"github.com/yacchi/ebml/parser"
)

type dumpOptions struct {
	maxBinary int
	hex       bool
}

type dumpHandler struct {
	out       io.Writer
	maxBinary int
}

func (d dumpHandler) header(n parser.Node) string {
	typ, known := matroska.TypeFor(n.ID)
	if !known {
		typ = matroska.TypeUnknown
	}
	return fmt.Sprintf("%s%s [%s]",
		strings.Repeat("  ", n.Depth),
		matroska.Describe(n.ID),
		dumpDetails(n, typ))
}

func dumpDetails(n parser.Node, typ matroska.ValueType) string {
	details := fmt.Sprintf("offset %d, size %s", n.Offset, sizeText(n.Size))
	if typ != matroska.TypeMaster {
		details = fmt.Sprintf("type %s, %s", typ.String(), details)
	}
	return details
}

func (d dumpHandler) Master(n parser.Node) (parser.Action, error) {
	fmt.Fprintln(d.out, d.header(n))
	return parser.Descend, nil
}

func (d dumpHandler) Leaf(n parser.Node) (parser.Action, error) {
	typ, known := matroska.TypeFor(n.ID)
	if d.maxBinary <= 0 && (!known || typ == matroska.TypeBinary || typ == matroska.TypeBlock) {
		fmt.Fprintf(d.out, "%s = binary %s bytes\n", d.header(n), sizeText(n.Size))
		return parser.SkipPayload, nil
	}
	return parser.ReadPayload, nil
}

func (d dumpHandler) Payload(n parser.Node, payload []byte) error {
	typ, known := matroska.TypeFor(n.ID)
	line := d.header(n)
	switch {
	case typ == matroska.TypeBlock:
		if s, ok := blockSummary(payload); ok {
			fmt.Fprintf(d.out, "%s = %s\n", line, s)
			return nil
		}
		fmt.Fprintf(d.out, "%s = %s\n", line, d.binaryValue(payload))
	case typ == matroska.TypeBinary || !known:
		fmt.Fprintf(d.out, "%s = %s\n", line, d.binaryValue(payload))
	default:
		if s, ok := scalarValue(n.ID, payload); ok {
			if typ == matroska.TypeString || typ == matroska.TypeUTF8 {
				fmt.Fprintf(d.out, "%s = %q\n", line, s)
			} else {
				fmt.Fprintf(d.out, "%s = %s\n", line, s)
			}
			return nil
		}
		fmt.Fprintf(d.out, "%s = %s\n", line, d.binaryValue(payload))
	}
	return nil
}

func (d dumpHandler) Close(parser.Node) error { return nil }

func (d dumpHandler) Boundary(open, next parser.Node) bool {
	return next.ID == matroska.IDEBML || next.ID == matroska.IDSegment
}

func (d dumpHandler) binaryValue(payload []byte) string {
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
	v := dumpHandler{out: out, maxBinary: opt.maxBinary}
	return scan(src, v)
}

func dumpCommand(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("dump", flag.ContinueOnError)
	fs.SetOutput(stderr)
	maxBinary := fs.Int("max-binary", 16, "max bytes of a binary leaf to show as hex (0 = size only)")
	asHex := fs.Bool("hex", false, "read the commented-hex fixture format (# comments + hex bytes) instead of raw EBML")
	fs.Usage = func() {
		fmt.Fprintln(stderr, "Usage: ebml dump [flags] [FILE]")
		fmt.Fprintln(stderr, "  Print an indented EBML element tree. FILE absent or \"-\" reads stdin.")
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		return 2
	}
	in, closeFn, err := openInput(fs.Args(), stdin)
	if err != nil {
		fmt.Fprintf(stderr, "ebml: %v\n", err)
		return 1
	}
	defer closeFn()
	if err := runDump(in, stdout, dumpOptions{maxBinary: *maxBinary, hex: *asHex}); err != nil {
		fmt.Fprintf(stderr, "ebml: %v\n", err)
		return 1
	}
	return 0
}
