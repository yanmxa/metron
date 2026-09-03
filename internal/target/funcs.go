package target

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"sort"
	"strings"
)

// Generated marks files written by a tool. Mutating or grading them measures
// the generator, not the change.
const generatedMarker = "Code generated"

// ChangedFuncs maps a file's changed line ranges onto the functions that
// enclose them. src is the file's current contents.
//
// The unit is the function, not the line, because every axis is about
// functions: you mutate inside one, you score the complexity of one, and you
// ask the graph what calls one.
func ChangedFuncs(path string, src []byte, ranges []LineRange) ([]Func, error) {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, path, src, parser.ParseComments)
	if err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	if IsGenerated(f) {
		return nil, nil
	}

	var out []Func
	for _, fn := range AllFuncs(fset, f, path) {
		for _, r := range ranges {
			if r.Overlaps(fn.StartLine, fn.EndLine) {
				out = append(out, fn)
				break
			}
		}
	}
	return out, nil
}

// AllFuncs lists every function and method with a body in a parsed file.
func AllFuncs(fset *token.FileSet, f *ast.File, path string) []Func {
	var out []Func
	for _, d := range f.Decls {
		fd, ok := d.(*ast.FuncDecl)
		if !ok || fd.Body == nil {
			continue // an external (assembly-backed) declaration has nothing to measure
		}
		out = append(out, Func{
			Path:      path,
			Name:      fd.Name.Name,
			Recv:      receiverType(fd),
			StartLine: fset.Position(fd.Pos()).Line,
			EndLine:   fset.Position(fd.End()).Line,
			BodyStart: fset.Position(fd.Body.Lbrace).Line,
			BodyEnd:   fset.Position(fd.Body.Rbrace).Line,
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].StartLine < out[j].StartLine })
	return out
}

// IsGenerated reports the //go:generate convention: a "Code generated ... DO
// NOT EDIT." line in a comment before the package clause.
func IsGenerated(f *ast.File) bool {
	for _, cg := range f.Comments {
		if f.Package != token.NoPos && cg.Pos() > f.Package {
			break
		}
		for _, c := range cg.List {
			if strings.Contains(c.Text, generatedMarker) && strings.Contains(c.Text, "DO NOT EDIT") {
				return true
			}
		}
	}
	return false
}

// IsTestFile reports whether a path is Go test source.
func IsTestFile(path string) bool { return strings.HasSuffix(path, "_test.go") }

// receiverType renders a method receiver as a bare type name: "*Server" and
// "Server" both come back as "Server", and generic receivers drop their type
// parameters. Callers use this for identity, not for rendering Go source.
func receiverType(fd *ast.FuncDecl) string {
	if fd.Recv == nil || len(fd.Recv.List) == 0 {
		return ""
	}
	return exprName(fd.Recv.List[0].Type)
}

func exprName(e ast.Expr) string {
	switch t := e.(type) {
	case *ast.Ident:
		return t.Name
	case *ast.StarExpr:
		return exprName(t.X)
	case *ast.IndexExpr: // Server[T]
		return exprName(t.X)
	case *ast.IndexListExpr: // Server[K, V]
		return exprName(t.X)
	case *ast.SelectorExpr:
		return t.Sel.Name
	}
	return ""
}
