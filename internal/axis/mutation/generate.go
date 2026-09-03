package mutation

import (
	"fmt"
	"go/ast"
	"go/constant"
	"go/token"
	"go/types"
	"sort"
	"strings"
)

// Operator names. The vocabulary follows PIT and gremlins so the numbers mean
// the same thing they do elsewhere, plus one Go-specific operator.
const (
	OpConditionalsBoundary = "CONDITIONALS_BOUNDARY"
	OpConditionalsNegation = "CONDITIONALS_NEGATION"
	OpArithmeticBase       = "ARITHMETIC_BASE"
	OpInvertLogical        = "INVERT_LOGICAL"
	OpIncrementDecrement   = "INCREMENT_DECREMENT"
	OpInvertAssignments    = "INVERT_ASSIGNMENTS"
	OpInvertLoopCtrl       = "INVERT_LOOP_CTRL"
	OpNilErrorReturn       = "NIL_ERROR_RETURN"
	OpRemoveStatement      = "REMOVE_STATEMENT"
	OpConditionForce       = "CONDITION_FORCE"
)

// DefaultOperators is the v1 set.
func DefaultOperators() []string {
	return []string{
		OpConditionalsBoundary, OpConditionalsNegation, OpArithmeticBase,
		OpInvertLogical, OpIncrementDecrement, OpInvertAssignments,
		OpInvertLoopCtrl, OpNilErrorReturn, OpRemoveStatement, OpConditionForce,
	}
}

// FuncSpan is one function the change touched.
type FuncSpan struct {
	Label     string
	Decl      *ast.FuncDecl
	StartLine int
	EndLine   int
}

// Generator turns changed functions into mutants.
//
// It is type-aware, and that is not a refinement. Go overloads `+` for string
// concatenation and does not overload `-`, so every arithmetic mutant landing
// on a string concat is guaranteed not to compile — and Go code is full of
// string concatenation. Measured on one real file, a purely syntactic
// generator produced 274 mutants of which 26 could not compile, and 87% of the
// arithmetic operator's output was garbage. The type gate removes all of it
// for free, because the package is already loaded.
type Generator struct {
	Fset      *token.FileSet
	Info      *types.Info
	Operators map[string]bool

	src        []byte
	file       string
	pkg        string
	fn         string
	out        []Mutant
	Suppressed map[string]int
}

func NewGenerator(fset *token.FileSet, info *types.Info, ops []string) *Generator {
	set := map[string]bool{}
	for _, o := range ops {
		set[o] = true
	}
	return &Generator{Fset: fset, Info: info, Operators: set, Suppressed: map[string]int{}}
}

// Generate emits mutants for the given functions in one file.
func (g *Generator) Generate(file, pkg string, src []byte, fns []FuncSpan) []Mutant {
	g.src, g.file, g.pkg = src, file, pkg
	g.out = nil

	for _, fn := range fns {
		g.fn = fn.Label
		if fn.Decl == nil || fn.Decl.Body == nil {
			continue
		}
		returnsErr := funcReturnsError(fn.Decl, g.Info)
		ast.Inspect(fn.Decl.Body, func(n ast.Node) bool {
			g.visit(n, fn.Decl, returnsErr)
			return true
		})
	}

	sort.Slice(g.out, func(i, j int) bool {
		a, b := g.out[i], g.out[j]
		if a.Line != b.Line {
			return a.Line < b.Line
		}
		if a.Col != b.Col {
			return a.Col < b.Col
		}
		return a.Operator < b.Operator
	})
	return g.out
}

func (g *Generator) visit(n ast.Node, fn *ast.FuncDecl, returnsErr bool) {
	switch x := n.(type) {
	case *ast.BinaryExpr:
		g.binary(x)
	case *ast.IncDecStmt:
		g.incDec(x)
	case *ast.AssignStmt:
		g.assign(x)
	case *ast.BranchStmt:
		g.branch(x)
	case *ast.ReturnStmt:
		if returnsErr {
			g.nilErrorReturn(x)
		}
	case *ast.ExprStmt:
		g.removeStatement(x)
	case *ast.IfStmt:
		g.forceCondition(x)
	}
}

