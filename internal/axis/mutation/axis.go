package mutation

import (
	"context"
	"fmt"
	"go/ast"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"time"

	"golang.org/x/tools/go/packages"

	"github.com/yanmxa/metron/internal/axis"
	"github.com/yanmxa/metron/internal/gopkg"
	"github.com/yanmxa/metron/internal/target"
)

type Axis struct{ cfg Config }

func New(cfg Config) *Axis { return &Axis{cfg: cfg} }

func (a *Axis) ID() string { return "mutation" }

func (a *Axis) Available(_ context.Context, t *target.Target) (bool, string) {
	if _, err := exec.LookPath("go"); err != nil {
		return false, "go is not on PATH"
	}
	if len(t.GoFiles()) == 0 {
		return false, "no Go files changed"
	}
	if _, err := os.Stat(filepath.Join(t.Root, "go.mod")); err != nil {
		return false, "not a Go module"
	}
	return true, ""
}

func (a *Axis) Run(ctx context.Context, t *target.Target, prog chan<- axis.Progress) (*axis.Result, error) {
	r := &axis.Result{AxisID: a.ID()}
	runner := &Runner{Root: t.Root}

	send(prog, axis.Progress{AxisID: a.ID(), Stage: "packages"})
	pg, err := gopkg.Load(ctx, t.Root)
	if err != nil {
		return nil, err
	}

	targets, notes := a.targetPackages(t, pg)
	r.Notes = append(r.Notes, notes...)
	if len(targets) == 0 {
		return unmeasuredResult(r, a.cfg, "the change touches no measurable Go package"), nil
	}

	scope := testScope(pg, targets)
	if len(scope) == 0 {
		return unmeasuredResult(r, a.cfg, "the packages this change touches have no tests"), nil
	}

	// A checkpoint makes an interrupted run resumable. Mutation runs are long
	// enough that starting over is the difference between a tool people use and
	// one they abandon.
	digest, derr := ScopeDigest(t.Root, scopeDirs(pg, targets, scope))
	if derr != nil {
		return nil, derr
	}
	store, cached, snap, serr := OpenStore(t.Root, t.BaseSHA, digest, a.cfg.Hash(), a.cfg.Fresh)
	if serr != nil {
		return nil, serr
	}
	defer store.Close()

	// Baseline first: it produces the coverage profile, the quarantine set and
	// the reference duration the per-mutant timeout is derived from. Every
	// later stage depends on one of the three.
	coverPath := store.CoverPath()
	var base *Baseline
	if snap != nil && fileExists(coverPath) {
		// The scope digest matched, so the code and the tests are byte-identical
		// to when these were measured.
		base = &Baseline{
			Quarantine: toSet(snap.Quarantine),
			Duration:   time.Duration(snap.DurationMS) * time.Millisecond,
			AllTests:   snap.AllTests, CoverPath: coverPath,
		}
		r.Notes = append(r.Notes, "reusing the previous baseline and coverage")
	} else {
		send(prog, axis.Progress{AxisID: a.ID(), Stage: "baseline"})
		base, err = RunBaseline(ctx, runner, scope, a.cfg.Workers, a.cfg.BaselineRounds,
			2*time.Minute, coverPath)
		if err != nil {
			return nil, err
		}
		if base.Red {
			// Scoring against a red suite makes every mutant look killed.
			return unmeasuredResult(r, a.cfg, "the baseline suite is red: "+base.RedDetail), nil
		}
		if err := store.SaveBaseline(base); err != nil {
			return nil, err
		}
	}
	if q := base.Quarantined(); len(q) > 0 {
		r.Notes = append(r.Notes, fmt.Sprintf("quarantined %d flaky test(s): %s", len(q), join(q, 3)))
	}

	send(prog, axis.Progress{AxisID: a.ID(), Stage: "generate"})
	mutants, suppressed, err := a.generate(ctx, t, pg, targets)
	if err != nil {
		return nil, err
	}
	if len(mutants) == 0 {
		return unmeasuredResult(r, a.cfg, "nothing in the change can be mutated"), nil
	}
	for reason, n := range suppressed {
		r.Notes = append(r.Notes, fmt.Sprintf("suppressed %d: %s", n, reason))
	}

	// Coverage pre-filter: a mutant on a line nothing executes cannot be
	// killed, so it is classified without ever being run.
	ci, cerr := BuildCoverIndex(coverPath, pg.DirOf, t.Root)
	if cerr != nil {
		r.Notes = append(r.Notes, "coverage pre-filter unavailable: "+cerr.Error())
	}
	var runnable []Mutant
	reused := 0
	for i := range mutants {
		if ci != nil && !ci.Reachable(mutants[i], mutants[i].Line, mutants[i].Line) {
			mutants[i].Outcome = NotCovered
			continue
		}
		// A cached verdict is only reached when the scope digest matched, which
		// means nothing that could change it has moved.
		if prev, ok := cached[mutants[i].ID]; ok && prev.Outcome != "" && prev.Outcome != Skipped {
			mutants[i] = prev
			reused++
			continue
		}
		runnable = append(runnable, mutants[i])
	}
	if reused > 0 {
		r.Notes = append(r.Notes, fmt.Sprintf("reused %d verdicts from the checkpoint", reused))
	}

	runnable = Stratify(runnable)
	capped := false
	if a.cfg.MaxMutants > 0 && len(runnable) > a.cfg.MaxMutants {
		// Stratified first, so the cap samples every changed function.
		runnable = runnable[:a.cfg.MaxMutants]
		capped = true
	}

	// Refuse to report a number the evidence cannot support.
	//
	// Every timing behind this design came from a package whose whole suite
	// runs in well under a second. A package with a thirty-second suite —
	// integration tests, containers, a network — makes each mutant cost thirty
	// seconds, and a budget then buys a handful of them. A lab report with a
	// missing value is honest; one carrying a fabricated reference range is
	// worse than having no tool.
	if why, ok := a.tooSlow(base, len(runnable)); !ok {
		r.Notes = append(r.Notes, why)
		return unmeasuredResult(r, a.cfg, why), nil
	}

	timeout := base.MutantTimeout(a.cfg.TimeoutFactor, a.cfg.TimeoutMin, a.cfg.TimeoutMax)
	allowed := base.Allowed()
	plan := a.planner(t, pg, scope, allowed, timeout)

	send(prog, axis.Progress{AxisID: a.ID(), Stage: "execute", Total: len(runnable)})
	done := 0
	executed, truncated := Execute(ctx, runner, runnable, plan, a.cfg.Workers, a.cfg.Budget,
		func(m Mutant) {
			done++
			// Recorded as it lands, not at the end: a run killed halfway still
			// keeps everything it had established.
			_ = store.Record(m)
			send(prog, axis.Progress{AxisID: a.ID(), Stage: "execute", Done: done, Total: len(runnable)})
		})

	executed = Adjudicate(ctx, runner, executed, base.Quarantine, plan)
	if a.cfg.Paranoid {
		executed = recheckKills(ctx, runner, executed, plan)
	}

	all := append(executed, notExecuted(mutants, runnable)...)
	tally := Count(all)
	r.Measures = tally.Measures(a.cfg)
	r.Funcs = perFunc(all)
	r.Observations = a.observations(all)
	r.Partial = truncated || capped
	if capped {
		r.Notes = append(r.Notes, fmt.Sprintf("capped at %d mutants, sampled across every changed function", a.cfg.MaxMutants))
	}
	if truncated {
		r.Notes = append(r.Notes, fmt.Sprintf("budget spent; %d mutants went unscored", tally.Skipped))
	}
	return r, nil
}

