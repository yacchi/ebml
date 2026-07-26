// Package tags reads Matroska Tags elements out of retained tree elements.
package tags

import (
	"github.com/yacchi/ebml/impl/go/matroska"
	"github.com/yacchi/ebml/impl/go/parser"
	"github.com/yacchi/ebml/impl/go/tree"
)

// Target is a decoded Targets element. Its zero value is the whole Segment,
// which is also what an absent or empty Targets means.
type Target struct {
	TypeValue                                       uint64
	TrackUID, EditionUID, ChapterUID, AttachmentUID uint64
}

// Set is a computed view of tags, grouped by their decoded target.
//
// A SimpleTag contributes a value only when it declares a TagString element: an
// ABSENT TagString is not an empty one, so a SimpleTag carrying its value in a
// TagBinary, or one that names a tag without stating it, is not reported as the
// empty string. It would otherwise be indistinguishable from a declared empty
// value, and under the last-wins precedence Get documents it would ERASE a value
// stated earlier in the stream. A TagString that is present and empty is a value
// and is reported as one. Skipping the value never skips the subtree: a nested
// SimpleTag inside a valueless one still counts.
type Set struct {
	values  map[Target]map[string][]string
	targets []Target
}

// Read builds a view over the Tag elements found anywhere under roots.
//
// A root is an element that CONTAINS the tags to read, and is searched to any
// depth. A whole retained Segment is therefore one argument:
//
//	set := tags.Read(segment)
//
// A root is never a Tag itself: a root is searched, not counted, so passing Tag
// elements yields an EMPTY view rather than their contents. Pass what encloses
// them -- the Segment, or the Tags elements. A producer that hands out elements
// by ID has ReadFrom instead, which spares the caller naming the ID at all.
//
// Taking retained elements is the design, not just convenience. Every retention
// path in this library lands in a *tree.Element, so that is the shared currency
// and the only thing a tag reader needs to name. This package therefore names no
// producer of them, and a producer names no tag accessor: an entry point per
// producer gives the plainer name to the narrower case, and a caller holding one
// of the other producers then reads the plainer name as the whole story.
//
// Roots must not overlap. An element passed together with one of its own
// ancestors contributes its tags twice; the last-wins precedence Get documents
// hides that, and Values does not.
//
// The view is computed rather than accumulated, so it is rebuilt, not updated,
// once the tree behind it has grown. A Segment retained from a LIVE stream does
// grow -- RFC 9559 tags are cumulative and positionless, so a writer may state
// more of them at any point before the Segment ends -- which is why this returns
// a value computed on the spot and holds no reference to a producer.
func Read(roots ...*tree.Element) *Set {
	return read(roots)
}

// Source is a producer that hands over the retained elements carrying a given
// ID. It is satisfied by a method an element-agnostic retainer already has FOR
// ITS OWN SAKE, never by one invented for this package -- which is the whole
// point: nothing implements anything for tags, and tags names nothing that
// implements it.
//
// That distinction is what makes this interface admissible where an obvious
// alternative is not. A purpose-built method -- TagRoots, say -- would put
// Matroska tag knowledge into every type that implemented it, so a retainer that
// must stay element-agnostic could not offer one at all.
type Source interface {
	GetAll(id parser.ElementID) []*tree.Element
}

// ReadFrom builds the same view from a producer that indexes elements by ID:
//
//	set := tags.ReadFrom(src)
//
// Naming the Tags ID is this package's job, not the caller's, and that is the
// only reason this function exists. The call it replaces --
// tags.Read(sc.GetAll(matroska.IDTags)...) -- has two silent failure modes and
// no loud one: passing IDTag instead of IDTags yields an empty view, because a
// Tag root is searched rather than counted, and reaching for Get instead of
// GetAll silently keeps the LAST Tags element only, discarding tags a live
// stream wrote before its Cluster. Neither mistake can be made here.
func ReadFrom(src Source) *Set {
	if src == nil {
		return read(nil)
	}
	return read(src.GetAll(matroska.IDTags))
}

func read(roots []*tree.Element) *Set {
	out := &Set{values: make(map[Target]map[string][]string)}
	for _, root := range roots {
		for _, tag := range root.Descendants(matroska.IDTag) {
			target := decodeTarget(tag.Find(matroska.IDTargets))
			for _, simple := range tag.Descendants(matroska.IDSimpleTag) {
				name := simple.Find(matroska.IDTagName).AsString()
				if name == "" {
					continue
				}
				value := simple.Find(matroska.IDTagString)
				if !value.Exists() {
					continue
				}
				if _, ok := out.values[target]; !ok {
					out.values[target] = make(map[string][]string)
					out.targets = append(out.targets, target)
				}
				out.values[target][name] = append(
					out.values[target][name],
					value.AsString(),
				)
			}
		}
	}
	return out
}

func decodeTarget(element *tree.Element) Target {
	var target Target
	if element == nil {
		return target
	}
	target.TypeValue = uintValue(element.Find(matroska.IDTargetTypeValue))
	target.TrackUID = uintValue(element.Find(matroska.IDTagTrackUID))
	target.EditionUID = uintValue(element.Find(matroska.IDTagEditionUID))
	target.ChapterUID = uintValue(element.Find(matroska.IDTagChapterUID))
	target.AttachmentUID = uintValue(element.Find(matroska.IDTagAttachmentUID))
	return target
}

func uintValue(element *tree.Element) uint64 {
	value, err := element.AsUint()
	if err != nil {
		return 0
	}
	return value
}

// Get returns the LAST value seen for name under target.
//
// RFC 9559 defines no precedence for a repeated TagName, so last-wins is the
// library's choice, not the spec's: later is the more recent statement in a
// stream, and first-wins would make a value written after the Cluster
// permanently invisible whenever the same name also appeared before it -- which
// is exactly the shape a live Amazon Connect stream sends. This diverges from
// the obvious analogue: net/http.Header.Get returns the FIRST value.
func (t *Set) Get(target Target, name string) (string, bool) {
	values := t.valuesFor(target, name)
	if len(values) == 0 {
		return "", false
	}
	return values[len(values)-1], true
}

// Values returns every value for name under target, in stream order.
func (t *Set) Values(target Target, name string) []string {
	values := t.valuesFor(target, name)
	if len(values) == 0 {
		return nil
	}
	return append([]string(nil), values...)
}

// All returns every name under target with its last-wins value.
func (t *Set) All(target Target) map[string]string {
	out := make(map[string]string)
	if t == nil {
		return out
	}
	for name, values := range t.values[target] {
		if len(values) > 0 {
			out[name] = values[len(values)-1]
		}
	}
	return out
}

// Targets returns every distinct target the observed tags name, in first-seen
// order.
func (t *Set) Targets() []Target {
	if t == nil {
		return nil
	}
	return append([]Target(nil), t.targets...)
}

func (t *Set) valuesFor(target Target, name string) []string {
	if t == nil {
		return nil
	}
	return t.values[target][name]
}
