package matroska

import (
	"errors"
	"testing"

	"github.com/yacchi/ebml-reader/parser"
)

// vendor element IDs used by the registry tests: valid 2-byte EBML IDs that the
// built-in RFC 9559 table does not use.
const (
	idVendorBox   parser.ElementID = 0x4F01
	idVendorCount parser.ElementID = 0x4F02
	idVendorBlob  parser.ElementID = 0x4F03
)

func TestDefaultRegistryIsImmutable(t *testing.T) {
	for name, register := range map[string]func(ElementInfo) error{
		"Register": Default().Register,
		"Override": Default().Override,
	} {
		err := register(ElementInfo{ID: idVendorBox, Name: "VendorBox", Type: TypeMaster})
		if !errors.Is(err, ErrRegistryImmutable) {
			t.Errorf("Default().%s() error = %v, want ErrRegistryImmutable", name, err)
		}
	}
	if _, ok := Lookup(idVendorBox); ok {
		t.Error("a rejected Register still changed the built-in table")
	}
	if got := len(Default().Elements()); got != len(elements) {
		t.Errorf("Default().Elements() has %d entries, want %d", got, len(elements))
	}
}

func TestDerivedRegistryFallsBackToBase(t *testing.T) {
	reg := NewRegistry(Default())
	if err := reg.Register(ElementInfo{ID: idVendorBox, Name: "VendorBox", Type: TypeMaster}); err != nil {
		t.Fatalf("Register() error = %v", err)
	}
	if err := reg.Register(ElementInfo{ID: idVendorCount, Name: "VendorCount", Type: TypeUint}); err != nil {
		t.Fatalf("Register() error = %v", err)
	}

	// Own entries.
	if got := reg.NameForID(idVendorBox); got != "VendorBox" {
		t.Errorf("NameForID(vendor master) = %q, want VendorBox", got)
	}
	if got := reg.KindForElementID(idVendorBox); got != parser.KindMaster {
		t.Errorf("KindForElementID(vendor master) = %q, want master", got)
	}
	if got := reg.KindForElementID(idVendorCount); got != parser.KindUint {
		t.Errorf("KindForElementID(vendor uint) = %q, want uint", got)
	}
	if id, ok := reg.IDForName("VendorBox"); !ok || id != idVendorBox {
		t.Errorf("IDForName(VendorBox) = %s, %v, want %s, true", id, ok, idVendorBox)
	}

	// Base entries, including the legacy-alias fallback that only Default holds.
	if got := reg.NameForID(IDCluster); got != "Cluster" {
		t.Errorf("NameForID(Cluster) via base = %q, want Cluster", got)
	}
	if got := reg.KindForElementID(IDCluster); got != parser.KindMaster {
		t.Errorf("KindForElementID(Cluster) via base = %q, want master", got)
	}
	if id, ok := reg.IDForName("SegmentUID"); !ok || id != IDSegmentUUID {
		t.Errorf("IDForName(SegmentUID) via base = %s, %v, want %s, true", id, ok, IDSegmentUUID)
	}
	if typ, ok := reg.TypeFor(IDTagString); !ok || typ != TypeUTF8 {
		t.Errorf("TypeFor(TagString) via base = %v, %v, want utf-8, true", typ, ok)
	}

	// Extending a derived registry never changes the built-in table.
	if _, ok := Lookup(idVendorBox); ok {
		t.Error("registering on a derived registry leaked into Default()")
	}
	if got, want := len(reg.Elements()), len(elements)+2; got != want {
		t.Errorf("Elements() has %d entries, want %d", got, want)
	}
	var seenVendor bool
	previous := parser.ElementID(0)
	for _, info := range reg.Elements() {
		if info.ID < previous {
			t.Fatalf("Elements() is not sorted by ID at %s", info.ID)
		}
		previous = info.ID
		if info.ID == idVendorBox {
			seenVendor = true
		}
	}
	if !seenVendor {
		t.Error("Elements() omits the vendor element")
	}
}

func TestRegisterRejectsDuplicates(t *testing.T) {
	reg := NewRegistry(Default())
	if err := reg.Register(ElementInfo{ID: idVendorBox, Name: "VendorBox", Type: TypeMaster}); err != nil {
		t.Fatalf("Register() error = %v", err)
	}
	for name, info := range map[string]ElementInfo{
		"own ID again":   {ID: idVendorBox, Name: "VendorBoxAgain", Type: TypeMaster},
		"own name again": {ID: idVendorBlob, Name: "VendorBox", Type: TypeBinary},
		"base ID":        {ID: IDCluster, Name: "NotCluster", Type: TypeBinary},
		"base name":      {ID: idVendorBlob, Name: "Cluster", Type: TypeBinary},
	} {
		if err := reg.Register(info); !errors.Is(err, ErrDuplicateElement) {
			t.Errorf("Register(%s) error = %v, want ErrDuplicateElement", name, err)
		}
	}
	// The refused registrations left nothing behind.
	if _, ok := reg.Lookup(idVendorBlob); ok {
		t.Error("a refused Register stored the entry anyway")
	}
	if got := reg.NameForID(IDCluster); got != "Cluster" {
		t.Errorf("NameForID(Cluster) = %q, want the base entry intact", got)
	}
}

