package db

import (
	"github.com/sandboxws/gluon/internal/cmdspec"
	"github.com/sandboxws/gluon/internal/plugin"
)

// The migration plugins.
//
// Both answer the same question — which migrations have run — and both want the
// same command name, because the question is the library's, not the library
// author's. What differs is where the answer is kept: goose and golang-migrate
// track applied versions in differently shaped tables, so each needs its own
// statement and neither can read the other's.
//
// That is why this is two plugins sharing a name rather than one plugin with a
// branch. The registry order is the declared tie-break and :plugins reports the
// loser, which is the shape :routes already uses for seven web frameworks —
// and it means a project with both in its build list is told which table was
// read rather than being handed an answer from a tool it stopped using.
//
// Neither is asked its own status API. goose's Status and golang-migrate's
// Version both take a live connection through the library, and both can create
// the tracking table or take a lock on a database that has neither. A SELECT
// against the table cannot: it passes db.CheckStatement with the write flag
// unset, so what enforces read-only here is the allowlist that already exists.

// migrationsCommand is the command both libraries contribute, differing only in
// the statement and the table it reads.
func migrationsCommand(detail string, q *plugin.DBQuery) plugin.Command {
	return plugin.Command{
		Name: ":migrations",
		// A bare name, the spelling :db already uses, and -d <name> as well
		// for symmetry with :query. Neither takes a connection string: a
		// connection is configured, and both `-dsn` and `:db connect <url>`
		// are permanently rejected for putting a password in a history file.
		Arg: "[name]",
		Usage: cmdspec.Spec{
			Kind:   cmdspec.Words,
			Params: []cmdspec.Param{{Name: "name", Optional: true, Values: cmdspec.Values{Source: cmdspec.Databases}}},
			Flags:  []cmdspec.Flag{{Name: "-d", Value: "name", Values: cmdspec.Values{Source: cmdspec.Databases}, Help: "which database, as :query -d picks one"}},
			Examples: []cmdspec.Example{
				{Line: ":migrations", Says: "the tracking table, newest first"},
				{Line: ":migrations -d analytics"},
			},
			See: []string{":db", ":query"},
		},
		Summary: "which migrations have been applied, from the tracking table",
		Detail:  detail,
		Query:   q,
	}
}

// Goose is the plugin for github.com/pressly/goose.
//
// goose_db_version is goose's own table and its columns have been these since
// v2: an id, the migration's version_id, whether it is applied, and when it was
// stamped. The rows are read newest first, because "what ran last" is the
// question somebody at a prompt is asking.
type Goose struct{}

func (Goose) Meta() plugin.Meta {
	return plugin.Meta{
		Name:    "goose",
		Module:  "github.com/pressly/goose",
		Summary: ":migrations reads goose_db_version through the project's database",
	}
}

func (Goose) Imports() []plugin.Import {
	return []plugin.Import{{Name: "goose", Path: "github.com/pressly/goose/v3"}}
}

func (Goose) Aliases() []plugin.Alias {
	return []plugin.Alias{{Name: "goose", Module: "github.com/pressly/goose/v3"}}
}

func (Goose) Commands() []plugin.Command {
	return []plugin.Command{migrationsCommand(
		"Reads goose_db_version, newest first: the version, whether it is applied,\n"+
			"and when it was stamped.\n\n"+
			"It goes through the project's configured database — the same connection\n"+
			"and the same driver :query uses, with the connection string reaching the\n"+
			"child through its environment and never through the generated source.\n"+
			"-d <name> picks one when a project configures several.\n\n"+
			"Nothing is applied. This is a SELECT, checked by the same read-only\n"+
			"allowlist :query uses, and goose's own status API is deliberately not\n"+
			"called: it can create the tracking table on a database that has none.",
		&plugin.DBQuery{
			Table: "goose_db_version",
			SQL:   "SELECT version_id, is_applied, tstamp FROM goose_db_version ORDER BY id DESC",
			Empty: "goose_db_version is empty — no migration has been recorded against this database",
		})}
}

// Migrate is the plugin for github.com/golang-migrate/migrate.
//
// schema_migrations holds one row: the version reached, and whether the run
// that reached it failed partway. dirty is the field worth having — a dirty
// database refuses every subsequent migration until somebody forces a version,
// and the symptom is a migration step that will not run with no explanation
// anywhere near it.
type Migrate struct{}

func (Migrate) Meta() plugin.Meta {
	return plugin.Meta{
		Name:    "migrate",
		Module:  "github.com/golang-migrate/migrate",
		Summary: ":migrations reads schema_migrations, including the dirty flag",
	}
}

func (Migrate) Imports() []plugin.Import {
	return []plugin.Import{{Name: "migrate", Path: "github.com/golang-migrate/migrate/v4"}}
}

func (Migrate) Aliases() []plugin.Alias {
	return []plugin.Alias{{Name: "migrate", Module: "github.com/golang-migrate/migrate/v4"}}
}

func (Migrate) Commands() []plugin.Command {
	return []plugin.Command{migrationsCommand(
		"Reads schema_migrations: the version this database has reached, and whether\n"+
			"it is dirty.\n\n"+
			"golang-migrate records one row, not a history — dirty means a migration\n"+
			"failed partway and every later one will refuse to run until the version is\n"+
			"forced, which is worth seeing before you spend an afternoon on it.\n\n"+
			"It goes through the project's configured database — the same connection\n"+
			"and the same driver :query uses, with the connection string reaching the\n"+
			"child through its environment and never through the generated source.\n"+
			"-d <name> picks one when a project configures several.\n\n"+
			"Nothing is applied. This is a SELECT, checked by the same read-only\n"+
			"allowlist :query uses.",
		&plugin.DBQuery{
			Table: "schema_migrations",
			SQL:   "SELECT version, dirty FROM schema_migrations",
			Empty: "schema_migrations is empty — no migration has been applied to this database",
		})}
}
