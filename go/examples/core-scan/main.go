// Command core-scan demonstrates the thin event API without retaining a tree.
package main

import (
	"fmt"
	"io"
	"os"

	"github.com/yacchi/ebml-reader/matroska"
	"github.com/yacchi/ebml-reader/parser"
)

func scan(r io.Reader, w io.Writer) error {
	h := parser.HandlerFuncs{
		MasterFunc: func(parser.Node) (parser.Action, error) { return parser.Descend, nil },
		LeafFunc: func(n parser.Node) (parser.Action, error) {
			if n.ID == matroska.IDDocType || n.ID == matroska.IDTimestamp {
				return parser.ReadPayload, nil
			}
			return parser.SkipPayload, nil
		},
		PayloadFunc: func(n parser.Node, b []byte) error {
			if n.ID == matroska.IDDocType {
				fmt.Fprintf(w, "DocType=%s\n", parser.DecodeString(b))
			} else if n.ID == matroska.IDTimestamp {
				v, err := parser.DecodeUint(b)
				if err != nil {
					return err
				}
				fmt.Fprintf(w, "Timestamp=%d\n", v)
			}
			return nil
		},
	}
	s := parser.NewScanner(h, matroska.KindForElementID)
	buf := make([]byte, 4096)
	for {
		n, err := r.Read(buf)
		if n > 0 {
			if err := s.Feed(buf[:n]); err != nil {
				return err
			}
		}
		if err == io.EOF {
			return s.Finalize()
		}
		if err != nil {
			return err
		}
	}
}

func main() {
	if err := scan(os.Stdin, os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, "core-scan:", err)
		os.Exit(1)
	}
}
