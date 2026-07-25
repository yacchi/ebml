package specconform

import (
	"fmt"
	"io"
	"sort"
	"strings"
)

// WriteText renders a human-readable report. When verbose is false the notes --
// the divergences this repository declares on purpose -- are summarized as a
// count instead of listed.
func (r *Report) WriteText(w io.Writer, verbose bool) {
	fmt.Fprintf(w, "schemas: %s\n", strings.Join(r.Sources, ", "))
	fmt.Fprintf(w, "registry: %d elements, %d of them declared by a schema\n", r.Registered, r.Covered)
	fmt.Fprintf(w, "schema:   %d elements, %d of them registered (%d missing)\n",
		r.Declared, r.Declared-len(r.Missing), len(r.Missing))

	byCheck := map[string][]Finding{}
	notes := 0
	for _, f := range r.Findings {
		if f.Severity == SeverityNote && !verbose {
			notes++
			continue
		}
		byCheck[f.Check] = append(byCheck[f.Check], f)
	}
	checks := make([]string, 0, len(byCheck))
	for check := range byCheck {
		checks = append(checks, check)
	}
	sort.Strings(checks)
	for _, check := range checks {
		fmt.Fprintf(w, "\n%s\n", check)
		for _, f := range byCheck[check] {
			if f.Element == "" {
				fmt.Fprintf(w, "  %-8s %s\n", f.Severity, f.Detail)
				continue
			}
			fmt.Fprintf(w, "  %-8s %s: %s\n", f.Severity, f.Element, f.Detail)
		}
	}
	if notes > 0 {
		fmt.Fprintf(w, "\n%d note(s) suppressed; pass -v to list them\n", notes)
	}
	fmt.Fprintf(w, "\n%d mismatch(es)\n", r.Mismatches())
}

// WriteMissing renders the coverage worklist grouped by parent path, which is
// the order the registry would be extended in.
func (r *Report) WriteMissing(w io.Writer) {
	if len(r.Missing) == 0 {
		fmt.Fprintln(w, "the registry declares every element the loaded schemas do")
		return
	}
	fmt.Fprintf(w, "%d schema element(s) the registry does not know:\n", len(r.Missing))
	group := ""
	for _, e := range r.Missing {
		parent := e.Parent
		if e.Global {
			parent = "(global)"
		}
		if parent != group {
			group = parent
			fmt.Fprintf(w, "\n  %s\n", group)
		}
		fmt.Fprintf(w, "    0x%-8X %-32s %s\n", uint32(e.ID), e.Name, e.Type)
	}
}
