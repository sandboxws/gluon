package db

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/sandboxws/gluon/internal/cmdspec"
	"github.com/sandboxws/gluon/internal/plugin"
)

// Ent is the plugin for entgo.io/ent.
//
// It offers no :sql, and that is a finding rather than an omission.
//
// What was looked for, in ent v0.14.6: an exported way to make a generated
// query builder produce its statement without running it. There is none. The
// builder's two SQL-producing methods are both unexported —
// `(*UserQuery).querySpec() *sqlgraph.QuerySpec` and
// `(*UserQuery).sqlQuery(context.Context) *sql.Selector`
// (entc/gen/template/dialect/sql/query.tmpl:320,392) — and the generated type
// carries no exported `ToSQL`, no `String`, and no `DryRun` option of gorm's
// kind. The one thing that looks like a preview is not one: `Client.Debug()`
// (entc/gen/template/client.tmpl:207) installs `dialect.Debug`, whose
// `DebugDriver.Query` logs the statement and then calls the underlying driver
// with it (dialect/dialect.go:106). It prints the query because it is running
// the query.
//
// So a :sql for ent could only be a command that executed. It would look
// exactly like gorm's, take the same shape of argument, and hit the database —
// which is the worst outcome on offer here, and worse than not shipping it.
// What ent can answer without executing anything is what its schema is, and
// that is what this plugin offers. Guide() says so at the prompt, because a
// user comparing this plugin against gorm's should learn something true about
// ent rather than assume gluon's support is half-finished.
type Ent struct{}

func (Ent) Meta() plugin.Meta {
	return plugin.Meta{
		Name:    "ent",
		Module:  "entgo.io/ent",
		Summary: ":schema — the tables, columns and edges the generated code describes",
	}
}

// Imports preloads the two packages a session needs to name ent's schema
// descriptors, alongside ent itself.
//
// Without them `schema.Table` at the prompt is "undefined: schema" —
// goimports does not find either package, so the types :schema reads cannot be
// named by the line that builds or holds them. The names are ordinary enough to
// belong to a project too, and that is safe: the host's own package index is
// consulted before the preload map, so a project with its own schema package
// keeps it.
func (Ent) Imports() []plugin.Import {
	return []plugin.Import{
		{Name: "ent", Path: "entgo.io/ent"},
		{Name: "schema", Path: "entgo.io/ent/dialect/sql/schema"},
		{Name: "field", Path: "entgo.io/ent/schema/field"},
	}
}

func (Ent) Aliases() []plugin.Alias {
	return []plugin.Alias{{Name: "ent", Module: "entgo.io/ent"}}
}

func (Ent) Commands() []plugin.Command {
	return []plugin.Command{{
		Name: ":schema",
		Arg:  "<tables>",
		Usage: cmdspec.Spec{
			Kind: cmdspec.GoExpr,
			Examples: []cmdspec.Example{
				{Line: ":schema migrate.Tables", Says: "tables, columns and edges, with no connection"},
			},
			See: []string{":query", ":db"},
		},
		Text:    true,
		Summary: "the tables, columns and edges ent generated, without a connection",
		Detail: "Takes the generated table set, which ent writes as migrate.Tables:\n\n" +
			"    :schema migrate.Tables\n\n" +
			"Everything shown is a runtime descriptor the generated package already\n" +
			"holds — nothing is opened, and no migration is planned or applied.\n\n" +
			"Edges appear as the foreign keys they became, because that is what the\n" +
			"generated schema records: an edge to another type is a column and a\n" +
			"reference, and a many-to-many edge is a join table of its own.\n\n" +
			"There is deliberately no :sql for ent. Its query builders expose no\n" +
			"supported way to produce a statement without running it — :guide ent has\n" +
			"the detail, and the alternative would be a preview that queried.",
		Rewrite: entRewrite,
	}}
}

// entColumnFormat is the one column line: name, type, attributes.
//
// Fixed widths rather than measured, for the reason :routes has them — the
// lines are built in the child, one at a time, by source that never holds the
// whole listing. Measuring would need a second pass and a second definition of
// the format.
const entColumnFormat = "  %-24s %-12s %s"

// entColumn is gluon's copy of what the generated source does, so the shape is
// testable without ent and a toolchain, and the two cannot drift on a width.
func entColumn(name, typ, attrs string) string {
	return fmt.Sprintf(entColumnFormat, name, typ, attrs)
}

