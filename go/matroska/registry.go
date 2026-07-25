package matroska

import (
	"errors"
	"fmt"
	"sort"

	"github.com/yacchi/ebml/parser"
)

// Errors reported by Registry.Register and Registry.Override. They are sentinels
// wrapped with the offending element's details, so a caller matches them with
// errors.Is.
var (
	// ErrRegistryImmutable is reported when Register or Override is called on an
	// immutable registry, which is what Default returns: the built-in RFC 9559
	// table is shared by every consumer of this package, so no package can alter
	// what another one sees. Extend it with NewRegistry(Default()) instead.
	ErrRegistryImmutable = errors.New("registry is immutable")
	// ErrDuplicateElement is reported when Register would shadow an element the
	// registry already resolves, by ID or by name. Override is the explicit path
	// for replacing an entry on purpose.
	ErrDuplicateElement = errors.New("element is already registered")
	// ErrInvalidElement is reported for an entry that cannot be registered at
	// all: a zero ID, an empty name, or a value type outside ValueType.
	ErrInvalidElement = errors.New("invalid element")
)

// Registry maps element IDs to their names and value types, and classifies them
// as master or leaf for the cursor.
//
// Default returns the built-in RFC 9559 table. NewRegistry(Default()) derives an
// extendable registry that answers for its own entries first and falls back to
// the built-in table for everything else, which is how a consumer teaches this
// library about vendor or private elements without forking it.
//
// A Registry is safe for concurrent use once it is built. Register and Override
// are not: register everything a registry needs (typically during
// initialization) before handing it to a reader.
//
// Every method is safe on a nil *Registry: it then knows no element at all,
// which is the same answer a registry gives for an ID it has never heard of.
type Registry struct {
	// base is consulted for every ID this registry does not answer for itself.
	base *Registry
	// entries and names are this registry's own entries only, never the base's.
	entries map[parser.ElementID]ElementInfo
	names   map[string]parser.ElementID
	// legacy maps pre-RFC 9559 element names to the renamed element's ID. Only
	// the built-in registry carries aliases; derived registries reach them
	// through base.
	legacy map[string]parser.ElementID
	// immutable rejects Register and Override, protecting the built-in table.
	immutable bool
}

// defaultRegistry is the built-in RFC 9559 element table. It wraps the package
// tables directly and is immutable, so the maps it exposes can never be mutated
// through the registry API.
var defaultRegistry = &Registry{
	entries:   elements,
	names:     names,
	legacy:    legacyNames,
	immutable: true,
}

// Default returns the built-in registry of the standard RFC 9559 elements.
//
// It is immutable: Register and Override on it report ErrRegistryImmutable, so
// one package can never change the elements another package sees. It is also
// what every package-level function in this package resolves through.
//
// To add elements, derive from it: NewRegistry(Default()).
func Default() *Registry { return defaultRegistry }

// NewRegistry returns an empty, extendable registry that falls back to base for
// every ID it does not answer for itself.
//
// Pass Default() as base to extend the standard element set -- the usual case,
// since a stream carrying vendor elements is otherwise ordinary Matroska. Pass
// nil for a registry that knows only what is registered on it, which is what an
// EBML document type unrelated to Matroska needs.
func NewRegistry(base *Registry) *Registry {
	return &Registry{
		base:    base,
		entries: make(map[parser.ElementID]ElementInfo),
		names:   make(map[string]parser.ElementID),
	}
}

// Register adds an element to r.
//
// It reports ErrDuplicateElement if r already resolves info.ID -- including
// through its base, so shadowing a standard element is never accidental -- or if
// info.Name already resolves to a different ID. Override is the explicit path
// for replacing an entry. An entry with a zero ID, an empty name or a value type
// outside ValueType is rejected with ErrInvalidElement, and TypeUnknown is not a
// registrable type: it is what TypeFor reports for an ID no registry knows.
//
// Registering an element with Type TypeMaster is what makes the cursor DESCEND
// INTO it instead of reading it as one opaque binary leaf: KindForElementID
// classifies exactly the registered masters as parser.KindMaster. A vendor or
// private master that no registry knows is still readable -- it arrives as a
// binary leaf whose bytes a consumer can re-parse -- but its children only
// become cursor events once it is registered as a master here and the cursor is
// driven with this registry's KindForElementID.
func (r *Registry) Register(info ElementInfo) error {
	if err := r.checkRegistrable(info); err != nil {
		return err
	}
	if existing, ok := r.Lookup(info.ID); ok {
		return fmt.Errorf("%w: %s is %s", ErrDuplicateElement, info.ID, existing.Name)
	}
	if id, ok := r.IDForName(info.Name); ok && id != info.ID {
		return fmt.Errorf("%w: name %q is %s", ErrDuplicateElement, info.Name, id)
	}
	r.put(info)
	return nil
}

