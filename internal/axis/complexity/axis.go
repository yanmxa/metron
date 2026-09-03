package complexity

import (
	"context"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"sort"

	"github.com/yanmxa/metron/internal/axis"
	"github.com/yanmxa/metron/internal/target"
)

// Config holds the reference ranges. They are per-repo settings, not universal
// truths — a parser package will sit higher than a service layer and should be
// allowed to.
type Config struct {
	MaxCognitive int // adjusted cognitive a single changed function may reach
	MaxDelta     int // how much an already-existing function may get worse
	MaxObs       int // cap on reported observations
}

func DefaultConfig() Config {
	return Config{MaxCognitive: 15, MaxDelta: 0, MaxObs: 20}
}

type Axis struct{ cfg Config }

func New(cfg Config) *Axis { return &Axis{cfg: cfg} }

func (a *Axis) ID() string { return "complexity" }

func (a *Axis) Available(_ context.Context, t *target.Target) (bool, string) {
	if len(t.GoFiles()) == 0 {
		return false, "no Go files changed"
	}
	return true, ""
}

// FuncScore pairs a changed function with its readings and, when it existed
// before, how much it moved.
type FuncScore struct {
	Fn    target.Func
	Score Score
	Base  *Score // nil when the function is new
	Delta int    // adjusted cognitive increase; 0 for new functions
	IsNew bool
}

func (a *Axis) Run(ctx context.Context, t *target.Target, prog chan<- axis.Progress) (*axis.Result, error) {
	files := t.GoFiles()
	var scores []FuncScore

	for i, cf := range files {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		send(prog, axis.Progress{AxisID: a.ID(), Stage: "score", Done: i, Total: len(files), Message: cf.Path})

		if target.IsTestFile(cf.Path) {
			continue // test complexity is a different question than the one being asked
		}
		fs, err := a.scoreFile(ctx, t, cf)
		if err != nil {
			return nil, err
		}
		scores = append(scores, fs...)
	}

	return a.aggregate(scores), nil
}

func (a *Axis) scoreFile(ctx context.Context, t *target.Target, cf target.ChangedFile) ([]FuncScore, error) {
	src, err := os.ReadFile(filepath.Join(t.Root, cf.Path))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil // deleted between diff and read
		}
		return nil, fmt.Errorf("read %s: %w", cf.Path, err)
	}

	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, cf.Path, src, parser.ParseComments)
	if err != nil {
		// A file that does not parse is the compiler's problem to report, not
		// something to fail the whole run over.
		return nil, nil
	}
	if target.IsGenerated(f) {
		return nil, nil
	}

	changed := map[string]bool{}
	for _, fn := range target.AllFuncs(fset, f, cf.Path) {
		for _, r := range cf.Ranges {
			if r.Overlaps(fn.StartLine, fn.EndLine) {
				changed[fn.Key()] = true
				break
			}
		}
	}
	if len(changed) == 0 {
		return nil, nil
	}

	baseScores, err := a.baseScores(ctx, t, cf)
	if err != nil {
		return nil, err
	}

	var out []FuncScore
	for _, d := range f.Decls {
		fd, ok := d.(*ast.FuncDecl)
		if !ok || fd.Body == nil {
			continue
		}
		fn := target.Func{
			Path: cf.Path, Name: fd.Name.Name, Recv: recvOf(fd),
			StartLine: fset.Position(fd.Pos()).Line,
			EndLine:   fset.Position(fd.End()).Line,
		}
		if !changed[fn.Key()] {
			continue
		}
		fs := FuncScore{Fn: fn, Score: Cognitive(fd, fset)}
		if b, ok := baseScores[fn.Key()]; ok {
			bb := b
			fs.Base = &bb
			fs.Delta = fs.Score.Adjusted - b.Adjusted
		} else {
			fs.IsNew = true
			fn.IsNew = true
			fs.Fn = fn
		}
		out = append(out, fs)
	}
	return out, nil
}

// baseScores reads the file as it was at the merge-base. A missing file means
// the change created it, so every function in it is new — that is a fact worth
// knowing, not an error.
func (a *Axis) baseScores(ctx context.Context, t *target.Target, cf target.ChangedFile) (map[string]Score, error) {
	path := cf.Path
	if cf.Status == target.Renamed && cf.OldPath != "" {
		path = cf.OldPath
	}
	src, err := target.ShowFile(ctx, t.Root, t.BaseSHA, path)
	if err != nil || src == nil {
		return map[string]Score{}, nil
	}

	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, path, src, 0)
	if err != nil {
		return map[string]Score{}, nil
	}

	out := map[string]Score{}
	for _, d := range f.Decls {
		fd, ok := d.(*ast.FuncDecl)
		if !ok || fd.Body == nil {
			continue
		}
		// Key against the NEW path so a renamed file still matches up.
		k := target.Func{Path: cf.Path, Name: fd.Name.Name, Recv: recvOf(fd)}.Key()
		out[k] = Cognitive(fd, fset)
	}
	return out, nil
}

