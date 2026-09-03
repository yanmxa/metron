package graph

import (
	"strings"
	"testing"
)

// build assembles a small graph. Nodes are "file.go:Name" or
// "file.go:Recv::Name"; edges are "a -> b".
func build(t *testing.T, nodes []Node, calls [][2]string) *Graph {
	t.Helper()
	g := &Graph{
		Nodes:    map[string]Node{},
		inbound:  map[string][]Edge{},
		outbound: map[string][]Edge{},
		byFile:   map[string][]string{},
	}
	for _, n := range nodes {
		if n.Kind == "" {
			n.Kind = "function"
		}
		if n.ID == "" {
			n.ID = n.File + ":" + n.Qualified
		}
		if n.Qualified == "" {
			n.Qualified = n.Name
			n.ID = n.File + ":" + n.Name
		}
		g.Nodes[n.ID] = n
		g.byFile[n.File] = append(g.byFile[n.File], n.ID)
	}
	for _, c := range calls {
		e := Edge{Source: c[0], Target: c[1], Kind: "calls"}
		g.Edges = append(g.Edges, e)
		g.inbound[e.Target] = append(g.inbound[e.Target], e)
		g.outbound[e.Source] = append(g.outbound[e.Source], e)
	}
	return g
}

func fn(file, name string) Node {
	return Node{ID: file + ":" + name, Name: name, Qualified: name, File: file, Kind: "function"}
}

func TestExportedIsDecidedFromTheNameNotTheIndex(t *testing.T) {
	// The index reports is_exported = 0 for exported Go methods, so metron
	// decides the way the language does.
	m := Node{Name: "GenBashCompletion", Qualified: "Command::GenBashCompletion", Kind: "method"}
	if !m.Exported() {
		t.Error("exported method should read as exported")
	}
	if m.Recv() != "Command" {
		t.Errorf("recv = %q, want Command", m.Recv())
	}
	if !fn("x.go", "Foo").Exported() || fn("x.go", "foo").Exported() {
		t.Error("export detection is wrong for plain functions")
	}
}

func TestOrphanSuppressedWhenTheNameIsUsedAsAValue(t *testing.T) {
	// `return defaultUsageFunc` produces no call edge. Every orphan the graph
	// alone reported on cobra was of exactly this shape.
	g := build(t, []Node{fn("a.go", "handler")}, nil)
	changed := []Node{g.Nodes["a.go:handler"]}
	app := func(string) bool { return true }

	unused := &IdentUse{uses: map[string]int{"handler": 1}, decls: map[string]int{"handler": 1}}
	if got := Orphans(g, changed, unused, app); len(got) != 1 {
		t.Fatalf("declared but never mentioned again: got %d findings, want 1", len(got))
	}

	valued := &IdentUse{uses: map[string]int{"handler": 2}, decls: map[string]int{"handler": 1}}
	if got := Orphans(g, changed, valued, app); len(got) != 0 {
		t.Errorf("used as a value: got %d findings, want 0 — %v", len(got), got[0].Title)
	}
}

func TestOrphanSkipsConventionEntryPointsAndLibraryAPI(t *testing.T) {
	nodes := []Node{
		fn("m.go", "main"), fn("m.go", "init"),
		fn("t_test.go", "TestThing"),
		fn("lib.go", "PublicAPI"),
		fn("lib.go", "privateHelper"),
	}
	g := build(t, nodes, nil)
	var changed []Node
	for _, n := range nodes {
		changed = append(changed, g.Nodes[n.ID])
	}
	iu := &IdentUse{uses: map[string]int{}, decls: map[string]int{}}

	// lib.go is a library package: an exported symbol with no callers is API.
	library := func(string) bool { return false }
	got := Orphans(g, changed, iu, library)
	if len(got) != 1 || got[0].Node.Name != "privateHelper" {
		t.Fatalf("got %v, want only privateHelper", titles(got))
	}

	// In an application package, an uncalled exported symbol is fair game.
	app := func(string) bool { return true }
	got = Orphans(g, changed, iu, app)
	if len(got) != 2 {
		t.Errorf("got %v, want privateHelper and PublicAPI", titles(got))
	}
}

