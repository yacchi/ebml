package main

import (
	"encoding/xml"
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

// xmlHandler emits a well-formed XML document via encoding/xml, so element names
// and text content are escaped correctly and the output round-trips through
// encoding/xml.
type xmlHandler struct {
	enc       *xml.Encoder
	maxBinary int
	stack     []string // open master tag names, for balanced EndElement tokens
	err       error
}

func (x *xmlHandler) tagName(n parser.Node) string {
	if name := matroska.NameForID(n.ID); name != "" {
		return name
	}
	return "Unknown"
}

func (x *xmlHandler) baseAttrs(n parser.Node) []xml.Attr {
	attrs := []xml.Attr{
		{Name: xml.Name{Local: "id"}, Value: parser.FormatID(n.ID)},
		{Name: xml.Name{Local: "offset"}, Value: fmt.Sprintf("%d", n.Offset)},
		{Name: xml.Name{Local: "size"}, Value: sizeText(n.Size)},
	}
	if matroska.NameForID(n.ID) == "" {
		attrs = append(attrs, xml.Attr{Name: xml.Name{Local: "unknown"}, Value: "true"})
	}
	return attrs
}

func (x *xmlHandler) token(t xml.Token) {
	if x.err != nil {
		return
	}
	x.err = x.enc.EncodeToken(t)
}

func (x *xmlHandler) Master(n parser.Node) (parser.Action, error) {
	name := x.tagName(n)
	x.stack = append(x.stack, name)
	x.token(xml.StartElement{Name: xml.Name{Local: name}, Attr: x.baseAttrs(n)})
	return parser.Descend, x.err
}

func (x *xmlHandler) Close(parser.Node) error {
	if len(x.stack) == 0 {
		return nil
	}
	name := x.stack[len(x.stack)-1]
	x.stack = x.stack[:len(x.stack)-1]
	x.token(xml.EndElement{Name: xml.Name{Local: name}})
	return x.err
}

func (x *xmlHandler) Leaf(n parser.Node) (parser.Action, error) {
	typ, known := matroska.TypeFor(n.ID)
	if x.maxBinary <= 0 && (!known || typ == matroska.TypeBinary || typ == matroska.TypeBlock) {
		attrs := x.baseAttrs(n)
		attrs = append(attrs,
			xml.Attr{Name: xml.Name{Local: "encoding"}, Value: "hex"},
			xml.Attr{Name: xml.Name{Local: "bytes"}, Value: sizeText(n.Size)})
		x.emitLeaf(n, attrs, "")
		return parser.SkipPayload, nil
	}
	return parser.ReadPayload, nil
}

func (x *xmlHandler) Payload(n parser.Node, payload []byte) error {
	typ, known := matroska.TypeFor(n.ID)
	attrs := x.baseAttrs(n)
	var text string

	switch {
	case typ == matroska.TypeBlock:
		if s, ok := blockSummary(payload); ok {
			attrs = append(attrs, xml.Attr{Name: xml.Name{Local: "block"}, Value: s})
			x.emitLeaf(n, attrs, "")
			return x.err
		}
		x.emitBinary(n, attrs, payload)
		return x.err
	case typ == matroska.TypeBinary || !known:
		x.emitBinary(n, attrs, payload)
		return x.err
	default:
		if s, ok := scalarValue(n.ID, payload); ok {
			text = s
		} else {
			x.emitBinary(n, attrs, payload)
			return x.err
		}
	}
	x.emitLeaf(n, attrs, text)
	return x.err
}

func (x *xmlHandler) Boundary(open, next parser.Node) bool {
	return next.ID == matroska.IDEBML || next.ID == matroska.IDSegment
}

func (x *xmlHandler) emitBinary(n parser.Node, attrs []xml.Attr, payload []byte) {
	attrs = append(attrs, xml.Attr{Name: xml.Name{Local: "encoding"}, Value: "hex"})
	attrs = append(attrs, xml.Attr{Name: xml.Name{Local: "bytes"}, Value: fmt.Sprintf("%d", len(payload))})
	text, truncated := hexBytes(payload, x.maxBinary)
	if truncated {
		attrs = append(attrs, xml.Attr{Name: xml.Name{Local: "truncated"}, Value: "true"})
	}
	x.emitLeaf(n, attrs, text)
}

func (x *xmlHandler) emitLeaf(n parser.Node, attrs []xml.Attr, text string) {
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
	v := &xmlHandler{enc: enc, maxBinary: opt.maxBinary}

	root := xml.StartElement{Name: xml.Name{Local: "EBMLStream"}}
	if err := enc.EncodeToken(root); err != nil {
		return err
	}

	walkErr := scan(src, v)

	// Always close every open element (including the root) so stdout is
	// well-formed XML, even when walk or the encoder failed mid-stream.
	for len(v.stack) > 0 {
		v.Close(parser.Node{})
	}
	v.token(xml.EndElement{Name: root.Name})

	flushErr := enc.Flush()
	if walkErr != nil {
		return walkErr
	}
	if v.err != nil {
		return v.err
	}
	if flushErr != nil {
		return flushErr
	}
	_, err = io.WriteString(out, "\n")
	return err
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
