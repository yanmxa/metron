package gate

import (
	"testing"

	"github.com/yanmxa/metron/internal/axis"
)

func measure(key string, v, high float64, headline bool) axis.Measure {
	return axis.Measure{Key: key, Value: v, RefHigh: &high, Headline: headline}
}

func TestHeadlinePolicyIgnoresDiagnostics(t *testing.T) {
	r := &axis.Result{Measures: []axis.Measure{
		measure("headline", 1, 5, true),
		measure("diagnostic", 99, 5, false),
	}}
	if got := Decide([]*axis.Result{r}, FailOnHeadline); got != ExitOK {
		t.Errorf("exit = %d, want %d — a diagnostic must not block", got, ExitOK)
	}
	if got := Decide([]*axis.Result{r}, FailOnAny); got != ExitOutOfRange {
		t.Errorf("exit = %d, want %d under fail-on=any", got, ExitOutOfRange)
	}
}

func TestPartialRunCannotFailTheBuild(t *testing.T) {
	// The sample is not the population. A tool that fails on incomplete
	// evidence teaches people to ignore it.
	r := &axis.Result{Partial: true, Measures: []axis.Measure{measure("m", 99, 5, true)}}
	if got := Decide([]*axis.Result{r}, FailOnHeadline); got != ExitPartial {
		t.Errorf("exit = %d, want %d", got, ExitPartial)
	}
}

func TestUnmeasuredNeverCountsAsAPass(t *testing.T) {
	// It also must not count as a failure — it is simply absent, and the panel
	// says so. What matters is that it does not silently satisfy a gate.
	r := &axis.Result{Measures: []axis.Measure{
		{Key: "m", Status: axis.StatusUnmeasured, Headline: true},
	}}
	if got := Decide([]*axis.Result{r}, FailOnHeadline); got != ExitOK {
		t.Errorf("exit = %d, want %d", got, ExitOK)
	}
}

func TestFailOnNoneMeasuresWithoutBlocking(t *testing.T) {
	r := &axis.Result{Measures: []axis.Measure{measure("m", 99, 5, true)}}
	if got := Decide([]*axis.Result{r}, FailOnNone); got != ExitOK {
		t.Errorf("exit = %d, want %d", got, ExitOK)
	}
}
