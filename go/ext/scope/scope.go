// Package scope retains the directly completed children of one master element.
//
// It is deliberately element-agnostic. A scope contains only nodes the caller
// observed, and nested access is left to tree.
package scope

import (
	"bytes"

	"github.com/yacchi/ebml/parser"
	"github.com/yacchi/ebml/tree"
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
			}
			el := element(node)
			t.stack = append(t.stack, openElement{element: el, depth: node.Depth()})
			return done, nil
		}
		el := element(node)
		t.stack = append(t.stack, openElement{element: el, depth: node.Depth()})
	case *parser.LeafNode:
		if t.current != nil && len(t.stack) > 0 {
			el := element(node)
			el.Payload = payload
			t.stack[len(t.stack)-1].element.AppendChild(el)
			if len(t.stack) == 1 {
				t.current.add(el)
			}
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
	// children holds every directly completed child in stream order. One ordered
	// slice is the whole index: Get, GetAll and IDs are all derived from it by a
	// linear scan. A scope holds a master's direct children -- a handful of
	// elements -- so a map would buy nothing and would be a second piece of state
	// to keep in step with this one.
	children []*tree.Element
}

func (s *Scope) add(el *tree.Element) {
	s.children = append(s.children, el)
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
	for i := len(s.children) - 1; i >= 0; i-- {
		if s.children[i].ID == id {
			return s.children[i]
		}
	}
	return nil
}

// GetAll returns directly completed children with id in stream order, as a slice
// the caller owns: the scope never hands out the state it is still accumulating
// into. It never searches descendants; use tree.Element.Descendants on a returned
// child for that.
func (s *Scope) GetAll(id parser.ElementID) []*tree.Element {
	if s == nil {
		return nil
	}
	var out []*tree.Element
	for _, el := range s.children {
		if el.ID == id {
			out = append(out, el)
		}
	}
	return out
}

// IDs returns the IDs of directly completed children in first-seen order.
func (s *Scope) IDs() []parser.ElementID {
	if s == nil {
		return nil
	}
	var out []parser.ElementID
	seen := make(map[parser.ElementID]struct{}, len(s.children))
	for _, el := range s.children {
		if _, ok := seen[el.ID]; ok {
			continue
		}
		seen[el.ID] = struct{}{}
		out = append(out, el.ID)
	}
	return out
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
