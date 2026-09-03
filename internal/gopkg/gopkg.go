// Package gopkg answers questions about a module's package graph using the go
// command itself, so the answers match what `go test` will actually do.
package gopkg

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
)

// Package is the slice of `go list -json` metadata metron needs.
type Package struct {
	ImportPath   string
	Dir          string
	Name         string
	GoFiles      []string
	CgoFiles     []string
	TestGoFiles  []string
	XTestGoFiles []string
	Deps         []string
	Standard     bool
}

// HasTests reports whether anything would run for this package.
func (p Package) HasTests() bool { return len(p.TestGoFiles)+len(p.XTestGoFiles) > 0 }

// UsesCgo reports whether overlay-based mutation is unsafe here. The -overlay
// flag has documented limitations with cgo files pulled in from outside the
// include path, so those packages are skipped with a stated reason rather than
// mutated unreliably.
func (p Package) UsesCgo() bool { return len(p.CgoFiles) > 0 }

// Graph is the module's packages plus the reverse-dependency edges.
type Graph struct {
	Root       string
	ByPath     map[string]Package
	byDir      map[string]string   // absolute dir -> import path
	dependents map[string][]string // import path -> packages that import it
}

// Load runs `go list -json ./...` in root.
func Load(ctx context.Context, root string) (*Graph, error) {
	cmd := exec.CommandContext(ctx, "go", "list", "-json", "./...")
	cmd.Dir = root
	out, err := cmd.Output()
	if err != nil {
		var ee *exec.ExitError
		if ok := asExit(err, &ee); ok && len(ee.Stderr) > 0 {
			return nil, fmt.Errorf("go list: %s", strings.TrimSpace(string(ee.Stderr)))
		}
		return nil, fmt.Errorf("go list: %w", err)
	}

	g := &Graph{
		Root: root, ByPath: map[string]Package{},
		byDir: map[string]string{}, dependents: map[string][]string{},
	}
	dec := json.NewDecoder(strings.NewReader(string(out)))
	for dec.More() {
		var p Package
		if err := dec.Decode(&p); err != nil {
			return nil, fmt.Errorf("decode go list output: %w", err)
		}
		g.ByPath[p.ImportPath] = p
		g.byDir[p.Dir] = p.ImportPath
	}
	for _, p := range g.ByPath {
		for _, d := range p.Deps {
			if _, local := g.ByPath[d]; local {
				g.dependents[d] = append(g.dependents[d], p.ImportPath)
			}
		}
	}
	return g, nil
}

// PackageFor maps a repo-relative file to its import path.
func (g *Graph) PackageFor(relFile string) (Package, bool) {
	dir := filepath.Dir(filepath.Join(g.Root, relFile))
	ip, ok := g.byDir[dir]
	if !ok {
		return Package{}, false
	}
	return g.ByPath[ip], true
}

// TestScope returns the packages worth running when code in importPath changes:
// the package itself plus everything in the module that depends on it, keeping
// only those that actually hold tests.
//
// This is where nearly all of the real saving in test selection lives. Going
// finer — picking individual test functions — was measured to cost far more to
// compute than it saves, because linking and starting the test binary is most
// of a mutant's cost and no selection avoids that.
func (g *Graph) TestScope(importPath string) []string {
	seen := map[string]bool{importPath: true}
	queue := []string{importPath}
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		for _, d := range g.dependents[cur] {
			if !seen[d] {
				seen[d] = true
				queue = append(queue, d)
			}
		}
	}

	var out []string
	for p := range seen {
		if pkg, ok := g.ByPath[p]; ok && pkg.HasTests() {
			out = append(out, p)
		}
	}
	sort.Strings(out) // deterministic command lines, deterministic scores
	return out
}

// DirOf returns a package's absolute directory.
func (g *Graph) DirOf(importPath string) string { return g.ByPath[importPath].Dir }

func asExit(err error, dst **exec.ExitError) bool {
	if ee, ok := err.(*exec.ExitError); ok {
		*dst = ee
		return true
	}
	return false
}
