package complexity

import (
	"go/ast"
	"go/parser"
	"go/token"
	"testing"
)

func scoreOf(t *testing.T, src string) Score {
	t.Helper()
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "x.go", "package p\n"+src, 0)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	for _, d := range f.Decls {
		if fd, ok := d.(*ast.FuncDecl); ok {
			return Cognitive(fd, fset)
		}
	}
	t.Fatal("no function in source")
	return Score{}
}

func TestCognitiveCountsNestingNotJustBranches(t *testing.T) {
	// The whole point of cognitive over cyclomatic: same number of decisions,
	// very different difficulty. Flat costs 1+1+1; nested costs 1+2+3.
	flat := scoreOf(t, `
func f(a, b, c bool) {
	if a { g() }
	if b { g() }
	if c { g() }
}
func g() {}`)
	nested := scoreOf(t, `
func f(a, b, c bool) {
	if a {
		if b {
			if c { g() }
		}
	}
}
func g() {}`)

	if flat.Cognitive != 3 {
		t.Errorf("flat: cognitive = %d, want 3", flat.Cognitive)
	}
	if nested.Cognitive != 6 {
		t.Errorf("nested: cognitive = %d, want 6", nested.Cognitive)
	}
	if flat.Cyclomatic != nested.Cyclomatic {
		t.Errorf("cyclomatic should not tell these apart: %d vs %d",
			flat.Cyclomatic, nested.Cyclomatic)
	}
}

func TestElseIfTakesAFlatIncrement(t *testing.T) {
	// The reader is already inside the conditional; charging the nesting
	// penalty again would double-bill them.
	got := scoreOf(t, `
func f(a, b, c int) {
	if a > 0 {
		g()
	} else if b > 0 {
		g()
	} else {
		g()
	}
}
func g() {}`)
	if got.Cognitive != 3 {
		t.Errorf("cognitive = %d, want 3 (if + else-if + else, all flat)", got.Cognitive)
	}
}

func TestLogicalSequenceCostsOnePerRun(t *testing.T) {
	tests := []struct {
		name string
		cond string
		want int // the if itself is 1; the rest is the operator sequences
	}{
		{"single and", "a && b", 2},
		{"like operators are one idea", "a && b && c", 2},
		{"mixed operators switch mode", "a && b || c", 3},
		{"three runs", "a && b || c && d", 4},
		{"no logical ops", "a", 1},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := scoreOf(t, `
func f(a, b, c, d bool) {
	if `+tc.cond+` { g() }
}
func g() {}`)
			if got.Cognitive != tc.want {
				t.Errorf("cognitive = %d, want %d", got.Cognitive, tc.want)
			}
		})
	}
}

func TestErrGuardDiscountedOnlyInAdjusted(t *testing.T) {
	// Raw stays comparable with gocognit and SonarSource; adjusted is the one
	// the reference range is set against.
	got := scoreOf(t, `
func f() error {
	a, err := one()
	if err != nil {
		return err
	}
	b, err := two()
	if err != nil {
		return err
	}
	if a > b {
		return nil
	}
	return nil
}
func one() (int, error) { return 0, nil }
func two() (int, error) { return 0, nil }`)

	if got.Cognitive != 3 {
		t.Errorf("raw cognitive = %d, want 3 (two guards + one real branch)", got.Cognitive)
	}
	if got.Adjusted != 1 {
		t.Errorf("adjusted = %d, want 1 (only the real branch)", got.Adjusted)
	}
	if got.ErrGuards != 2 {
		t.Errorf("errGuards = %d, want 2", got.ErrGuards)
	}
}

