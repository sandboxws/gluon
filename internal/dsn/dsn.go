// Package dsn parses database connection strings, and refuses to print them.
//
// It exists as its own package for two reasons. It is the contract between
// detection and querying — one side produces a DSN, the other opens it, and
// neither needs to know how the other works. And it has no dependencies at all,
// which is what lets the redaction rules below be tested exhaustively without a
// fixture tree or a toolchain.
//
// The rule the whole package is built around is invariant 23: a value is a DSN
// by shape, never by the name of the key holding it. DATABASE_URL=https://api
// is a URL; SMTP_URL=smtp://… is mail; LOG_PATH=/var/log/app.log is a path.
// Parse is therefore never told the name of the key a value came from — it
// cannot be influenced by it, rather than merely choosing not to be.
package dsn

import (
	"fmt"
	"net"
	"net/url"
	"sort"
	"strconv"
	"strings"

	"github.com/sandboxws/gluon/internal/redact"
)

// Shape is the grammar that matched.
//
// It is reported rather than kept private so an error can name what gluon
// thought it was reading, and so :db can say "the Go mysql form" instead of
// leaving someone to wonder why their connection string was rejected.
type Shape string

const (
	ShapeURL      Shape = "url"      // postgres://user:pw@host:5432/db
	ShapeMySQLGo  Shape = "mysql-go" // user:pw@tcp(host:3306)/db — not a URL
	ShapeKeyValue Shape = "keyvalue" // host=… port=… dbname=…
	ShapeFile     Shape = "file"     // a path whose first 16 bytes say SQLite
	ShapeFields   Shape = "fields"   // assembled from config, never parsed
)

// Driver families. This is the *family*, not the name a driver registers with
// database/sql: lib/pq registers "postgres" but pgx registers "pgx", and
// modernc.org/sqlite registers "sqlite" where mattn's registers "sqlite3". The
// registered name is a property of the driver module in the build list, not of
// the connection string, so it is resolved where the module is known.
const (
	Postgres  = "postgres"
	MySQL     = "mysql"
	SQLite    = "sqlite"
	SQLServer = "sqlserver"
)

// DSN is a connection string parsed into the fields every driver needs, plus
// the bytes that would actually be handed to sql.Open.
//
// The password and the raw string are unexported, and String is Redacted. That
// is deliberate and it is the whole security posture of the type: the
// zero-effort path — a %v in an fmt.Errorf, a struct dropped into a -json
// envelope, a t.Logf while debugging — cannot leak a secret, because there is
// no exported way to reach one except ConnectString, which is named so that a
// reviewer sees it.
type DSN struct {
	// Driver is the family: postgres, mysql, sqlite, sqlserver.
	Driver string
	// Host and Port are empty for SQLite, which is a file.
	Host string
	Port int
	User string
	// Database is the database name; File is the path, for SQLite.
	Database string
	File     string
	// Params are the query parameters, e.g. sslmode=disable.
	Params map[string]string
	// Shape is the grammar that produced this.
	Shape Shape

	password string
	hasPass  bool
	raw      string
}

// HasPassword reports whether a password was present, without revealing it.
//
// It matters because "no password" and "password hidden" are different answers
// when a connection fails, and a redacted string that always said *** could not
// tell them apart.
func (d DSN) HasPassword() bool { return d.hasPass }

// hides reports whether Redacted hides anything: a password, or a query
// parameter that is itself a secret.
func (d DSN) hides() bool {
	if d.hasPass {
		return true
	}
	for k := range d.Params {
		if redact.Param(k) {
			return true
		}
	}
	return false
}

// Password is the secret itself. It is unexported-by-convention: nothing in
// detection or reporting calls it, and the one caller that does is the code
// that registers strings to mask out of child output.
func (d DSN) Password() string { return d.password }

// String is Redacted. See the type comment: this is what makes a leak take
// deliberate effort rather than a moment's inattention.
func (d DSN) String() string { return d.Redacted() }

