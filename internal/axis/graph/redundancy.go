package graph

import (
	"fmt"
	"sort"
	"strings"
)

// Finding is one thing the graph noticed.
type Finding struct {
	Rule   string // "orphan" | "near-duplicate" | "reimplementation" | ...
	Node   Node
	Other  *Node // the counterpart, for pairwise rules
	Title  string
	Detail string
}

// Thresholds tune the duplicate rules. They are deliberately conservative:
// a false positive here costs the user's trust in every other reading.
type Thresholds struct {
	MinCallees     int     // ignore trivial functions — everything looks alike below this
	CalleeJaccard  float64 // structural similarity to call it a duplicate
	NameOverlap    float64 // required name-word overlap on top of structure
	WrapperCallers int     // how established a wrapper must be to count as the way in
	// MaxDirectCallers is how many callers a target may already have and still
	// be considered funnelled through a wrapper. A symbol nineteen places call
	// directly has no single established path, however popular one of those
	// callers happens to be.
	MaxDirectCallers int
	// MaxWrapperCallees keeps the wrapper thin. A funnel does one thing: it
	// calls the target and adds a concern around it. Thick business logic that
	// happens to call the target once is not the sanctioned path to it.
	MaxWrapperCallees int
}

func DefaultThresholds() Thresholds {
	return Thresholds{
		MinCallees: 3, CalleeJaccard: 0.6, NameOverlap: 0.3,
		WrapperCallers: 3, MaxDirectCallers: 1, MaxWrapperCallees: 4,
	}
}

// Orphans finds changed symbols nothing reaches.
//
// Three filters keep this honest, each added after watching the rule misfire
// on real indexes:
//
//   - Convention entry points (main, init, Test*, interface methods) are called
//     by the language or a framework, not by name.
//   - A library's exported symbols have no internal callers by design. Only
//     unexported symbols, and exported ones inside main or internal packages,
//     can be judged.
//   - Go passes functions as values constantly, and the index records no call
//     edge for that. Every orphan cobra reported was of this kind, so a symbol
//     whose identifier appears anywhere in the source is not orphaned.
func Orphans(g *Graph, changed []Node, iu *IdentUse, appPkg func(file string) bool) []Finding {
	var out []Finding
	for _, n := range changed {
		if !n.IsFunc() || isTestFile(n.File) || conventionEntryPoint(n) {
			continue
		}
		if n.Exported() && !appPkg(n.File) {
			continue // public API of a library; absence of callers proves nothing
		}
		if g.Referenced(n.ID) || iu.Used(n.Name) {
			continue
		}
		out = append(out, Finding{
			Rule: "orphan", Node: n,
			Title:  n.Label() + " is never reached",
			Detail: "no inbound edge in the graph, and the identifier appears nowhere else in the source",
		})
	}
	return out
}

// NearDuplicates finds changed functions that do the same job as one that
// already exists.
//
// Two signals must agree. Structural similarity alone over-fires on sibling
// variants — complete_text against complete_json, update_scenario against
// create_scenario — so the names must overlap too, and the pair must live in
// different files: two similar helpers side by side are usually intentional.
//
// Only changed symbols are compared against the repository, never every pair
// against every other, which would be quadratic in the size of the index.
func NearDuplicates(g *Graph, changed []Node, th Thresholds) []Finding {
	var out []Finding
	seen := map[string]bool{}

	for _, n := range changed {
		if !n.IsFunc() || isTestFile(n.File) {
			continue
		}
		mine := g.Callees(n.ID)
		if len(mine) < th.MinCallees {
			continue
		}

		type cand struct {
			node  Node
			jac   float64
			names float64
		}
		var best *cand
		for id, other := range g.Nodes {
			if id == n.ID || !other.IsFunc() || other.File == n.File || isTestFile(other.File) {
				continue
			}
			theirs := g.Callees(id)
			if len(theirs) < th.MinCallees {
				continue
			}
			j := jaccard(mine, theirs)
			if j < th.CalleeJaccard {
				continue
			}
			no := nameOverlap(n.Name, other.Name)
			if no < th.NameOverlap {
				continue
			}
			if best == nil || j > best.jac {
				c := cand{node: other, jac: j, names: no}
				best = &c
			}
		}
		if best == nil {
			continue
		}
		key := pairKey(n.ID, best.node.ID)
		if seen[key] {
			continue
		}
		seen[key] = true

		other := best.node
		out = append(out, Finding{
			Rule: "near-duplicate", Node: n, Other: &other,
			Title: fmt.Sprintf("%s and %s do the same job", n.Label(), other.Label()),
			Detail: fmt.Sprintf("calls almost the same set (Jaccard %.2f) and the names overlap (%.2f) · %s:%d",
				best.jac, best.names, other.File, other.StartLine),
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Node.File < out[j].Node.File })
	return out
}

// Reimplementations finds a changed function that mirrors an existing one's
// name and signature without using it — the shape of an agent that could not
// find the helper that already existed and wrote its own.
func Reimplementations(g *Graph, changed []Node, th Thresholds) []Finding {
	var out []Finding
	for _, n := range changed {
		if !n.IsFunc() || isTestFile(n.File) || n.Signature == "" {
			continue
		}
		callees := g.Callees(n.ID)

		for id, other := range g.Nodes {
			if id == n.ID || !other.IsFunc() || other.File == n.File || isTestFile(other.File) {
				continue
			}
			if other.Signature != n.Signature {
				continue
			}
			if nameOverlap(n.Name, other.Name) < 0.5 {
				continue
			}
			if _, calls := callees[id]; calls {
				continue // it delegates to the original; that is reuse, not duplication
			}
			o := other
			out = append(out, Finding{
				Rule: "reimplementation", Node: n, Other: &o,
				Title: fmt.Sprintf("%s may be reimplementing %s", n.Label(), other.Label()),
				Detail: fmt.Sprintf("identical signature %s and a near-identical name, but it never calls it · %s:%d",
					n.Signature, other.File, other.StartLine),
			})
			break
		}
	}
	return out
}

func pairKey(a, b string) string {
	if a > b {
		a, b = b, a
	}
	return a + "|" + b
}

func isTestFile(path string) bool { return strings.HasSuffix(path, "_test.go") }
