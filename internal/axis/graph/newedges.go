package graph

import (
	"go/ast"
	"go/parser"
	"go/token"
)

// NewCallees is, per changed function, the set of callee names the change
// introduced — present in the working tree, absent at the merge base.
//
// This distinction is the difference between the consistency rules working and
// being noise. Both of them ask about edges the change *added*; without the
// base comparison they fire on every call a merely-touched function has always
// made. Measured on cobra, adding a single comment line to five untouched
// functions produced five bypassed-wrapper findings, every one of them about a
// call that had been there for years.
//
// Comparing callee names parsed from the two revisions is enough, and it costs
// one extra parse of a file already being read for the complexity axis.
type NewCallees map[string]map[string]bool

// Add records the callee names a function gained.
func (nc NewCallees) Add(fnKey string, names map[string]bool) {
	if len(names) > 0 {
		nc[fnKey] = names
	}
}

// Gained reports whether a function newly calls a name.
func (nc NewCallees) Gained(fnKey, callee string) bool {
	return nc[fnKey][callee]
}

// Introduced reports whether the function introduced any call at all. A
// function whose call set did not change cannot have bypassed anything.
func (nc NewCallees) Introduced(fnKey string) bool { return len(nc[fnKey]) > 0 }

// CalleeNames returns the names each top-level function in src calls, keyed by
// the same identity target.Func uses.
func CalleeNames(path string, src []byte) map[string]map[string]bool {
	out := map[string]map[string]bool{}
	if len(src) == 0 {
		return out
	}
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, path, src, 0)
	if err != nil {
		return out
	}
	for _, d := range f.Decls {
		fd, ok := d.(*ast.FuncDecl)
		if !ok || fd.Body == nil {
			continue
		}
		names := map[string]bool{}
		ast.Inspect(fd.Body, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			switch fn := call.Fun.(type) {
			case *ast.Ident:
				names[fn.Name] = true
			case *ast.SelectorExpr:
				names[fn.Sel.Name] = true
			}
			return true
		})
		out[funcKey(path, fd)] = names
	}
	return out
}

// Diff returns the names present in cur but not in base.
func Diff(cur, base map[string]bool) map[string]bool {
	out := map[string]bool{}
	for n := range cur {
		if !base[n] {
			out[n] = true
		}
	}
	return out
}

func funcKey(path string, fd *ast.FuncDecl) string {
	recv := ""
	if fd.Recv != nil && len(fd.Recv.List) > 0 {
		recv = exprName(fd.Recv.List[0].Type)
	}
	if recv != "" {
		return path + ":(" + recv + ")." + fd.Name.Name
	}
	return path + ":" + fd.Name.Name
}

func exprName(e ast.Expr) string {
	switch t := e.(type) {
	case *ast.Ident:
		return t.Name
	case *ast.StarExpr:
		return exprName(t.X)
	case *ast.IndexExpr:
		return exprName(t.X)
	case *ast.IndexListExpr:
		return exprName(t.X)
	case *ast.SelectorExpr:
		return t.Sel.Name
	}
	return ""
}
