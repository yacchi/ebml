package main

import (
	"flag"
	"fmt"
	"io"
	"strings"

	"github.com/yacchi/ebml/impl/go/matroska"
	"github.com/yacchi/ebml/impl/go/parser"
	"github.com/yacchi/ebml/impl/go/stream"
)

type dumpOptions struct {
	maxBinary int
	hex       bool
}

type dumper struct {
	out       io.Writer
	maxBinary int
}

func (d dumper) header(n parser.Node) string {
	typ, known := matroska.TypeFor(n.ID())
	if !known {
		typ = matroska.TypeUnknown
	}
	return fmt.Sprintf("%s%s [%s]",
		strings.Repeat("  ", n.Depth()),
		matroska.Describe(n.ID()),
		dumpDetails(n, typ))
}

func dumpDetails(n parser.Node, typ matroska.ValueType) string {
	details := fmt.Sprintf("offset %d, size %s", n.Offset(), sizeText(n.Size()))
	if typ != matroska.TypeMaster {
		details = fmt.Sprintf("type %s, %s", typ.String(), details)
	}
	return details
}

// leaf prints one leaf element. It is where the cursor's lazy default earns its
// keep: when nothing of the payload would be printed, the leaf is left untouched
// and those bytes are never materialised at all.
func (d dumper) leaf(s *stream.Stream, n *parser.LeafNode) error {
	typ, known := matroska.TypeFor(n.ID())
	if d.maxBinary <= 0 && (!known || typ == matroska.TypeBinary || typ == matroska.TypeBlock) {
		fmt.Fprintf(d.out, "%s = binary %s bytes\n", d.header(n), sizeText(n.Size()))
		return nil
	}
	payload, err := s.Payload(n)
	if err != nil {
		return err
	}
	d.value(n, typ, known, payload)
	return nil
}

func (d dumper) value(n *parser.LeafNode, typ matroska.ValueType, known bool, payload []byte) {
	line := d.header(n)
	switch {
	case typ == matroska.TypeBlock:
		if s, ok := blockSummary(payload); ok {
			fmt.Fprintf(d.out, "%s = %s\n", line, s)
			return
		}
		fmt.Fprintf(d.out, "%s = %s\n", line, d.binaryValue(payload))
	case typ == matroska.TypeBinary || !known:
		fmt.Fprintf(d.out, "%s = %s\n", line, d.binaryValue(payload))
	default:
		if s, ok := scalarValue(n.ID(), payload); ok {
			if typ == matroska.TypeString || typ == matroska.TypeUTF8 {
				fmt.Fprintf(d.out, "%s = %q\n", line, s)
			} else {
				fmt.Fprintf(d.out, "%s = %s\n", line, s)
			}
			return
		}
		fmt.Fprintf(d.out, "%s = %s\n", line, d.binaryValue(payload))
	}
}

func (d dumper) binaryValue(payload []byte) string {
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
	s := stream.New(src, matroska.KindForElementID, parser.WithBoundary(matroska.StreamBoundary))
	d := dumper{out: out, maxBinary: opt.maxBinary}
	for node, err := range s.Nodes() {
		if err != nil {
			return fmt.Errorf("at offset %d: %w", s.Offset(), err)
		}
		switch n := node.(type) {
		case *parser.MasterNode:
			// Descending is the default, and the indentation shows the nesting.
			fmt.Fprintln(out, d.header(n))
		case *parser.LeafNode:
			if err := d.leaf(s, n); err != nil {
				return fmt.Errorf("at offset %d: %w", s.Offset(), err)
			}
		case *parser.EndNode:
			// A master's extent is settled; the dump states structure by depth.
		}
	}
	return nil
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
