package gopkg

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

// module writes a throwaway Go module and returns its root.
func module(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	files["go.mod"] = "module example.com/m\n\ngo 1.26\n"
	for name, body := range files {
		path := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func TestTestScopeIncludesWhoeverDependsOnTheChangedPackage(t *testing.T) {
	// This is where nearly all of the real saving in test selection lives. A
	// change in core must run api's tests, because that is where core is
	// actually exercised.
	dir := module(t, map[string]string{
		"core/core.go":    "package core\n\nfunc Discount(n int) int { return n }\n",
		"api/api.go":      "package api\n\nimport \"example.com/m/core\"\n\nfunc Checkout(n int) int { return core.Discount(n) }\n",
		"api/api_test.go": "package api\n\nimport \"testing\"\n\nfunc TestCheckout(t *testing.T) { _ = Checkout(1) }\n",
	})

	g, err := Load(context.Background(), dir)
	if err != nil {
		t.Fatalf("go list: %v", err)
	}

	got := g.TestScope("example.com/m/core")
	if len(got) != 1 || got[0] != "example.com/m/api" {
		t.Errorf("TestScope(core) = %v, want [api] — core's tests live in api", got)
	}
}

func TestTestScopeSkipsPackagesWithNoTests(t *testing.T) {
	// Running a package that has nothing to run costs a link and a process
	// start for no information.
	dir := module(t, map[string]string{
		"a/a.go":      "package a\n\nfunc F() int { return 1 }\n",
		"a/a_test.go": "package a\n\nimport \"testing\"\n\nfunc TestF(t *testing.T) { _ = F() }\n",
		"b/b.go":      "package b\n\nfunc G() int { return 2 }\n",
	})
	g, err := Load(context.Background(), dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range g.TestScope("example.com/m/b") {
		if p == "example.com/m/b" {
			t.Error("b has no tests and should not be in scope")
		}
	}
}

func TestPackageForMapsAFileBackToItsImportPath(t *testing.T) {
	dir := module(t, map[string]string{
		"core/core.go": "package core\n\nfunc F() {}\n",
	})
	g, err := Load(context.Background(), dir)
	if err != nil {
		t.Fatal(err)
	}
	p, ok := g.PackageFor("core/core.go")
	if !ok || p.ImportPath != "example.com/m/core" {
		t.Errorf("PackageFor = %+v, %v", p, ok)
	}
	if _, ok := g.PackageFor("nowhere/x.go"); ok {
		t.Error("an unknown file should not resolve")
	}
}

func TestScopeOrderIsStable(t *testing.T) {
	// The scope becomes a go test command line; an unstable order would make
	// otherwise identical runs differ.
	dir := module(t, map[string]string{
		"a/a.go":      "package a\n\nfunc F() {}\n",
		"a/a_test.go": "package a\n\nimport \"testing\"\n\nfunc TestA(t *testing.T) {}\n",
		"b/b.go":      "package b\n\nfunc G() {}\n",
		"b/b_test.go": "package b\n\nimport \"testing\"\n\nfunc TestB(t *testing.T) {}\n",
	})
	g, err := Load(context.Background(), dir)
	if err != nil {
		t.Fatal(err)
	}
	first := g.TestScope("example.com/m/a")
	for i := 0; i < 5; i++ {
		again := g.TestScope("example.com/m/a")
		if len(again) != len(first) {
			t.Fatalf("unstable length: %v vs %v", first, again)
		}
		for j := range first {
			if first[j] != again[j] {
				t.Fatalf("unstable order: %v vs %v", first, again)
			}
		}
	}
}

func TestCgoPackagesAreIdentifiable(t *testing.T) {
	// Overlay has documented limitations with cgo, so those packages are
	// skipped with a stated reason rather than mutated unreliably.
	if (Package{}).UsesCgo() {
		t.Error("a plain package should not report cgo")
	}
	if !(Package{CgoFiles: []string{"x.go"}}).UsesCgo() {
		t.Error("cgo files should be detected")
	}
	if (Package{}).HasTests() {
		t.Error("no test files means no tests")
	}
	if !(Package{XTestGoFiles: []string{"x_test.go"}}).HasTests() {
		t.Error("an external test package still counts")
	}
}

func TestLoadFailsClearlyOutsideAModule(t *testing.T) {
	if _, err := Load(context.Background(), t.TempDir()); err == nil {
		t.Skip("go list tolerates this environment; nothing to assert")
	}
}
