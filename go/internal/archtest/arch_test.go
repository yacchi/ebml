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
//
// It also pins crc as a LEAF, which is why that package exists at all. The
// CRC-32 checksum primitive is needed on both sides -- writer emits a checksum,
// tree verifies one -- and this test confines writer to parser, so writer may
// not reach it through matroska. A primitive placed above writer would therefore
// have to be copied into writer, and the two copies would drift the way the
// stream boundary rule's three copies drifted before they were merged: each on
// its own schedule, each still self-consistent. Sitting below both packages is
// the only position from which one implementation can serve both, so crc must
// keep importing nothing from this module.
func TestCoreDependencyGraph(t *testing.T) {
	expected := map[string][]string{
		modulePath + "/parser":   nil,
		modulePath + "/crc":      nil,
		modulePath + "/writer":   {modulePath + "/crc", modulePath + "/parser"},
		modulePath + "/matroska": {modulePath + "/parser"},
		modulePath + "/tree": {
			modulePath + "/parser",
			modulePath + "/matroska",
			modulePath + "/writer",
			// VerifyChecksum sums the covered bytes with the one checksum
			// primitive; crc is a leaf package and pulls nothing else in.
			modulePath + "/crc",
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