func TestRegisterRejectsInvalidEntries(t *testing.T) {
	reg := NewRegistry(Default())
	for name, info := range map[string]ElementInfo{
		"zero ID":      {ID: 0, Name: "Nothing", Type: TypeBinary},
		"empty name":   {ID: idVendorBox, Name: "", Type: TypeBinary},
		"unknown type": {ID: idVendorBox, Name: "VendorBox", Type: TypeUnknown},
		"out of range": {ID: idVendorBox, Name: "VendorBox", Type: TypeUnknown + 1},
	} {
		if err := reg.Register(info); !errors.Is(err, ErrInvalidElement) {
			t.Errorf("Register(%s) error = %v, want ErrInvalidElement", name, err)
		}
		if err := reg.Override(info); !errors.Is(err, ErrInvalidElement) {
			t.Errorf("Override(%s) error = %v, want ErrInvalidElement", name, err)
		}
	}
	if len(reg.Elements()) != len(elements) {
		t.Error("an invalid entry was stored")
	}
}

// TestOverrideReplaces pins the explicit override path: it is the only way to
// redefine an element, it can turn a standard leaf into a master, and it changes
// nothing outside the registry it is called on.
func TestOverrideReplaces(t *testing.T) {
	reg := NewRegistry(Default())
	// CodecPrivate is registered as binary; a caller that knows its content is a
	// nested EBML document can make the cursor descend into it.
	if err := reg.Override(ElementInfo{ID: IDCodecPrivate, Name: "CodecPrivate", Type: TypeMaster}); err != nil {
		t.Fatalf("Override() error = %v", err)
	}
	if got := reg.KindForElementID(IDCodecPrivate); got != parser.KindMaster {
		t.Errorf("KindForElementID(CodecPrivate) = %q, want master", got)
	}
	if got := KindForElementID(IDCodecPrivate); got != parser.KindBinary {
		t.Errorf("the built-in classifier changed to %q; Override must be local", got)
	}
	if got, want := len(reg.Elements()), len(elements); got != want {
		t.Errorf("Elements() has %d entries, want %d after an override", got, want)
	}

	// Overriding an own entry with a new name retires the old name.
	if err := reg.Override(ElementInfo{ID: idVendorBox, Name: "VendorBox", Type: TypeMaster}); err != nil {
		t.Fatalf("Override() error = %v", err)
	}
	if err := reg.Override(ElementInfo{ID: idVendorBox, Name: "VendorCrate", Type: TypeMaster}); err != nil {
		t.Fatalf("Override() error = %v", err)
	}
	if _, ok := reg.IDForName("VendorBox"); ok {
		t.Error("IDForName still resolves the retired name of an overridden element")
	}
	if id, ok := reg.IDForName("VendorCrate"); !ok || id != idVendorBox {
		t.Errorf("IDForName(VendorCrate) = %s, %v, want %s, true", id, ok, idVendorBox)
	}
}

// TestStandaloneRegistry covers NewRegistry(nil): a registry for an EBML document
// type that is not Matroska knows only what was registered on it.
func TestStandaloneRegistry(t *testing.T) {
	reg := NewRegistry(nil)
	if err := reg.Register(ElementInfo{ID: idVendorBox, Name: "VendorBox", Type: TypeMaster}); err != nil {
		t.Fatalf("Register() error = %v", err)
	}
	if got := len(reg.Elements()); got != 1 {
		t.Errorf("Elements() has %d entries, want 1", got)
	}
	if _, ok := reg.Lookup(IDCluster); ok {
		t.Error("a registry with no base resolved a standard element")
	}
	if got := reg.KindForElementID(IDCluster); got != parser.KindBinary {
		t.Errorf("KindForElementID(Cluster) = %q, want binary with no base", got)
	}
}

// TestUnknownIDIsNamelessNotFatal pins the naming half of the unknown-element
// guarantees: an ID no registry knows has no name, has no type information, is
// still printable, and never panics.
func TestUnknownIDIsNamelessNotFatal(t *testing.T) {
	for _, reg := range map[string]*Registry{
		"default":    Default(),
		"derived":    NewRegistry(Default()),
		"standalone": NewRegistry(nil),
		"nil":        nil,
	} {
		if got := reg.NameForID(idVendorBox); got != "" {
			t.Errorf("NameForID(unknown) = %q, want empty", got)
		}
		if got := reg.Describe(idVendorBox); got != "0x4F01" {
			t.Errorf("Describe(unknown) = %q, want the hex form 0x4F01", got)
		}
		if typ, ok := reg.TypeFor(idVendorBox); ok || typ != TypeUnknown {
			t.Errorf("TypeFor(unknown) = %v, %v, want unknown, false", typ, ok)
		}
		if got := reg.KindForElementID(idVendorBox); got != parser.KindBinary {
			t.Errorf("KindForElementID(unknown) = %q, want binary", got)
		}
		if _, ok := reg.Lookup(idVendorBox); ok {
			t.Error("Lookup(unknown) reported a hit")
		}
		if _, ok := reg.IDForName("VendorBox"); ok {
			t.Error("IDForName reported a hit for an unregistered name")
		}
	}
}

// TestNilRegistryIsUsable makes the nil-receiver contract explicit: a nil
// registry knows nothing, and registering on it is an error rather than a panic.
func TestNilRegistryIsUsable(t *testing.T) {
	var reg *Registry
	if got := len(reg.Elements()); got != 0 {
		t.Errorf("Elements() on a nil registry has %d entries, want 0", got)
	}
	if err := reg.Register(ElementInfo{ID: idVendorBox, Name: "VendorBox", Type: TypeMaster}); err == nil {
		t.Error("Register() on a nil registry succeeded")
	}
	if got := reg.Describe(IDCluster); got != "0x1F43B675" {
		t.Errorf("Describe(Cluster) on a nil registry = %q, want the hex form", got)
	}
}

// TestDescribeKnownElement pins the readable form the CLI and the tree print.
func TestDescribeKnownElement(t *testing.T) {
	if got, want := Describe(IDCluster), "Cluster (0x1F43B675)"; got != want {
		t.Errorf("Describe(Cluster) = %q, want %q", got, want)
	}
}
