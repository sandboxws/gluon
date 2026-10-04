package db

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/printer"
	"go/token"
	"strings"

	"github.com/sandboxws/gluon/internal/cmdspec"
	"github.com/sandboxws/gluon/internal/plugin"
	"github.com/sandboxws/gluon/internal/syntax"
)

// Gorm is the plugin for gorm.io/gorm.
//
// The question people actually have about a query builder is "what SQL does
// this produce", and gorm answers it only in DryRun mode — which has to be set
// on the session at the *start* of the chain, not the end. So :sql parses the
// argument and injects it at the base, which is the one thing a text template
// cannot do and the reason plugin commands are Go rather than TOML alone.
type Gorm struct{}

func (Gorm) Meta() plugin.Meta {
	return plugin.Meta{
		Name:    "gorm",
		Module:  "gorm.io/gorm",
		Summary: ":sql shows the SQL a builder chain produces, without running it",
	}
}

func (Gorm) Imports() []plugin.Import {
	return []plugin.Import{{Name: "gorm", Path: "gorm.io/gorm"}}
}

func (Gorm) Aliases() []plugin.Alias {
	return []plugin.Alias{{Name: "gorm", Module: "gorm.io/gorm"}}
}

func (Gorm) Commands() []plugin.Command {
	return []plugin.Command{{
		Name: ":sql",
		Lang: syntax.SQL,
		Arg:  "<chain>",
		Usage: cmdspec.Spec{
			Kind: cmdspec.GoExpr,
			Examples: []cmdspec.Example{
				{Line: `:sql db.Where("age > ?", 30).Find(&users)`, Says: "the SQL and its arguments; nothing reaches the database"},
			},
			See: []string{":query", ":expand"},
		},
		Text:    true,
		Summary: "the SQL a gorm chain produces, and its arguments",
		Detail: "Nothing is sent to the database. gorm builds the statement in DryRun mode,\n" +
			"which has to be set at the start of the chain — so :sql parses what you\n" +
			"typed and injects .Session(&gorm.Session{DryRun: true}) at its base.\n\n" +
			"The arguments are printed separately rather than interpolated, because a\n" +
			"query with its parameters already substituted is not the query that runs.",
		Rewrite: gormRewrite,
	}}
}

func gormRewrite(arg string) (string, error) {
	arg = strings.TrimSpace(arg)
	if arg == "" {
		return "", fmt.Errorf("usage: :sql <chain>   e.g. :sql db.Where(\"age > ?\", 30).Find(&users)")
	}
	dry, err := injectDryRun(arg)
	if err != nil {
		return "", err
	}
	// Statement.SQL is only populated once the chain has been built, which is
	// what DryRun makes happen without a connection.
	return "func() string { __t := " + dry + "; " +
		"__q := __t.Statement.SQL.String(); " +
		"if __q == \"\" { return \"no statement was built — is this a gorm chain?\" }; " +
		"if len(__t.Statement.Vars) == 0 { return __q }; " +
		"return __q + \"\\nargs  \" + fmt.Sprint(__t.Statement.Vars) }()", nil
}

// injectDryRun rewrites `db.Where(x).Find(&u)` into
// `db.Session(&gorm.Session{DryRun: true}).Where(x).Find(&u)`.
//
// It walks to the base of the method chain — the receiver every call hangs off
// — and wraps that, rather than appending, because DryRun is a property of the
// session the statement is built on and setting it at the end is too late.
func injectDryRun(src string) (string, error) {
	expr, err := parser.ParseExpr(src)
	if err != nil {
		return "", fmt.Errorf(":sql needs a Go expression: %w", err)
	}

	base, replace := chainBase(expr)
	if replace == nil {
		// The whole expression is the base: `:sql db` on its own.
		expr = sessionCall(expr)
	} else {
		replace(sessionCall(base))
	}

	var b strings.Builder
	if err := printer.Fprint(&b, token.NewFileSet(), expr); err != nil {
		return "", err
	}
	return b.String(), nil
}

// chainBase finds the receiver the method chain hangs off, and returns a setter
// that swaps it out. A nil setter means there was no chain — the argument is
// the receiver itself.
//
// The subtlety is that the base is the operand of the *innermost method call*,
// not the deepest identifier. In `app.DB.Model(&U{}).Count(&n)` the receiver is
// `app.DB`, and walking selectors all the way down would wrap `app` — which
// compiles, calls the wrong thing, and is exactly the kind of quiet wrongness
// this project exists to avoid.
func chainBase(e ast.Expr) (ast.Expr, func(ast.Expr)) {
	var (
		base    ast.Expr
		replace func(ast.Expr)
	)
	cur := e
	for {
		call, ok := cur.(*ast.CallExpr)
		if !ok {
			return base, replace
		}
		sel, ok := call.Fun.(*ast.SelectorExpr)
		if !ok {
			// A plain function call, so its result is the receiver.
			return base, replace
		}
		base, replace = sel.X, func(x ast.Expr) { sel.X = x }
		cur = sel.X
	}
}

// sessionCall is `<base>.Session(&gorm.Session{DryRun: true})`.
func sessionCall(base ast.Expr) ast.Expr {
	return &ast.CallExpr{
		Fun: &ast.SelectorExpr{X: base, Sel: ast.NewIdent("Session")},
		Args: []ast.Expr{&ast.UnaryExpr{
			Op: token.AND,
			X: &ast.CompositeLit{
				Type: &ast.SelectorExpr{X: ast.NewIdent("gorm"), Sel: ast.NewIdent("Session")},
				Elts: []ast.Expr{&ast.KeyValueExpr{
					Key:   ast.NewIdent("DryRun"),
					Value: ast.NewIdent("true"),
				}},
			},
		}},
	}
}