func (a *Axis) aggregate(scores []FuncScore) *axis.Result {
	r := &axis.Result{AxisID: "complexity"}
	if len(scores) == 0 {
		r.Measures = []axis.Measure{{
			Key: "complexity.cognitive_max", Label: "cognitive max",
			Status: axis.StatusUnmeasured, Headline: true,
			Note: "no non-test Go functions changed",
		}}
		return r
	}

	maxCog, maxRaw, maxDelta, over := 0, 0, 0, 0
	var cogAt, deltaAt string
	for _, s := range scores {
		if s.Score.Adjusted > maxCog {
			maxCog, cogAt = s.Score.Adjusted, s.Fn.Label()
		}
		if s.Score.Cognitive > maxRaw {
			maxRaw = s.Score.Cognitive
		}
		if s.Delta > maxDelta {
			maxDelta, deltaAt = s.Delta, s.Fn.Label()
		}
		if s.Score.Adjusted > a.cfg.MaxCognitive {
			over++
		}
	}

	hiCog := float64(a.cfg.MaxCognitive)
	hiDelta := float64(a.cfg.MaxDelta)

	r.Measures = []axis.Measure{
		{
			Key: "complexity.cognitive_max", Label: "cognitive max",
			Value: float64(maxCog), Unit: axis.UnitCount,
			RefHigh: &hiCog, Headline: true,
			Status: statusFor(float64(maxCog) <= hiCog),
			// A reading nobody can locate is not actionable, so the worst
			// function is named even when it sits inside the range.
			Note: cogAt,
		},
		{
			// The reading this axis exists for: agents pile branches into an
			// existing function instead of extracting a new one.
			Key: "complexity.delta_max", Label: "cognitive \u0394",
			Value: float64(maxDelta), Unit: axis.UnitDelta,
			RefHigh: &hiDelta, Headline: true,
			Status: statusFor(float64(maxDelta) <= hiDelta),
			Note:   deltaAt,
		},
	}
	r.Diagnostics = map[string]float64{
		"complexity.over_threshold":    float64(over),
		"complexity.cognitive_raw_max": float64(maxRaw),
		"complexity.functions":         float64(len(scores)),
	}

	r.Observations = a.observations(scores)
	return r
}

// observations reports the functions worth looking at: anything over the
// range, and anything that got worse. Sorted by how much attention it deserves.
func (a *Axis) observations(scores []FuncScore) []axis.Observation {
	var interesting []FuncScore
	for _, s := range scores {
		if s.Score.Adjusted > a.cfg.MaxCognitive || s.Delta > a.cfg.MaxDelta {
			interesting = append(interesting, s)
		}
	}
	sort.Slice(interesting, func(i, j int) bool {
		if interesting[i].Delta != interesting[j].Delta {
			return interesting[i].Delta > interesting[j].Delta
		}
		return interesting[i].Score.Adjusted > interesting[j].Score.Adjusted
	})
	if len(interesting) > a.cfg.MaxObs {
		interesting = interesting[:a.cfg.MaxObs]
	}

	out := make([]axis.Observation, 0, len(interesting))
	for _, s := range interesting {
		detail := fmt.Sprintf("cognitive %d (raw %d, %d err guards discounted) \u00b7 cyclomatic %d \u00b7 %d lines \u00b7 fan-out %d",
			s.Score.Adjusted, s.Score.Cognitive, s.Score.ErrGuards,
			s.Score.Cyclomatic, s.Score.Lines, s.Score.FanOut)
		title := s.Fn.Label()
		switch {
		case s.IsNew:
			title += " (new)"
		case s.Delta > 0:
			title += fmt.Sprintf(" (\u0394 +%d, was %d)", s.Delta, s.Base.Adjusted)
		}
		out = append(out, axis.Observation{
			Path: s.Fn.Path, Line: s.Fn.StartLine, EndLine: s.Fn.EndLine,
			Kind: "complexity", Title: title, Detail: detail,
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

func send(ch chan<- axis.Progress, p axis.Progress) {
	if ch == nil {
		return
	}
	select {
	case ch <- p:
	default: // never let progress reporting stall the measurement
	}
}
