// Package db detects the database a hosted project uses, and turns a statement
// into the Go source that runs against it.
//
// gluon never links a database driver. The gorm plugin works because the user's
// own module supplies gorm, and a driver is the same: the generated program
// blank-imports whatever the attached project already requires, so gluon's own
// binary has no database dependency and the child stays a program the user
// could have written.
package db

import (
	"strings"

	"github.com/sandboxws/gluon/internal/dsn"
)

// A Driver is one database/sql driver gluon knows how to reach.
//
// Module is what must be in the build list; Import is what the program
// blank-imports, which is not always the same — pgx registers its database/sql
// driver from a subpackage. Name is what it registers *as*, and it is why the
// driver family in a DSN is not enough on its own: a postgres:// URL says
// nothing about whether the project vendored lib/pq or pgx, and sql.Open takes
// the registered name rather than the family.
type Driver struct {
	Module string
	Import string
	Name   string
	Family string
}

// drivers is every driver gluon can generate a program for.
//
// The order is the tie-break, exactly as internal/plugins.Builtin's is: when a
// project requires two drivers for the same family, the first entry wins and
// :db says which. Declared order means the outcome is the same on every run.
var drivers = []Driver{
	{Module: "github.com/lib/pq", Import: "github.com/lib/pq", Name: "postgres", Family: dsn.Postgres},
	{Module: "github.com/jackc/pgx/v5", Import: "github.com/jackc/pgx/v5/stdlib", Name: "pgx", Family: dsn.Postgres},
	{Module: "github.com/jackc/pgx/v4", Import: "github.com/jackc/pgx/v4/stdlib", Name: "pgx", Family: dsn.Postgres},

	{Module: "github.com/go-sql-driver/mysql", Import: "github.com/go-sql-driver/mysql", Name: "mysql", Family: dsn.MySQL},

	// modernc's is pure Go, so it works without cgo and is the one more likely
	// to be present in a project that cares about cross-compiling.
	{Module: "modernc.org/sqlite", Import: "modernc.org/sqlite", Name: "sqlite", Family: dsn.SQLite},
	{Module: "github.com/mattn/go-sqlite3", Import: "github.com/mattn/go-sqlite3", Name: "sqlite3", Family: dsn.SQLite},

	{Module: "github.com/microsoft/go-mssqldb", Import: "github.com/microsoft/go-mssqldb", Name: "sqlserver", Family: dsn.SQLServer},
	{Module: "github.com/denisenkom/go-mssqldb", Import: "github.com/denisenkom/go-mssqldb", Name: "mssql", Family: dsn.SQLServer},
}

// gormDrivers map a gorm dialect module to the database/sql driver underneath
// it. A project using gorm has the real driver in its build list transitively,
// which is what makes :query possible there without any extra dependency.
var gormDrivers = map[string]string{
	"gorm.io/driver/postgres":  "github.com/jackc/pgx/v5",
	"gorm.io/driver/mysql":     "github.com/go-sql-driver/mysql",
	"gorm.io/driver/sqlite":    "github.com/mattn/go-sqlite3",
	"gorm.io/driver/sqlserver": "github.com/microsoft/go-mssqldb",
}

// DriversIn returns the drivers a build list actually provides, in declared
// order.
//
// requires is what Evaluator.Requires reports — "path version" strings — which
// is the same slice plugin.Set.Activate reads. Asking the same question of the
// same data means "can gluon query this" and "is the gorm plugin active" can
// never disagree.
func DriversIn(requires []string) []Driver {
	have := map[string]bool{}
	for _, r := range requires {
		p, _, ok := strings.Cut(r, " ")
		if !ok {
			p = r
		}
		have[p] = true
		if under, isGorm := gormDrivers[p]; isGorm {
			have[under] = true
		}
	}
	var out []Driver
	for _, d := range drivers {
		if moduleIn(have, d.Module) {
			out = append(out, d)
		}
	}
	return out
}

// moduleIn is plugin.moduleIn's rule: a module counts when it is present, or
// when something beneath it is. A project requiring github.com/jackc/pgx/v5/stdlib
// directly has pgx.
func moduleIn(have map[string]bool, module string) bool {
	if have[module] {
		return true
	}
	for p := range have {
		if strings.HasPrefix(p, module+"/") {
			return true
		}
	}
	return false
}

// DriverFor picks the driver to open a family with.
//
// pin names a specific module, for a project that requires two. Without one the
// first in declared order wins, and the caller reports the choice — silently
// picking between two drivers that behave differently is the kind of thing that
// shows up as a type conversion error three queries later.
func DriverFor(family string, available []Driver, pin string) (Driver, bool) {
	for _, d := range available {
		if d.Family != family {
			continue
		}
		if pin == "" || d.Module == pin || d.Name == pin {
			return d, true
		}
	}
	return Driver{}, false
}

// KnownDriverModules is every module gluon could use, for the message that says
// what to :get. Ordered by family so the suggestion for postgres is a postgres
// driver.
func KnownDriverModules(family string) []string {
	var out []string
	for _, d := range drivers {
		if family == "" || d.Family == family {
			out = append(out, d.Module)
		}
	}
	return out
}
