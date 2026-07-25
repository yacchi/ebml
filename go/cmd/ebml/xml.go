package main

import (
	"encoding/xml"
	"errors"
	"flag"
	"fmt"
	"io"

	"github.com/yacchi/ebml/matroska"
	"github.com/yacchi/ebml/parser"
)

type xmlOptions struct {
	maxBinary int
	hex       bool
}

// xmlEmitter emits a well-formed XML document via encoding/xml, so element names
// and text content are escaped correctly and the output round-trips through
// encoding/xml.
type xmlEmitter struct {
	enc       *xml.Encoder
	maxBinary int
	stack     []string // open master tag names, for balanced EndElement tokens
	err       error
}

func (x *xmlEmitter) tagName(n parser.Node) string {
	if name := matroska.NameForID(n.ID()); name != "" {
		return name
	}
	return "Unknown"
}

func (x *xmlEmitter) baseAttrs(n parser.Node) []xml.Attr {
	attrs := []xml.Attr{
		{Name: xml.Name{Local: "id"}, Value: parser.FormatID(n.ID())},
		{Name: xml.Name{Local: "offset"}, Value: fmt.Sprintf("%d", n.Offset())},
		{Name: xml.Name{Local: "size"}, Value: sizeText(n.Size())},
	}
	if matroska.NameForID(n.ID()) == "" {
		attrs = append(attrs, xml.Attr{Name: xml.Name{Local: "unknown"}, Value: "true"})
	}
	return attrs
}

func (x *xmlEmitter) token(t xml.Token) {
	if x.err != nil {
		return
	}
	x.err = x.enc.EncodeToken(t)
}

func (x *xmlEmitter) master(n *parser.MasterNode) {
	name := x.tagName(n)
	x.stack = append(x.stack, name)
	x.token(xml.StartElement{Name: xml.Name{Local: name}, Attr: x.baseAttrs(n)})
}

// closeTop emits the end tag of the innermost open element. It is called both on a
// master's end and, at the very end, for whatever is still open after a failure, so
// the document is well-formed either way.
func (x *xmlEmitter) closeTop() {
	if len(x.stack) == 0 {
		return
	}
	name := x.stack[len(x.stack)-1]
	x.stack = x.stack[:len(x.stack)-1]
	x.token(xml.EndElement{Name: xml.Name{Local: name}})
}

// leaf emits one leaf element. When no payload byte would be emitted the leaf keeps
// the cursor's skipping default, so those bytes are never materialised.
func (x *xmlEmitter) leaf(s *stream, n *parser.LeafNode) error {
	typ, known := matroska.TypeFor(n.ID())
	if x.maxBinary <= 0 && (!known || typ == matroska.TypeBinary || typ == matroska.TypeBlock) {
		attrs := x.baseAttrs(n)
		attrs = append(attrs,
			xml.Attr{Name: xml.Name{Local: "encoding"}, Value: "hex"},
			xml.Attr{Name: xml.Name{Local: "bytes"}, Value: sizeText(n.Size())})
		x.emitLeaf(n, attrs, "")
		return x.err
	}
	payload, err := s.Payload(n)
	if err != nil {
		return err
	}
	x.value(n, typ, known, payload)
	return x.err
}

func (x *xmlEmitter) value(n *parser.LeafNode, typ matroska.ValueType, known bool, payload []byte) {
	attrs := x.baseAttrs(n)
	var text string

	switch {
	case typ == matroska.TypeBlock:
		if s, ok := blockSummary(payload); ok {
			attrs = append(attrs, xml.Attr{Name: xml.Name{Local: "block"}, Value: s})
			x.emitLeaf(n, attrs, "")
			return
		}
		x.emitBinary(n, attrs, payload)
		return
	case typ == matroska.TypeBinary || !known:
		x.emitBinary(n, attrs, payload)
		return
	default:
		if s, ok := scalarValue(n.ID(), payload); ok {
			text = s
		} else {
			x.emitBinary(n, attrs, payload)
			return
		}
	}
	x.emitLeaf(n, attrs, text)
}

func (x *xmlEmitter) emitBinary(n parser.Node, attrs []xml.Attr, payload []byte) {
	attrs = append(attrs, xml.Attr{Name: xml.Name{Local: "encoding"}, Value: "hex"})
	attrs = append(attrs, xml.Attr{Name: xml.Name{Local: "bytes"}, Value: fmt.Sprintf("%d", len(payload))})
	text, truncated := hexBytes(payload, x.maxBinary)
	if truncated {
		attrs = append(attrs, xml.Attr{Name: xml.Name{Local: "truncated"}, Value: "true"})
	}
	x.emitLeaf(n, attrs, text)
}

func (x *xmlEmitter) emitLeaf(n parser.Node, attrs []xml.Attr, text string) {
	name := x.tagName(n)
	x.token(xml.StartElement{Name: xml.Name{Local: name}, Attr: attrs})
	if text != "" {
		x.token(xml.CharData(text))
	}
	x.token(xml.EndElement{Name: xml.Name{Local: name}})
}

func runXML(in io.Reader, out io.Writer, opt xmlOptions) error {
	src, err := sourceReader(in, opt.hex)
	if err != nil {
		return err
	}
	enc := xml.NewEncoder(out)
	enc.Indent("", "  ")
	x := &xmlEmitter{enc: enc, maxBinary: opt.maxBinary}

	root := xml.StartElement{Name: xml.Name{Local: "EBMLStream"}}
	if err := enc.EncodeToken(root); err != nil {
		return err
	}

	walkErr := x.walk(newStream(src))

	// Always close every open element (including the root) so stdout is
	// well-formed XML, even when the walk or the encoder failed mid-stream.
	for len(x.stack) > 0 {
		x.closeTop()
	}
	x.token(xml.EndElement{Name: root.Name})

	flushErr := enc.Flush()
	if walkErr != nil {
		return walkErr
	}
	if x.err != nil {
		return x.err
	}
	if flushErr != nil {
		return flushErr
	}
	_, err = io.WriteString(out, "\n")
	return err
}

// walk pulls every element of s and emits it.
func (x *xmlEmitter) walk(s *stream) error {
	for {
		node, err := s.Next()
		if err != nil {
			if errors.Is(err, io.EOF) {
				return nil
			}
			return fmt.Errorf("at offset %d: %w", s.Offset(), err)
		}
		switch n := node.(type) {
		case *parser.MasterNode:
			x.master(n)
		case *parser.LeafNode:
			if err := x.leaf(s, n); err != nil {
				return fmt.Errorf("at offset %d: %w", s.Offset(), err)
			}
		case *parser.EndNode:
			x.closeTop()
		}
		if x.err != nil {
			return x.err
		}
	}
}

func xmlCommand(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("xml", flag.ContinueOnError)
	fs.SetOutput(stderr)
	maxBinary := fs.Int("max-binary", 16, "max bytes of a binary leaf to emit as hex text (0 = none)")
	asHex := fs.Bool("hex", false, "read the commented-hex fixture format (# comments + hex bytes) instead of raw EBML")
	fs.Usage = func() {
		fmt.Fprintln(stderr, "Usage: ebml xml [flags] [FILE]")
		fmt.Fprintln(stderr, "  Emit the EBML tree as well-formed XML. FILE absent or \"-\" reads stdin.")
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
	if err := runXML(in, stdout, xmlOptions{maxBinary: *maxBinary, hex: *asHex}); err != nil {
		fmt.Fprintf(stderr, "ebml: %v\n", err)
		return 1
	}
	return 0
}
