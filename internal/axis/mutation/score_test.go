package mutation

import (
	"testing"
	"time"
)

func tally(killed, survived, timedOut, notCovered, notViable int) Tally {
	return Tally{Killed: killed, Survived: survived, TimedOut: timedOut,
		NotCovered: notCovered, NotViable: notViable}
}

func TestUncoveredMutantsCountAgainstTheScore(t *testing.T) {
	// The failure mode metron exists to catch: a large change with one small
	// well-tested corner. Excluding uncovered mutants scores that near-perfect
	// and can be gamed by writing one excellent test for one tiny function.
	partial := tally(10, 0, 0, 90, 0)

	score, _ := partial.Score()
	if score > 0.15 {
		t.Errorf("score = %.2f, want it dominated by the untested 90%%", score)
	}
	strength, _ := partial.Strength()
	if strength != 1.0 {
		t.Errorf("strength = %.2f, want 1.0 — the tests that run are perfect", strength)
	}
	reach, _ := partial.Reach()
	if reach > 0.15 {
		t.Errorf("reach = %.2f, want low — almost nothing is exercised", reach)
	}
}

func TestStrengthAndReachSeparateTheTwoFailureModes(t *testing.T) {
	// Untested code: reach is low, strength is fine.
	untested := tally(10, 0, 0, 40, 0)
	// Tested but unasserted: reach is perfect, strength is poor.
	unasserted := tally(10, 40, 0, 0, 0)

	ur, _ := untested.Reach()
	us, _ := untested.Strength()
	ar, _ := unasserted.Reach()
	as, _ := unasserted.Strength()

	if ur >= ar {
		t.Errorf("reach should separate them: untested %.2f vs unasserted %.2f", ur, ar)
	}
	if us <= as {
		t.Errorf("strength should separate them: untested %.2f vs unasserted %.2f", us, as)
	}
	// The headline alone cannot tell them apart, which is exactly why both are
	// reported next to it.
	uScore, _ := untested.Score()
	aScore, _ := unasserted.Score()
	if uScore != aScore {
		t.Errorf("the two shapes happen to score the same here: %.2f vs %.2f", uScore, aScore)
	}
}

func TestNonViableMutantsStayOutOfTheDenominator(t *testing.T) {
	// They are an artifact of metron's generator, not a property of the tests.
	// Counting them would make a file with more string concatenation grade
	// lower for reasons having nothing to do with testing.
	clean := tally(7, 3, 0, 0, 0)
	noisy := tally(7, 3, 0, 0, 50)

	a, _ := clean.Score()
	b, _ := noisy.Score()
	if a != b {
		t.Errorf("score moved with generator noise: %.3f vs %.3f", a, b)
	}
	if noisy.NotViableRate() < 0.8 {
		t.Errorf("notViableRate = %.2f, want it surfaced as a diagnostic", noisy.NotViableRate())
	}
}

func TestTimeoutCountsAsDetected(t *testing.T) {
	// A mutation that sends the tests into an infinite loop was noticed.
	got := tally(0, 0, 5, 0, 0)
	score, ok := got.Score()
	if !ok || score != 1.0 {
		t.Errorf("score = %.2f (ok=%v), want 1.0", score, ok)
	}
}

func TestNothingToScoreIsUnmeasuredNotZero(t *testing.T) {
	// A fabricated reading is worse than a missing one.
	if _, ok := (Tally{}).Score(); ok {
		t.Error("an empty tally must not produce a score")
	}
	ms := (Tally{}).Measures(DefaultConfig())
	if ms[0].Status.String() != "unmeasured" {
		t.Errorf("status = %s, want unmeasured", ms[0].Status)
	}
}

func TestCountClassifiesEveryOutcome(t *testing.T) {
	got := Count([]Mutant{
		{Outcome: Killed}, {Outcome: Survived}, {Outcome: TimedOut},
		{Outcome: NotCovered}, {Outcome: NotViable}, {Outcome: Skipped},
		{Outcome: Errored}, {Outcome: ""},
	})
	if got.Killed != 1 || got.Survived != 1 || got.TimedOut != 1 ||
		got.NotCovered != 1 || got.NotViable != 1 || got.Skipped != 1 {
		t.Errorf("miscounted: %+v", got)
	}
	if got.Errored != 2 {
		t.Errorf("errored = %d, want 2 (an empty outcome is not a pass)", got.Errored)
	}
}

func TestSlowSuitesReportNoNumberRatherThanASample(t *testing.T) {
	// A number from a handful of mutants looks like a measurement and is not
	// one. The axis must say it could not measure instead.
	a := New(Config{Budget: 10 * time.Second, Workers: 2, MinSampleShare: 0.25})
	slow := &Baseline{Duration: 30 * time.Second}
	if why, ok := a.tooSlow(slow, 100); ok {
		t.Errorf("a 30s suite with a 10s budget should refuse to score; got ok, %q", why)
	}

	fast := &Baseline{Duration: 200 * time.Millisecond}
	if why, ok := a.tooSlow(fast, 100); !ok {
		t.Errorf("a fast suite should score: %s", why)
	}

	// With no budget there is nothing to project against.
	nolimit := New(Config{Workers: 2, MinSampleShare: 0.25})
	if _, ok := nolimit.tooSlow(slow, 100); !ok {
		t.Error("an unbounded run should always attempt to score")
	}
}
