package config

import (
	"fmt"
	"path/filepath"
	"strings"
)

// Database is one [[database]] entry: how to reach a database, and never the
// password for it.
//
// The schema is an array of tables rather than nested keys, and that is a
// structural decision rather than a stylistic one. It makes every write an
// append, which is what lets gluon add an entry to a config somebody wrote by
// hand without re-encoding the document and dropping their comments. See
// AppendDatabase.
type Database struct {
	// Module scopes this entry to one attached module path — the same key
	// [hosts."..."] already uses. Empty means it applies to any host, which is
	// what a project-local gluon.toml wants.
	Module string `toml:"module"`
	// Name is what :db switches between when a project has more than one. The
	// first entry matching a host is its default unless Default names another.
	Name string `toml:"name"`
	// Driver is the family: postgres, mysql, sqlite, sqlserver.
	Driver string `toml:"driver"`
	// DriverModule pins which driver module supplies it, for the case where a
	// project requires two. Empty means gluon picks from the build list.
	DriverModule string `toml:"driver_module"`

	// Exactly one of the four groups below must be given.

	// DSNEnv names an environment variable holding a whole connection string.
	DSNEnv string `toml:"dsn_env"`
	// DSNFile and DSNKey name a file and the key inside it. .env, YAML, TOML
	// and JSON are understood; the key is dotted for the structured ones. The
	// path is relative to the config file that named it.
	DSNFile string `toml:"dsn_file"`
	DSNKey  string `toml:"dsn_key"`
	// DSN is a literal connection string. It is accepted, never written by
	// gluon init, and refused outright when it carries a password.
	DSN string `toml:"dsn"`
	// File is a SQLite path, relative to the config file that named it.
	File string `toml:"file"`

	// The field group, for a connection assembled rather than parsed.
	Host     string            `toml:"host"`
	Port     int               `toml:"port"`
	User     string            `toml:"user"`
	DBName   string            `toml:"database"`
	Params   map[string]string `toml:"params"`
	PassEnv  string            `toml:"password_env"`
	PassFile string            `toml:"password_file"`

	// Password exists only to be refused. It is declared rather than left
	// unknown so the error can say what to do instead: md.Undecoded() would
	// otherwise catch it with "unknown setting password", which teaches
	// nothing to someone who has just written the obvious thing.
	Password string `toml:"password"`

	// ReadOnly is a pointer because unset and false are different answers:
	// unset means the default (true), false means the user said so.
	ReadOnly *bool `toml:"readonly"`

	// Dir is the directory of the file this was read from, so relative paths
	// resolve against the config rather than the working directory. Not a
	// setting.
	Dir string `toml:"-"`
	// From is the file it was read from, for :db and gluon doctor.
	From string `toml:"-"`
}

// IsReadOnly reports the effective setting. Read-only is the default: a REPL
// that could drop a table by typo is not a REPL anyone should point at a
// database they care about.
func (d Database) IsReadOnly() bool { return d.ReadOnly == nil || *d.ReadOnly }

// Label is how :db and gluon doctor name this entry.
func (d Database) Label() string {
	if d.Name != "" {
		return d.Name
	}
	return "database"
}

// SecretRef describes where the connection string comes from, in the words a
// report should use. It never contains a value.
func (d Database) SecretRef() string {
	switch {
	case d.DSNEnv != "":
		return "$" + d.DSNEnv
	case d.DSNFile != "":
		return d.DSNFile + " → " + d.DSNKey
	case d.File != "":
		return d.File
	case d.DSN != "":
		return "the config file"
	default:
		return "host/port/user/database"
	}
}

// Resolve turns relative paths into absolute ones, against the directory of the
// config file that named them.
//
// `file = "./data/app.db"` in a config means next to the config. Resolving it
// against the process's working directory would make a session's database
// depend on where gluon happened to be launched from.
func (d Database) resolvePaths() Database {
	if d.Dir == "" {
		return d
	}
	for _, p := range []*string{&d.DSNFile, &d.File, &d.PassFile} {
		if *p != "" && !filepath.IsAbs(*p) && !strings.HasPrefix(*p, "~") {
			*p = filepath.Join(d.Dir, *p)
		}
	}
	return d
}