// entRewrite turns a table set into the listing.
//
// Every field it reads is exported and has been since ent's schema package
// settled: Table.Name, .Columns, .PrimaryKey, .ForeignKeys, .View;
// Column.Name, .Type, .Unique, .Nullable, .Enums; ForeignKey.RefTable and
// .RefColumns. Column.Type is a field.Type, whose String is what names it —
// called as a method so the child needs no ent import of its own.
//
// That String is ent's naming rather than the schema's: field.TypeEnum reports
// "string", because a Go enum field is a string (schema/field/type.go,
// typeNames). The enum values are listed as an attribute for exactly that
// reason — the attribute is what tells an enum apart from a string, and
// inventing a better name for the type would be gluon disagreeing with ent
// about ent.
func entRewrite(arg string) (string, error) {
	arg = strings.TrimSpace(arg)
	if arg == "" {
		return "", fmt.Errorf("usage: :schema <tables>   e.g. :schema migrate.Tables")
	}
	return "func() string { " +
		"__ts := " + arg + "; " +
		"if len(__ts) == 0 { return \"no tables — :schema takes the generated migrate.Tables\" }; " +
		"var __out []string; " +
		"for _, __t := range __ts { " +
		"__head := __t.Name; " +
		"if __t.View { __head += \"  (view)\" }; " +
		"__out = append(__out, __head); " +
		// The primary key is a separate list on the table, so a column does
		// not know it is one. Marking it needs the set built first.
		"__pk := map[string]bool{}; " +
		"for _, __c := range __t.PrimaryKey { __pk[__c.Name] = true }; " +
		"for _, __c := range __t.Columns { " +
		"__a := []string{}; " +
		"if __pk[__c.Name] { __a = append(__a, \"pk\") }; " +
		"if __c.Unique { __a = append(__a, \"unique\") }; " +
		// Both spellings, always. A blank cell where "not null" belongs reads
		// as "this column has no nullability", which is never true.
		"if __c.Nullable { __a = append(__a, \"null\") } else { __a = append(__a, \"not null\") }; " +
		"if len(__c.Enums) > 0 { __a = append(__a, \"enum(\"+strings.Join(__c.Enums, \"|\")+\")\") }; " +
		"__out = append(__out, fmt.Sprintf(" + strconv.Quote(entColumnFormat) +
		", __c.Name, __c.Type.String(), strings.Join(__a, \"  \"))) }; " +
		"for _, __k := range __t.ForeignKeys { " +
		"__from := []string{}; " +
		"for _, __c := range __k.Columns { __from = append(__from, __c.Name) }; " +
		"__to := []string{}; " +
		"for _, __c := range __k.RefColumns { __to = append(__to, __c.Name) }; " +
		"__ref := \"\"; " +
		"if __k.RefTable != nil { __ref = __k.RefTable.Name }; " +
		"__out = append(__out, \"  → \"+strings.Join(__from, \",\")+\" references \"+" +
		"__ref+\"(\"+strings.Join(__to, \",\")+\")\") } }; " +
		"return strings.Join(__out, \"\\n\") }()", nil
}

// Guide is the first Guider among the built-in plugins, and the reason it is
// here is that the ent plugin has something to say that its command list
// cannot: the shape of a question it does not answer.
func (Ent) Guide() string {
	return `# ent

## What this plugin answers

    :schema migrate.Tables

The tables ent generated, their columns with types and constraints, and the
edges as the foreign keys they became. Nothing is opened and no migration is
planned — every descriptor printed is one the generated package is already
holding in memory.

## What it does not answer, and why

There is no ` + "`:sql`" + ` for ent.

gorm gets one because gorm has DryRun: a session flag that makes the builder
assemble the statement and stop. ent has no equivalent. In v0.14.6 the two
methods on a generated query builder that produce SQL are both unexported —
` + "`querySpec()`" + ` and ` + "`sqlQuery()`" + ` — and the builder carries no
` + "`ToSQL`" + `, no ` + "`String`" + `, and no dry-run option.

` + "`client.Debug()`" + ` is the near miss. It prints the statement, so it looks
like the answer, but ` + "`dialect.DebugDriver`" + ` logs the query on its way to
the driver and then runs it. It shows you the SQL *because* it executed it.

So a ` + "`:sql`" + ` for ent could only be a command that queried. It would take
gorm's argument, print gorm's output, and quietly hit your database. That is a
worse thing to ship than the gap.

## What to do instead

- ` + "`:schema migrate.Tables`" + ` — the structure, free.
- ` + "`:query EXPLAIN ...`" + ` — the plan for a statement you already have,
  read-only by default and through the project's configured connection.
- ` + "`client.Debug()`" + ` in your own code, when you accept that the query runs.
`
}
