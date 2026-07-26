// Package tags reads Matroska Tags elements from a retained Segment scope.
package tags

import (
	"github.com/yacchi/ebml/ext/scope"
	"github.com/yacchi/ebml/matroska"
	"github.com/yacchi/ebml/tree"
)

// Target is a decoded Targets element. Its zero value is the whole Segment,
// which is also what an absent or empty Targets means.
type Target struct {
	TypeValue                                       uint64
	TrackUID, EditionUID, ChapterUID, AttachmentUID uint64
}

// Set is a computed view of tags, grouped by their decoded target.
type Set struct {
	values  map[Target]map[string][]string
	targets []Target
}

// Read builds a view over the Tags elements the scope observed. It is computed,
// not accumulated: scope.Scope.Rev is all a caller needs to know when to rebuild.
func Read(s *scope.Scope) *Set {
	if s == nil {
		return ReadElement(nil)
	}
	var roots []*tree.Element
	for _, element := range s.GetAll(matroska.IDTags) {
		roots = append(roots, element)
	}
	return read(roots)
}

// ReadElement builds the same view from a retained Segment tree. It is the tree
// entry point used by consumers that retain a segment without a scope.
func ReadElement(segment *tree.Element) *Set {
	if segment == nil {
		return read(nil)
	}
	return read(segment.ChildrenByID(matroska.IDTags))
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
				if _, ok := out.values[target]; !ok {
					out.values[target] = make(map[string][]string)
					out.targets = append(out.targets, target)
				}
				out.values[target][name] = append(
					out.values[target][name],
					simple.Find(matroska.IDTagString).AsString(),
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

// Targets returns every distinct target the scope's tags name, in first-seen
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
