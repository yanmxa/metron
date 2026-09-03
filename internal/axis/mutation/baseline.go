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
	if workers < 1 {
		workers = 1
	}
	if rounds < 1 {
		rounds = 1
	}

	b := &Baseline{Quarantine: map[string]bool{}, CoverPath: coverPath}

	// The quarantine is only enforceable if we know the full test list: Go's
	// -run has no negation, so excluding a test means naming every other one.
	// Without this the exclusion is computed and then silently ignored.
	seen := map[string]bool{}
	for _, p := range pkgs {
		names, lerr := r.ListTests(ctx, p)
		if lerr != nil {
			continue
		}
		for _, n := range names {
			if !seen[n] {
				seen[n] = true
				b.AllTests = append(b.AllTests, n)
			}
		}
	}
	sort.Strings(b.AllTests)

	// The first run is sequential and carries coverage: a clean profile, and
	// the reference duration the per-mutant timeout is derived from.
	v, d := r.Test(ctx, Invocation{Packages: pkgs, Timeout: timeout, Cover: coverPath})
	b.Duration = d
	switch v.Outcome {
	case Survived:
		// clean
	case Killed:
		for _, t := range v.KilledBy {
			b.Quarantine[t] = true
		}
	default:
		b.Red = true
		b.RedDetail = string(v.Outcome) + ": " + v.Detail
		return b, nil
	}

	failCount := map[string]int{}
	for _, t := range v.KilledBy {
		failCount[t]++
	}

	// The remaining rounds run at the mutant phase's concurrency, which is what
	// surfaces load-dependent flakiness.
	for round := 1; round < rounds; round++ {
		if err := ctx.Err(); err != nil {
			return b, err
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

	// A test that failed every single time is not flaky — the suite is broken,
	// and scoring against a red baseline makes every mutant look killed.
	total := 1 + (rounds-1)*workers
	var always []string
	for t, n := range failCount {
		if n == total {
			always = append(always, t)
		}
	}
	if len(always) > 0 {
		sort.Strings(always)
		b.Red = true
		b.RedDetail = "baseline 里这些测试始终失败: " + join(always, 5)
	}
	return b, nil
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