// Override registers info unconditionally, replacing whatever r resolved for
// that ID or name before. It is the explicit path Register refuses to take: a
// caller that means to redefine a standard element -- to rename it, or to make a
// master out of an element the built-in table types as binary -- says so by
// calling Override.
//
// Only this registry is affected: the base registry keeps its entry, so an ID
// overridden here still resolves to the standard element through Default().
func (r *Registry) Override(info ElementInfo) error {
	if err := r.checkRegistrable(info); err != nil {
		return err
	}
	r.put(info)
	return nil
}

func (r *Registry) checkRegistrable(info ElementInfo) error {
	if r == nil {
		return fmt.Errorf("%w: nil registry", ErrRegistryImmutable)
	}
	if r.immutable {
		return fmt.Errorf("%w: derive one with NewRegistry(Default())", ErrRegistryImmutable)
	}
	if info.ID == 0 {
		return fmt.Errorf("%w: zero element ID", ErrInvalidElement)
	}
	if info.Name == "" {
		return fmt.Errorf("%w: %s has no name", ErrInvalidElement, info.ID)
	}
	if info.Type >= TypeUnknown {
		return fmt.Errorf("%w: %s has value type %s", ErrInvalidElement, info.ID, info.Type)
	}
	return nil
}

// put stores info as one of r's own entries, dropping the name of the entry it
// replaces so IDForName never resolves a name this registry no longer reports.
func (r *Registry) put(info ElementInfo) {
	if previous, ok := r.entries[info.ID]; ok && previous.Name != info.Name {
		delete(r.names, previous.Name)
	}
	r.entries[info.ID] = info
	r.names[info.Name] = info.ID
}

// Lookup returns the entry for id, from this registry or, failing that, from its
// base.
func (r *Registry) Lookup(id parser.ElementID) (ElementInfo, bool) {
	if r == nil {
		return ElementInfo{}, false
	}
	if info, ok := r.entries[id]; ok {
		return info, true
	}
	return r.base.Lookup(id)
}

// NameForID returns the registered name of id, or an empty string when no
// registry in the chain knows it. An unknown ID is never an error: it is simply
// nameless.
func (r *Registry) NameForID(id parser.ElementID) string {
	info, ok := r.Lookup(id)
	if !ok {
		return ""
	}
	return info.Name
}

// Describe returns "Name (0xID)" for a known element and the bare "0xID" hex
// form for an unknown one, so an element is always printable.
func (r *Registry) Describe(id parser.ElementID) string {
	if name := r.NameForID(id); name != "" {
		return fmt.Sprintf("%s (%s)", name, id)
	}
	return id.String()
}

// TypeFor returns the registered value type of id. For an unknown ID it reports
// TypeUnknown and false, so "no type information" is distinguishable from a
// registered TypeMaster or TypeBinary entry.
func (r *Registry) TypeFor(id parser.ElementID) (ValueType, bool) {
	info, ok := r.Lookup(id)
	if !ok {
		return TypeUnknown, false
	}
	return info.Type, true
}

// LegalChildren returns the element IDs the RFC 9559 schema allows directly
// inside master, and false when this registry has no COMPLETE child list for
// it. An incomplete list is never returned: a boundary rule that ended a
// master on a missing entry would corrupt the parse, so "no complete list" and
// "not a child" must stay distinguishable.
//
// NewRegistry and Register cannot declare containment for vendor elements. A
// vendor element is therefore never used as a boundary, which is the safe
// behavior.
func (r *Registry) LegalChildren(master parser.ElementID) ([]parser.ElementID, bool) {
	if r == nil {
		return nil, false
	}
	if r.immutable {
		if children, ok := completeChildren[master]; ok {
			result := append([]parser.ElementID(nil), children...)
			sort.Slice(result, func(i, j int) bool { return result[i] < result[j] })
			return result, true
		}
		return nil, false
	}
	if _, own := r.entries[master]; own {
		return nil, false
	}
	return r.base.LegalChildren(master)
}

