package db

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/printer"
	"go/token"
	"strconv"
	"strings"

	"github.com/sandboxws/gluon/internal/cmdspec"
	"github.com/sandboxws/gluon/internal/plugin"
	"github.com/sandboxws/gluon/internal/syntax"
)

// Sqlx is the plugin for github.com/jmoiron/sqlx.
//
// It does not get :sql, and that is the point. sqlx is a scanning layer over
// database/sql with no query builder in it, so a command named for gorm's
// capability would be named for one sqlx does not have.
//
// What sqlx does do is rewrite the query before it runs, and that is where its
// users are actually surprised. sqlx.Named turns :name into a bindvar and
// produces the argument list in the order the rewritten query now needs;
// sqlx.In flattens a slice into as many placeholders as it has elements and
// reorders everything around it. Get the two out of step and the failure is a
// driver error about the number of arguments, which names neither the query
// that was built nor the list that was built with it. :expand shows both halves.
type Sqlx struct{}

func (Sqlx) Meta() plugin.Meta {
	return plugin.Meta{
		Name:    "sqlx",
		Module:  "github.com/jmoiron/sqlx",
		Summary: ":expand shows what named parameters and IN clauses rewrote to",
	}
}

func (Sqlx) Imports() []plugin.Import {
	return []plugin.Import{{Name: "sqlx", Path: "github.com/jmoiron/sqlx"}}
}

func (Sqlx) Aliases() []plugin.Alias {
	return []plugin.Alias{{Name: "sqlx", Module: "github.com/jmoiron/sqlx"}}
}

func (Sqlx) Commands() []plugin.Command {
	return []plugin.Command{{
		Name: ":expand",
		Arg:  "<query>",
		Usage: cmdspec.Spec{
			Kind: cmdspec.GoExpr,
			Params: []cmdspec.Param{
				{Name: "query", Help: "a query with named parameters or an IN (?)"},
				{Name: "args", Optional: true, Repeat: true, Sep: ","},
			},
			Examples: []cmdspec.Example{
				{Line: `:expand "SELECT * FROM users WHERE id IN (?)", []int{1, 2, 3}`, Says: "the query after sqlx rewrote it, and the arguments in their new order"},
			},
			See: []string{":sql", ":query"},
		},
		Text:    true,
		Lang:    syntax.SQL,
		Summary: "the query after sqlx rewrites it, and the arguments in their new order",
		Detail: "Takes the query and the arguments you would have passed:\n\n" +
			"    :expand \"SELECT * FROM users WHERE id IN (?)\", []int{1, 2, 3}\n" +
			"    :expand \"SELECT * FROM users WHERE name = :name\", map[string]any{\"name\": \"Ada\"}\n\n" +
			"Nothing is sent to the database — sqlx.Named and sqlx.In are pure functions\n" +
			"over the text, so this opens no connection and needs no driver.\n\n" +
			"Named runs first, and only when the query really carries a named parameter:\n" +
			"on a positional query it returns an empty argument list without an error,\n" +
			"which would silently drop what you passed. A doubled colon is a Postgres\n" +
			"cast rather than a parameter, and is skipped the way sqlx skips it.\n\n" +
			"The placeholders come back as ?, which is what Named produces before\n" +
			"db.Rebind translates them for the driver you are on.\n\n" +
			"The arguments are printed separately rather than interpolated, because a\n" +
			"query with its parameters already substituted is not the query that runs.",
		Rewrite: expandRewrite,
	}}
}

// argsPrefix separates the two halves of the answer. It is the same prefix
// :sql uses, so a query and its arguments read the same whichever library
// produced them.
const argsPrefix = "\nargs  "

// expandRewrite turns `<query>, <args>...` into the program that runs sqlx's
// own rewrites over it and reports each half.
//
// The rewrites run in sqlx's documented order — Named, then In — because that
// is the order they compose in: Named is what produces the flat, ordered
// argument list that In then expands, and running In first would expand
// against placeholders Named has not written yet.
func expandRewrite(arg string) (string, error) {
	arg = strings.TrimSpace(arg)
	if arg == "" {
		return "", fmt.Errorf("usage: :expand <query>[, args...]   " +
			"e.g. :expand \"SELECT * FROM users WHERE id IN (?)\", []int{1, 2, 3}")
	}
	query, args, err := splitExpandArgs(arg)
	if err != nil {
		return "", err
	}

	return "func() string { " +
		"__q, __a := " + query + ", []any{" + strings.Join(args, ", ") + "}; " +
		// sqlx's own rule for what counts as a named parameter, and the reason
		// the whole scan is here rather than a strings.Contains: "::" is a
		// Postgres cast, and a query full of casts must not be handed to
		// Named, which would return no arguments at all.
		"__named := false; " +
		"for __i := 0; __i+1 < len(__q); __i++ { " +
		"if __q[__i] != ':' { continue }; " +
		"if __q[__i+1] == ':' { __i++; continue }; " +
		"if __c := __q[__i+1] | 0x20; __c >= 'a' && __c <= 'z' || __q[__i+1] == '_' { __named = true; break } }; " +
		"if __named && len(__a) == 1 { " +
		"__nq, __na, __err := sqlx.Named(__q, __a[0]); " +
		"if __err != nil { return \"sqlx.Named: \" + __err.Error() }; " +
		"__q, __a = __nq, __na }; " +
		"if len(__a) > 0 { " +
		"__iq, __ia, __err := sqlx.In(__q, __a...); " +
		// In's own error is the answer when it fires: it is the mismatch
		// between placeholders and arguments that this command exists to
		// find. The query as it stood goes with it, since the count is
		// meaningless without the text it was counted in.
		"if __err != nil { return \"sqlx.In: \" + __err.Error() + \"\\n\" + __q }; " +
		"__q, __a = __iq, __ia }; " +
		"if len(__a) == 0 { return __q }; " +
		"return __q + " + strconv.Quote(argsPrefix) + " + fmt.Sprint(__a) }()", nil
}

// splitExpandArgs separates the query from the arguments.
//
// It parses rather than splits on commas: a query is a Go expression and may
// carry a comma inside a string literal, a composite literal or a call, and
// `strings.Split(arg, ",")` would cut `[]int{1, 2}` in half.
func splitExpandArgs(arg string) (query string, args []string, err error) {
	expr, perr := parser.ParseExpr("__f(" + arg + ")")
	if perr != nil {
		return "", nil, fmt.Errorf(":expand needs Go expressions: %w", perr)
	}
	call, ok := expr.(*ast.CallExpr)
	if !ok || len(call.Args) == 0 {
		return "", nil, fmt.Errorf("usage: :expand <query>[, args...]")
	}
	// `xs...` is refused rather than guessed at. Spread means the slice is the
	// whole argument list, but a slice passed to sqlx.In means one argument to
	// expand into several placeholders, and the two produce different queries.
	if call.Ellipsis.IsValid() {
		return "", nil, fmt.Errorf(":expand does not take a spread argument: " +
			"xs... means one thing to sqlx.In and another to a call, so name the arguments")
	}
	fset := token.NewFileSet()
	out := make([]string, 0, len(call.Args))
	for _, a := range call.Args {
		var b strings.Builder
		if err := printer.Fprint(&b, fset, a); err != nil {
			return "", nil, err
		}
		out = append(out, b.String())
	}
	return out[0], out[1:], nil
}
