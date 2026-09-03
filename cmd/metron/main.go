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
	"strings"
	"time"

	"github.com/yanmxa/metron/internal/axis"
	"github.com/yanmxa/metron/internal/axis/complexity"
	"github.com/yanmxa/metron/internal/axis/graph"
	"github.com/yanmxa/metron/internal/axis/mutation"
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
	)
	flag.Usage = usage
	flag.Parse()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	t, err := target.Resolve(ctx, *dir, *since)
	if err != nil {
		return err
	}

	axes, err := buildAxes(*axesFlag, *budget, *paranoid, *fresh)
	if err != nil {
		return err
	}

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
		r, err := a.Run(ctx, t, nil)
		if err != nil {
			return fmt.Errorf("%s axis: %w", a.ID(), err)
		}
		results = append(results, r)
	}

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
		p := &panel.Panel{Target: t, Results: results}
		fmt.Print(p.Render())
	default:
		return fmt.Errorf("unknown format %q", *format)
	}

	os.Exit(gate.Decide(results, gate.Policy(*failOn)))
	return nil
}

// buildAxes assembles the requested axes in a fixed order, cheapest first, so
// a run that is interrupted has still produced the readings that were free.
func buildAxes(spec string, budget time.Duration, paranoid, fresh bool) ([]axis.Axis, error) {
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
		out = append(out, complexity.New(complexity.DefaultConfig()))
	}
	if all || want["graph"] {
		out = append(out, graph.New(graph.DefaultConfig()))
	}
	if all || want["mutation"] {
		cfg := mutation.DefaultConfig()
		cfg.Budget = budget
		cfg.Paranoid = paranoid
		cfg.Fresh = fresh
		out = append(out, mutation.New(cfg))
	}
	if len(out) == 0 {
		return nil, errors.New("no axes selected")
	}
	return out, nil
}

func usage() {
	fmt.Fprint(os.Stderr, `metron — 代码化验单

对一次改动出三项读数,每项对照参考区间:测试是不是真的承重(变异得分)、
代码有多难读懂和扩展(认知复杂度)、有没有重复造轮子和偏离既有结构(图)。

用法:
  metron [flags]

Flags:
`)
	flag.PrintDefaults()
	fmt.Fprint(os.Stderr, `
退出码:
  0  全部在区间内
  1  出错
  2  有读数超出区间
  3  预算耗尽,读数不完整
`)
}
