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
	"os"

	"github.com/yacchi/ebml/impl/go/internal/specconform"
)

type schemaPaths []string

func (p *schemaPaths) String() string     { return fmt.Sprint(*p) }
func (p *schemaPaths) Set(v string) error { *p = append(*p, v); return nil }

func main() {
	var paths schemaPaths
	flag.Var(&paths, "schema", "path to an EBML Schema XML document (repeatable)")
	verbose := flag.Bool("v", false, "list the declared divergences as well as the defects")
	missing := flag.Bool("missing", false, "list the schema elements the registry does not know")
	flag.Parse()

	if len(paths) == 0 {
		fmt.Fprintln(os.Stderr, "checkschema: at least one -schema is required")
		flag.Usage()
		os.Exit(2)
	}

	schemas := make([]*specconform.Schema, 0, len(paths))
	for _, path := range paths {
		schema, err := specconform.LoadSchema(path)
		if err != nil {
			fmt.Fprintf(os.Stderr, "checkschema: %v\n", err)
			os.Exit(2)
		}
		schemas = append(schemas, schema)
	}

	report := specconform.Check(schemas...)
	report.WriteText(os.Stdout, *verbose)
	if *missing {
		fmt.Println()
		report.WriteMissing(os.Stdout)
	}
	if report.Mismatches() > 0 {
		os.Exit(1)
	}
}
