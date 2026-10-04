package db

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/sandboxws/gluon/internal/dsn"
	"github.com/sandboxws/gluon/internal/find"
)

// maxSourceFiles bounds the Go scan. Parsing a large module to answer one
// question is not worth an unbounded walk.
const maxSourceFiles = 400

// openFuncs are the calls that name a driver in their first argument.
var openFuncs = map[string]bool{
	"sql.Open": true, "sql.OpenDB": true, "sqlx.Connect": true,
	"sqlx.Open": true, "sqlx.MustConnect": true, "goose.SetDialect": true,
}

// scanGoSource reads driver names and connection-string literals out of the
// project's own code.
//
// It is syntactic — nothing is type-checked — so a local variable named sql
// would fool it. That is an acceptable failure mode precisely because a match
// here only *suggests*: a literal still has to parse as a connection string by
// shape before it becomes a candidate, and the shape rule is what backstops the
// guess.
func scanGoSource(root string) []Candidate {
	var out []Candidate
	n := 0
	fset := token.NewFileSet()

	_ = find.Walk(root, 3, func(p string, d fs.DirEntry) error {
		if n >= maxSourceFiles {
			return fs.SkipAll
		}
		if filepath.Ext(p) != ".go" || strings.HasSuffix(p, "_test.go") {
			return nil
		}
		n++
		f, err := parser.ParseFile(fset, p, nil, 0)
		if err != nil {
			return nil
		}
		ast.Inspect(f, func(node ast.Node) bool {
			call, ok := node.(*ast.CallExpr)
			if !ok || len(call.Args) == 0 {
				return true
			}
			sel, ok := call.Fun.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			pkg, ok := sel.X.(*ast.Ident)
			if !ok || !openFuncs[pkg.Name+"."+sel.Sel.Name] {
				return true
			}
			// The second argument, when it is a literal, is a connection
			// string somebody committed. It still has to parse by shape.
			if len(call.Args) < 2 {
				return true
			}
			lit, ok := call.Args[1].(*ast.BasicLit)
			if !ok || lit.Kind != token.STRING {
				return true
			}
			value, err := strconv.Unquote(lit.Value)
			if err != nil {
				return true
			}
			parsed, perr := dsn.Parse(value, filepath.Dir(p))
			if perr != nil {
				return true
			}
			out = append(out, Candidate{
				DSN:   parsed,
				Rank:  RankApp,
				Depth: 0,
				From: []Provenance{{
					File: p,
					Line: fset.Position(lit.Pos()).Line,
					Key:  pkg.Name + "." + sel.Sel.Name,
					Kind: KindGoSource,
				}},
			})
			return true
		})
		return nil
	})
	return out
}
