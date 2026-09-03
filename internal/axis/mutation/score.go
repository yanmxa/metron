package mutation

import "github.com/yanmxa/metron/internal/axis"

// Tally counts outcomes.
type Tally struct {
	Killed, Survived, TimedOut, NotCovered, NotViable, Skipped, Errored int
}

func Count(ms []Mutant) Tally {
	var t Tally
	for _, m := range ms {
		switch m.Outcome {
		case Killed:
			t.Killed++
		case Survived:
			t.Survived++
		case TimedOut:
			t.TimedOut++
		case NotCovered:
			t.NotCovered++
		case NotViable:
			t.NotViable++
		case Skipped:
			t.Skipped++
		default:
			t.Errored++
		}
	}
	return t
}

// Detected counts mutants the suite noticed. A mutation that sends the tests
// into an infinite loop was noticed, so a timeout is a kill.
func (t Tally) Detected() int { return t.Killed + t.TimedOut }

// Score is the headline reading, and NOT_COVERED is in its denominator.
//
// The alternative — killed over killed-plus-survived — measures whether the
// tests you wrote are good tests. That is not the product question. metron
// grades a change, and the dominant failure mode in agent-written code is 200
// new lines with 20 of them tested well: excluding uncovered mutants scores
// that near 1.0, and it can be gamed outright by writing one excellent test for
// one tiny function.
//
// NOT_VIABLE stays out. It is an artifact of metron's own generator, not a
// property of the user's tests, and including it would move the score for
// reasons having nothing to do with testing — a file with more string
// concatenation would grade lower. It is reported separately as a diagnostic
// on the generator instead.
func (t Tally) Score() (float64, bool) {
	d := t.Detected() + t.Survived + t.NotCovered
	if d == 0 {
		return 0, false
	}
	return float64(t.Detected()) / float64(d), true
}

// Strength asks only whether the tests that do run are sharp.
func (t Tally) Strength() (float64, bool) {
	d := t.Detected() + t.Survived
	if d == 0 {
		return 0, false
	}
	return float64(t.Detected()) / float64(d), true
}

// Reach asks how much of the change the tests touch at all.
func (t Tally) Reach() (float64, bool) {
	d := t.Detected() + t.Survived + t.NotCovered
	if d == 0 {
		return 0, false
	}
	return 1 - float64(t.NotCovered)/float64(d), true
}

// NotViableRate is a diagnostic on metron itself: a high rate means the
// operator gating is underperforming, not that the code is bad.
func (t Tally) NotViableRate() float64 {
	total := t.Killed + t.Survived + t.TimedOut + t.NotCovered + t.NotViable
	if total == 0 {
		return 0
	}
	return float64(t.NotViable) / float64(total)
}

// Measures renders the tally as lab-report readings.
//
// Strength and reach decompose the headline into which failure you are in:
// low reach means the code is untested, low strength means the tests run it
// and assert nothing. That decomposition is the actionable part; the single
// number is only the gate.
func (t Tally) Measures(cfg Config) []axis.Measure {
	lo := func(v float64) *float64 { return &v }
	hiRate := cfg.RefNotViable

	measure := func(key, label string, v float64, ok bool, ref *float64, headline bool) axis.Measure {
		m := axis.Measure{Key: key, Label: label, Unit: axis.UnitRatio, Headline: headline}
		if !ok {
			m.Status = axis.StatusUnmeasured
			m.Note = "no scoreable mutants"
			return m
		}
		m.Value, m.RefLow = v, ref
		if ref != nil && v < *ref {
			m.Status = axis.StatusFail
		} else {
			m.Status = axis.StatusOK
		}
		return m
	}

	score, sok := t.Score()
	strength, stok := t.Strength()
	reach, rok := t.Reach()

	out := []axis.Measure{
		measure("mutation.score", "mutation score", score, sok, lo(cfg.RefScore), true),
		measure("mutation.strength", "test strength", strength, stok, lo(cfg.RefStrength), false),
		measure("mutation.reach", "reach", reach, rok, lo(cfg.RefReach), false),
	}
	zero := 0.0
	out = append(out, axis.Measure{
		Key: "mutation.survived", Label: "surviving mutants",
		Value: float64(t.Survived), Unit: axis.UnitCount, RefHigh: &zero,
		Status: statusFor(t.Survived == 0),
	})
	out = append(out, axis.Measure{
		Key: "mutation.not_covered", Label: "uncovered mutants",
		Value: float64(t.NotCovered), Unit: axis.UnitCount, RefHigh: &zero,
		Status: statusFor(t.NotCovered == 0),
	})
	out = append(out, axis.Measure{
		Key: "mutation.not_viable_rate", Label: "non-viable rate",
		Value: t.NotViableRate(), Unit: axis.UnitRatio, RefHigh: &hiRate,
		Status: statusFor(t.NotViableRate() <= hiRate),
	})
	return out
}

func statusFor(ok bool) axis.Status {
	if ok {
		return axis.StatusOK
	}
	return axis.StatusFail
}
