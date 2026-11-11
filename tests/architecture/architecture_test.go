package architecture

import (
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"testing"
)

// buildTags are all build tags of the module, so that every file is checked.
var buildTags = []string{"e2e", "performance"}

func repoRoot(t *testing.T) string {
	t.Helper()
	_, file, _, _ := runtime.Caller(0)
	return filepath.Clean(filepath.Join(filepath.Dir(file), "..", ".."))
}

func needTools(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath(Arc42CLI[0]); err != nil {
		t.Fatalf("the architecture test needs Node.js (%s) for the arc42 CLI: %v", Arc42CLI[0], err)
	}
}

// TestArchitecture checks the code of this repository against docs/architecture/.
func TestArchitecture(t *testing.T) {
	needTools(t)
	root := repoRoot(t)
	m, err := LoadModel(filepath.Join(root, "docs", "architecture"))
	if err != nil {
		t.Fatal(err)
	}
	pkgs, err := LoadPackages(root, buildTags)
	if err != nil {
		t.Fatal(err)
	}
	if len(pkgs) == 0 {
		t.Fatal("go list found no packages")
	}
	for _, v := range Check(root, m, pkgs) {
		t.Error(v)
	}
}

// TestRulesCatchViolations runs the check on a small workspace with deliberate violations. It
// proves that each rule fails when it should.
func TestRulesCatchViolations(t *testing.T) {
	needTools(t)
	root := filepath.Join(repoRoot(t), "tests", "architecture", "testdata", "violations")
	m, err := LoadModel(filepath.Join(root, "arch"))
	if err != nil {
		t.Fatal(err)
	}
	pkgs, err := LoadPackages(root, nil)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, v := range Check(root, m, pkgs) {
		got = append(got, v.String())
	}
	sort.Strings(got)
	want := []string{
		// a sibling uses the inside of another building block
		"import: beta imports alpha/inner: bb-beta uses the inside of bb-alpha without an interface",
		// a building block uses an interface it does not require
		"import: gamma imports alpha/api: bb-gamma uses if-alpha without requiring it",
		// an actor uses the inside of a building block
		"import: tests imports alpha/inner: an actor (tests) uses the inside of bb-alpha",
		// an interface path outside its provider
		"model: interface if-beta: path gamma is not inside the path beta of its provider bb-beta",
		// a building block without a path
		"model: building block bb-delta has no path",
		// code without a building block
		"owner: package stray belongs to no building block",
		// a sibling uses the interface of its parent that another child realizes
		"import: omega/two imports omega/one/api: bb-two uses if-omega without requiring it",
		// x-realized-by names a block that is not a direct child of the provider
		"model: interface if-bad: x-realized-by bb-alpha is not a direct child of the provider bb-omega",
		// a required interface that is never imported
		"requires: building block bb-beta requires if-alpha but does not import its path alpha/api",
		// two interfaces share a path
		"model: path alpha/api is shared by if-alpha and if-alpha-twin",
		// an interface shares the path of a building block
		"model: path gamma is shared by bb-gamma and if-beta",
		"model: path omega is shared by bb-omega and if-bad",
	}
	for _, w := range want {
		found := false
		for _, g := range got {
			if strings.HasPrefix(g, w) {
				found = true
			}
		}
		if !found {
			t.Errorf("violation not found: %s", w)
		}
	}
	if len(got) != len(want) {
		t.Errorf("got %d violations, want %d:\n%s", len(got), len(want), strings.Join(got, "\n"))
	}
}
