// Package archtest pins the core package dependency graph.
package archtest

import (
	"bufio"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"testing"
)

const modulePath = "github.com/yacchi/ebml"

// TestCoreDependencyGraph protects the parser sanctuary: parser is a
// StAX-shaped reader, so it must never reach the retained document state in
// tree. The graph is computed from the packages go list sees, not maintained
// by hand; the table makes an intentional graph change visible in a diff.
func TestCoreDependencyGraph(t *testing.T) {
	expected := map[string][]string{
		modulePath + "/parser":   nil,
		modulePath + "/writer":   {modulePath + "/parser"},
		modulePath + "/matroska": {modulePath + "/parser"},
		modulePath + "/tree": {
			modulePath + "/parser",
			modulePath + "/matroska",
			modulePath + "/writer",
		},
		modulePath + "/stream": {modulePath + "/parser"},
	}

	_, filename, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	moduleRoot := filepath.Clean(filepath.Join(filepath.Dir(filename), "../.."))

	for pkg, want := range expected {
		got := packageDependencies(t, moduleRoot, pkg)
		sort.Strings(want)
		if strings.Join(got, "\n") != strings.Join(want, "\n") {
			t.Errorf("%s imports:\n%s\nwant:\n%s", pkg, strings.Join(got, "\n"), strings.Join(want, "\n"))
		}
		for _, dep := range got {
			if strings.HasPrefix(dep, modulePath+"/ext/") {
				t.Errorf("core package %s imports ext package %s; core must not depend on ext", pkg, dep)
			}
		}
	}
}

func packageDependencies(t *testing.T, moduleRoot, pkg string) []string {
	t.Helper()
	cmd := exec.Command("go", "list", "-deps", "-f", "{{.ImportPath}}", pkg)
	cmd.Dir = moduleRoot
	output, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatalf("go list %s: %v", pkg, err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatalf("go list %s: %v", pkg, err)
	}

	var deps []string
	scanner := bufio.NewScanner(output)
	for scanner.Scan() {
		importPath := scanner.Text()
		if importPath == pkg {
			continue
		}
		if importPath == modulePath || strings.HasPrefix(importPath, modulePath+"/") {
			deps = append(deps, importPath)
		}
	}
	if err := scanner.Err(); err != nil {
		t.Fatalf("read go list %s: %v", pkg, err)
	}
	if err := cmd.Wait(); err != nil {
		t.Fatalf("go list %s: %v", pkg, err)
	}
	sort.Strings(deps)
	return deps
}