func (g *Generator) binary(x *ast.BinaryExpr) {
	switch x.Op {
	case token.GTR, token.LSS, token.GEQ, token.LEQ:
		if g.on(OpConditionalsBoundary) {
			if to, ok := boundarySwap[x.Op]; ok {
				g.emitToken(OpConditionalsBoundary, x.OpPos, x.Op, to, x)
			}
		}
		if g.on(OpConditionalsNegation) {
			g.emitToken(OpConditionalsNegation, x.OpPos, x.Op, negationSwap[x.Op], x)
		}

	case token.EQL, token.NEQ:
		if g.on(OpConditionalsNegation) {
			g.emitToken(OpConditionalsNegation, x.OpPos, x.Op, negationSwap[x.Op], x)
		}

	case token.LAND, token.LOR:
		if g.on(OpInvertLogical) {
			g.emitToken(OpInvertLogical, x.OpPos, x.Op, logicalSwap[x.Op], x)
		}

	case token.ADD, token.SUB, token.MUL, token.QUO, token.REM:
		if !g.on(OpArithmeticBase) {
			return
		}
		if !g.numeric(x.X) {
			// The string-concatenation trap: `-` is not defined on strings.
			g.suppress("non-viable: operator not defined on non-numeric operand")
			return
		}
		if to, ok := arithSwap[x.Op]; ok {
			if g.degenerateArith(x, to) {
				g.suppress("equivalent: identity or degenerate arithmetic")
				return
			}
			g.emitToken(OpArithmeticBase, x.OpPos, x.Op, to, x)
		}
	}
}

func (g *Generator) incDec(x *ast.IncDecStmt) {
	if !g.on(OpIncrementDecrement) {
		return
	}
	to := token.DEC
	if x.Tok == token.DEC {
		to = token.INC
	}
	g.emitToken(OpIncrementDecrement, x.TokPos, x.Tok, to, x.X)
}

func (g *Generator) assign(x *ast.AssignStmt) {
	if !g.on(OpInvertAssignments) {
		return
	}
	to, ok := assignSwap[x.Tok]
	if !ok {
		return
	}
	if len(x.Lhs) != 1 || !g.numeric(x.Lhs[0]) {
		g.suppress("non-viable: compound assignment on non-numeric operand")
		return
	}
	g.emitToken(OpInvertAssignments, x.TokPos, x.Tok, to, x.Lhs[0])
}

// branch swaps break and continue. `break L` only converts when L labels a
// loop; labelling a switch or select makes `continue L` a compile error.
func (g *Generator) branch(x *ast.BranchStmt) {
	if !g.on(OpInvertLoopCtrl) {
		return
	}
	var to token.Token
	switch x.Tok {
	case token.BREAK:
		to = token.CONTINUE
	case token.CONTINUE:
		to = token.BREAK
	default:
		return
	}
	if x.Label != nil {
		if !g.labelsLoop(x.Label) {
			g.suppress("non-viable: label does not name a loop")
			return
		}
	}
	g.emitToken(OpInvertLoopCtrl, x.TokPos, x.Tok, to, x)
}

// nilErrorReturn drops a returned error.
//
// This is the Go-specific operator and the highest-value one for
// agent-written code: agents generate error plumbing constantly and test it
// almost never. Nothing in gremlins or go-mutesting covers it, and a survivor
// here is nearly always a real gap rather than an equivalent mutant.
func (g *Generator) nilErrorReturn(x *ast.ReturnStmt) {
	if !g.on(OpNilErrorReturn) {
		return
	}
	for _, r := range x.Results {
		if !g.isErrorValued(r) {
			continue
		}
		if id, ok := r.(*ast.Ident); ok && id.Name == "nil" {
			continue // already nil
		}
		g.emitRange(OpNilErrorReturn, r.Pos(), r.End(), "nil", r)
	}
}

// removeStatement deletes a call whose result nobody uses.
//
// Observability calls are excluded: removing a log or a metric is invisible to
// any reasonable test, so those mutants are unkillable by construction and
// only serve to depress the score.
func (g *Generator) removeStatement(x *ast.ExprStmt) {
	if !g.on(OpRemoveStatement) {
		return
	}
	call, ok := x.X.(*ast.CallExpr)
	if !ok {
		return
	}
	if isObservability(call) {
		g.suppress("equivalent: observability call")
		return
	}
	if isPanicOrFatal(call) {
		g.suppress("equivalent: panic/fatal argument")
		return
	}
	start, end := g.offset(x.Pos()), g.offset(x.End())
	g.emitRange(OpRemoveStatement, x.Pos(), x.End(), blank(string(g.src[start:end])), call)
}

// forceCondition drives a branch one way, catching code no test ever reaches.
func (g *Generator) forceCondition(x *ast.IfStmt) {
	if !g.on(OpConditionForce) || x.Cond == nil {
		return
	}
	if lit, ok := x.Cond.(*ast.Ident); ok && (lit.Name == "true" || lit.Name == "false") {
		return
	}
	g.emitRange(OpConditionForce, x.Cond.Pos(), x.Cond.End(), "true", x.Cond)
	g.emitRange(OpConditionForce, x.Cond.Pos(), x.Cond.End(), "false", x.Cond)
}

