package graph

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// IdentUse counts how often each identifier appears in the repository's Go
// source, separately from how often it is declared.
//
// This exists to keep the orphan rule honest. CodeGraph records a `calls` edge
// when a function is invoked, but Go code passes functions around as values
// constantly — `return defaultUsageFunc`, `cmd.Run = handler`, a func in a
// struct literal — and none of those produce a call edge. Measured on cobra,
// every one of the three symbols the graph alone called orphaned was in fact
// used exactly that way. Cross-checking against real identifier occurrences
// removed all three.
//
// The check is deliberately conservative: it can only ever suppress a finding,
// never create one.
type IdentUse struct {
	uses     map[string]int
	decls    map[string]int
	pkgByDir map[string]string
}

// AppPackage reports whether a file lives in a package whose exported symbols
// are still internal to this repository — a main package, or anything under
// internal/. Elsewhere, an exported symbol with no callers is public API and
// says nothing about dead code.
func (iu *IdentUse) AppPackage(file string) bool {
	if iu == nil {
		return false
	}
	if strings.Contains("/"+file, "/internal/") {
		return true
	}
	dir := "./"
	if i := strings.LastIndex(file, "/"); i >= 0 {
		dir = file[:i+1]
	}
	return iu.pkgByDir[dir] == "main"
}

// Used reports whether an identifier appears anywhere beyond its declarations.
func (iu *IdentUse) Used(name string) bool {
	if iu == nil {
		return false
	}
	return iu.uses[name] > iu.decls[name]
}

// ScanIdents walks the module's Go source and tallies identifier occurrences.
// Test files count: a symbol exercised only by tests is not dead code.
func ScanIdents(root string) (*IdentUse, error) {
	iu := &IdentUse{uses: map[string]int{}, decls: map[string]int{}, pkgByDir: map[string]string{}}

	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil // an unreadable corner of the tree is not worth failing over
		}
		if d.IsDir() {
			if skipDir(d.Name()) && path != root {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") {
			return nil
		}
		src, err := os.ReadFile(path)
		if err != nil {
			return nil
		}
		fset := token.NewFileSet()
		f, err := parser.ParseFile(fset, path, src, 0)
		if err != nil {
			return nil
		}
		collect(f, iu)
		if rel, rerr := filepath.Rel(root, path); rerr == nil {
			dir := "./"
			if i := strings.LastIndex(filepath.ToSlash(rel), "/"); i >= 0 {
				dir = filepath.ToSlash(rel)[:i+1]
			}
			// A directory holds one non-test package; main wins if present.
			if iu.pkgByDir[dir] == "" || f.Name.Name == "main" {
				iu.pkgByDir[dir] = f.Name.Name
			}
		}
		return nil
	})
	return iu, err
}

func collect(f *ast.File, iu *IdentUse) {
	for _, d := range f.Decls {
		switch decl := d.(type) {
		case *ast.FuncDecl:
			iu.decls[decl.Name.Name]++
		case *ast.GenDecl:
			for _, spec := range decl.Specs {
				switch s := spec.(type) {
				case *ast.TypeSpec:
					iu.decls[s.Name.Name]++
				case *ast.ValueSpec:
					for _, n := range s.Names {
						iu.decls[n.Name]++
					}
				}
			}
		}
	}
	ast.Inspect(f, func(n ast.Node) bool {
		if id, ok := n.(*ast.Ident); ok {
			iu.uses[id.Name]++
		}
		return true
	})
}

func skipDir(name string) bool {
	switch name {
	case ".git", "vendor", "node_modules", "testdata", ".codegraph", ".metron":
		return true
	}
	return strings.HasPrefix(name, ".")
}