// Redacted is every printable form gluon has. There is no unredacted printer.
func (d DSN) Redacted() string {
	const mask = redact.Mask
	switch d.Shape {
	case ShapeFile:
		// A file path carries no password. A sqlcipher key rides in the query
		// string, and that is the one thing worth hiding here.
		if len(d.Params) == 0 {
			return d.File
		}
		return d.File + "?" + redactedParams(d.Params, mask)

	case ShapeMySQLGo:
		var b strings.Builder
		b.WriteString(d.User)
		if d.hasPass {
			b.WriteString(":" + mask)
		}
		b.WriteString("@tcp(" + net.JoinHostPort(d.Host, strconv.Itoa(d.Port)) + ")/" + d.Database)
		if len(d.Params) > 0 {
			b.WriteString("?" + redactedParams(d.Params, mask))
		}
		return b.String()

	case ShapeKeyValue:
		var parts []string
		if d.Host != "" {
			parts = append(parts, "host="+d.Host)
		}
		if d.Port != 0 {
			parts = append(parts, "port="+strconv.Itoa(d.Port))
		}
		if d.User != "" {
			parts = append(parts, "user="+d.User)
		}
		if d.Database != "" {
			parts = append(parts, "dbname="+d.Database)
		}
		if d.hasPass {
			parts = append(parts, "password="+mask)
		}
		for _, k := range sortedKeys(d.Params) {
			parts = append(parts, k+"="+d.Params[k])
		}
		return strings.Join(parts, " ")

	default: // ShapeURL, ShapeFields
		if d.Driver == SQLite {
			// A sqlite URL carries its path where a server DSN carries a host,
			// so the generic branch below renders it as a bare "sqlite://" and
			// loses the one thing worth reading. The path is not the secret;
			// only a sqlcipher key in the query string is.
			if len(d.Params) == 0 {
				return d.Driver + "://" + d.File
			}
			return d.Driver + "://" + d.File + "?" + redactedParams(d.Params, mask)
		}
		var b strings.Builder
		b.WriteString(d.Driver + "://")
		if d.User != "" {
			b.WriteString(d.User)
			if d.hasPass {
				b.WriteString(":" + mask)
			}
			b.WriteString("@")
		}
		b.WriteString(d.hostPort())
		if d.Database != "" {
			b.WriteString("/" + d.Database)
		}
		if len(d.Params) > 0 {
			b.WriteString("?" + redactedParams(d.Params, mask))
		}
		return b.String()
	}
}

// ConnectString is the only method that returns the password. Nothing in
// detection or reporting calls it; it exists for the one moment a connection is
// actually opened.
//
// It returns the original bytes when there were any. A DSN gluon reconstructed
// from parsed fields is a DSN gluon might have reconstructed wrongly — a driver
// takes options gluon does not model — so the string the project actually wrote
// is preferred over anything this package could rebuild.
func (d DSN) ConnectString() string {
	if d.raw != "" {
		return d.raw
	}
	return d.build()
}

// build assembles a connection string from fields, for the config form where
// there never was a raw string.
func (d DSN) build() string {
	switch d.Driver {
	case SQLite:
		if len(d.Params) == 0 {
			return d.File
		}
		return d.File + "?" + params(d.Params)
	case MySQL:
		var b strings.Builder
		b.WriteString(d.User)
		if d.hasPass {
			b.WriteString(":" + d.password)
		}
		b.WriteString("@tcp(" + net.JoinHostPort(d.Host, strconv.Itoa(d.Port)) + ")/" + d.Database)
		if len(d.Params) > 0 {
			b.WriteString("?" + params(d.Params))
		}
		return b.String()
	default:
		u := url.URL{Scheme: d.Driver, Host: d.hostPort(), Path: "/" + d.Database}
		if d.User != "" {
			if d.hasPass {
				u.User = url.UserPassword(d.User, d.password)
			} else {
				u.User = url.User(d.User)
			}
		}
		if len(d.Params) > 0 {
			q := url.Values{}
			for k, v := range d.Params {
				q.Set(k, v)
			}
			u.RawQuery = q.Encode()
		}
		return u.String()
	}
}

// Target is the identity two candidates are compared on.
//
// Credentials are deliberately excluded. A compose file's superuser and an
// app's own user naming the same server, port and database are one answer with
// two provenances, not two answers to refuse between — and telling those apart
// is what keeps invariant 13's refusal for the case that actually needs it.
func (d DSN) Target() string {
	if d.Driver == SQLite {
		return "sqlite:" + d.File
	}
	return d.Driver + "://" + d.hostPort() + "/" + d.Database
}

func (d DSN) hostPort() string {
	if d.Port == 0 {
		return d.Host
	}
	return net.JoinHostPort(d.Host, strconv.Itoa(d.Port))
}

// DefaultPort is the port a driver family listens on when none was given.
func DefaultPort(driver string) int {
	switch driver {
	case Postgres:
		return 5432
	case MySQL:
		return 3306
	case SQLServer:
		return 1433
	}
	return 0
}

func params(m map[string]string) string {
	var parts []string
	for _, k := range sortedKeys(m) {
		parts = append(parts, k+"="+m[k])
	}
	return strings.Join(parts, "&")
}

// redactedParams hides the parameters that are themselves secrets. sqlcipher
// carries its key in the query string, and a password in a URL parameter is a
// documented libpq form.
func redactedParams(m map[string]string, mask string) string {
	var parts []string
	for _, k := range sortedKeys(m) {
		v := m[k]
		if redact.Param(k) {
			v = mask
		}
		parts = append(parts, k+"="+v)
	}
	return strings.Join(parts, "&")
}

func sortedKeys(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// Error is a refusal that knows why. A scheme gluon recognises but cannot open
// deserves better than "not a DSN", especially inside a wizard.
type Error struct {
	Input  string
	Reason string
}

func (e *Error) Error() string { return e.Reason }

func refuse(input, reason string) error { return &Error{Input: input, Reason: reason} }

func refusef(input, format string, a ...any) error {
	return &Error{Input: input, Reason: fmt.Sprintf(format, a...)}
}
