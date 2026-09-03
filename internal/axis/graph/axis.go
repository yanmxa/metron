package graph

import (
	"context"
	"fmt"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"sort"

	"github.com/yanmxa/metron/internal/axis"
	"github.com/yanmxa/metron/internal/target"
)

type Config struct {
	Thresholds  Thresholds
	MinSiblings int     // how many neighbours a convention needs before it is one
	MinShare    float64 // how consistent they must be
	MaxObs      int
	Sync        bool // refresh the index before reading it
}

func DefaultConfig() Config {
	return Config{
		Thresholds: DefaultThresholds(), MinSiblings: 5, MinShare: 0.8,
		MaxObs: 20, Sync: true,
	}
}

type Axis struct{ cfg Config }

func New(cfg Config) *Axis { return &Axis{cfg: cfg} }

func (a *Axis) ID() string { return "graph" }

func (a *Axis) Available(_ context.Context, t *target.Target) (bool, string) {
	if !HasIndex(t.Root) {
		return false, "no .codegraph index — run codegraph init in this repository"
	}
	if len(t.GoFiles()) == 0 {
		return false, "no Go files changed"
	}
	return true, ""
}

func (a *Axis) Run(ctx context.Context, t *target.Target, prog chan<- axis.Progress) (*axis.Result, error) {
	r := &axis.Result{AxisID: a.ID()}

	if a.cfg.Sync {
		send(prog, axis.Progress{AxisID: a.ID(), Stage: "sync"})
		if err := Sync(ctx, t.Root); err != nil {
			// A stale index still answers most questions; say so rather than
			// refusing to measure.
			r.Notes = append(r.Notes, "index may be stale (codegraph sync failed)")
		}
	}

	send(prog, axis.Progress{AxisID: a.ID(), Stage: "load"})
	db, closeDB, err := Open(t.Root)
	if err != nil {
		return nil, err
	}
	defer closeDB()

	g, err := Load(ctx, db)
	if err != nil {
		return nil, err
	}

	iu, err := ScanIdents(t.Root)
	if err != nil {
		return nil, fmt.Errorf("scan identifiers: %w", err)
	}

	changed, newCallees, missing := a.changedNodes(ctx, t, g)
	if len(changed) == 0 {
		r.Measures = unmeasured("the index has none of the changed symbols — it may need rebuilding")
		return r, nil
	}
	if missing > 0 {
		r.Notes = append(r.Notes, fmt.Sprintf("%d changed functions are missing from the index", missing))
	}

	send(prog, axis.Progress{AxisID: a.ID(), Stage: "rules", Total: len(changed)})

	orphans := Orphans(g, changed, iu, iu.AppPackage)
	dups := append(NearDuplicates(g, changed, a.cfg.Thresholds),
		Reimplementations(g, changed, a.cfg.Thresholds)...)
	bypassed := BypassedWrappers(g, changed, newCallees, a.cfg.Thresholds)
	crossings := LayerCrossings(g, changed, newCallees)
	divergence := SiblingDivergence(g, changed, a.cfg.MinSiblings, a.cfg.MinShare)

	zero := 0.0
	count := func(key, label string, n int, headline bool) axis.Measure {
		return axis.Measure{
			Key: key, Label: label, Value: float64(n), Unit: axis.UnitCount,
			RefHigh: &zero, Headline: headline, Status: statusFor(n == 0),
		}
	}
	r.Measures = []axis.Measure{
		count("graph.orphans", "orphaned symbols", len(orphans), true),
		count("graph.duplicates", "duplicated work", len(dups), true),
		count("graph.bypassed", "bypassed paths", len(bypassed), true),
		count("graph.layer_crossings", "unprecedented deps", len(crossings), false),
		count("graph.sibling_divergence", "sibling divergence", len(divergence), false),
	}

	all := append(append(append(append(orphans, dups...), bypassed...), crossings...), divergence...)
	r.Observations = a.observations(all)
	return r, nil
}

// changedNodes maps the change's functions onto index symbols. A function the
// index does not know about is reported as a gap rather than silently dropped —
// a stale index would otherwise look like a clean bill of health.
func (a *Axis) changedNodes(ctx context.Context, t *target.Target, g *Graph) ([]Node, NewCallees, int) {
	var out []Node
	seen := map[string]bool{}
	nc := NewCallees{}
	missing := 0

	for _, cf := range t.GoFiles() {
		if target.IsTestFile(cf.Path) {
			continue
		}
		src, err := os.ReadFile(filepath.Join(t.Root, cf.Path))
		if err != nil {
			continue
		}
		fset := token.NewFileSet()
		f, perr := parser.ParseFile(fset, cf.Path, src, parser.ParseComments)
		if perr != nil || target.IsGenerated(f) {
			continue
		}

		// What this file called before the change, so the consistency rules can
		// tell a newly drawn edge from one that was always there.
		basePath := cf.Path
		if cf.Status == target.Renamed && cf.OldPath != "" {
			basePath = cf.OldPath
		}
		baseSrc, _ := target.ShowFile(ctx, t.Root, t.BaseSHA, basePath)
		curCalls := CalleeNames(cf.Path, src)
		baseCalls := CalleeNames(cf.Path, baseSrc)

		for _, fn := range target.AllFuncs(fset, f, cf.Path) {
			touched := false
			for _, rg := range cf.Ranges {
				if rg.Overlaps(fn.StartLine, fn.EndLine) {
					touched = true
					break
				}
			}
			if !touched {
				continue
			}
			n, ok := g.FindFunc(cf.Path, fn.Name, fn.Recv, fn.StartLine)
			if !ok {
				missing++
				continue
			}
			if !seen[n.ID] {
				seen[n.ID] = true
				out = append(out, n)
				nc.Add(n.Key(), Diff(curCalls[fn.Key()], baseCalls[fn.Key()]))
			}
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].File != out[j].File {
			return out[i].File < out[j].File
		}
		return out[i].StartLine < out[j].StartLine
	})
	return out, nc, missing
}

func (a *Axis) observations(fs []Finding) []axis.Observation {
	if len(fs) > a.cfg.MaxObs {
		fs = fs[:a.cfg.MaxObs]
	}
	out := make([]axis.Observation, 0, len(fs))
	for _, f := range fs {
		out = append(out, axis.Observation{
			Path: f.Node.File, Line: f.Node.StartLine, EndLine: f.Node.EndLine,
			Kind: f.Rule, Title: f.Title, Detail: f.Detail,
		})
	}
	return out
}

func unmeasured(why string) []axis.Measure {
	keys := []struct{ key, label string }{
		{"graph.orphans", "orphaned symbols"},
		{"graph.duplicates", "duplicated work"},
		{"graph.bypassed", "bypassed paths"},
	}
	var out []axis.Measure
	for _, k := range keys {
		out = append(out, axis.Measure{
			Key: k.key, Label: k.label, Headline: true,
			Status: axis.StatusUnmeasured, Note: why,
		})
	}
	return out
}

func statusFor(ok bool) axis.Status {
	if ok {
		return axis.StatusOK
	}
	return axis.StatusFail
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
