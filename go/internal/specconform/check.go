package specconform

import (
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/yacchi/ebml/internal/ebmltest"
	"github.com/yacchi/ebml/matroska"
	"github.com/yacchi/ebml/parser"
)

// Severity separates the two answers this checker can give, and they are not
// the same kind of work. A Mismatch is a DEFECT: the registry states something
// the schema contradicts, and a consumer reading a conforming stream gets the
// wrong answer. A Gap is COVERAGE: the registry is silent where the schema
// speaks, which is never wrong -- an unregistered element stays readable as a
// binary leaf -- but it is the worklist for broadening Matroska support. A Note
// is a divergence this repository declares on purpose.
type Severity int

const (
	SeverityMismatch Severity = iota
	SeverityGap
	SeverityNote
)

func (s Severity) String() string {
	switch s {
	case SeverityMismatch:
		return "MISMATCH"
	case SeverityGap:
		return "GAP"
	case SeverityNote:
		return "note"
	default:
		return fmt.Sprintf("Severity(%d)", int(s))
	}
}

// Finding is one difference between the registry and the schema.
type Finding struct {
	Severity Severity
	// Check names the invariant that produced the finding, so a report can be
	// read check by check: "identity", "value-type", "containment",
	// "unknown-size", "global", "header-limits", "schema-set".
	Check   string
	Element string
	Detail  string
}

// Report is the result of one check run.
type Report struct {
	Sources    []string
	DocTypes   []string
	Registered int
	Declared   int
	Covered    int
	// Missing lists the schema elements the registry does not know, sorted by
	// path. It is the coverage worklist.
	Missing  []SchemaElement
	Findings []Finding
}

// Mismatches returns the number of findings that are defects.
func (r *Report) Mismatches() int {
	n := 0
	for _, f := range r.Findings {
		if f.Severity == SeverityMismatch {
			n++
		}
	}
	return n
}

func (r *Report) add(severity Severity, check, element, format string, args ...any) {
	r.Findings = append(r.Findings, Finding{
		Severity: severity,
		Check:    check,
		Element:  element,
		Detail:   fmt.Sprintf(format, args...),
	})
}

// declaredValueTypeDivergence lists the elements this library deliberately
// types differently from the schema, with the reason. TypeBlock is a
// library-level REFINEMENT of the schema's "binary": the payload is still an
// opaque binary leaf to the cursor, and the distinct type only tells a caller
// that parser.ParseSimpleBlock can decode its internals.
var declaredValueTypeDivergence = map[parser.ElementID]string{
	matroska.IDSimpleBlock: "TypeBlock refines the schema's binary type; the payload is decodable with parser.ParseSimpleBlock",
	matroska.IDBlock:       "TypeBlock refines the schema's binary type; the payload is decodable with parser.ParseSimpleBlock",
}

// schemaValueTypes maps the schema's type tokens to the registry's ValueType.
var schemaValueTypes = map[string]matroska.ValueType{
	"master":   matroska.TypeMaster,
	"uinteger": matroska.TypeUint,
	"integer":  matroska.TypeInt,
	"float":    matroska.TypeFloat,
	"string":   matroska.TypeString,
	"utf-8":    matroska.TypeUTF8,
	"date":     matroska.TypeDate,
	"binary":   matroska.TypeBinary,
}

// Check compares the built-in registry against the supplied schemas.
//
// It reads the registry through its EXPORTED API only -- Elements, LegalChildren
// and EndsUnknownSizeMaster -- so what it validates is the behavior a consumer
// actually sees, not an internal table that might disagree with it.
func Check(schemas ...*Schema) *Report {
	report := &Report{Registered: len(matroska.Elements())}
	docTypes := make(map[string]bool)
	// The base EBML schema and the Matroska schema both declare the global
	// elements, so the element total is counted over distinct IDs.
	distinct := make(map[parser.ElementID]bool)
	for _, s := range schemas {
		report.Sources = append(report.Sources, s.Source)
		report.DocTypes = append(report.DocTypes, s.DocType)
		docTypes[s.DocType] = true
		for _, e := range s.Elements {
			distinct[e.ID] = true
		}
	}
	report.Declared = len(distinct)
	if !docTypes["ebml"] || !docTypes["matroska"] {
		report.add(SeverityNote, "schema-set", "",
			"loaded doc types %v; both \"ebml\" (header elements) and \"matroska\" (body elements) are needed for a complete check",
			report.DocTypes)
	}

	checkUnassigned(report, schemas)
	checkIdentityAndTypes(report, schemas)
	checkContainment(report, schemas)
	checkUnknownSize(report, schemas)
	checkGlobals(report, schemas)
	checkHeaderLimits(report, schemas)
	collectMissing(report, schemas)
	return report
}

