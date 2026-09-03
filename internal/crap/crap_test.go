package crap

import (
	"math"
	"testing"

	"github.com/yanmxa/metron/internal/axis"
)

func TestComplexityIsForgivenWhenTested(t *testing.T) {
	// The shape of the metric: the same function is cheap when its behaviour is
	// pinned and expensive when it is not.
	tests := []struct {
		name       string
		cyclomatic int
		tested     float64
		want       float64
	}{
		{"fully pinned costs only its complexity", 10, 1.0, 10},
		{"untested costs comp squared on top", 10, 0.0, 110},
		{"simple and untested is still cheap", 2, 0.0, 6},
		{"complex and half-pinned", 10, 0.5, 22.5},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := Score(tc.cyclomatic, tc.tested); math.Abs(got-tc.want) > 0.01 {
				t.Errorf("Score(%d, %.2f) = %.2f, want %.2f",
					tc.cyclomatic, tc.tested, got, tc.want)
			}
		})
	}
}

func TestScoreClampsNonsenseInput(t *testing.T) {
	if a, b := Score(5, -1), Score(5, 0); a != b {
		t.Errorf("negative coverage should behave as zero: %.2f vs %.2f", a, b)
	}
	if a, b := Score(5, 2), Score(5, 1); a != b {
		t.Errorf("coverage above one should behave as one: %.2f vs %.2f", a, b)
	}
}

func TestRankCombinesTheTwoAxes(t *testing.T) {
	// Neither axis alone knows enough: complexity has the cyclomatic count,
	// mutation has how much of the function is actually pinned.
	results := []*axis.Result{
		{AxisID: "complexity", Funcs: []axis.PerFunc{
			{Path: "a.go", Function: "Risky", Line: 10, Cyclomatic: 10, Cognitive: 12},
			{Path: "a.go", Function: "Safe", Line: 40, Cyclomatic: 10, Cognitive: 12},
		}},
		{AxisID: "mutation", Funcs: []axis.PerFunc{
			{Path: "a.go", Function: "Risky", Mutants: 10, Detected: 1},
			{Path: "a.go", Function: "Safe", Mutants: 10, Detected: 10},
		}},
	}
	got := Rank(results)
	if len(got) != 2 {
		t.Fatalf("got %d functions, want 2", len(got))
	}
	if got[0].Function != "Risky" {
		t.Errorf("worst first: got %q", got[0].Function)
	}
	if !got[0].Crappy() {
		t.Errorf("Risky scored %.1f, expected it over the threshold", got[0].CRAP)
	}
	if got[1].Crappy() {
		t.Errorf("Safe scored %.1f, expected it under the threshold", got[1].CRAP)
	}
	// Identical complexity — only the testing tells them apart.
	if got[0].Cyclomatic != got[1].Cyclomatic {
		t.Fatal("fixture should hold complexity equal")
	}
}

func TestAFunctionWithNoMutantsGetsNoScore(t *testing.T) {
	// Nothing was measured about how well it is tested, and inventing a number
	// would be worse than leaving the score off.
	got := Rank([]*axis.Result{{AxisID: "complexity", Funcs: []axis.PerFunc{
		{Path: "a.go", Function: "Unmeasured", Cyclomatic: 9},
	}}})
	if len(got) != 1 {
		t.Fatalf("got %d, want 1", len(got))
	}
	if got[0].Scored() || got[0].CRAP != 0 {
		t.Errorf("expected no score, got CRAP %.2f", got[0].CRAP)
	}
	if got[0].Crappy() {
		t.Error("an unscored function must not be called crappy")
	}
}

func TestApplyPromotesARiskyFunctionComplexityLetPass(t *testing.T) {
	// The reason CRAP is here: cyclomatic 6 clears a threshold of 15 easily,
	// but if four fifths of its mutants survive it is the worst thing present.
	complexity := &axis.Result{AxisID: "complexity", Funcs: []axis.PerFunc{
		{Path: "a.go", Function: "Sneaky", Line: 10, Cyclomatic: 8, Cognitive: 6},
	}}
	mutation := &axis.Result{AxisID: "mutation", Funcs: []axis.PerFunc{
		{Path: "a.go", Function: "Sneaky", Mutants: 10, Detected: 2},
	}}
	results := []*axis.Result{complexity, mutation}

	if len(complexity.Observations) != 0 {
		t.Fatal("fixture should start with nothing reported")
	}
	Apply(results)

	if len(complexity.Observations) != 1 {
		t.Fatalf("got %d observations, want 1", len(complexity.Observations))
	}
	o := complexity.Observations[0]
	if o.Path != "a.go" || o.Line != 10 {
		t.Errorf("observation points at %s:%d", o.Path, o.Line)
	}
	if !contains(o.Detail, "CRAP") {
		t.Errorf("detail should carry the score: %q", o.Detail)
	}
	if complexity.Diagnostics["complexity.crap_max"] == 0 {
		t.Error("crap_max should reach the diagnostics")
	}
}

func TestApplyAnnotatesAndReordersExistingFindings(t *testing.T) {
	complexity := &axis.Result{
		AxisID: "complexity",
		Observations: []axis.Observation{
			{Path: "a.go", Line: 10, Title: "Mild", Detail: "cognitive 16"},
			{Path: "a.go", Line: 40, Title: "Nasty", Detail: "cognitive 16"},
		},
		Funcs: []axis.PerFunc{
			{Path: "a.go", Function: "Mild", Line: 10, Cyclomatic: 4},
			{Path: "a.go", Function: "Nasty", Line: 40, Cyclomatic: 12},
		},
	}
	mutation := &axis.Result{AxisID: "mutation", Funcs: []axis.PerFunc{
		{Path: "a.go", Function: "Mild", Mutants: 10, Detected: 9},
		{Path: "a.go", Function: "Nasty", Mutants: 10, Detected: 1},
	}}
	Apply([]*axis.Result{complexity, mutation})

	if complexity.Observations[0].Title != "Nasty" {
		t.Errorf("worst should sort first, got %q", complexity.Observations[0].Title)
	}
	for _, o := range complexity.Observations {
		if !contains(o.Detail, "CRAP") {
			t.Errorf("%s lost its annotation: %q", o.Title, o.Detail)
		}
		if !contains(o.Detail, "cognitive 16") {
			t.Errorf("%s lost its original detail: %q", o.Title, o.Detail)
		}
	}
}

func contains(s, sub string) bool { return indexOf(s, sub) >= 0 }

func TestMissingMutationIsSaidOutLoud(t *testing.T) {
	// Printing nothing is what made this look unimplemented. An absent score
	// has to announce itself, the same as an axis that cannot run.
	complexity := &axis.Result{AxisID: "complexity", Funcs: []axis.PerFunc{
		{Path: "a.go", Function: "F", Cyclomatic: 12},
	}}
	Apply([]*axis.Result{complexity})

	if len(complexity.Notes) != 1 || !contains(complexity.Notes[0], "mutation") {
		t.Errorf("notes = %v, want one explaining the ranking is unavailable", complexity.Notes)
	}
	if _, ok := complexity.Diagnostics["complexity.crap_max"]; ok {
		t.Error("no score should be published when nothing was measured")
	}
}