// --- gates -------------------------------------------------------------

func (g *Generator) on(op string) bool { return g.Operators[op] }

func (g *Generator) suppress(reason string) { g.Suppressed[reason]++ }

// numeric reports whether an expression's type supports arithmetic. Without
// type information the answer is no: guessing here is what produces mutants
// that cannot compile.
func (g *Generator) numeric(e ast.Expr) bool {
	if g.Info == nil {
		return false
	}
	t := g.Info.TypeOf(e)
	if t == nil {
		return false
	}
	b, ok := t.Underlying().(*types.Basic)
	if !ok {
		return false
	}
	return b.Info()&(types.IsInteger|types.IsFloat|types.IsComplex) != 0
}

// Note on a gate that is deliberately absent: comparing an unsigned value
// against zero is often cited as producing equivalent boundary mutants,
// because `u >= 0` is a tautology. It does not. Swapping it to `u > 0`
// changes behaviour at zero, and swapping `u < 0` to `u <= 0` does too — both
// are killable by a test that passes zero. Suppressing them would remove real
// mutants from the denominator and hide the gap instead of reporting it.

// degenerateArith rejects rewrites that cannot change behaviour: multiplying
// or dividing by one, adding or subtracting zero, dividing by zero.
func (g *Generator) degenerateArith(x *ast.BinaryExpr, to token.Token) bool {
	v := g.constVal(x.Y)
	if v == nil {
		return false
	}
	n, ok := constant.Int64Val(v)
	if !ok {
		return false
	}
	switch {
	case n == 0 && (to == token.QUO || to == token.REM):
		return true // a guaranteed panic, not a behavioural difference worth testing
	case n == 0 && (x.Op == token.ADD || x.Op == token.SUB):
		return true
	case n == 1 && (x.Op == token.MUL || x.Op == token.QUO):
		return true
	}
	return false
}

func (g *Generator) constVal(e ast.Expr) constant.Value {
	if g.Info == nil {
		return nil
	}
	tv, ok := g.Info.Types[e]
	if !ok {
		return nil
	}
	return tv.Value
}

func (g *Generator) isErrorValued(e ast.Expr) bool {
	if g.Info == nil {
		return false
	}
	t := g.Info.TypeOf(e)
	if t == nil {
		return false
	}
	return types.Implements(t, errorInterface) ||
		(t.String() == "error") ||
		types.Implements(types.NewPointer(t), errorInterface)
}

// labelsLoop resolves a label to the statement it names.
func (g *Generator) labelsLoop(label *ast.Ident) bool {
	if label.Obj == nil || label.Obj.Decl == nil {
		return false // unresolved: assume unsafe
	}
	ls, ok := label.Obj.Decl.(*ast.LabeledStmt)
	if !ok {
		return false
	}
	switch ls.Stmt.(type) {
	case *ast.ForStmt, *ast.RangeStmt:
		return true
	}
	return false
}

func (g *Generator) offset(p token.Pos) int { return g.Fset.Position(p).Offset }

func (g *Generator) emitToken(op string, pos token.Pos, from, to token.Token, n ast.Node) {
	if to == token.ILLEGAL {
		return
	}
	start := g.offset(pos)
	g.emit(op, start, start+len(from.String()), to.String(), n)
}

func (g *Generator) emitRange(op string, from, to token.Pos, replacement string, n ast.Node) {
	g.emit(op, g.offset(from), g.offset(to), replacement, n)
}

// text returns the source of an AST node.
func (g *Generator) text(n ast.Node) string {
	s, e := g.offset(n.Pos()), g.offset(n.End())
	if s < 0 || e > len(g.src) || s >= e {
		return ""
	}
	return strings.TrimSpace(string(g.src[s:e]))
}