// targetPackages maps changed files to the packages worth mutating.
func (a *Axis) targetPackages(t *target.Target, pg *gopkg.Graph) (map[string][]target.ChangedFile, []string) {
	out := map[string][]target.ChangedFile{}
	var notes []string
	skippedCgo := map[string]bool{}

	for _, cf := range t.GoFiles() {
		if target.IsTestFile(cf.Path) {
			continue // mutating a test measures nothing about the tests
		}
		p, ok := pg.PackageFor(cf.Path)
		if !ok {
			continue
		}
		if p.UsesCgo() {
			skippedCgo[p.ImportPath] = true
			continue
		}
		out[p.ImportPath] = append(out[p.ImportPath], cf)
	}
	for p := range skippedCgo {
		notes = append(notes, "skipped cgo package "+p+" (overlay has known limitations with cgo)")
	}
	return out, notes
}

func testScope(pg *gopkg.Graph, targets map[string][]target.ChangedFile) []string {
	seen := map[string]bool{}
	var out []string
	for ip := range targets {
		for _, p := range pg.TestScope(ip) {
			if !seen[p] {
				seen[p] = true
				out = append(out, p)
			}
		}
	}
	sort.Strings(out)
	return out
}

// generate loads each changed package with full type information and emits
// mutants inside the functions the change touched.
func (a *Axis) generate(ctx context.Context, t *target.Target, pg *gopkg.Graph,
	targets map[string][]target.ChangedFile) ([]Mutant, map[string]int, error) {

	suppressed := map[string]int{}
	var out []Mutant

	paths := make([]string, 0, len(targets))
	for ip := range targets {
		paths = append(paths, ip)
	}
	sort.Strings(paths)

	cfg := &packages.Config{
		Mode: packages.NeedName | packages.NeedFiles | packages.NeedSyntax |
			packages.NeedTypes | packages.NeedTypesInfo | packages.NeedCompiledGoFiles,
		Dir:     t.Root,
		Context: ctx,
	}
	loaded, err := packages.Load(cfg, paths...)
	if err != nil {
		return nil, nil, fmt.Errorf("load packages: %w", err)
	}

	for _, p := range loaded {
		files := targets[p.PkgPath]
		if len(files) == 0 {
			continue
		}
		changedByPath := map[string][]target.LineRange{}
		for _, cf := range files {
			changedByPath[cf.Path] = cf.Ranges
		}

		for i, syn := range p.Syntax {
			if i >= len(p.CompiledGoFiles) {
				break
			}
			abs := p.CompiledGoFiles[i]
			rel, rerr := filepath.Rel(t.Root, abs)
			if rerr != nil {
				continue
			}
			rel = filepath.ToSlash(rel)
			ranges, ok := changedByPath[rel]
			if !ok || target.IsGenerated(syn) {
				continue
			}
			src, serr := os.ReadFile(abs)
			if serr != nil {
				continue
			}

			var spans []FuncSpan
			for _, d := range syn.Decls {
				fd, ok := d.(*ast.FuncDecl)
				if !ok || fd.Body == nil {
					continue
				}
				start := p.Fset.Position(fd.Pos()).Line
				end := p.Fset.Position(fd.End()).Line
				touched := false
				for _, rg := range ranges {
					if rg.Overlaps(start, end) {
						touched = true
						break
					}
				}
				if touched {
					// Labelled the way target.Func does, so per-function
					// readings from different axes line up.
					label := fd.Name.Name
					if recv := recvOf(fd); recv != "" {
						label = "(" + recv + ")." + label
					}
					spans = append(spans, FuncSpan{
						Label: label, Decl: fd, StartLine: start, EndLine: end,
					})
				}
			}
			if len(spans) == 0 {
				continue
			}
			g := NewGenerator(p.Fset, p.TypesInfo, a.cfg.Operators)
			out = append(out, g.Generate(rel, p.PkgPath, src, spans)...)
			for k, v := range g.Suppressed {
				suppressed[k] += v
			}
		}
	}
	return out, suppressed, nil
}

