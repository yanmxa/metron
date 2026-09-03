// Command metron measures a change and prints a lab report.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"runtime/debug"
	"strings"
	"time"

	"github.com/yanmxa/metron/internal/axis"
	"github.com/yanmxa/metron/internal/axis/complexity"
	"github.com/yanmxa/metron/internal/axis/graph"
	"github.com/yanmxa/metron/internal/axis/mutation"
	"github.com/yanmxa/metron/internal/config"
	"github.com/yanmxa/metron/internal/crap"
	"github.com/yanmxa/metron/internal/gate"
	"github.com/yanmxa/metron/internal/panel"
	"github.com/yanmxa/metron/internal/target"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "metron: %v\n", err)
		os.Exit(gate.ExitError)
	}
}

func run() error {
	var (
		since    = flag.String("since", "main", "base ref to measure the change against")
		format   = flag.String("format", "table", "output format: table | json")
		failOn   = flag.String("fail-on", "headline", "what makes the exit code non-zero: headline | any | none")
		dir      = flag.String("C", ".", "run as if metron were started in this directory")
		axesFlag = flag.String("axes", "complexity,graph", "which axes to run: complexity, graph, mutation (mutation runs your tests and takes longer)")
		budget   = flag.Duration("budget", 5*time.Minute, "wall-clock budget for the mutation axis")
		paranoid = flag.Bool("paranoid", false, "re-run every killed mutant sequentially before believing it")
		fresh    = flag.Bool("fresh", false, "ignore any checkpoint and re-measure from scratch")
		showVer  = flag.Bool("version", false, "print the version and exit")
		quiet    = flag.Bool("quiet", false, "suppress progress output")
		all      = flag.Bool("all", false, "measure the whole repository instead of one change; readings that need a base revision report as unmeasured")
	)
	flag.Usage = usage
	flag.Parse()

	if *showVer {
		fmt.Println(versionString())
		return nil
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	var err error

	var t *target.Target
	if *all {
		t, err = target.ResolveAll(ctx, *dir)
	} else {
		t, err = target.Resolve(ctx, *dir, *since)
	}
	if err != nil {
		return err
	}

	cfg, cfgPath, cerr := config.Load(t.Root)
	if cerr != nil {
		return cerr
	}
	axes, aerr := buildAxes(*axesFlag, cfg, *budget, *paranoid, *fresh)
	if aerr != nil {
		return aerr
	}

	// A mutation run can take minutes. Reporting nothing while it works is
	// indistinguishable from being hung, which is the usual reason a tool gets
	// killed just before it would have succeeded. Progress goes to stderr so
	// --format json stays pipeable.
	showProgress := !*quiet && *format == "table" && isTerminal(os.Stderr)
	prog := panel.NewProgress(os.Stderr, showProgress)

	var results []*axis.Result
	for _, a := range axes {
		ok, why := a.Available(ctx, t)
		if !ok {
			results = append(results, &axis.Result{
				AxisID: a.ID(),
				Measures: []axis.Measure{{
					Key: a.ID(), Label: a.ID(), Headline: true,
					Status: axis.StatusUnmeasured, Note: why,
				}},
			})
			continue
		}
		ch := make(chan axis.Progress, 64)
		done := make(chan struct{})
		go func() { prog.Watch(ch); close(done) }()
		r, err := a.Run(ctx, t, ch)
		close(ch)
		<-done
		prog.Clear()
		if err != nil {
			return fmt.Errorf("%s axis: %w", a.ID(), err)
		}
		results = append(results, r)
	}

	// CRAP needs both complexity and mutation, so it is computed once every
	// axis has reported rather than inside either of them.
	crap.Apply(results)

	switch *format {
	case "json":
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		if err := enc.Encode(map[string]any{
			"base":    t.BaseRef,
			"baseSHA": t.BaseSHA,
			"files":   len(t.Files),
			"axes":    results,
		}); err != nil {
			return err
		}
	case "table":
		p := &panel.Panel{Target: t, Results: results, ConfigPath: cfgPath}
		fmt.Print(p.Render())
	default:
		return fmt.Errorf("unknown format %q", *format)
	}

	os.Exit(gate.Decide(results, gate.Policy(*failOn)))
	return nil
}

// setInt applies a configured override only when the file actually stated one,
// so an absent field keeps the current default rather than freezing whatever it
// happened to be when the config was written.
func setInt(dst *int, v *int) {
	if v != nil {
		*dst = *v
	}
}

func setFloat(dst *float64, v *float64) {
	if v != nil {
		*dst = *v
	}
}

// buildAxes assembles the requested axes in a fixed order, cheapest first, so
// a run that is interrupted has still produced the readings that were free.
func buildAxes(spec string, cfg *config.File, budget time.Duration,
	paranoid, fresh bool) ([]axis.Axis, error) {
	want := map[string]bool{}
	for _, name := range strings.Split(spec, ",") {
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}
		switch name {
		case "complexity", "graph", "mutation", "all":
			want[name] = true
		default:
			return nil, fmt.Errorf("unknown axis %q", name)
		}
	}
	all := want["all"]

	var out []axis.Axis
	if all || want["complexity"] {
		c := complexity.DefaultConfig()
		if s := cfg.Complexity; s != nil {
			setInt(&c.MaxCognitive, s.MaxCognitive)
			setInt(&c.MaxDelta, s.MaxDelta)
		}
		out = append(out, complexity.New(c))
	}
	if all || want["graph"] {
		g := graph.DefaultConfig()
		if s := cfg.Graph; s != nil {
			setInt(&g.MinSiblings, s.MinSiblings)
		}
		out = append(out, graph.New(g))
	}
	if all || want["mutation"] {
		m := mutation.DefaultConfig()
		m.Budget = cfg.MutationBudget(budget)
		m.Paranoid = paranoid
		m.Fresh = fresh
		if s := cfg.Mutation; s != nil {
			setFloat(&m.RefScore, s.MinScore)
			setFloat(&m.RefStrength, s.MinStrength)
			setFloat(&m.RefReach, s.MinReach)
			setInt(&m.Workers, s.Workers)
			setInt(&m.MaxMutants, s.MaxMutants)
		}
		out = append(out, mutation.New(m))
	}
	if len(out) == 0 {
		return nil, errors.New("no axes selected")
	}
	return out, nil
}