func TestBypassedWrapperNeedsTheChangeToHaveIntroducedTheCall(t *testing.T) {
	// Without this, touching a function reports every call it has ever made.
	// On cobra that turned five no-op edits into five findings.
	nodes := []Node{
		fn("a.go", "target"), fn("a.go", "wrapper"),
		fn("a.go", "c1"), fn("a.go", "c2"), fn("a.go", "c3"),
		fn("a.go", "newcode"),
	}
	g := build(t, nodes, [][2]string{
		{"a.go:wrapper", "a.go:target"},
		{"a.go:c1", "a.go:wrapper"}, {"a.go:c2", "a.go:wrapper"}, {"a.go:c3", "a.go:wrapper"},
		{"a.go:newcode", "a.go:target"},
	})
	changed := []Node{g.Nodes["a.go:newcode"]}
	th := DefaultThresholds()

	stale := NewCallees{}
	if got := BypassedWrappers(g, changed, stale, th); len(got) != 0 {
		t.Errorf("pre-existing call: got %d findings, want 0", len(got))
	}

	fresh := NewCallees{"a.go:newcode": {"target": true}}
	got := BypassedWrappers(g, changed, fresh, th)
	if len(got) != 1 {
		t.Fatalf("newly introduced call: got %d findings, want 1", len(got))
	}
	if got[0].Other.Name != "wrapper" {
		t.Errorf("named %q as the sanctioned path, want wrapper", got[0].Other.Name)
	}
}

func TestWidelyCalledTargetHasNoSanctionedPath(t *testing.T) {
	// A utility nineteen places call directly is not funnelled through anything,
	// however popular one of those callers is.
	nodes := []Node{fn("a.go", "util"), fn("a.go", "popular"), fn("a.go", "newcode")}
	for _, n := range []string{"d1", "d2", "d3", "p1", "p2", "p3"} {
		nodes = append(nodes, fn("a.go", n))
	}
	calls := [][2]string{
		{"a.go:popular", "a.go:util"},
		{"a.go:d1", "a.go:util"}, {"a.go:d2", "a.go:util"}, {"a.go:d3", "a.go:util"},
		{"a.go:p1", "a.go:popular"}, {"a.go:p2", "a.go:popular"}, {"a.go:p3", "a.go:popular"},
		{"a.go:newcode", "a.go:util"},
	}
	g := build(t, nodes, calls)
	changed := []Node{g.Nodes["a.go:newcode"]}
	nc := NewCallees{"a.go:newcode": {"util": true}}

	if got := BypassedWrappers(g, changed, nc, DefaultThresholds()); len(got) != 0 {
		t.Errorf("got %v, want none", titles(got))
	}
}

func TestTokensSplitCamelAndAcronyms(t *testing.T) {
	tests := []struct {
		in   string
		want []string
	}{
		{"parseTimeWindow", []string{"parse", "time", "window"}},
		{"HTTPHandler", []string{"http", "handler"}},
		{"snake_case_name", []string{"snake", "case", "name"}},
		{"X", []string{"x"}},
	}
	for _, tc := range tests {
		got := tokens(tc.in)
		if len(got) != len(tc.want) {
			t.Errorf("%s: got %v, want %v", tc.in, got, tc.want)
			continue
		}
		for i := range got {
			if got[i] != tc.want[i] {
				t.Errorf("%s: got %v, want %v", tc.in, got, tc.want)
				break
			}
		}
	}
}

func TestJaccardAndNameOverlap(t *testing.T) {
	a := map[string]struct{}{"x": {}, "y": {}, "z": {}}
	b := map[string]struct{}{"x": {}, "y": {}, "w": {}}
	if got := jaccard(a, b); got < 0.49 || got > 0.51 {
		t.Errorf("jaccard = %.2f, want 0.5", got)
	}
	if got := jaccard(a, nil); got != 0 {
		t.Errorf("jaccard with empty = %.2f, want 0", got)
	}
	if nameOverlap("parseWindow", "parseTimeWindow") <= nameOverlap("parseWindow", "writeFile") {
		t.Error("related names should overlap more than unrelated ones")
	}
}

func titles(fs []Finding) []string {
	out := make([]string, len(fs))
	for i, f := range fs {
		out[i] = f.Title
	}
	return out
}

func node(file, name, sig string) Node {
	return Node{ID: file + ":" + name, Name: name, Qualified: name,
		File: file, Kind: "function", Signature: sig}
}

func TestLayerCrossingsOnlyFlagDirectionsWithNoPrecedent(t *testing.T) {
	// A dependency the repository already draws dozens of times is the
	// architecture, not a violation of it.
	nodes := []Node{
		node("api/a.go", "Handler", "()"),
		node("api/b.go", "Other", "()"),
		node("core/c.go", "Helper", "()"),
		node("secret/s.go", "Peek", "()"),
		node("api/new.go", "Fresh", "()"),
	}
	g := build(t, nodes, [][2]string{
		{"api/b.go:Other", "core/c.go:Helper"},   // api -> core has precedent
		{"api/new.go:Fresh", "core/c.go:Helper"}, // so this is fine
		{"api/new.go:Fresh", "secret/s.go:Peek"}, // api -> secret has none
	})
	changed := []Node{g.Nodes["api/new.go:Fresh"]}
	nc := NewCallees{"api/new.go:Fresh": {"Helper": true, "Peek": true}}

	got := LayerCrossings(g, changed, nc)
	if len(got) != 1 {
		t.Fatalf("got %v, want only the unprecedented direction", titles(got))
	}
	if !strings.Contains(got[0].Title, "secret/") {
		t.Errorf("flagged the wrong direction: %s", got[0].Title)
	}
}

