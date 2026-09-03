package panel

import (
	"strings"
	"testing"

	"github.com/yanmxa/metron/internal/axis"
	"github.com/yanmxa/metron/internal/target"
)

func ref(v float64) *float64 { return &v }

func render(t *testing.T, results []*axis.Result, opts ...func(*Panel)) string {
	t.Helper()
	p := &Panel{
		Target: &target.Target{
			BaseRef: "main", HeadDesc: "working tree",
			Files: []target.ChangedFile{{Path: "a.go", Ranges: []target.LineRange{{Start: 1, End: 10}}}},
		},
		Results: results,
	}
	for _, o := range opts {
		o(p)
	}
	return p.Render()
}

func TestEveryReadingIsPrintedNextToItsRange(t *testing.T) {
	// A number with no reference beside it is not actionable.
	out := render(t, []*axis.Result{{AxisID: "mutation", Measures: []axis.Measure{
		{Key: "mutation.score", Label: "mutation score", Value: 0.2,
			Unit: axis.UnitRatio, RefLow: ref(0.7), Status: axis.StatusFail, Headline: true},
	}}})
	for _, want := range []string{"mutation score", "20%", "≥ 70%", "L"} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q:\n%s", want, out)
		}
	}
}

func TestSubReadingsAreIndentedUnderTheirParent(t *testing.T) {
	// Presenting a breakdown as a peer is what turns a short report into a wall
	// of numbers.
	out := render(t, []*axis.Result{{AxisID: "mutation", Measures: []axis.Measure{
		{Key: "mutation.score", Label: "mutation score", Value: 0.2, Unit: axis.UnitRatio},
		{Key: "mutation.strength", Label: "test strength", Value: 0.2, Unit: axis.UnitRatio, Sub: true},
	}}})
	for _, line := range strings.Split(out, "\n") {
		if strings.Contains(line, "test strength") && !strings.Contains(line, "  test strength") {
			t.Errorf("sub-reading not indented: %q", line)
		}
	}
}

func TestUnmeasuredSaysSoAndNeverLooksLikeAPass(t *testing.T) {
	out := render(t, []*axis.Result{{AxisID: "graph", Measures: []axis.Measure{
		{Key: "graph.redundant", Label: "redundant code",
			Status: axis.StatusUnmeasured, Note: "no .codegraph index", Headline: true},
	}}})
	if !strings.Contains(out, "n/a") || !strings.Contains(out, "no .codegraph index") {
		t.Errorf("an unmeasured reading must state itself and why:\n%s", out)
	}
	if strings.Contains(out, "✓") {
		t.Errorf("unmeasured must not be flagged as in range:\n%s", out)
	}
	if !strings.Contains(out, "unmeasured") {
		t.Errorf("the footer should count it:\n%s", out)
	}
}

func TestFooterCountsWhatFellOutOfRange(t *testing.T) {
	out := render(t, []*axis.Result{{AxisID: "x", Measures: []axis.Measure{
		{Label: "a", Value: 9, RefHigh: ref(1)},
		{Label: "b", Value: 9, RefHigh: ref(1)},
		{Label: "c", Value: 0, RefHigh: ref(1)},
	}}})
	if !strings.Contains(out, "2 out of range") {
		t.Errorf("footer wrong:\n%s", out)
	}
}

func TestAllGreenSaysSo(t *testing.T) {
	out := render(t, []*axis.Result{{AxisID: "x", Measures: []axis.Measure{
		{Label: "a", Value: 0, RefHigh: ref(1)},
	}}})
	if !strings.Contains(out, "all within range") {
		t.Errorf("footer wrong:\n%s", out)
	}
}

func TestAPartialRunSaysTheReadingsAreASample(t *testing.T) {
	out := render(t, []*axis.Result{{
		AxisID: "mutation", Partial: true,
		Measures: []axis.Measure{{Label: "a", Value: 0, RefHigh: ref(1)}},
	}})
	if !strings.Contains(out, "budget") && !strings.Contains(out, "sample") {
		t.Errorf("a partial run must not read as complete:\n%s", out)
	}
}

func TestFindingsShowTheChangeNoTestNoticed(t *testing.T) {
	// The diff is the actionable half of a surviving mutant.
	out := render(t, []*axis.Result{{
		AxisID:   "mutation",
		Measures: []axis.Measure{{Label: "a", Value: 0, RefHigh: ref(1)}},
		Observations: []axis.Observation{{
			Path: "pricing.go", Line: 9, Title: "no test caught this",
			Before: "if total < 0 {", After: "if total <= 0 {",
			Detail: "assert the behaviour at the boundary total == 0",
		}},
	}})
	for _, want := range []string{"pricing.go:9", "- if total < 0 {", "+ if total <= 0 {", "boundary total == 0"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q:\n%s", want, out)
		}
	}
}

func TestConfigSourceIsNamedSoASurprisingRangeCanBeTraced(t *testing.T) {
	out := render(t, []*axis.Result{{AxisID: "x", Measures: []axis.Measure{
		{Label: "a", Value: 0, RefHigh: ref(1)},
	}}}, func(p *Panel) { p.ConfigPath = "/repo/metron.json" })
	if !strings.Contains(out, "metron.json") {
		t.Errorf("the config source should be named:\n%s", out)
	}
}

func TestColumnsAlignWithWideCharacters(t *testing.T) {
	// CJK is double-width in a terminal; ignoring that makes the table ragged.
	if width("认知复杂度") != 10 {
		t.Errorf("width = %d, want 10", width("认知复杂度"))
	}
	if width("cognitive") != 9 {
		t.Errorf("ascii width = %d, want 9", width("cognitive"))
	}
	if got := pad("认知", 8); width(got) != 8 {
		t.Errorf("pad produced width %d, want 8", width(got))
	}
	if got := padLeft("20%", 8); width(got) != 8 {
		t.Errorf("padLeft produced width %d, want 8", width(got))
	}
}

func TestNothingMeasurableIsStated(t *testing.T) {
	out := render(t, nil)
	if !strings.Contains(out, "nothing measurable") {
		t.Errorf("an empty run should say so:\n%s", out)
	}
}
