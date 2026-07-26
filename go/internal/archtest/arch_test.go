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

// TestExtPackagesAreLeaves pins the other half of the ext rule. Core must not
// depend on ext, which TestCoreDependencyGraph already checks; this checks that
// no ext package depends on ANOTHER ext package either.
//
// An ext package is a way of USING the core, and a way of using something is
// never a prerequisite of another way of using it. The edge this forbids existed
// twice and each time it was a capability wearing the wrong name: ext/fragment
// imported ext/tags for Fragment.Tag and Fragment.Tags, which were ext/tags
// applied to Target{} behind a plainer spelling, and ext/tags imported ext/scope
// for a Read(*scope.Scope) that named the narrow case with the base name. Both
// are gone -- a fragment's tags are tags.Read(frag.Segment), and a scope's are
// tags.ReadFrom(sc) through an interface scope satisfies without knowing it
// exists -- and neither may come back through a convenience accessor.
//
// A TEST may still import a sibling ext package: go list -deps reports the
// non-test dependencies, which is the shipped graph, and proving that ext/tags
// reads what ext/fragment retains is exactly what such a test is for.
func TestExtPackagesAreLeaves(t *testing.T) {
	_, filename, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	moduleRoot := filepath.Clean(filepath.Join(filepath.Dir(filename), "../.."))

	// The list is discovered, never written down: a prohibition that skips a
	// package it has not heard of is not a prohibition.
	pkgs := listPackages(t, moduleRoot, "./ext/...")
	if len(pkgs) == 0 {
		t.Fatal("no ext packages found")
	}
	for _, pkg := range pkgs {
		for _, dep := range packageDependencies(t, moduleRoot, pkg) {
			if strings.HasPrefix(dep, modulePath+"/ext/") {
				t.Errorf("ext package %s imports ext package %s; an ext package is a way of using the core, not a prerequisite of another ext package", pkg, dep)
			}
		}
	}
}

func listPackages(t *testing.T, moduleRoot, pattern string) []string {
	t.Helper()
	cmd := exec.Command("go", "list", "-f", "{{.ImportPath}}", pattern)
	cmd.Dir = moduleRoot
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("go list %s: %v", pattern, err)
	}
	var pkgs []string
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if line != "" {
			pkgs = append(pkgs, line)
		}
	}
	sort.Strings(pkgs)
	return pkgs
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
