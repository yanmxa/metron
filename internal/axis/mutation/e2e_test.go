package mutation_test

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/yanmxa/metron/internal/axis"
	"github.com/yanmxa/metron/internal/axis/mutation"
	"github.com/yanmxa/metron/internal/target"
)

const source = `package calc

// Classify is deliberately small: the point is whether the tests pin it.
func Classify(n int) string {
	if n < 0 {
		return "negative"
	}
	if n > 10 {
		return "big"
	}
	return "small"
}
`

const sharpTest = `package calc

import "testing"

func TestClassify(t *testing.T) {
	cases := map[int]string{-1: "negative", 0: "small", 10: "small", 11: "big"}
	for in, want := range cases {
		if got := Classify(in); got != want {
			t.Fatalf("Classify(%d) = %q, want %q", in, got, want)
		}
	}
}
`

const blindTest = `package calc

import "testing"

// Runs every line and checks almost nothing.
func TestClassify(t *testing.T) {
	for _, n := range []int{-1, 0, 10, 11} {
		if Classify(n) == "" {
			t.Fatal("empty")
		}
	}
}
`

func mutationRepo(t *testing.T, test string) string {
	t.Helper()
	for _, bin := range []string{"git", "go"} {
		if _, err := exec.LookPath(bin); err != nil {
			t.Skipf("%s not available", bin)
		}
	}
	dir := t.TempDir()

	must := func(name, body string) {
		p := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	git := func(args ...string) {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
		}
	}

	must("go.mod", "module example.com/calc\n\ngo 1.26\n")
	must("calc/placeholder.go", "package calc\n")
	git("init", "-q", "-b", "main")
	git("config", "user.email", "t@example.com")
	git("config", "user.name", "t")
	git("add", "-A")
	git("commit", "-qm", "base")

	must("calc/calc.go", source)
	must("calc/calc_test.go", test)
	return dir
}

func score(t *testing.T, dir string) (*axis.Result, float64) {
	t.Helper()
	tgt, err := target.Resolve(context.Background(), dir, "main")
	if err != nil {
		t.Fatal(err)
	}
	cfg := mutation.DefaultConfig()
	cfg.Budget = 4 * time.Minute
	cfg.BaselineRounds = 1 // the screen is exercised elsewhere; keep this quick

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	res, err := mutation.New(cfg).Run(ctx, tgt, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range res.Measures {
		if m.Key == "mutation.score" {
			if m.Status == axis.StatusUnmeasured {
				t.Fatalf("score unmeasured: %s", m.Note)
			}
			return res, m.Value
		}
	}
	t.Fatal("no mutation.score")
	return nil, 0
}

func TestSharpTestsScoreFarAboveBlindOnesAtTheSameCoverage(t *testing.T) {
	if testing.Short() {
		t.Skip("runs a real test suite per mutant")
	}
	// Both suites execute every line of Classify. Only the assertions differ,
	// and that difference is the entire reason this tool exists.
	_, sharp := score(t, mutationRepo(t, sharpTest))
	blindRes, blind := score(t, mutationRepo(t, blindTest))

	if sharp <= blind {
		t.Fatalf("sharp %.2f did not beat blind %.2f", sharp, blind)
	}
	if sharp < 0.9 {
		t.Errorf("sharp suite scored %.2f; it pins every branch and boundary", sharp)
	}
	if blind > 0.5 {
		t.Errorf("blind suite scored %.2f; it asserts almost nothing", blind)
	}

	// Every survivor must say which assertion is missing, or the report is a
	// complaint rather than a task list.
	if len(blindRes.Observations) == 0 {
		t.Fatal("no survivors reported for the blind suite")
	}
	for _, o := range blindRes.Observations {
		if o.Detail == "" {
			t.Errorf("%s:%d has no guidance", o.Path, o.Line)
		}
		if o.Before == "" || o.After == "" {
			t.Errorf("%s:%d has no before/after", o.Path, o.Line)
		}
	}
}

func TestPerFunctionRecordsAreEmittedForCRAP(t *testing.T) {
	if testing.Short() {
		t.Skip("runs a real test suite per mutant")
	}
	res, _ := score(t, mutationRepo(t, blindTest))
	if len(res.Funcs) == 0 {
		t.Fatal("no per-function records; CRAP cannot be computed")
	}
	for _, f := range res.Funcs {
		if f.Mutants == 0 {
			t.Errorf("%s has no mutants recorded", f.Function)
		}
		if f.Detected > f.Mutants {
			t.Errorf("%s: detected %d > mutants %d", f.Function, f.Detected, f.Mutants)
		}
	}
}

func TestUnavailableWhenThereIsNothingToMeasure(t *testing.T) {
	dir := t.TempDir()
	tgt := &target.Target{Root: dir}
	ok, why := mutation.New(mutation.DefaultConfig()).Available(context.Background(), tgt)
	if ok {
		t.Error("a directory with no Go files is not measurable")
	}
	if why == "" {
		t.Error("it must say why rather than failing silently")
	}
}