// planner builds the invocation for a mutant: the packages that can reach it,
// the tests that survived the flakiness screen, and the derived timeout.
func (a *Axis) planner(t *target.Target, pg *gopkg.Graph, scope, allowed []string,
	timeout time.Duration) Planner {

	srcCache := map[string][]byte{}
	return func(m Mutant) (Invocation, []byte, error) {
		src, ok := srcCache[m.File]
		if !ok {
			b, err := os.ReadFile(filepath.Join(t.Root, m.File))
			if err != nil {
				return Invocation{}, nil, err
			}
			srcCache[m.File] = b
			src = b
		}
		pkgs := pg.TestScope(m.Package)
		if len(pkgs) == 0 {
			pkgs = scope
		}
		return Invocation{
			Packages: pkgs, Run: allowed, Timeout: timeout, Parallel: a.cfg.Workers,
		}, src, nil
	}
}

func recheckKills(ctx context.Context, r *Runner, ms []Mutant, plan Planner) []Mutant {
	for i, m := range ms {
		if m.Outcome == Killed {
			ms[i] = runOne(ctx, r, m, plan)
		}
	}
	return ms
}

// notExecuted returns the mutants that never entered the worker pool: the ones
// the coverage filter ruled out, and the ones whose verdict came from the
// checkpoint.
// perFunc groups verdicts by the function they landed in, so how well a single
// function is tested can be combined with how complex it is.
func perFunc(ms []Mutant) []axis.PerFunc {
	byKey := map[string]*axis.PerFunc{}
	var order []string
	for _, m := range ms {
		switch m.Outcome {
		case Killed, TimedOut, Survived, NotCovered:
		default:
			continue // skipped, errored and non-viable say nothing about the tests
		}
		k := m.File + ":" + m.Function
		f, ok := byKey[k]
		if !ok {
			f = &axis.PerFunc{Path: m.File, Function: m.Function, Line: m.Line}
			byKey[k] = f
			order = append(order, k)
		}
		f.Mutants++
		if m.Outcome == Killed || m.Outcome == TimedOut {
			f.Detected++
		}
	}
	out := make([]axis.PerFunc, 0, len(order))
	for _, k := range order {
		out = append(out, *byKey[k])
	}
	return out
}

