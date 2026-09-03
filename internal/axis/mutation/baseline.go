package mutation

import (
	"context"
	"errors"
	"sort"
	"sync"
	"time"
)

// Baseline is what the unmutated suite does, measured before anything is
// mutated.
type Baseline struct {
	Quarantine map[string]bool // tests that did not pass every round
	Duration   time.Duration   // longest single suite run
	CoverPath  string
	Red        bool // the suite fails on its own
	RedDetail  string
	AllTests   []string // top-level tests, for building the complement pattern
}

// Quarantined returns the excluded test names, sorted.
func (b *Baseline) Quarantined() []string {
	out := make([]string, 0, len(b.Quarantine))
	for k := range b.Quarantine {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// Allowed returns the tests that may take part in scoring.
//
// Go's -run has no negation, so excluding the quarantine means naming the
// complement explicitly.
func (b *Baseline) Allowed() []string {
	var out []string
	for _, t := range b.AllTests {
		if !b.Quarantine[t] {
			out = append(out, t)
		}
	}
	return out
}

// MutantTimeout derives the per-mutant deadline from what the suite actually
// takes.
//
// A fixed generous default is a trap: one runaway mutant on a real repository
// consumed 120 seconds of a 354-second run — 34% of the wall clock — purely
// because the timeout had nothing to do with how long the tests normally take.
// Deriving it cut that run by a third and lost no information, since a timeout
// counts as a kill either way.
func (b *Baseline) MutantTimeout(factor float64, lo, hi time.Duration) time.Duration {
	d := time.Duration(float64(b.Duration) * factor)
	if d < lo {
		return lo
	}
	if d > hi {
		return hi
	}
	return d
}

// RunBaseline verifies the suite and screens for flaky tests.
//
// The screen runs at the same concurrency the mutant phase will use, and that
// is the entire point. A real suite passed five sequential runs in a row and
// then failed intermittently the moment four ran at once, because one test
// shells out with a relative path and races under load. Left in, that test
// makes mutants look killed and inflates the score — the result set actually
// diverged between runs at two workers. Quarantine, not a lower worker count,
// is what makes parallelism safe here.
func RunBaseline(ctx context.Context, r *Runner, pkgs []string, workers, rounds int,
	timeout time.Duration, coverPath string) (*Baseline, error) {

	if len(pkgs) == 0 {
		return nil, errors.New("no packages with tests in scope")
	}
	workers = atLeast(workers, 1)
	rounds = atLeast(rounds, 1)

	b := &Baseline{Quarantine: map[string]bool{}, CoverPath: coverPath}
	b.AllTests = listTests(ctx, r, pkgs)

	failCount := map[string]int{}
	if err := b.first(ctx, r, pkgs, timeout, coverPath, failCount); err != nil || b.Red {
		return b, err
	}
	if err := b.underLoad(ctx, r, pkgs, workers, rounds, timeout, failCount); err != nil {
		return b, err
	}

	b.markRedIfAlwaysFailing(failCount, 1+(rounds-1)*workers)
	return b, nil
}

// first runs the suite once, sequentially, carrying coverage. It produces the
// clean profile and the reference duration the per-mutant timeout derives from.
func (b *Baseline) first(ctx context.Context, r *Runner, pkgs []string,
	timeout time.Duration, coverPath string, failCount map[string]int) error {

	v, d := r.Test(ctx, Invocation{Packages: pkgs, Timeout: timeout, Cover: coverPath})
	b.Duration = d

	switch v.Outcome {
	case Survived:
		return nil
	case Killed:
		for _, t := range v.KilledBy {
			b.Quarantine[t] = true
			failCount[t]++
		}
		return nil
	default:
		b.Red = true
		b.RedDetail = string(v.Outcome) + ": " + v.Detail
		return nil
	}
}

// underLoad repeats the suite at the concurrency the mutant phase will use.
// That is the entire point: a suite can pass five sequential runs and still
// fail intermittently the moment several run at once.
func (b *Baseline) underLoad(ctx context.Context, r *Runner, pkgs []string,
	workers, rounds int, timeout time.Duration, failCount map[string]int) error {

	for round := 1; round < rounds; round++ {
		if err := ctx.Err(); err != nil {
			return err
		}
		var mu sync.Mutex
		var wg sync.WaitGroup
		for w := 0; w < workers; w++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				rv, rd := r.Test(ctx, Invocation{
					Packages: pkgs, Timeout: timeout, Parallel: workers,
				})
				mu.Lock()
				defer mu.Unlock()
				if rd > b.Duration {
					b.Duration = rd
				}
				for _, t := range rv.KilledBy {
					b.Quarantine[t] = true
					failCount[t]++
				}
			}()
		}
		wg.Wait()
	}
	return nil
}

// markRedIfAlwaysFailing separates a flaky test from a broken suite. A test
// that failed every single time is not flaky, and scoring against a red suite
// makes every mutant look killed.
func (b *Baseline) markRedIfAlwaysFailing(failCount map[string]int, total int) {
	var always []string
	for t, n := range failCount {
		if n == total {
			always = append(always, t)
		}
	}
	if len(always) == 0 {
		return
	}
	sort.Strings(always)
	b.Red = true
	b.RedDetail = "these tests fail every time on the unmutated suite: " + join(always, 5)
}

// listTests enumerates the top-level tests. The quarantine is only enforceable
// with the full list: Go's -run has no negation, so excluding a test means
// naming every other one.
func listTests(ctx context.Context, r *Runner, pkgs []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, p := range pkgs {
		names, err := r.ListTests(ctx, p)
		if err != nil {
			continue
		}
		for _, n := range names {
			if !seen[n] {
				seen[n] = true
				out = append(out, n)
			}
		}
	}
	sort.Strings(out)
	return out
}

func atLeast(v, min int) int {
	if v < min {
		return min
	}
	return v
}

func join(ss []string, max int) string {
	if len(ss) > max {
		ss = append(append([]string{}, ss[:max]...), "…")
	}
	out := ""
	for i, s := range ss {
		if i > 0 {
			out += ", "
		}
		out += s
	}
	return out
}
