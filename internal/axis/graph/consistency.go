package graph

import (
	"fmt"
	"sort"
	"strings"
)

// BypassedWrappers finds changed code that reaches a target directly when the
// rest of the repository goes through a wrapper.
//
// The shape: some function W calls T and has many callers of its own, while T
// has few direct callers besides W. W is how you are supposed to get to T —
// it is where the retry, the locking, the auth check lives — and new code
// calling T directly has stepped around it.
func BypassedWrappers(g *Graph, changed []Node, nc NewCallees, th Thresholds) []Finding {
	changedIDs := map[string]bool{}
	for _, n := range changed {
		changedIDs[n.ID] = true
	}

	var out []Finding
	seen := map[string]bool{}

	for _, n := range changed {
		if !n.IsFunc() || isTestFile(n.File) {
			continue
		}
		for target := range g.Callees(n.ID) {
			t, ok := g.Nodes[target]
			if !ok || !t.IsFunc() || target == n.ID {
				continue // a function calling itself has bypassed nothing
			}
			if !nc.Gained(n.Key(), t.Name) {
				continue // the call was already there; the change did not introduce it
			}
			w, found := dominantWrapper(g, target, n.ID, changedIDs, th)
			if !found {
				continue
			}
			key := n.ID + "->" + target
			if seen[key] {
				continue
			}
			seen[key] = true
			wrapper := w
			out = append(out, Finding{
				Rule: "bypassed-wrapper", Node: n, Other: &wrapper,
				Title: fmt.Sprintf("%s calls %s directly, stepping around %s", n.Label(), t.Label(), w.Label()),
				Detail: fmt.Sprintf("%s has %d callers, while %s has only %d other direct ones · %s:%d",
					w.Label(), len(g.CallersOf(w.ID)), t.Label(),
					directCallersExcluding(g, target, w.ID, changedIDs), w.File, w.StartLine),
			})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Title < out[j].Title })
	return out
}

// dominantWrapper looks for the established way in to a target.
//
// The test that matters is that the target is *not* reached directly by anyone
// else. An early version compared the candidate wrapper's popularity against
// the target's direct callers, which is meaningless: on cobra it concluded
// that AddCommand — a method with 135 callers that happens to call
// CommandPath once — was the sanctioned path to CommandPath, a utility
// nineteen other places call directly. Six such findings, all nonsense.
//
// A genuine wrapper funnels its target: almost nothing calls T except W, and
// plenty of things call W. The wrapper also has to live beside what it wraps,
// since a funnel spanning packages is a coincidence more often than a rule,
// and it has to be thin — cobra's IsAvailableCommand has four branches and
// seven collaborators, and calling one of them once does not make it the
// sanctioned path to that one.
func dominantWrapper(g *Graph, target, from string, changed map[string]bool, th Thresholds) (Node, bool) {
	t, ok := g.Nodes[target]
	if !ok {
		return Node{}, false
	}
	var best Node
	bestCallers := 0

	for _, w := range g.CallersOf(target) {
		if w == from || changed[w] {
			continue // the change itself cannot be the established path
		}
		wn, ok := g.Nodes[w]
		if !ok || !wn.IsFunc() || isTestFile(wn.File) || wn.Dir() != t.Dir() {
			continue
		}
		callers := len(g.CallersOf(w))
		if callers < th.WrapperCallers {
			continue
		}
		if directCallersExcluding(g, target, w, changed) > th.MaxDirectCallers {
			continue // widely called directly: there is no single way in to step around
		}
		if len(g.Callees(w)) > th.MaxWrapperCallees {
			continue // thick logic, not a funnel
		}
		if callers > bestCallers {
			best, bestCallers = wn, callers
		}
	}
	return best, bestCallers > 0
}

func directCallersExcluding(g *Graph, target, wrapper string, changed map[string]bool) int {
	n := 0
	for _, c := range g.CallersOf(target) {
		if c == wrapper || changed[c] {
			continue
		}
		if cn, ok := g.Nodes[c]; ok && isTestFile(cn.File) {
			continue
		}
		n++
	}
	return n
}

