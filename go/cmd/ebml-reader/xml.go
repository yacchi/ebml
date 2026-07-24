package main

import (
	"encoding/xml"
	"flag"
	"fmt"
	"io"

	"github.com/yacchi/ebml-reader/matroska"
	"github.com/yacchi/ebml-reader/parser"
)

type xmlOptions struct {
	maxBinary int
	hex       bool
}

// xmlVisitor emits a well-formed XML document via encoding/xml, so element names
// and text content are escaped correctly and the output round-trips through
// encoding/xml.
type xmlVisitor struct {
	enc       *xml.Encoder
	maxBinary int
	stack     []string // open master tag names, for balanced EndElement tokens
	err       error
}

func (x *xmlVisitor) tagName(e element) string {
	if e.name != "" {
		return e.name
	}
	return "Unknown"
}

func (x *xmlVisitor) baseAttrs(e element) []xml.Attr {
	attrs := []xml.Attr{
		{Name: xml.Name{Local: "id"}, Value: parser.FormatID(e.id)},
		{Name: xml.Name{Local: "offset"}, Value: fmt.Sprintf("%d", e.offset)},
		{Name: xml.Name{Local: "size"}, Value: sizeText(e.size)},
	}
	if e.name == "" {
		attrs = append(attrs, xml.Attr{Name: xml.Name{Local: "unknown"}, Value: "true"})
	}
	return attrs
}

func (x *xmlVisitor) token(t xml.Token) {
	if x.err != nil {
		return
	}
	x.err = x.enc.EncodeToken(t)
}

func (x *xmlVisitor) startMaster(e element) {
	name := x.tagName(e)
	x.stack = append(x.stack, name)
	x.token(xml.StartElement{Name: xml.Name{Local: name}, Attr: x.baseAttrs(e)})
}

func (x *xmlVisitor) endMaster() {
	if len(x.stack) == 0 {
		return
	}
	name := x.stack[len(x.stack)-1]
	x.stack = x.stack[:len(x.stack)-1]
	x.token(xml.EndElement{Name: xml.Name{Local: name}})
}

func (x *xmlVisitor) leaf(e element, payload []byte) {
	attrs := x.baseAttrs(e)
	var text string

	switch {
	case e.typ == matroska.TypeBlock:
		if s, ok := blockSummary(payload); ok {
			attrs = append(attrs, xml.Attr{Name: xml.Name{Local: "block"}, Value: s})
			x.emitLeaf(e, attrs, "")
			return
		}
		x.emitBinary(e, attrs, payload)
		return
	case e.typ == matroska.TypeBinary || !e.known:
		x.emitBinary(e, attrs, payload)
		return
	default:
		if s, ok := scalarValue(e, payload); ok {
			text = s
		} else {
			x.emitBinary(e, attrs, payload)
			return
		}
	}
	x.emitLeaf(e, attrs, text)
}

func (x *xmlVisitor) emitBinary(e element, attrs []xml.Attr, payload []byte) {
	attrs = append(attrs, xml.Attr{Name: xml.Name{Local: "encoding"}, Value: "hex"})
	attrs = append(attrs, xml.Attr{Name: xml.Name{Local: "bytes"}, Value: fmt.Sprintf("%d", len(payload))})
	text, truncated := hexBytes(payload, x.maxBinary)
	if truncated {
		attrs = append(attrs, xml.Attr{Name: xml.Name{Local: "truncated"}, Value: "true"})
	}
	x.emitLeaf(e, attrs, text)
}

func (x *xmlVisitor) emitLeaf(e element, attrs []xml.Attr, text string) {
	name := x.tagName(e)
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
	v := &xmlVisitor{enc: enc, maxBinary: opt.maxBinary}

	root := xml.StartElement{Name: xml.Name{Local: "EBMLStream"}}
	if err := enc.EncodeToken(root); err != nil {
		return err
	}

	walkErr := walk(src, v)

	// Always close every open element (including the root) so stdout is
	// well-formed XML, even when walk or the encoder failed mid-stream.
	for len(v.stack) > 0 {
		v.endMaster()
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
		fmt.Fprintln(stderr, "Usage: ebml-reader xml [flags] [FILE]")
		fmt.Fprintln(stderr, "  Emit the EBML tree as well-formed XML. FILE absent or \"-\" reads stdin.")
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
	if err := runXML(in, stdout, xmlOptions{maxBinary: *maxBinary, hex: *asHex}); err != nil {
		fmt.Fprintf(stderr, "ebml-reader: %v\n", err)
		return 1
	}
	return 0
}