func TestErrCheckWithRealHandlingIsNotDiscounted(t *testing.T) {
	// A guard that only bails out reads as one token. Anything that actually
	// handles the error is a branch and must be counted in full, or the
	// adjustment would hide real complexity.
	tests := []struct {
		name string
		body string
	}{
		{"has an else", `
	if err != nil {
		return err
	} else {
		g()
	}
	return nil`},
		{"does more than bail", `
	if err != nil {
		log(err)
		metrics()
		return err
	}
	return nil`},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := scoreOf(t, `
func f(err error) error {`+tc.body+`
}
func g() {}
func log(error) {}
func metrics() {}`)
			if got.ErrGuards != 0 {
				t.Errorf("errGuards = %d, want 0 — this is a real branch", got.ErrGuards)
			}
			if got.Adjusted != got.Cognitive {
				t.Errorf("adjusted (%d) should equal cognitive (%d) with nothing to discount",
					got.Adjusted, got.Cognitive)
			}
		})
	}
}

func TestClosureBodyIsReadOneLevelDeeper(t *testing.T) {
	// The FuncLit itself is not a decision, but its contents are nested.
	got := scoreOf(t, `
func f(xs []int) {
	each(func() {
		if len(xs) > 0 { g() }
	})
}
func each(func()) {}
func g() {}`)
	if got.Cognitive != 2 {
		t.Errorf("cognitive = %d, want 2 (if at nesting 1)", got.Cognitive)
	}
}

func TestLabelledJumpCountsAndPlainOneDoesNot(t *testing.T) {
	plain := scoreOf(t, `
func f(xs []int) {
	for range xs {
		break
	}
}`)
	labelled := scoreOf(t, `
func f(xs []int) {
outer:
	for range xs {
		for range xs {
			break outer
		}
	}
}`)
	if plain.Cognitive != 1 {
		t.Errorf("plain break: cognitive = %d, want 1 (the loop only)", plain.Cognitive)
	}
	// for(1) + nested for(1+1) + labelled break(1)
	if labelled.Cognitive != 4 {
		t.Errorf("labelled break: cognitive = %d, want 4", labelled.Cognitive)
	}
}

func TestDirectRecursionCounts(t *testing.T) {
	got := scoreOf(t, `
func f(n int) int {
	if n <= 1 { return 1 }
	return n * f(n-1)
}`)
	if got.Cognitive != 2 {
		t.Errorf("cognitive = %d, want 2 (if + recursion)", got.Cognitive)
	}
}

func TestSurroundingMetrics(t *testing.T) {
	got := scoreOf(t, `
func f(a int, b, c string, _ bool) {
	one()
	two()
	one()
}
func one() {}
func two() {}`)
	if got.Params != 4 {
		t.Errorf("params = %d, want 4 (unnamed still counts)", got.Params)
	}
	if got.FanOut != 2 {
		t.Errorf("fanOut = %d, want 2 (distinct callees)", got.FanOut)
	}
	if got.Lines != 5 {
		t.Errorf("lines = %d, want 5", got.Lines)
	}
}

func TestComplexityHidingInDeclarationsIsCounted(t *testing.T) {
	// `var x = func(){...}` is a declaration statement, not an assignment.
	// Skipping it made complex helpers read as trivial.
	got := scoreOf(t, `
func f(a, b bool) {
	var run = func() {
		if a {
			if b { g() }
		}
	}
	run()
}
func g() {}`)
	// The closure body sits one level deep, so the ifs cost 2 and 3.
	// Before DeclStmt was handled this whole function scored 0.
	if got.Cognitive != 5 {
		t.Errorf("cognitive = %d, want 5", got.Cognitive)
	}
}

func TestElseBodyRaisesNestingPerTheSpec(t *testing.T) {
	// SonarSource increments the nesting level for an else body; gocognit does
	// not. This is metron's one systematic divergence and it is deliberate —
	// code inside an else really is one level deeper for the reader.
	got := scoreOf(t, `
func f(a, b bool) {
	if a {
		g()
	} else {
		if b { g() }
	}
}
func g() {}`)
	if got.Cognitive != 4 {
		t.Errorf("cognitive = %d, want 4 (if 1 + else 1 + inner if at nesting 1 = 2)", got.Cognitive)
	}
}