// checkUnassigned proves the IDs the test corpus uses as "no registry knows
// this" really are unassigned. A fixture built on an ID the schema turns out to
// define documents a claim that is false, and the golden trace recorded from it
// then looks like evidence for it.
func checkUnassigned(report *Report, schemas []*Schema) {
	for _, id := range ebmltest.UnassignedIDs {
		declared, _, ok := lookupSchema(schemas, id)
		if !ok {
			continue
		}
		report.add(SeverityMismatch, "unassigned", declared.Name,
			"%s is reserved by internal/ebmltest as an unassigned ID, but the schema declares it as %s",
			id, declared.Name)
	}
}

func lookupSchema(schemas []*Schema, id parser.ElementID) (SchemaElement, *Schema, bool) {
	for _, s := range schemas {
		if e, ok := s.ByID(id); ok {
			return e, s, true
		}
	}
	return SchemaElement{}, nil, false
}

func checkIdentityAndTypes(report *Report, schemas []*Schema) {
	for _, info := range matroska.Elements() {
		declared, _, ok := lookupSchema(schemas, info.ID)
		if !ok {
			report.add(SeverityMismatch, "identity", info.Name,
				"%s is registered but no loaded schema declares that ID", info.ID)
			continue
		}
		report.Covered++
		if declared.Name != info.Name {
			report.add(SeverityMismatch, "identity", info.Name,
				"%s is registered as %q but the schema names it %q", info.ID, info.Name, declared.Name)
		}
		expected, known := schemaValueTypes[declared.Type]
		if !known {
			report.add(SeverityMismatch, "value-type", info.Name,
				"schema type %q is not one this checker maps to a ValueType", declared.Type)
			continue
		}
		if info.Type == expected {
			continue
		}
		if reason, declaredDivergence := declaredValueTypeDivergence[info.ID]; declaredDivergence {
			report.add(SeverityNote, "value-type", info.Name,
				"registered as %s, schema says %s: %s", info.Type, declared.Type, reason)
			continue
		}
		report.add(SeverityMismatch, "value-type", info.Name,
			"registered as %s but the schema says %s", info.Type, declared.Type)
	}

	// A name the registry resolves must resolve to the ID the schema gives it.
	// This catches an alias or a typo that identity-by-ID cannot see.
	for _, s := range schemas {
		for _, declared := range s.Elements {
			id, ok := matroska.IDForName(declared.Name)
			if !ok || id == declared.ID {
				continue
			}
			report.add(SeverityMismatch, "identity", declared.Name,
				"the registry resolves the name to %s but the schema declares it as %s", id, declared.ID)
		}
	}
}

func checkContainment(report *Report, schemas []*Schema) {
	for _, info := range matroska.Elements() {
		ours, complete := matroska.LegalChildren(info.ID)
		if !complete {
			continue
		}
		declared, schema, ok := lookupSchema(schemas, info.ID)
		if !ok {
			continue // already reported by the identity check
		}
		theirs := schema.Children(declared.Path)

		declaredIDs := make(map[parser.ElementID]SchemaElement, len(theirs))
		for _, child := range theirs {
			declaredIDs[child.ID] = child
		}
		for _, child := range ours {
			if _, ok := declaredIDs[child]; !ok {
				report.add(SeverityMismatch, "containment", info.Name,
					"lists %s as a legal child but the schema does not", matroska.Describe(child))
			}
		}

		ourIDs := make(map[parser.ElementID]bool, len(ours))
		for _, child := range ours {
			ourIDs[child] = true
		}
		for _, child := range theirs {
			if ourIDs[child.ID] {
				continue
			}
			// A child missing from a list documented as COMPLETE is only safe
			// while the element is also absent from the registry:
			// EndsUnknownSizeMaster refuses to end a master on an unregistered
			// ID, so an unregistered omission can never cause a premature
			// boundary. Registering it without adding it here is exactly the
			// change that would break the boundary rule, which is what this
			// check exists to catch.
			if _, registered := matroska.Lookup(child.ID); registered {
				report.add(SeverityMismatch, "containment", info.Name,
					"the schema declares %s as a child, and it is registered, but the complete child list omits it: it would end this master early",
					matroska.Describe(child.ID))
				continue
			}
			report.add(SeverityNote, "containment", info.Name,
				"the schema declares %s (0x%X) as a child; it is absent from both the child list and the registry, so it cannot trigger a boundary",
				child.Name, uint32(child.ID))
		}
	}
}

func checkUnknownSize(report *Report, schemas []*Schema) {
	for _, s := range schemas {
		for _, declared := range s.Elements {
			if !declared.UnknownSizeAllowed {
				continue
			}
			if _, registered := matroska.Lookup(declared.ID); !registered {
				report.add(SeverityGap, "unknown-size", declared.Name,
					"the schema allows an unknown size for it, but it is not registered")
				continue
			}
			if _, complete := matroska.LegalChildren(declared.ID); !complete {
				report.add(SeverityMismatch, "unknown-size", declared.Name,
					"the schema allows an unknown size for it, but the registry has no complete child list, so no boundary can be derived")
			}
		}
	}
}

