// Package crap computes Change Risk Anti-Patterns and uses it to order what the
// complexity axis reports.
//
// CRAP was defined by Alberto Savoia and Bob Evans for Crap4j: complexity is
// forgiven when the code is tested and punished hard when it is not. A function
// with cyclomatic complexity 10 scores 10 when fully covered and 110 when not.
//
// metron departs from the original in one place, and it matters. Crap4j feeds
// on line coverage — the very number this tool exists to distrust. A function
// with 100% coverage and no assertions scores as safe under the original and
// stays dangerous here, because the coverage term is the mutation score
// instead. That is the case CRAP was invented to catch and the coverage-based
// version misses.
//
// It deliberately does not become a reading of its own. It is per-function,
// and its job is to answer "which one do I fix first", not to gate a build.
package crap

import (
	"fmt"
	"math"
	"sort"

	"github.com/yanmxa/metron/internal/axis"
)

// Threshold is the conventional line above which Crap4j calls a method crappy.
const Threshold = 30.0

// Score returns CRAP for one function.
//
//	CRAP = comp² × (1 − tested)³ + comp
//
// tested is clamped to [0,1]. At full confidence the risk term vanishes and the
// score is just the complexity; at zero it is comp² + comp.
func Score(cyclomatic int, tested float64) float64 {
	c := float64(cyclomatic)
	tested = math.Max(0, math.Min(1, tested))
	untested := 1 - tested
	return c*c*untested*untested*untested + c
}

func sprintf(f string, a ...any) string { return fmt.Sprintf(f, a...) }

// Ranked is a function with a CRAP score attached.
type Ranked struct {
	axis.PerFunc
	CRAP   float64
	Tested float64
}

// Rank merges the per-function readings from every axis and scores what it can.
//
// A function with no mutants has no CRAP: nothing was measured about how well
// it is tested, and inventing a coverage number would be worse than omitting
// the score. Those come back with CRAP == 0 and ok == false.
func Rank(results []*axis.Result) []Ranked {
	merged := map[string]*axis.PerFunc{}
	for _, r := range results {
		for _, f := range r.Funcs {
			cur, ok := merged[f.Key()]
			if !ok {
				cp := f
				merged[f.Key()] = &cp
				continue
			}
			// Each axis fills in the fields it knows about.
			if f.Cyclomatic > 0 {
				cur.Cyclomatic = f.Cyclomatic
			}
			if f.Cognitive > 0 {
				cur.Cognitive = f.Cognitive
			}
			if f.Delta != 0 {
				cur.Delta = f.Delta
			}
			if f.Line > 0 && cur.Line == 0 {
				cur.Line, cur.EndLine = f.Line, f.EndLine
			}
			cur.Mutants += f.Mutants
			cur.Detected += f.Detected
			cur.IsNew = cur.IsNew || f.IsNew
		}
	}

	out := make([]Ranked, 0, len(merged))
	for _, f := range merged {
		r := Ranked{PerFunc: *f}
		if f.Mutants > 0 {
			r.Tested = float64(f.Detected) / float64(f.Mutants)
			r.CRAP = Score(f.Cyclomatic, r.Tested)
		}
		out = append(out, r)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].CRAP != out[j].CRAP {
			return out[i].CRAP > out[j].CRAP
		}
		if out[i].Path != out[j].Path {
			return out[i].Path < out[j].Path
		}
		return out[i].Line < out[j].Line
	})
	return out
}

// Scored reports whether a CRAP score could be computed for this function.
func (r Ranked) Scored() bool { return r.Mutants > 0 }

// Crappy reports whether the function is over the conventional threshold.
func (r Ranked) Crappy() bool { return r.Scored() && r.CRAP > Threshold }

// Apply attaches CRAP to the complexity axis's findings, reorders them worst
// first, and adds any function that is risky enough to deserve attention even
// though its complexity alone was inside range.
//
// That last part is the whole reason CRAP is here. A function with cyclomatic
// complexity 6 passes a threshold of 15 comfortably; if a fifth of the mutants
// in it survive, it is still the most dangerous thing in the change.
func Apply(results []*axis.Result) {
	ranked := Rank(results)
	if len(ranked) == 0 {
		return
	}
	byKey := make(map[string]Ranked, len(ranked))
	for _, r := range ranked {
		byKey[r.Key()] = r
	}

	var complexity *axis.Result
	for _, r := range results {
		if r.AxisID == "complexity" {
			complexity = r
		}
	}
	if complexity == nil {
		return
	}

	seen := make(map[string]bool, len(complexity.Observations))
	for i, o := range complexity.Observations {
		r, ok := byKey[o.Path+":"+functionOf(o)]
		if !ok || !r.Scored() {
			continue
		}
		seen[r.Key()] = true
		complexity.Observations[i].Detail = annotate(o.Detail, r)
	}

	// Promote functions this axis passed over but CRAP says are risky.
	for _, r := range ranked {
		if !r.Crappy() || seen[r.Key()] {
			continue
		}
		complexity.Observations = append(complexity.Observations, axis.Observation{
			Path: r.Path, Line: r.Line, EndLine: r.EndLine, Kind: "risk",
			Title: r.Function + " is the riskiest thing in this change",
			Detail: annotate(
				sprintf("cognitive %d · cyclomatic %d", r.Cognitive, r.Cyclomatic), r),
		})
		seen[r.Key()] = true
	}

	sort.SliceStable(complexity.Observations, func(i, j int) bool {
		a := byKey[complexity.Observations[i].Path+":"+functionOf(complexity.Observations[i])]
		b := byKey[complexity.Observations[j].Path+":"+functionOf(complexity.Observations[j])]
		return a.CRAP > b.CRAP
	})

	if complexity.Diagnostics == nil {
		complexity.Diagnostics = map[string]float64{}
	}
	complexity.Diagnostics["complexity.crap_max"] = ranked[0].CRAP
}

func annotate(detail string, r Ranked) string {
	s := sprintf("CRAP %.0f (%.0f%% of mutants caught)", r.CRAP, r.Tested*100)
	if r.Crappy() {
		s += sprintf(" — over the usual limit of %.0f", Threshold)
	}
	if detail == "" {
		return s
	}
	return s + " · " + detail
}

// functionOf recovers the function name from an observation title, which is
// where the complexity axis puts it.
func functionOf(o axis.Observation) string {
	name := o.Title
	for _, cut := range []string{" (", " is ", " — "} {
		if i := indexOf(name, cut); i >= 0 {
			name = name[:i]
		}
	}
	return name
}

func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}
