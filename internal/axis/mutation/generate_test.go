package mutation

import (
	"go/ast"
	"go/importer"
	"go/parser"
	"go/token"
	"go/types"
	"strings"
	"testing"
)

// gen type-checks a source snippet and generates mutants for every function.
func gen(t *testing.T, src string, ops ...string) ([]Mutant, *Generator) {
	t.Helper()
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "x.go", src, 0)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	info := &types.Info{
		Types: map[ast.Expr]types.TypeAndValue{},
		Defs:  map[*ast.Ident]types.Object{},
		Uses:  map[*ast.Ident]types.Object{},
	}
	conf := types.Config{Importer: importer.Default(), Error: func(error) {}}
	_, _ = conf.Check("p", fset, []*ast.File{f}, info) // type errors are tolerated

	if len(ops) == 0 {
		ops = DefaultOperators()
	}
	g := NewGenerator(fset, info, ops)

	var spans []FuncSpan
	for _, d := range f.Decls {
		if fd, ok := d.(*ast.FuncDecl); ok && fd.Body != nil {
			spans = append(spans, FuncSpan{Label: fd.Name.Name, Decl: fd})
		}
	}
	return g.Generate("x.go", "p", []byte(src), spans), g
}

func opsOf(ms []Mutant) map[string]int {
	out := map[string]int{}
	for _, m := range ms {
		out[m.Operator]++
	}
	return out
}

func TestArithmeticOnStringsIsSuppressed(t *testing.T) {
	// Go overloads + for strings and does not overload -, so an arithmetic
	// mutant on a concatenation is guaranteed not to compile. On one real file
	// this was 87% of everything the arithmetic operator produced.
	ms, g := gen(t, `package p
func concat(a, b string) string { return a + b }
`, OpArithmeticBase)
	if len(ms) != 0 {
		t.Errorf("got %d mutants, want 0: %+v", len(ms), ms)
	}
	if len(g.Suppressed) == 0 {
		t.Error("the suppression should be recorded, not silent")
	}

	ms, _ = gen(t, `package p
func add(a, b int) int { return a + b }
`, OpArithmeticBase)
	if len(ms) != 1 {
		t.Fatalf("numeric addition: got %d mutants, want 1", len(ms))
	}
	if ms[0].After != "func add(a, b int) int { return a - b }" {
		t.Errorf("after = %q", ms[0].After)
	}
}

func TestByteSpliceKeepsTheFileLineForLine(t *testing.T) {
	// Coverage blocks, compiler positions and the before/after report all rely
	// on the mutated file having exactly the same shape.
	src := `package p

func f(n int) bool {
	if n > 10 {
		return true
	}
	return false
}
`
	ms, _ := gen(t, src, OpConditionalsBoundary)
	if len(ms) != 1 {
		t.Fatalf("got %d mutants, want 1", len(ms))
	}
	out := ms[0].Apply([]byte(src))
	if a, b := strings.Count(src, "\n"), strings.Count(string(out), "\n"); a != b {
		t.Errorf("line count changed: %d -> %d", a, b)
	}
	if !strings.Contains(string(out), "if n >= 10 {") {
		t.Errorf("mutation not applied:\n%s", out)
	}
}

func TestRemoveStatementBlanksWithoutMovingLines(t *testing.T) {
	src := `package p

func f() {
	work()
	more()
}

func work() {}
func more() {}
`
	ms, _ := gen(t, src, OpRemoveStatement)
	if len(ms) != 2 {
		t.Fatalf("got %d mutants, want 2", len(ms))
	}
	out := string(ms[0].Apply([]byte(src)))
	if strings.Count(src, "\n") != strings.Count(out, "\n") {
		t.Error("removing a statement must not move the lines below it")
	}
	if strings.Contains(out, "\twork()\n") {
		t.Errorf("statement not removed:\n%s", out)
	}
	if !strings.Contains(out, "\tmore()\n") {
		t.Error("only the targeted statement should go")
	}
}

func TestObservabilityCallsAreNotRemoved(t *testing.T) {
	// Deleting a log line is invisible to any reasonable test, so the mutant is
	// unkillable by construction and only depresses the score.
	ms, _ := gen(t, `package p

import "log"

func f() {
	log.Printf("hello")
	panic("boom")
}
`, OpRemoveStatement)
	if len(ms) != 0 {
		t.Errorf("got %d mutants, want 0: %+v", len(ms), opsOf(ms))
	}
}

func TestNilErrorReturnDropsTheError(t *testing.T) {
	ms, _ := gen(t, `package p

func f(err error) error {
	if err != nil {
		return err
	}
	return nil
}
`, OpNilErrorReturn)
	if len(ms) != 1 {
		t.Fatalf("got %d mutants, want 1 (the already-nil return is skipped): %+v", len(ms), ms)
	}
	if !strings.Contains(ms[0].After, "return nil") {
		t.Errorf("after = %q", ms[0].After)
	}
}

func TestLabelledBreakOnlyConvertsForLoops(t *testing.T) {
	// `continue L` where L labels a switch is a compile error.
	ms, _ := gen(t, `package p

func f(xs []int) {
loop:
	for range xs {
		break loop
	}
sw:
	switch len(xs) {
	case 1:
		break sw
	}
}
`, OpInvertLoopCtrl)
	if len(ms) != 1 {
		t.Fatalf("got %d mutants, want 1 (only the loop label): %+v", len(ms), ms)
	}
	if ms[0].Line != 6 {
		t.Errorf("mutated line %d, want the labelled break inside the loop", ms[0].Line)
	}
}

