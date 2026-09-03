package mutation

import (
	"runtime"
	"time"
)

type Config struct {
	// Workers bounds concurrent `go test` invocations. Throughput was measured
	// to saturate at two and degrade past four, because go test is already
	// internally parallel.
	Workers int
	// Budget caps the execute stage's wall clock. Exceeding it yields honest
	// partial results rather than a silently truncated score.
	Budget time.Duration
	// MaxMutants caps a very large diff. Mutant density runs about one per
	// seven and a half lines of Go, so a two-thousand-line change would
	// otherwise take minutes.
	MaxMutants int
	Operators  []string

	BaselineRounds int // suite repetitions used to screen for flaky tests
	TimeoutFactor  float64
	TimeoutMin     time.Duration
	TimeoutMax     time.Duration

	// Paranoid re-runs every kill sequentially before reporting it. Roughly
	// doubles the execute stage; worth it in CI, where a false kill is worse
	// than a slow run.
	Paranoid bool

	// Fresh discards any checkpoint and re-measures from scratch.
	Fresh bool

	// MinSampleShare is the fraction of mutants that must be scoreable within
	// the budget before a score is reported at all. Below it the axis reports
	// Unmeasured rather than a number derived from a handful of mutants.
	MinSampleShare float64

	RefScore     float64
	RefStrength  float64
	RefReach     float64
	RefNotViable float64
	MaxObs       int
}

func DefaultConfig() Config {
	w := runtime.NumCPU() / 4
	if w < 2 {
		w = 2
	}
	if w > 4 {
		w = 4
	}
	return Config{
		Workers: w, Budget: 5 * time.Minute, MaxMutants: 400,
		Operators:      DefaultOperators(),
		BaselineRounds: 3, TimeoutFactor: 4,
		TimeoutMin: 5 * time.Second, TimeoutMax: 60 * time.Second,
		MinSampleShare: 0.25,
		RefScore:       0.70, RefStrength: 0.80, RefReach: 0.85, RefNotViable: 0.15,
		MaxObs: 20,
	}
}
