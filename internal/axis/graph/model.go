package graph

import (
	"strings"
	"unicode"
)

// Node is one symbol in the index.
type Node struct {
	ID        string
	Kind      string // function | method | struct | interface | constant | variable | ...
	Name      string
	Qualified string // "Command::Execute" for methods; the bare name for functions
	File      string // repo-relative
	StartLine int
	EndLine   int
	Signature string
}

// IsFunc reports whether this node is callable code worth measuring.
func (n Node) IsFunc() bool { return n.Kind == "function" || n.Kind == "method" }

// Exported reports whether Go would export this symbol.
//
// The index's own is_exported column is unreliable for Go methods — it reads 0
// for exported ones — so metron decides from the name, which is what the Go
// language does anyway.
func (n Node) Exported() bool {
	for _, r := range n.Name {
		return unicode.IsUpper(r)
	}
	return false
}

// Recv returns a method's receiver type, or "" for a plain function.
func (n Node) Recv() string {
	if i := strings.Index(n.Qualified, "::"); i >= 0 {
		return n.Qualified[:i]
	}
	return ""
}

// Key identifies a symbol the same way target.Func does, so index nodes and
// parsed declarations can be matched up.
func (n Node) Key() string {
	if r := n.Recv(); r != "" {
		return n.File + ":(" + r + ")." + n.Name
	}
	return n.File + ":" + n.Name
}

func (n Node) Label() string {
	if r := n.Recv(); r != "" {
		return "(" + r + ")." + n.Name
	}
	return n.Name
}

// Dir is the directory the symbol lives in, used for layering questions.
func (n Node) Dir() string {
	if i := strings.LastIndex(n.File, "/"); i >= 0 {
		return n.File[:i+1]
	}
	return "./"
}

// Edge is one relationship between symbols.
type Edge struct {
	Source string
	Target string
	Kind   string // calls | contains | imports | references | instantiates
	Line   int
}

// Graph is the loaded index.
type Graph struct {
	Nodes    map[string]Node
	Edges    []Edge
	inbound  map[string][]Edge
	outbound map[string][]Edge
	byFile   map[string][]string
}

func (g *Graph) In(id string) []Edge  { return g.inbound[id] }
func (g *Graph) Out(id string) []Edge { return g.outbound[id] }

// FileNodes returns the symbol ids declared in a file.
func (g *Graph) FileNodes(path string) []string { return g.byFile[path] }

// Callees returns the distinct call targets of a symbol. This set is the
// structural fingerprint used to spot two functions doing the same job.
func (g *Graph) Callees(id string) map[string]struct{} {
	out := map[string]struct{}{}
	for _, e := range g.outbound[id] {
		if e.Kind == "calls" || e.Kind == "instantiates" {
			out[e.Target] = struct{}{}
		}
	}
	return out
}

// Referenced reports whether anything in the graph points at this symbol.
func (g *Graph) Referenced(id string) bool {
	for _, e := range g.inbound[id] {
		switch e.Kind {
		case "calls", "references", "instantiates":
			return true
		}
	}
	return false
}

// CallersOf returns the distinct symbols that call a target.
func (g *Graph) CallersOf(id string) []string {
	seen := map[string]bool{}
	var out []string
	for _, e := range g.inbound[id] {
		if e.Kind == "calls" && !seen[e.Source] {
			seen[e.Source] = true
			out = append(out, e.Source)
		}
	}
	return out
}