// LayerCrossings finds dependencies in a direction the repository has never
// taken before.
//
// Every established (source directory → target directory) pair carries a
// frequency, and on a real codebase that distribution is steep — the top pairs
// have dozens of edges. A changed function reaching into a directory nothing
// in its own directory has ever reached is a new architectural edge, and it is
// usually one nobody decided on.
//
// The precedent is computed from edges that do not originate in the change, so
// the change cannot vouch for itself.
func LayerCrossings(g *Graph, changed []Node, nc NewCallees) []Finding {
	changedIDs := map[string]bool{}
	for _, n := range changed {
		changedIDs[n.ID] = true
	}

	precedent := map[string]int{}
	for _, e := range g.Edges {
		if e.Kind != "calls" && e.Kind != "instantiates" {
			continue
		}
		if changedIDs[e.Source] {
			continue
		}
		src, sok := g.Nodes[e.Source]
		dst, dok := g.Nodes[e.Target]
		if !sok || !dok || isTestFile(src.File) {
			continue
		}
		if src.Dir() == dst.Dir() {
			continue
		}
		precedent[src.Dir()+"->"+dst.Dir()]++
	}

	var out []Finding
	seen := map[string]bool{}
	for _, n := range changed {
		if !n.IsFunc() || isTestFile(n.File) {
			continue
		}
		for target := range g.Callees(n.ID) {
			t, ok := g.Nodes[target]
			if !ok || t.Dir() == n.Dir() {
				continue
			}
			if !nc.Gained(n.Key(), t.Name) {
				continue // a pre-existing dependency, not one this change drew
			}
			pair := n.Dir() + "->" + t.Dir()
			if precedent[pair] > 0 || seen[pair] {
				continue
			}
			seen[pair] = true
			tt := t
			out = append(out, Finding{
				Rule: "layer-crossing", Node: n, Other: &tt,
				Title:  fmt.Sprintf("nothing in this repository has ever gone %s → %s", n.Dir(), t.Dir()),
				Detail: fmt.Sprintf("%s calls %s (%s:%d)", n.Label(), t.Label(), t.File, t.StartLine),
			})
		}
	}
	return out
}

// SiblingDivergence finds a changed function that breaks a convention its
// neighbours all follow.
//
// Only two conventions are checked, both mechanical and both common enough in
// Go to be worth a reading: taking a context as the first parameter, and
// returning an error. A convention is only asserted when the neighbours are
// overwhelmingly consistent about it, so a mixed directory says nothing.
func SiblingDivergence(g *Graph, changed []Node, minSiblings int, minShare float64) []Finding {
	byDir := map[string][]Node{}
	for _, n := range g.Nodes {
		if n.IsFunc() && !isTestFile(n.File) {
			byDir[n.Dir()] = append(byDir[n.Dir()], n)
		}
	}

	changedIDs := map[string]bool{}
	for _, n := range changed {
		changedIDs[n.ID] = true
	}

	conventions := []struct {
		name  string
		label string
		holds func(Node) bool
	}{
		{"ctx-first", "a context.Context first parameter", takesContextFirst},
		{"returns-error", "returning an error", returnsError},
	}

	var out []Finding
	for _, n := range changed {
		if !n.IsFunc() || isTestFile(n.File) || n.Signature == "" {
			continue
		}
		siblings := make([]Node, 0, len(byDir[n.Dir()]))
		for _, s := range byDir[n.Dir()] {
			if s.ID != n.ID && !changedIDs[s.ID] && s.Signature != "" {
				siblings = append(siblings, s)
			}
		}
		if len(siblings) < minSiblings {
			continue
		}
		for _, c := range conventions {
			if c.holds(n) {
				continue
			}
			hold := 0
			for _, s := range siblings {
				if c.holds(s) {
					hold++
				}
			}
			share := float64(hold) / float64(len(siblings))
			if share < minShare {
				continue
			}
			out = append(out, Finding{
				Rule: "sibling-divergence", Node: n,
				Title: fmt.Sprintf("%s breaks a convention its neighbours in %s follow: %s", n.Label(), n.Dir(), c.label),
				Detail: fmt.Sprintf("%d of %d functions in that directory do it (%.0f%%)",
					len(siblings), hold, share*100),
			})
		}
	}
	return out
}

func takesContextFirst(n Node) bool {
	s := strings.TrimPrefix(n.Signature, "(")
	i := strings.IndexAny(s, ",)")
	if i < 0 {
		return false
	}
	return strings.Contains(s[:i], "context.Context")
}

func returnsError(n Node) bool {
	i := strings.LastIndex(n.Signature, ")")
	if i < 0 || i+1 >= len(n.Signature) {
		return false
	}
	return strings.Contains(n.Signature[i+1:], "error")
}