func TestLayerCrossingIgnoresEdgesTheChangeDidNotDraw(t *testing.T) {
	nodes := []Node{node("api/a.go", "Old", "()"), node("secret/s.go", "Peek", "()")}
	g := build(t, nodes, [][2]string{{"api/a.go:Old", "secret/s.go:Peek"}})
	changed := []Node{g.Nodes["api/a.go:Old"]}

	if got := LayerCrossings(g, changed, NewCallees{}); len(got) != 0 {
		t.Errorf("a pre-existing dependency is not this change's doing: %v", titles(got))
	}
}

func TestSiblingDivergenceNeedsAnOverwhelmingConvention(t *testing.T) {
	// A mixed directory says nothing, and asserting a convention from a slim
	// majority would make the rule noise.
	withCtx := func(i int) Node {
		return node("svc/f.go", "Follower"+string(rune('A'+i)), "(ctx context.Context) error")
	}
	var nodes []Node
	for i := 0; i < 5; i++ {
		nodes = append(nodes, withCtx(i))
	}
	odd := node("svc/new.go", "Odd", "(n int) error")
	nodes = append(nodes, odd)

	g := build(t, nodes, nil)
	changed := []Node{g.Nodes[odd.ID]}

	got := SiblingDivergence(g, changed, 5, 0.8)
	if len(got) != 1 {
		t.Fatalf("got %v, want the ctx convention flagged", titles(got))
	}
	if !strings.Contains(got[0].Title, "context.Context") {
		t.Errorf("wrong convention: %s", got[0].Title)
	}

	// The same divergence with too few neighbours proves nothing.
	if got := SiblingDivergence(g, changed, 50, 0.8); len(got) != 0 {
		t.Errorf("a directory this small cannot establish a convention: %v", titles(got))
	}
}

func TestSiblingDivergenceIgnoresAConventionTheChangeItselfSets(t *testing.T) {
	// Other functions in the same change cannot vouch for a convention.
	var nodes []Node
	for i := 0; i < 5; i++ {
		nodes = append(nodes, node("svc/f.go", "New"+string(rune('A'+i)), "(ctx context.Context) error"))
	}
	odd := node("svc/new.go", "Odd", "(n int) error")
	nodes = append(nodes, odd)
	g := build(t, nodes, nil)

	var allChanged []Node
	for _, n := range nodes {
		allChanged = append(allChanged, g.Nodes[n.ID])
	}
	if got := SiblingDivergence(g, allChanged, 5, 0.8); len(got) != 0 {
		t.Errorf("the change cannot establish the convention it is judged against: %v", titles(got))
	}
}

func TestConventionDetection(t *testing.T) {
	tests := []struct {
		sig       string
		ctx, errs bool
	}{
		{"(ctx context.Context, n int) error", true, true},
		{"(n int, ctx context.Context) error", false, true},
		{"(ctx context.Context)", true, false},
		{"(n int) (int, error)", false, true},
		{"(n int) int", false, false},
		// A func-typed parameter must not be mistaken for the end of the list.
		{"(f func() error, n int) (string, error)", false, true},
		{"(ctx context.Context, f func(int) int) error", true, true},
	}
	for _, tc := range tests {
		n := Node{Signature: tc.sig}
		if got := takesContextFirst(n); got != tc.ctx {
			t.Errorf("%q: ctx-first = %v, want %v", tc.sig, got, tc.ctx)
		}
		if got := returnsError(n); got != tc.errs {
			t.Errorf("%q: returns-error = %v, want %v", tc.sig, got, tc.errs)
		}
	}
}

func TestNodeShape(t *testing.T) {
	m := Node{Name: "Do", Qualified: "Server::Do", File: "svc/a.go", Kind: "method"}
	if m.Dir() != "svc/" {
		t.Errorf("Dir() = %q", m.Dir())
	}
	if m.Label() != "(Server).Do" {
		t.Errorf("Label() = %q", m.Label())
	}
	if m.Key() != "svc/a.go:(Server).Do" {
		t.Errorf("Key() = %q", m.Key())
	}
	top := Node{Name: "F", Qualified: "F", File: "a.go", Kind: "function"}
	if top.Dir() != "./" || top.Label() != "F" {
		t.Errorf("root-level node: dir=%q label=%q", top.Dir(), top.Label())
	}
	if !top.IsFunc() || (Node{Kind: "struct"}).IsFunc() {
		t.Error("IsFunc is wrong")
	}
}