// recvOf renders a method receiver as a bare type name.
func recvOf(fd *ast.FuncDecl) string {
	if fd.Recv == nil || len(fd.Recv.List) == 0 {
		return ""
	}
	var name func(ast.Expr) string
	name = func(e ast.Expr) string {
		switch t := e.(type) {
		case *ast.Ident:
			return t.Name
		case *ast.StarExpr:
			return name(t.X)
		case *ast.IndexExpr:
			return name(t.X)
		case *ast.IndexListExpr:
			return name(t.X)
		case *ast.SelectorExpr:
			return t.Sel.Name
		}
		return ""
	}
	return name(fd.Recv.List[0].Type)
}

func notExecuted(all, ran []Mutant) []Mutant {
	inPool := make(map[string]bool, len(ran))
	for _, m := range ran {
		inPool[m.ID] = true
	}
	var out []Mutant
	for _, m := range all {
		if !inPool[m.ID] && m.Outcome != "" {
			out = append(out, m)
		}
	}
	return out
}

// tooSlow projects whether the budget can score a meaningful share of the
// mutants, using the measured suite duration as the per-mutant cost.
func (a *Axis) tooSlow(base *Baseline, n int) (string, bool) {
	if a.cfg.Budget <= 0 || n == 0 || base.Duration <= 0 || a.cfg.MinSampleShare <= 0 {
		return "", true
	}
	perMutant := base.Duration
	workers := a.cfg.Workers
	if workers < 1 {
		workers = 1
	}
	affordable := int(float64(a.cfg.Budget) / float64(perMutant) * float64(workers))
	if float64(affordable) >= float64(n)*a.cfg.MinSampleShare {
		return "", true
	}
	return fmt.Sprintf(
		"suite too slow: ~%s per mutant × %d mutants, and a %s budget buys only ~%d (under %.0f%%) — not scoring",
		base.Duration.Round(time.Millisecond), n, a.cfg.Budget, affordable,
		a.cfg.MinSampleShare*100), false
}

// scopeDirs lists every directory whose Go source could change a verdict.
func scopeDirs(pg *gopkg.Graph, targets map[string][]target.ChangedFile, scope []string) []string {
	seen := map[string]bool{}
	var out []string
	add := func(ip string) {
		if d := pg.DirOf(ip); d != "" && !seen[d] {
			seen[d] = true
			out = append(out, d)
		}
	}
	for ip := range targets {
		add(ip)
	}
	for _, ip := range scope {
		add(ip)
	}
	sort.Strings(out)
	return out
}

func toSet(ss []string) map[string]bool {
	out := make(map[string]bool, len(ss))
	for _, s := range ss {
		out[s] = true
	}
	return out
}

func fileExists(p string) bool {
	st, err := os.Stat(p)
	return err == nil && !st.IsDir()
}

// observations surface the survivors: each one is a concrete "write a test
// that tells these two lines apart".
func (a *Axis) observations(ms []Mutant) []axis.Observation {
	var live []Mutant
	for _, m := range ms {
		if m.Outcome == Survived {
			live = append(live, m)
		}
	}
	sort.Slice(live, func(i, j int) bool {
		if live[i].File != live[j].File {
			return live[i].File < live[j].File
		}
		return live[i].Line < live[j].Line
	})
	if len(live) > a.cfg.MaxObs {
		live = live[:a.cfg.MaxObs]
	}

	out := make([]axis.Observation, 0, len(live))
	for _, m := range live {
		out = append(out, axis.Observation{
			Path: m.File, Line: m.Line, Kind: "survived-mutant",
			Title: fmt.Sprintf("no test caught this change to %s (%s)", m.Function, m.Operator),
			// The guidance, not the diff, is the actionable half: it names the
			// case that is missing instead of leaving it to be inferred.
			Detail: m.Guidance,
			Before: m.Before, After: m.After,
		})
	}
	return out
}

func unmeasuredResult(r *axis.Result, cfg Config, why string) *axis.Result {
	r.Measures = []axis.Measure{{
		Key: "mutation.score", Label: "mutation score", Headline: true,
		Status: axis.StatusUnmeasured, Note: why,
	}}
	return r
}

func send(ch chan<- axis.Progress, p axis.Progress) {
	if ch == nil {
		return
	}
	select {
	case ch <- p:
	default:
	}
}
