package matroska

import (
	"go/ast"
	stdparser "go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/yacchi/ebml/parser"
)

func TestRegistryConstantsAreConsistent(t *testing.T) {
	constants := make(map[string]parser.ElementID)
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	fset := token.NewFileSet()
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".go") || strings.HasSuffix(entry.Name(), "_test.go") {
			continue
		}
		file, err := stdparser.ParseFile(fset, filepath.Join(".", entry.Name()), nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		for _, declaration := range file.Decls {
			gen, ok := declaration.(*ast.GenDecl)
			if !ok || gen.Tok.String() != "const" {
				continue
			}
			for _, specification := range gen.Specs {
				spec := specification.(*ast.ValueSpec)
				for index, name := range spec.Names {
					if strings.HasPrefix(name.Name, "ID") {
						literal, ok := spec.Values[index].(*ast.BasicLit)
						if !ok {
							t.Fatalf("%s is not an integer literal", name.Name)
						}
						value, err := strconv.ParseUint(literal.Value, 0, 32)
						if err != nil {
							t.Fatalf("parse %s: %v", name.Name, err)
						}
						constants[name.Name] = parser.ElementID(value)
					}
				}
			}
		}
	}

	exceptions := map[string]string{"IDCRC32": "CRC-32"}
	var missing, mismatched []string
	for constantName, id := range constants {
		expectedName := strings.TrimPrefix(constantName, "ID")
		if exception, ok := exceptions[constantName]; ok {
			expectedName = exception
		}
		info, ok := Lookup(id)
		if !ok {
			missing = append(missing, constantName)
			continue
		}
		if info.Name != expectedName {
			mismatched = append(mismatched, constantName+"="+info.Name)
		}
	}
	for id, info := range elements {
		if reverseID, ok := IDForName(info.Name); !ok {
			mismatched = append(mismatched, info.Name+" has no reverse lookup")
		} else if reverseID != id {
			mismatched = append(mismatched, info.Name+" reverse lookup mismatch")
		}
		found := false
		for _, constantID := range constants {
			if constantID == id {
				found = true
				break
			}
		}
		if !found {
			missing = append(missing, info.Name)
		}
	}
	if len(constants) != len(elements) {
		t.Errorf("ID constant count = %d, registry count = %d", len(constants), len(elements))
	}
	if len(missing) > 0 {
		t.Errorf("missing registry/constants: %s", strings.Join(missing, ", "))
	}
	if len(mismatched) > 0 {
		t.Errorf("name mismatches: %s", strings.Join(mismatched, ", "))
	}
}