// versionString prefers the version stamped by a release build, then whatever
// `go install` recorded, so it is never simply "unknown" in the common cases.
func versionString() string {
	if version != "" {
		return "metron " + version
	}
	if info, ok := debug.ReadBuildInfo(); ok && info.Main.Version != "" {
		return "metron " + info.Main.Version
	}
	return "metron (devel)"
}

// version is set at release time with -ldflags "-X main.version=vX.Y.Z".
var version string

// isTerminal reports whether f is attached to a terminal, so redraws never end
// up in a CI log or a pipe.
func isTerminal(f *os.File) bool {
	st, err := f.Stat()
	return err == nil && st.Mode()&os.ModeCharDevice != 0
}

func usage() {
	fmt.Fprint(os.Stderr, `metron — measure code, then say what to do about it

Seven readings against explicit reference ranges: whether the tests actually
hold the code up (mutation), how hard it is to read and extend (complexity),
and whether it duplicates or steps around what already exists (graph). Every
finding carries the change that closes it.

Usage:
  metron [flags]

Examples:
  metron --since main                    measure a change, fast axes only
  metron --since main --axes all         add mutation; runs your test suite
  metron --all --axes complexity,graph   measure the whole repository
  metron --since main --format json      machine-readable, for an agent loop

Flags:
`)
	flag.PrintDefaults()
	fmt.Fprint(os.Stderr, `
Exit codes:
  0  every reading within range
  1  error
  2  a reading fell outside its range
  3  budget spent; the readings cover only a sample
`)
}
