// Package scope retains the directly completed children of one master element.
//
// It is deliberately element-agnostic. A scope contains only nodes the caller
// observed, and nested access is left to ext/tree.
package scope

import (
	"bytes"

	"github.com/yacchi/ebml/ext/tree"
	"github.com/yacchi/ebml/parser"
)

// PayloadReader resolves a leaf's payload. *stream.Stream satisfies it; a
// consumer that pushes bytes into a cursor itself supplies its own.
type PayloadReader interface {
	Payload(*parser.LeafNode) ([]byte, error)
}

// Tracker follows one master ID through a stream. It wraps no cursor and drives
// no loop: the caller feeds it nodes from the loop it already has, and what is
// not observed is not in the scope.
type Tracker struct {
	master  parser.ElementID
	src     PayloadReader
	current *Scope
	stack   []openElement
}

type openElement struct {
	element *tree.Element
	depth   int
}

// NewTracker returns a tracker for master and a payload source.
func NewTracker(master parser.ElementID, src PayloadReader) *Tracker {
	return &Tracker{master: master, src: src}
}

// Observe records the node and, for a leaf, materialises and copies its
// payload. It returns a scope closed by this node, if any. Payload resolution
// occurs before tracker state is changed, so an error records nothing.
func (t *Tracker) Observe(n parser.Node) (done *Scope, err error) {
	if t == nil || n == nil {
		return nil, nil
	}

	var payload []byte
	leaf, isLeaf := n.(*parser.LeafNode)
	if isLeaf {
		payload, err = t.src.Payload(leaf)
		if err != nil {
			return nil, err
		}
		payload = bytes.Clone(payload)
	}

	if end, ok := n.(*parser.EndNode); ok {
		done = t.unwind(end.Depth()+1, end.End())
		if len(t.stack) > 0 && t.stack[len(t.stack)-1].depth == end.Depth() {
			if closed := t.closeTop(end.End()); closed != nil {
				done = closed
			}
		}
		if t.current != nil {
			t.current.rev++
		}
		return done, nil
	}
	done = t.unwind(n.Depth(), n.Offset())
	switch node := n.(type) {
	case *parser.MasterNode:
		if t.current == nil {
			if node.ID() != t.master {
				return done, nil
			}
			t.current = &Scope{
				master: node.ID(),
				start:  node.Offset(),
				end:    -1,
				rev:    1,
			}
			el := element(node)
			t.stack = append(t.stack, openElement{element: el, depth: node.Depth()})
			return done, nil
		}
		el := element(node)
		t.stack = append(t.stack, openElement{element: el, depth: node.Depth()})
		t.current.rev++
	case *parser.LeafNode:
		if t.current != nil && len(t.stack) > 0 {
			el := element(node)
			el.Payload = payload
			t.stack[len(t.stack)-1].element.AppendChild(el)
			if len(t.stack) == 1 {
				t.current.add(el)
			}
			t.current.rev++
		}
	}
	return done, nil
}

// Finish closes an open scope at end of input and returns it. A scope is
// returned only once.
func (t *Tracker) Finish() *Scope {
	if t == nil || t.current == nil {
		return nil
	}
	var done *Scope
	for len(t.stack) > 0 {
		if closed := t.closeTop(-1); closed != nil {
			done = closed
		}
	}
	if done == nil {
		return nil
	}
	done.end = -1
	return done
}

// Current returns the open scope, or nil when none is open.
func (t *Tracker) Current() *Scope {
	if t == nil {
		return nil
	}
	return t.current
}

// Scope is a query-only view; Tracker owns its lifecycle.
type Scope struct {
	master parser.ElementID
	start  int64
	end    int64
	rev    uint64
	ids    []parser.ElementID
	all    map[parser.ElementID][]*tree.Element
}

func (s *Scope) add(el *tree.Element) {
	if s.all == nil {
		s.all = make(map[parser.ElementID][]*tree.Element)
	}
	if _, ok := s.all[el.ID]; !ok {
		s.ids = append(s.ids, el.ID)
	}
	s.all[el.ID] = append(s.all[el.ID], el)
}

// Get returns the MOST RECENTLY completed direct child with id, or nil. Later
// wins, matching ext/tags: within one scope the elements a schema allows to
// repeat are cumulative and their order carries no meaning, so the later
// statement is the more recent one. It is the accessor for an element a schema
// declares at most once -- Info, Tracks -- while a repeating element wants
// GetAll.
//
// The returned element is nil-safe, so the result is usable unchecked; Exists
// tests it.
func (s *Scope) Get(id parser.ElementID) *tree.Element {
	if s == nil {
		return nil
	}
	group := s.all[id]
	if len(group) == 0 {
		return nil
	}
	return group[len(group)-1]
}

// GetAll returns directly completed children with id in stream order. It never
// searches descendants; use tree.Element.Descendants on a returned child for
// that operation.
func (s *Scope) GetAll(id parser.ElementID) []*tree.Element {
	if s == nil {
		return nil
	}
	return s.all[id]
}

// IDs returns the IDs of directly completed children in first-seen order.
func (s *Scope) IDs() []parser.ElementID {
	if s == nil {
		return nil
	}
	return append([]parser.ElementID(nil), s.ids...)
}

// Rev returns the revision of the scope.
func (s *Scope) Rev() uint64 {
	if s == nil {
		return 0
	}
	return s.rev
}

// Master returns the scoped master's ID.
func (s *Scope) Master() parser.ElementID {
	if s == nil {
		return 0
	}
	return s.master
}

// Start returns the scoped master's header offset.
func (s *Scope) Start() int64 {
	if s == nil {
		return 0
	}
	return s.start
}

// End returns the closing offset, or -1 while the scope is open.
func (s *Scope) End() int64 {
	if s == nil {
		return -1
	}
	return s.end
}

func (t *Tracker) unwind(depth int, end int64) *Scope {
	var done *Scope
	for len(t.stack) > 0 && t.stack[len(t.stack)-1].depth >= depth {
		if closed := t.closeTop(end); closed != nil {
			done = closed
		}
	}
	return done
}

func (t *Tracker) closeTop(end int64) *Scope {
	top := t.stack[len(t.stack)-1]
	t.stack = t.stack[:len(t.stack)-1]
	if len(t.stack) > 0 {
		parent := t.stack[len(t.stack)-1].element
		parent.AppendChild(top.element)
		if t.current != nil && len(t.stack) == 1 {
			t.current.add(top.element)
		}
		return nil
	}
	if t.current == nil {
		return nil
	}
	if end >= 0 {
		t.current.end = end
	}
	done := t.current
	t.current = nil
	return done
}

func element(n parser.Node) *tree.Element {
	return &tree.Element{
		ID:        n.ID(),
		Offset:    n.Offset(),
		HeaderLen: n.HeaderLen(),
		Size:      n.Size(),
	}
}
