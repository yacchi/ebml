// Package matroska is the element registry of the core layer: it maps EBML
// element IDs to their names and value types, and classifies them as master or
// leaf for the cursor in package parser.
//
// It is the single source of element knowledge in this library. The cursor
// deliberately holds no element table -- it only reads structure -- so a real
// Matroska stream is read by handing this package's classifier to the cursor:
//
//	p := parser.New(matroska.KindForElementID)
//
// Names, IDs and value types follow RFC 9559, which supersedes several older
// matroska.org spellings (SegmentUUID, FileMediaType, Timestamp/TimestampScale).
// Only IDForName accepts the well-known pre-RFC names as aliases. TypeBlock is a
// library-level refinement of the RFC's binary type, not a spec type.
//
// # Registry
//
// Default returns the built-in RFC 9559 table, and every package-level function
// resolves through it. It is immutable, so no package can change the elements
// another package sees: Register and Override on it report
// ErrRegistryImmutable.
//
// A consumer whose stream carries vendor or private elements derives its own
// registry, which answers for its entries and falls back to the built-in table
// for the rest:
//
//	reg := matroska.NewRegistry(matroska.Default())
//	_ = reg.Register(matroska.ElementInfo{ID: 0x4000, Name: "VendorBox", Type: matroska.TypeMaster})
//	_ = reg.Register(matroska.ElementInfo{ID: 0x4001, Name: "VendorCount", Type: matroska.TypeUint})
//	p := parser.New(reg.KindForElementID)
//
// Registering an element as TypeMaster is what makes the cursor DESCEND INTO it
// rather than read it as one opaque leaf, and the derived registry's own
// NameForID/Describe/TypeFor then report the vendor elements alongside the
// standard ones.
//
// # Unknown elements
//
// An element this library has never heard of is a normal, supported case, not an
// error. The guarantees are:
//
//   - An unregistered element never breaks the reader. KindForElementID reports
//     it as a binary leaf, the cursor honours its declared size, and its payload
//     bytes are readable -- so the elements after it are read normally. An
//     unknown ID is skippable and readable exactly like a registered binary leaf.
//   - Naming an unknown ID is safe and empty, never a panic: NameForID returns
//     "", TypeFor returns TypeUnknown and false, and Describe falls back to the
//     hex form of the ID (for example "0x4000"), so an unknown element is still
//     printable and reportable.
//   - Registering the elements makes them first class. Once a vendor master is
//     registered as TypeMaster and the cursor is driven with that registry's
//     KindForElementID, its children become cursor events at the correct depth
//     and its name appears wherever the registry is consulted.
//   - Nothing is lost by not knowing. A master that was not registered arrives
//     as one binary leaf, and its bytes are complete: re-parsing that payload --
//     for instance with the finite-buffer parser in ext/tree, optionally with a
//     registry that does know the element -- recovers the full internal
//     structure afterwards. Discovering an unknown master therefore never
//     requires re-reading the stream.
//
// The one input that is not readable is an element declaring an unknown size
// that is not classified as a master: EBML reserves the unknown size for
// masters, so such an element has no locatable end. The cursor reports it as
// parser.UnknownSizeLeafError, which carries the ID and the offset -- if the
// element is in fact a master, registering it as TypeMaster is the fix.
package matroska
