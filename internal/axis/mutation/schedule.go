package mutation

import (
	"context"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"
)

// Stratify orders mutants round-robin across the functions they sit in.
//
// Ordering matters because a budget can cut the run short, and "the first 41
// mutants" should be a sample of the whole change rather than everything in
// whichever function sorts first. Without this, partial results are
// systematically biased toward one function.
//
// The order is total and derived only from content, so the same commit always
// produces the same sequence.
func Stratify(ms []Mutant) []Mutant {
	byFunc := map[string][]Mutant{}
	var order []string
	for _, m := range ms {
		k := m.File + ":" + m.Function
		if _, ok := byFunc[k]; !ok {
			order = append(order, k)
		}
		byFunc[k] = append(byFunc[k], m)
	}
	sort.Strings(order)
	for k := range byFunc {
		g := byFunc[k]
		sort.Slice(g, func(i, j int) bool {
			if g[i].Line != g[j].Line {
				return g[i].Line < g[j].Line
			}
			if g[i].Col != g[j].Col {
				return g[i].Col < g[j].Col
			}
			return g[i].Operator < g[j].Operator
		})
		byFunc[k] = g
	}

	out := make([]Mutant, 0, len(ms))
	for round := 0; len(out) < len(ms); round++ {
		progressed := false
		for _, k := range order {
			if round < len(byFunc[k]) {
				out = append(out, byFunc[k][round])
				progressed = true
			}
		}
		if !progressed {
			break
		}
	}
	return out
}

// Planner builds the go test invocation for one mutant.
type Planner func(m Mutant) (Invocation, []byte, error)

// Execute runs mutants across a bounded worker pool under a wall-clock budget.
//
// Throughput saturates at two workers and degrades past it, because `go test`
// is already internally parallel — each invocation nearly saturates the
// machine on its own. More workers buy nothing and cost latency.
//
// Results are written into a pre-sized slice by dispatch index rather than
// appended from goroutines, so completion order cannot reach the output.
func Execute(ctx context.Context, r *Runner, ms []Mutant, plan Planner,
	workers int, budget time.Duration, onDone func(Mutant)) ([]Mutant, bool) {

	if workers < 1 {
		workers = 1
	}
	out := make([]Mutant, len(ms))
	copy(out, ms)

	deadline := time.Time{}
	if budget > 0 {
		deadline = time.Now().Add(budget)
	}

	var (
		mu        sync.Mutex
		next      int
		truncated bool
		wg        sync.WaitGroup
	)

	worker := func() {
		defer wg.Done()
		for {
			mu.Lock()
			// The budget is checked before dispatching, never mid-mutant: a
			// half-run mutant has no verdict to report.
			if next >= len(out) || ctx.Err() != nil ||
				(!deadline.IsZero() && time.Now().After(deadline)) {
				if next < len(out) {
					truncated = true
				}
				mu.Unlock()
				return
			}
			i := next
			next++
			mu.Unlock()

			out[i] = runOne(ctx, r, out[i], plan)
			if onDone != nil {
				mu.Lock()
				onDone(out[i])
				mu.Unlock()
			}
		}
	}

	wg.Add(workers)
	for i := 0; i < workers; i++ {
		go worker()
	}
	wg.Wait()

	if truncated {
		for i := range out {
			if out[i].Outcome == "" {
				out[i].Outcome = Skipped
			}
		}
	}
	return out, truncated
}

func runOne(ctx context.Context, r *Runner, m Mutant, plan Planner) Mutant {
	inv, src, err := plan(m)
	if err != nil {
		m.Outcome, m.Detail = Errored, err.Error()
		return m
	}

	dir, err := os.MkdirTemp("", "metron-mutant-*")
	if err != nil {
		m.Outcome, m.Detail = Errored, err.Error()
		return m
	}
	defer func() { _ = os.RemoveAll(dir) }()

	overlay, err := WriteOverlay(dir, r.Root, m, src)
	if err != nil {
		m.Outcome, m.Detail = Errored, err.Error()
		return m
	}
	inv.Overlay = overlay

	v, d := r.Test(ctx, inv)
	m.Outcome, m.KilledBy, m.Detail = v.Outcome, v.KilledBy, v.Detail
	m.DurationMS = d.Milliseconds()

	// Rewrite the temp path in compiler diagnostics: byte splicing keeps line
	// and column exact, so only the file name is misleading.
	if m.Detail != "" {
		m.Detail = replaceAll(m.Detail, filepath.Join(dir, "mutant.go"), m.File)
	}
	return m
}

// Adjudicate re-checks kills that only quarantined tests reported.
//
// A kill credited entirely to a test known to be unreliable under load is not
// evidence, so the mutant is re-run on its own before its verdict stands.
func Adjudicate(ctx context.Context, r *Runner, ms []Mutant, quarantine map[string]bool,
	plan Planner) []Mutant {

	if len(quarantine) == 0 {
		return ms
	}
	for i, m := range ms {
		if m.Outcome != Killed || len(m.KilledBy) == 0 {
			continue
		}
		onlyFlaky := true
		for _, t := range m.KilledBy {
			if !quarantine[t] {
				onlyFlaky = false
				break
			}
		}
		if onlyFlaky {
			ms[i] = runOne(ctx, r, m, plan)
		}
	}
	return ms
}

func replaceAll(s, old, new string) string {
	if old == "" {
		return s
	}
	out := ""
	for {
		i := indexOf(s, old)
		if i < 0 {
			return out + s
		}
		out += s[:i] + new
		s = s[i+len(old):]
	}
}

func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}