// sources counts the mutually exclusive ways an entry can name a connection.
func (d Database) sources() []string {
	var out []string
	if d.DSNEnv != "" {
		out = append(out, "dsn_env")
	}
	if d.DSNFile != "" || d.DSNKey != "" {
		out = append(out, "dsn_file")
	}
	if d.DSN != "" {
		out = append(out, "dsn")
	}
	if d.File != "" {
		out = append(out, "file")
	}
	if d.Host != "" || d.DBName != "" {
		out = append(out, "host/database")
	}
	return out
}

// validateDatabases enforces the three rules that are errors rather than
// defaults. It follows Load's existing stance: a setting that silently does
// nothing is worse than one that says why.
func validateDatabases(dbs []Database) error {
	seen := map[string]bool{}
	for i, d := range dbs {
		where := fmt.Sprintf("database #%d", i+1)
		if d.Name != "" {
			where = "database " + d.Name
		}

		// The rule that earns its keep. Someone will write this.
		if d.Password != "" {
			return fmt.Errorf("%s: password is not a setting — gluon does not store secrets. "+
				"Use password_env = \"PGPASSWORD\" or password_file = \"~/.pgpass\"", where)
		}
		if d.DSN != "" && dsnCarriesPassword(d.DSN) {
			return fmt.Errorf("%s: dsn carries a password — use dsn_env = \"DATABASE_URL\", "+
				"or dsn_file and dsn_key, so the value is never in a file gluon writes", where)
		}

		switch src := d.sources(); len(src) {
		case 0:
			return fmt.Errorf("%s: names no connection — give dsn_env, dsn_file and dsn_key, "+
				"file, or host and database", where)
		case 1:
		default:
			return fmt.Errorf("%s: names a connection %d ways (%s) — give exactly one",
				where, len(src), strings.Join(src, ", "))
		}

		if (d.DSNFile == "") != (d.DSNKey == "") {
			return fmt.Errorf("%s: dsn_file and dsn_key go together — one without the other "+
				"names half a location", where)
		}
		if d.Driver == "" && d.File == "" {
			return fmt.Errorf("%s: no driver — one of postgres, mysql, sqlite, sqlserver", where)
		}
		if d.Name != "" {
			key := d.Module + "\x00" + d.Name
			if seen[key] {
				return fmt.Errorf("two databases are named %q for the same module — "+
					"a name that means two things is a name gluon would have to guess between", d.Name)
			}
			seen[key] = true
		}
	}
	return nil
}

// dsnCarriesPassword reports whether a literal connection string holds a
// secret, without parsing it as one.
//
// internal/config cannot import internal/dsn — dsn is where detection and
// querying meet, and config sits under both — so this is deliberately a coarse
// syntactic check. It only has to be right about refusing, and both forms it
// looks for are unambiguous.
func dsnCarriesPassword(s string) bool {
	if i := strings.Index(s, "://"); i >= 0 {
		rest := s[i+3:]
		if at := strings.Index(rest, "@"); at > 0 {
			if c := strings.Index(rest[:at], ":"); c >= 0 {
				return true
			}
		}
		return false
	}
	for _, f := range strings.Fields(s) {
		if k, v, ok := strings.Cut(f, "="); ok && strings.EqualFold(k, "password") && v != "" {
			return true
		}
	}
	// The Go MySQL form: user:pass@tcp(...)
	if at := strings.Index(s, "@"); at > 0 && strings.Contains(s[at:], "(") {
		return strings.Contains(s[:at], ":")
	}
	return false
}

// DatabasesFor is every entry that applies to a module path, nearest first.
//
// An entry naming the module wins over one that names none, so a global default
// can sit alongside a rule for one project without the two being merged. They
// are never merged: a half-merged database configuration is untraceable.
func (c *Config) DatabasesFor(module string) []Database {
	var scoped, global []Database
	for _, d := range c.Databases {
		switch {
		case d.Module == "":
			global = append(global, d)
		case module != "" && d.Module == module:
			scoped = append(scoped, d)
		}
	}
	if len(scoped) > 0 {
		return scoped
	}
	return global
}
