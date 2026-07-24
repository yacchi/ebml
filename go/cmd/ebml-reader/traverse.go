package main

import (
	"fmt"
	"io"

	"github.com/yacchi/ebml-reader/matroska"
	"github.com/yacchi/ebml-reader/parser"
)

// element describes one EBML element encountered during a traversal.
type element struct {
	id     parser.ElementID
	name   string             // registry name, or "" for an unknown ID
	typ    matroska.ValueType // registry value type (TypeBinary for unknown IDs)
	known  bool               // true if the ID is in the Matroska registry
	offset int64              // byte offset of the element header
	size   int64              // declared payload size, or -1 for unknown-size
	depth  int                // nesting depth (0 = top level)
}

// nodeVisitor observes a streaming EBML traversal. startMaster is called when a
// master element opens, leaf when a leaf element's payload has been read, and
// endMaster when a master closes (whether by reaching its known size, a KVS
// fragment boundary, or EOF). start/end calls are always balanced.
type nodeVisitor interface {
	startMaster(e element)
	leaf(e element, payload []byte)
	endMaster()
}

// frame mirrors the parser's master stack so the walker knows whether the
// current top master is unknown-size (only those get boundary-closed).
type frame struct {
	unknown bool
}

func describe(id parser.ElementID, offset, size int64, depth int) element {
	info, ok := matroska.Lookup(id)
	e := element{id: id, offset: offset, size: size, depth: depth, known: ok, typ: matroska.TypeBinary}
	if ok {
		e.name = info.Name
		e.typ = info.Type
	}
	return e
}

// walk drives the streaming cursor over r, dispatching to v. It handles multiple
// concatenated top-level unknown-size Segments (KVS GetMedia dumps): an
// unknown-size master is closed the moment a new top-level EBML header or Segment
// is peeked — a structural boundary, never a byte-scan for the EBML magic.
//
// On malformed input it returns the parser's typed error annotated with the byte
// offset at which parsing failed.
func walk(r io.Reader, v nodeVisitor) error {
	p := parser.New(parser.WithKindClassifier(matroska.KindForElementID))
	var mstack []frame
	var pending *element // a leaf header was consumed; payload not yet read
	buf := make([]byte, 64*1024)

	fail := func(err error) error {
		return fmt.Errorf("at offset %d: %w", p.Offset(), err)
	}

	drain := func() error {
		for {
			if pending != nil {
				payload, err := p.ReadPayload()
				if err != nil {
					if _, ok := err.(parser.NeedMoreData); ok {
						return nil
					}
					return fail(err)
				}
				v.leaf(*pending, payload)
				pending = nil
				continue
			}

			h, err := p.Peek()
			if err != nil {
				if _, ok := err.(parser.NeedMoreData); ok {
					return nil
				}
				return fail(err)
			}

			if h.Kind == parser.KindEndMaster {
				v.endMaster()
				if err := p.LeaveMaster(); err != nil {
					return fail(err)
				}
				mstack = mstack[:len(mstack)-1]
				continue
			}

			// KVS fragment boundary: a new top-level element ends the open
			// unknown-size Segment.
			if len(mstack) > 0 && mstack[len(mstack)-1].unknown &&
				(h.ID == matroska.IDEBML || h.ID == matroska.IDSegment) {
				v.endMaster()
				if err := p.CloseMaster(); err != nil {
					return fail(err)
				}
				mstack = mstack[:len(mstack)-1]
				continue
			}

			e := describe(h.ID, p.Offset(), h.Size, p.Depth())
			if _, err := p.ConsumeHeader(); err != nil {
				return fail(err)
			}
			if h.Kind == parser.KindMaster {
				v.startMaster(e)
				if err := p.EnterMaster(); err != nil {
					return fail(err)
				}
				mstack = append(mstack, frame{unknown: h.Size < 0})
			} else {
				pending = &e
			}
		}
	}

	for {
		n, err := r.Read(buf)
		if n > 0 {
			p.Feed(buf[:n])
			if e := drain(); e != nil {
				return e
			}
		}
		if err == io.EOF {
			break
		}
		if err != nil {
			return fmt.Errorf("read input: %w", err)
		}
	}
	if e := drain(); e != nil {
		return e
	}

	closed, err := p.FinalizeEOF()
	if err != nil {
		return fail(err)
	}
	for range closed {
		v.endMaster()
	}
	return nil
}
