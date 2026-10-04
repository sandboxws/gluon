package check

import (
	"go/ast"
	"go/types"
)

// Tail describes the expression the generated program is about to print — the
// argument to the injected printer in the last statement of main.
//
// It is the whole reason the checker is wired into evaluation. Knowing the
// trailing expression's type decides three things a build would otherwise have
// to discover the slow way: whether the call has no result and so cannot be
// printed at all, whether it is a compile-time constant that needs no program
// run, and what to say when the user asks with :t.
type Tail struct {
	Expr ast.Expr
	TV   types.TypeAndValue
	// Void is a call with no results. It cannot be an argument to anything,
	// so it has to be re-rendered as a bare statement.
	Void bool
	// Const is a compile-time constant: the answer is already known and the
	// program never has to run.
	Const bool
	// Values is how many results the expression yields: 0 for a void call, 1
	// for an ordinary value, n for a tuple. Only a single value can be bound
	// to a name, so this is what decides whether _N can carry it forward.
	Values int
}

// Printed returns the trailing printer call's argument. It walks main's body
// rather than the whole file so a printer call inside a user-declared function
// is never mistaken for the session's trailing expression.
func (r *Result) Printed(printFunc string) (Tail, bool) {
	body := mainBody(r.File)
	if body == nil {
		return Tail{}, false
	}
	for i := len(body.List) - 1; i >= 0; i-- {
		es, ok := body.List[i].(*ast.ExprStmt)
		if !ok {
			continue
		}
		call, ok := es.X.(*ast.CallExpr)
		if !ok {
			continue
		}
		id, ok := call.Fun.(*ast.Ident)
		if !ok || id.Name != printFunc || len(call.Args) != 1 {
			continue
		}
		arg := call.Args[0]
		tv, ok := r.Info.Types[arg]
		if !ok {
			return Tail{Expr: arg}, true
		}
		return Tail{
			Expr:   arg,
			TV:     tv,
			Void:   tv.IsVoid(),
			Const:  tv.Value != nil,
			Values: arity(tv),
		}, true
	}
	return Tail{}, false
}

// VoidCalls returns every printer argument the checker found to be a call with
// no results, whatever its position in the session.
//
// It must look past the trailing entry: `slices.Sort(x)` typed mid-session is
// replayed on every later line, so the entry that has to be re-rendered is
// often not the one just submitted.
func (r *Result) VoidCalls(printFunc string) []ast.Expr {
	body := mainBody(r.File)
	if body == nil {
		return nil
	}
	var out []ast.Expr
	for _, st := range body.List {
		es, ok := st.(*ast.ExprStmt)
		if !ok {
			continue
		}
		call, ok := es.X.(*ast.CallExpr)
		if !ok {
			continue
		}
		id, ok := call.Fun.(*ast.Ident)
		if !ok || id.Name != printFunc || len(call.Args) != 1 {
			continue
		}
		if tv, ok := r.Info.Types[call.Args[0]]; ok && tv.IsVoid() {
			out = append(out, call.Args[0])
		}
	}
	return out
}

func mainBody(f *ast.File) *ast.BlockStmt {
	for _, d := range f.Decls {
		if fd, ok := d.(*ast.FuncDecl); ok && fd.Name.Name == "main" && fd.Recv == nil {
			return fd.Body
		}
	}
	return nil
}

// arity counts the results an expression yields.
func arity(tv types.TypeAndValue) int {
	if tv.IsVoid() {
		return 0
	}
	if tup, ok := tv.Type.(*types.Tuple); ok {
		return tup.Len()
	}
	return 1
}

// MainScope is the scope holding everything the session bound with := or var.
//
// Those live in main's body, not in the package scope, because only func, type
// and const entries are hoisted above main. Info.Scopes is populated on every
// check and read by nothing else; this is what makes a typed :ls free.
func (r *Result) MainScope() *types.Scope {
	for _, d := range r.File.Decls {
		fd, ok := d.(*ast.FuncDecl)
		if !ok || fd.Name.Name != "main" || fd.Recv != nil {
			continue
		}
		// go/types gives a function one scope covering parameters and body,
		// keyed by the FuncType. The body's BlockStmt is not in Scopes.
		return r.Info.Scopes[fd.Type]
	}
	return nil
}

// DeclaredHere reports whether an object comes from the session's own file
// rather than from the injected runtime, which is a sibling in the same
// package and therefore shares its scope.
func (r *Result) DeclaredHere(o types.Object) bool {
	// PositionFor with adjusted=false ignores the /*line*/ directives, which
	// rewrite main.go's positions to the synthetic per-entry files. The
	// unadjusted filename is the one that says which real file this came from.
	return r.Fset.PositionFor(o.Pos(), false).Filename == Filename
}