// EndsUnknownSizeMaster reports whether next must end an open unknown-size
// master per RFC 9559: it is true only when this registry has a COMPLETE child
// list for open, and next is a REGISTERED element that is neither in that list
// nor global. It is false whenever the answer is not certain -- an unknown
// master, an unregistered next, or a global element -- because the cost of a
// false positive is a corrupted parse and the cost of a false negative is only
// a master that closes later than it could.
func (r *Registry) EndsUnknownSizeMaster(open, next parser.ElementID) bool {
	children, ok := r.LegalChildren(open)
	if !ok {
		return false
	}
	if _, ok := globalElements[next]; ok {
		return false
	}
	// Only a BUILT-IN element can be a boundary. Testing the built-in table
	// rather than Lookup is deliberate and subsumes it: an ID this registry
	// knows only because a caller registered it has no schema position here, so
	// the complete child lists above say nothing about it and it must not end a
	// master. An ID nobody knows at all fails the same test.
	if _, ok := elements[next]; !ok {
		return false
	}
	for _, child := range children {
		if child == next {
			return false
		}
	}
	return true
}

// IDForName returns the ID registered for an exact element name, searching this
// registry before its base.
//
// Primary names are the RFC 9559 ones. Well-known pre-RFC names (for example
// "SegmentUID" for SegmentUUID, or "FileMimeType" for FileMediaType) resolve
// through a fallback, so callers written against the older matroska.org names
// still work; an alias never becomes an element's reported name. Only IDForName
// accepts aliases.
func (r *Registry) IDForName(name string) (parser.ElementID, bool) {
	if r == nil {
		return 0, false
	}
	if id, ok := r.names[name]; ok {
		return id, true
	}
	if id, ok := r.legacy[name]; ok {
		return id, true
	}
	return r.base.IDForName(name)
}

// Elements returns every element this registry resolves -- its own entries plus
// the base's, with its own winning -- sorted by ID.
func (r *Registry) Elements() []ElementInfo {
	merged := make(map[parser.ElementID]ElementInfo)
	r.collect(merged)
	result := make([]ElementInfo, 0, len(merged))
	for _, info := range merged {
		result = append(result, info)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].ID < result[j].ID })
	return result
}

// collect fills out with the base chain's entries first, so this registry's own
// entries overwrite the ones it shadows.
func (r *Registry) collect(out map[parser.ElementID]ElementInfo) {
	if r == nil {
		return
	}
	r.base.collect(out)
	for id, info := range r.entries {
		out[id] = info
	}
}

// Lookup returns the built-in registry entry for id. See Registry.Lookup.
func Lookup(id parser.ElementID) (ElementInfo, bool) { return Default().Lookup(id) }

// NameForID returns the built-in registered name of id, or an empty string for
// an unknown ID. See Registry.NameForID.
func NameForID(id parser.ElementID) string { return Default().NameForID(id) }

// Describe returns the built-in name and ID of id, or just its hex ID when
// unknown. See Registry.Describe.
func Describe(id parser.ElementID) string { return Default().Describe(id) }

// TypeFor returns the built-in value type of id. See Registry.TypeFor.
func TypeFor(id parser.ElementID) (ValueType, bool) { return Default().TypeFor(id) }

// LegalChildren returns the built-in complete child list for master, if one is
// available. See Registry.LegalChildren.
func LegalChildren(master parser.ElementID) ([]parser.ElementID, bool) {
	return Default().LegalChildren(master)
}

// EndsUnknownSizeMaster reports whether next structurally ends an open unknown-
// size master according to the built-in RFC 9559 containment lists. See
// Registry.EndsUnknownSizeMaster.
func EndsUnknownSizeMaster(open, next parser.ElementID) bool {
	return Default().EndsUnknownSizeMaster(open, next)
}

// IDForName returns the built-in ID for an exact element name, accepting
// well-known pre-RFC 9559 aliases. See Registry.IDForName.
func IDForName(name string) (parser.ElementID, bool) { return Default().IDForName(name) }

// Elements returns all built-in elements sorted by ID. See Registry.Elements.
func Elements() []ElementInfo { return Default().Elements() }