func TestUnsignedComparisonWithZeroIsStillMutated(t *testing.T) {
	// It is tempting to suppress these as equivalent, since `u >= 0` is a
	// tautology. But the mutant `u > 0` behaves differently at zero and a test
	// passing zero kills it. Suppressing it would drop a real mutant from the
	// denominator and hide the gap rather than report it.
	ms, _ := gen(t, `package p
func f(u uint) bool { return u >= 0 }
`, OpConditionalsBoundary)
	if len(ms) != 1 {
		t.Errorf("got %d mutants, want 1: %+v", len(ms), ms)
	}
}

func TestMutantIDsAreContentAddressedAndOrderIsStable(t *testing.T) {
	src := `package p

func f(a, b int) bool {
	if a > b {
		return a < b
	}
	return a == b
}
`
	first, _ := gen(t, src)
	second, _ := gen(t, src)
	if len(first) != len(second) || len(first) == 0 {
		t.Fatalf("unstable count: %d vs %d", len(first), len(second))
	}
	for i := range first {
		if first[i].ID != second[i].ID {
			t.Fatalf("mutant %d: id %s != %s — ids must survive re-runs and truncation",
				i, first[i].ID, second[i].ID)
		}
		if i > 0 {
			p, c := first[i-1], first[i]
			if p.Line > c.Line || (p.Line == c.Line && p.Col > c.Col) {
				t.Errorf("not sorted at %d: %d:%d then %d:%d", i, p.Line, p.Col, c.Line, c.Col)
			}
		}
	}
}

func TestSurvivorsSayWhichCaseIsMissing(t *testing.T) {
	// A surviving mutant is a specification for the absent assertion. Which one
	// follows from the operator and its operands, so metron can state it rather
	// than leaving an agent to infer it.
	//
	// Phrased as what to assert, never as a claim about what the tests do: a
	// survivor does not prove the input is unreached, only that nothing checks
	// the difference. The fixture below is exactly that case — a test passes
	// "gold" and never asserts the discount.
	tests := []struct {
		name string
		src  string
		op   string
		want string
	}{
		{
			"boundary names the exact value",
			"package p\nfunc f(total int) bool { return total < 0 }\n",
			OpConditionalsBoundary,
			"assert the behaviour at the boundary total == 0",
		},
		{
			"forcing true leaves the false side unchecked",
			"package p\nfunc f(n int) int { if n > 5 { return 1 }; return 0 }\n",
			OpConditionForce,
			"assert the behaviour that depends on n > 5 being false",
		},
		{
			"a dropped error names the assertion to add",
			"package p\nfunc f(err error) error { if err != nil { return err }; return nil }\n",
			OpNilErrorReturn,
			"assert that this path returns a non-nil error",
		},
		{
			"an unobserved call names itself",
			"package p\nfunc f() { work() }\nfunc work() {}\n",
			OpRemoveStatement,
			"assert the effect of work()",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ms, _ := gen(t, tc.src, tc.op)
			if len(ms) == 0 {
				t.Fatal("no mutants generated")
			}
			for _, m := range ms {
				if m.Guidance == tc.want {
					return
				}
			}
			var got []string
			for _, m := range ms {
				got = append(got, m.Guidance)
			}
			t.Errorf("want %q\n got %q", tc.want, got)
		})
	}
}

func TestEveryOperatorProducesGuidance(t *testing.T) {
	// A survivor with no instruction is the thing this is meant to remove.
	ms, _ := gen(t, `package p

func f(a, b int, s []int, err error) (int, error) {
	if a > b && a < 10 {
		a++
	}
	a += b
	for range s {
		break
	}
	if err != nil {
		return 0, err
	}
	work()
	return a + b, nil
}

func work() {}
`)
	if len(ms) == 0 {
		t.Fatal("no mutants generated")
	}
	seen := map[string]bool{}
	for _, m := range ms {
		if m.Guidance == "" {
			t.Errorf("%s at line %d has no guidance", m.Operator, m.Line)
		}
		seen[m.Operator] = true
	}
	if len(seen) < 5 {
		t.Errorf("fixture only exercised %d operators: %v", len(seen), seen)
	}
}

func TestGuidanceNeverClaimsWhatTheTestsDo(t *testing.T) {
	// A survivor cannot distinguish "the input is never supplied" from "the
	// input is supplied and the result is never checked". Guidance that asserts
	// the first would be wrong half the time, so none of it does.
	ms, _ := gen(t, `package p

func f(a, b int, s []int, err error) (int, error) {
	if a > b && a < 10 {
		a++
	}
	a += b
	for range s {
		break
	}
	if err != nil {
		return 0, err
	}
	work()
	return a + b, nil
}

func work() {}
`)
	for _, m := range ms {
		for _, claim := range []string{"no test", "nothing tests", "never tested", "untested"} {
			if strings.Contains(m.Guidance, claim) {
				t.Errorf("%s guidance claims what the tests do: %q", m.Operator, m.Guidance)
			}
		}
	}
}
