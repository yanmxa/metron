package graph

import (
	"os"
	"path/filepath"
	"testing"
)

func TestCalleeNamesSeesBothPlainAndMethodCalls(t *testing.T) {
	got := CalleeNames("a.go", []byte(`package p

func F(s *Server) error {
	helper()
	s.Reload()
	return wrap(s.Name())
}
`))
	names := got["a.go:F"]
	for _, want := range []string{"helper", "Reload", "wrap", "Name"} {
		if !names[want] {
			t.Errorf("missing %q from %v", want, names)
		}
	}
}

func TestCalleeNamesKeysMethodsByReceiver(t *testing.T) {
	// Two methods of the same name on different receivers must not merge, or a
	// change to one would look like a change to the other.
	got := CalleeNames("a.go", []byte(`package p

type A struct{}
type B struct{}

func (a *A) Do() { first() }
func (b B) Do()  { second() }
func Do()        { third() }
`))
	if !got["a.go:(A).Do"]["first"] {
		t.Errorf("(A).Do not keyed separately: %v", got)
	}
	if !got["a.go:(B).Do"]["second"] {
		t.Errorf("(B).Do not keyed separately: %v", got)
	}
	if !got["a.go:Do"]["third"] {
		t.Errorf("the plain function not keyed separately: %v", got)
	}
}

func TestDiffFindsOnlyWhatTheChangeAdded(t *testing.T) {
	// This is what took the graph axis from five false findings on six no-op
	// edits down to zero: a call a function has always made is not news.
	base := CalleeNames("a.go", []byte("package p\n\nfunc F() { old() }\n"))
	cur := CalleeNames("a.go", []byte("package p\n\nfunc F() { old(); fresh() }\n"))

	got := Diff(cur["a.go:F"], base["a.go:F"])
	if len(got) != 1 || !got["fresh"] {
		t.Errorf("Diff = %v, want only fresh", got)
	}
}

func TestNoBaseMeansEveryCallLooksNew(t *testing.T) {
	// A brand-new file has no previous version, so everything it calls is
	// genuinely introduced by the change.
	cur := CalleeNames("a.go", []byte("package p\n\nfunc F() { a(); b() }\n"))
	got := Diff(cur["a.go:F"], nil)
	if len(got) != 2 {
		t.Errorf("Diff = %v, want both", got)
	}
}

func TestUnparseableSourceYieldsNothingRatherThanPanicking(t *testing.T) {
	if got := CalleeNames("a.go", []byte("package p\n\nfunc F( {")); len(got) != 0 {
		t.Errorf("got %v, want nothing from source that does not parse", got)
	}
	if got := CalleeNames("a.go", nil); len(got) != 0 {
		t.Errorf("got %v, want nothing from empty source", got)
	}
}

func TestNewCalleesAnswersPerFunction(t *testing.T) {
	nc := NewCallees{}
	nc.Add("a.go:F", map[string]bool{"fresh": true})
	nc.Add("a.go:G", map[string]bool{}) // nothing gained; must not be recorded

	if !nc.Gained("a.go:F", "fresh") {
		t.Error("F gained fresh")
	}
	if nc.Gained("a.go:F", "old") {
		t.Error("F did not gain old")
	}
	if nc.Introduced("a.go:G") {
		t.Error("G introduced nothing and cannot have bypassed anything")
	}
}

func TestScanIdentsSeparatesUseFromDeclaration(t *testing.T) {
	// The check that keeps the orphan rule honest: Go passes functions as
	// values constantly and the index records no call edge for that.
	dir := t.TempDir()
	write := func(name, body string) {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("a.go", `package p

func declaredOnly() {}

func usedAsValue() {}

func Get() func() { return usedAsValue }
`)

	iu, err := ScanIdents(dir)
	if err != nil {
		t.Fatal(err)
	}
	if iu.Used("declaredOnly") {
		t.Error("declaredOnly appears only in its own declaration")
	}
	if !iu.Used("usedAsValue") {
		t.Error("usedAsValue is returned as a value and is not dead")
	}
}

func TestAppPackageDecidesWhereExportedSymbolsCanBeJudged(t *testing.T) {
	dir := t.TempDir()
	for name, body := range map[string]string{
		"main.go":         "package main\n\nfunc main() {}\n",
		"lib/lib.go":      "package lib\n\nfunc Public() {}\n",
		"internal/i/i.go": "package i\n\nfunc Helper() {}\n",
	} {
		p := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	iu, err := ScanIdents(dir)
	if err != nil {
		t.Fatal(err)
	}

	if !iu.AppPackage("main.go") {
		t.Error("main is not a library; its exports can be judged")
	}
	if !iu.AppPackage("internal/i/i.go") {
		t.Error("internal/ is not importable outside the module")
	}
	if iu.AppPackage("lib/lib.go") {
		t.Error("a library's exported symbols are API; absent callers prove nothing")
	}
}
