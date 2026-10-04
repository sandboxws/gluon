// Package db holds the plugins for database work.
package db

import (
	"strings"

	"github.com/sandboxws/gluon/internal/plugin"
	"github.com/sandboxws/gluon/internal/pretty"
)

// SQL is the plugin for database/sql. It is standard library, so it is always
// active — which matters, because the Null types are the ones people actually
// hold in a REPL while working out why a column came back empty.
//
// sql.NullString{String:"", Valid:false} and sql.NullString{String:"x",
// Valid:false} print differently and mean the same thing: NULL. The struct
// rendering shows the field that does not matter as prominently as the one that
// does.
type SQL struct{}

func (SQL) Meta() plugin.Meta {
	return plugin.Meta{
		Name:    "sql",
		Summary: "NULL columns as NULL, not as a value plus a boolean",
	}
}

func (SQL) Imports() []plugin.Import {
	return []plugin.Import{{Name: "sql", Path: "database/sql"}}
}

// nullTypes is every Null* in database/sql, with the field holding the value.
var nullTypes = map[string]string{
	"sql.NullString":  "String",
	"sql.NullBool":    "Bool",
	"sql.NullByte":    "Byte",
	"sql.NullFloat64": "Float64",
	"sql.NullInt16":   "Int16",
	"sql.NullInt32":   "Int32",
	"sql.NullInt64":   "Int64",
	"sql.NullTime":    "Time",
}

func (SQL) Renders() []plugin.Render {
	out := make([]plugin.Render, 0, len(nullTypes))
	for typ, field := range nullTypes {
		out = append(out, plugin.Render{
			Type: typ,
			Rich: nullRenderer(typ, field),
			// A struct of scanned columns is where this matters most: a row of
			// {String:"" Valid:false} cells is unreadable, and a column of
			// NULLs is the answer someone is looking for.
			Inline: nullInline(field),
		})
	}
	return out
}

func nullInline(field string) func(pretty.Value, pretty.Styles) (string, bool) {
	return func(v pretty.Value, st pretty.Styles) (string, bool) {
		if v.Kind != "struct" {
			return "", false
		}
		valid, ok := boolField(v, "Valid")
		if !ok {
			return "", false
		}
		if !valid {
			return st.Note.Render("NULL"), true
		}
		val, ok := fieldValue(v, field)
		if !ok {
			return "", false
		}
		if val.Kind == "string" {
			return st.Str.Render(pretty.Quote(val.Repr)), true
		}
		return val.Repr, true
	}
}

func nullRenderer(typ, field string) func(pretty.Value, pretty.Styles) (string, bool) {
	return func(v pretty.Value, st pretty.Styles) (string, bool) {
		if v.Kind != "struct" {
			return "", false
		}
		valid, ok := boolField(v, "Valid")
		if !ok {
			return "", false
		}
		head := st.Type.Render("(" + typ + ")")
		if !valid {
			// The carried value is deliberately not shown. It is whatever was
			// in the struct when the scan found NULL, and reading it is the
			// bug this rendering exists to prevent.
			return head + " " + st.Note.Render("NULL"), true
		}
		val, ok := fieldValue(v, field)
		if !ok {
			return "", false
		}
		body := val.Repr
		style := st.Num
		if val.Kind == "string" {
			// Quoted, as the printer quotes any string: a column holding the
			// four letters NULL is otherwise drawn exactly as a NULL is, and
			// colour is the only thing left to tell them apart — which a pipe
			// and this page do not have.
			body, style = pretty.Quote(body), st.Str
		}
		return head + " " + style.Render(body), true
	}
}

func boolField(v pretty.Value, name string) (bool, bool) {
	f, ok := fieldValue(v, name)
	if !ok {
		return false, false
	}
	switch strings.TrimSpace(f.Repr) {
	case "true":
		return true, true
	case "false":
		return false, true
	}
	return false, false
}

func fieldValue(v pretty.Value, name string) (pretty.Value, bool) {
	for _, f := range v.Fields {
		if f.Name == name {
			return f.Val, true
		}
	}
	return pretty.Value{}, false
}
