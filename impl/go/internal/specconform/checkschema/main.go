// Command checkschema checks the built-in go/matroska registry against the
// official EBML Schema documents published by the IETF CELLAR working group.
//
// The schema documents are CC-BY-4.0 works and are deliberately NOT vendored
// into this repository: they are a development-time input, so this tool takes
// their paths and the caller fetches them. The conformance-check skill
// (.claude/skills/conformance-check) does the fetching.
//
//	go run ./internal/specconform/checkschema -schema ebml.xml -schema ebml_matroska.xml
//
// It exits non-zero when the registry CONTRADICTS a schema. Elements the
// registry simply does not know are coverage, not failure, and are listed by
// -missing.
package main

import (
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/yacchi/ebml/impl/go/internal/specconform"
)

type schemaPaths []string

func (p *schemaPaths) String() string     { return fmt.Sprint(*p) }
func (p *schemaPaths) Set(v string) error { *p = append(*p, v); return nil }

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

// run holds everything but the exit call, so the exit codes this tool is read by
// -- 1 for a defect, 2 for a usage or input failure -- are testable. It is the
// same seam cmd/ebml uses.
func run(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("checkschema", flag.ContinueOnError)
	fs.SetOutput(stderr)
	var paths schemaPaths
	fs.Var(&paths, "schema", "path to an EBML Schema XML document (repeatable)")
	verbose := fs.Bool("v", false, "list the declared divergences as well as the defects")
	missing := fs.Bool("missing", false, "list the schema elements the registry does not know")
	if err := fs.Parse(args); err != nil {
		return 2
	}

	if len(paths) == 0 {
		fmt.Fprintln(stderr, "checkschema: at least one -schema is required")
		fs.Usage()
		return 2
	}

	schemas := make([]*specconform.Schema, 0, len(paths))
	for _, path := range paths {
		schema, err := specconform.LoadSchema(path)
		if err != nil {
			fmt.Fprintf(stderr, "checkschema: %v\n", err)
			return 2
		}
		schemas = append(schemas, schema)
	}

	report := specconform.Check(schemas...)
	report.WriteText(stdout, *verbose)
	if *missing {
		fmt.Fprintln(stdout)
		report.WriteMissing(stdout)
	}
	if report.Mismatches() > 0 {
		return 1
	}
	return 0
}