// guide states the assertion a surviving mutant proves is missing.
//
// This is the difference between a tool that grades code and one that
// instructs: the operator and its operands say exactly what is unchecked, and
// that is derivable with no judgement involved.
//
// Every line is phrased as an assertion to add, never as a claim about what the
// tests currently do. A survivor does not prove the case is unreached — it
// proves nothing *asserts* the difference. The two look identical from here,
// and a test may well drive the exact input already and simply not check the
// result. Saying "no test reaches this" in that situation sends whoever reads
// it to write a test that already exists.
func (g *Generator) guide(op string, n ast.Node, replacement string) string {
	switch op {
	case OpConditionalsBoundary:
		if b, ok := n.(*ast.BinaryExpr); ok {
			return fmt.Sprintf("assert the behaviour at the boundary %s == %s", g.text(b.X), g.text(b.Y))
		}
	case OpConditionalsNegation:
		if b, ok := n.(*ast.BinaryExpr); ok {
			return fmt.Sprintf("assert behaviour that changes when %s is negated", g.text(b))
		}
	case OpInvertLogical:
		if b, ok := n.(*ast.BinaryExpr); ok {
			return fmt.Sprintf("assert a case where exactly one side of %s holds", g.text(b))
		}
	case OpArithmeticBase, OpInvertAssignments:
		return fmt.Sprintf("assert the value %s computes", g.text(n))
	case OpIncrementDecrement:
		return fmt.Sprintf("assert the value of %s after this runs", g.text(n))
	case OpNilErrorReturn:
		return "assert that this path returns a non-nil error"
	case OpRemoveStatement:
		return fmt.Sprintf("assert the effect of %s", g.text(n))
	case OpInvertLoopCtrl:
		return "assert behaviour with further iterations after this point"
	case OpConditionForce:
		// Forcing the condition one way only changes behaviour for inputs where
		// it went the other. The survivor names which side goes unchecked.
		side := "true"
		if replacement == "true" {
			side = "false"
		}
		return fmt.Sprintf("assert the behaviour that depends on %s being %s", g.text(n), side)
	}
	return ""
}

func (g *Generator) emit(op string, start, end int, replacement string, node ast.Node) {
	if start < 0 || end > len(g.src) || start >= end {
		return
	}
	old := string(g.src[start:end])
	line, col := lineCol(g.src, start)
	m := Mutant{
		Operator: op, File: g.file, Package: g.pkg, Function: g.fn,
		Line: line, Col: col, Start: start, End: end, Replacement: replacement,
		Before: lineAt(g.src, start),
	}
	m.After = lineAt(m.Apply(g.src), start)
	m.Guidance = g.guide(op, node, replacement)
	m.ID = computeID(g.file, line, col, op, old, replacement)
	g.out = append(g.out, m)
}

func lineCol(src []byte, off int) (int, int) {
	line, last := 1, -1
	for i := 0; i < off && i < len(src); i++ {
		if src[i] == '\n' {
			line++
			last = i
		}
	}
	return line, off - last
}

// --- tables ------------------------------------------------------------

var (
	boundarySwap = map[token.Token]token.Token{
		token.GTR: token.GEQ, token.GEQ: token.GTR,
		token.LSS: token.LEQ, token.LEQ: token.LSS,
	}
	negationSwap = map[token.Token]token.Token{
		token.EQL: token.NEQ, token.NEQ: token.EQL,
		token.GTR: token.LEQ, token.LEQ: token.GTR,
		token.LSS: token.GEQ, token.GEQ: token.LSS,
	}
	logicalSwap = map[token.Token]token.Token{
		token.LAND: token.LOR, token.LOR: token.LAND,
	}
	arithSwap = map[token.Token]token.Token{
		token.ADD: token.SUB, token.SUB: token.ADD,
		token.MUL: token.QUO, token.QUO: token.MUL,
		token.REM: token.MUL,
	}
	assignSwap = map[token.Token]token.Token{
		token.ADD_ASSIGN: token.SUB_ASSIGN, token.SUB_ASSIGN: token.ADD_ASSIGN,
		token.MUL_ASSIGN: token.QUO_ASSIGN, token.QUO_ASSIGN: token.MUL_ASSIGN,
	}
	errorInterface = types.Universe.Lookup("error").Type().Underlying().(*types.Interface)
)

var observabilityPkgs = []string{"log", "slog", "fmt", "otel", "trace", "metrics", "prometheus", "zap", "logrus"}
var observabilityFuncs = []string{"Print", "Printf", "Println", "Debug", "Info", "Warn", "Error", "Trace", "Log"}

func isObservability(call *ast.CallExpr) bool {
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok {
		return false
	}
	if id, ok := sel.X.(*ast.Ident); ok {
		for _, p := range observabilityPkgs {
			if id.Name == p {
				return true
			}
		}
	}
	for _, f := range observabilityFuncs {
		if strings.HasPrefix(sel.Sel.Name, f) {
			return true
		}
	}
	return false
}

func isPanicOrFatal(call *ast.CallExpr) bool {
	switch f := call.Fun.(type) {
	case *ast.Ident:
		return f.Name == "panic"
	case *ast.SelectorExpr:
		return strings.HasPrefix(f.Sel.Name, "Fatal")
	}
	return false
}

func funcReturnsError(fd *ast.FuncDecl, info *types.Info) bool {
	if fd.Type == nil || fd.Type.Results == nil {
		return false
	}
	for _, r := range fd.Type.Results.List {
		if id, ok := r.Type.(*ast.Ident); ok && id.Name == "error" {
			return true
		}
	}
	return false
}