func checkGlobals(report *Report, schemas []*Schema) {
	ours := derivedGlobals()
	declared := make(map[parser.ElementID]SchemaElement)
	for _, s := range schemas {
		for _, e := range s.Globals() {
			declared[e.ID] = e
		}
	}
	if ours == nil {
		report.add(SeverityNote, "global", "",
			"no master carries a complete child list, so the registry's global elements cannot be derived")
		return
	}
	for id := range ours {
		if _, ok := declared[id]; !ok {
			report.add(SeverityMismatch, "global", matroska.NameForID(id),
				"the registry treats it as global (never a boundary) but no schema declares it global")
		}
	}
	for id, e := range declared {
		if ours[id] {
			continue
		}
		if _, registered := matroska.Lookup(id); !registered {
			report.add(SeverityGap, "global", e.Name, "declared global by the schema but not registered")
			continue
		}
		report.add(SeverityMismatch, "global", e.Name,
			"the schema declares it global but the registry would let it end an unknown-size master")
	}
}

// derivedGlobals reconstructs the registry's global-element set through the
// exported API. Inside a master with a complete child list, a REGISTERED
// element that is not a child and still does not end the master can only be one
// the registry treats as global.
func derivedGlobals() map[parser.ElementID]bool {
	var probe parser.ElementID
	var children []parser.ElementID
	for _, info := range matroska.Elements() {
		if list, complete := matroska.LegalChildren(info.ID); complete {
			probe, children = info.ID, list
			break
		}
	}
	if probe == 0 {
		return nil
	}
	isChild := make(map[parser.ElementID]bool, len(children))
	for _, child := range children {
		isChild[child] = true
	}
	globals := make(map[parser.ElementID]bool)
	for _, info := range matroska.Elements() {
		if isChild[info.ID] || info.ID == probe {
			continue
		}
		if !matroska.EndsUnknownSizeMaster(probe, info.ID) {
			globals[info.ID] = true
		}
	}
	return globals
}

// checkHeaderLimits ties the EBML header's declared VINT constraints to the
// parser's hard limits. The parser holds no element knowledge, so these two
// numbers are the one place where the header's schema constraints and the
// cursor's behavior have to be reconciled by hand.
func checkHeaderLimits(report *Report, schemas []*Schema) {
	limits := []struct {
		id      parser.ElementID
		name    string
		enforce int
	}{
		{matroska.IDEBMLMaxIDLength, "EBMLMaxIDLength", parser.MaxElementIDLength},
		{matroska.IDEBMLMaxSizeLength, "EBMLMaxSizeLength", parser.MaxElementSizeLength},
	}
	for _, limit := range limits {
		best, found := "", false
		for _, s := range schemas {
			declared, ok := s.ByID(limit.id)
			if !ok {
				continue
			}
			found = true
			// The matroska schema narrows what the base ebml schema leaves
			// open, so prefer a bounded range over an unbounded one.
			if _, ok := rangeUpperBound(declared.Range); ok || best == "" {
				best = declared.Range
			}
		}
		if !found {
			report.add(SeverityGap, "header-limits", limit.name, "no loaded schema declares it")
			continue
		}
		bound, ok := rangeUpperBound(best)
		if !ok {
			report.add(SeverityNote, "header-limits", limit.name,
				"schema range %q has no upper bound this checker can read; the parser enforces %d", best, limit.enforce)
			continue
		}
		if bound != limit.enforce {
			report.add(SeverityMismatch, "header-limits", limit.name,
				"schema range %q allows up to %d but the parser enforces %d", best, bound, limit.enforce)
		}
	}
}

// rangeUpperBound reads the upper bound out of the schema range forms this
// checker understands: "4", "1-8" and "<=8". Anything else reports false rather
// than a guess.
func rangeUpperBound(spec string) (int, bool) {
	spec = strings.TrimSpace(spec)
	if spec == "" {
		return 0, false
	}
	if rest, ok := strings.CutPrefix(spec, "<="); ok {
		return atoiOK(rest)
	}
	if _, rest, ok := strings.Cut(spec, "-"); ok {
		return atoiOK(rest)
	}
	return atoiOK(spec)
}

func atoiOK(s string) (int, bool) {
	v, err := strconv.Atoi(strings.TrimSpace(s))
	if err != nil {
		return 0, false
	}
	return v, true
}

func collectMissing(report *Report, schemas []*Schema) {
	seen := make(map[parser.ElementID]bool)
	for _, s := range schemas {
		for _, declared := range s.Elements {
			if _, ok := matroska.Lookup(declared.ID); ok {
				continue
			}
			if seen[declared.ID] {
				continue
			}
			seen[declared.ID] = true
			report.Missing = append(report.Missing, declared)
		}
	}
	// Group by parent, which is how the registry would be extended: one master's
	// children at a time.
	sort.Slice(report.Missing, func(i, j int) bool {
		if report.Missing[i].Parent != report.Missing[j].Parent {
			return report.Missing[i].Parent < report.Missing[j].Parent
		}
		return report.Missing[i].Name < report.Missing[j].Name
	})
}
