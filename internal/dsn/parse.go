package dsn

import (
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// sqliteMagic is the first 16 bytes of every SQLite database file.
//
// This constant is why SQLite detection is trustworthy where the rest is
// heuristic: it is a fact about the file, not a guess about its name. Invariant
// 12 in its purest available form.
const sqliteMagic = "SQLite format 3\x00"

// urlSchemes are the schemes gluon can open, mapped to the driver family.
var urlSchemes = map[string]string{
	"postgres":   Postgres,
	"postgresql": Postgres,
	"mysql":      MySQL,
	"sqlite":     SQLite,
	"sqlite3":    SQLite,
	"file":       SQLite,
	"libsql":     SQLite,
	"sqlserver":  SQLServer,
	"mssql":      SQLServer,
}

// notOurs maps a scheme gluon recognises but cannot open to the sentence it
// deserves.
//
// "not a DSN" is technically true for redis:// and completely useless — most
// often it appears when someone has pointed gluon at the wrong variable, and
// saying which wrong thing it is saves the round trip.
var notOurs = map[string]string{
	"redis": "redis is not SQL", "rediss": "redis is not SQL",
	"mongodb": "mongodb is not SQL", "mongodb+srv": "mongodb is not SQL",
	"amqp": "a message broker, not a database", "amqps": "a message broker, not a database",
	"kafka": "a message broker, not a database",
	"http":  "a URL, not a DSN", "https": "a URL, not a DSN",
	"smtp": "mail, not a database", "smtps": "mail, not a database",
	"memcached":     "a cache, not a SQL database",
	"elasticsearch": "a search index, not a SQL database",
	"s3":            "object storage, not a database",
}

// libpqKeys is every keyword libpq accepts in a keyword/value connection
// string. The set is closed on purpose: it is what stops `user=admin role=x`
// in a .env from reading as a connection string.
var libpqKeys = map[string]bool{
	"host": true, "hostaddr": true, "port": true, "dbname": true, "user": true,
	"password": true, "passfile": true, "connect_timeout": true, "sslmode": true,
	"sslcert": true, "sslkey": true, "sslrootcert": true, "sslcrl": true,
	"application_name": true, "fallback_application_name": true, "options": true,
	"keepalives": true, "target_session_attrs": true, "search_path": true,
	"client_encoding": true, "sslpassword": true, "service": true, "channel_binding": true,
}

// Parse reports whether s is a connection string, by grammar.
//
// The name of the variable holding s is deliberately not a parameter. Invariant
// 12's postmortem was a detector that matched on a name; the database version
// of that mistake is treating DATABASE_URL=https://api.example.com as a
// database because of what the key is called. Every rule below is a property of
// the bytes.
//
// dir is the directory s was found in. It is used only to resolve a relative
// SQLite path against the file that named it rather than against the process's
// working directory — `file = "./data/app.db"` in a config means next to the
// config, not next to wherever gluon was launched.
func Parse(s, dir string) (DSN, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return DSN{}, refuse(s, "empty")
	}

	// The URL branch must be gated on "://" before anything else tries.
	// url.Parse *succeeds* on the Go MySQL DSN root:secret@tcp(127.0.0.1:3306)/app
	// — it reads "root" as a scheme and the rest as opaque — and yields
	// nonsense that would then be reported as a database. See parseMySQLGo.
	if i := strings.Index(s, "://"); i > 0 {
		return parseURL(s)
	}
	// file:app.db?cache=shared is a URL with no authority, so it has no "://".
	if strings.HasPrefix(s, "file:") {
		return parseURL(s)
	}
	if isMySQLGo(s) {
		return parseMySQLGo(s)
	}
	if looksKeyValue(s) {
		return parseKeyValue(s)
	}
	return parseFile(s, dir)
}

func parseURL(s string) (DSN, error) {
	u, err := url.Parse(s)
	if err != nil {
		return DSN{}, refusef(s, "not a URL: %v", err)
	}
	scheme := strings.ToLower(u.Scheme)
	if why, known := notOurs[scheme]; known {
		return DSN{}, refuse(s, why)
	}
	driver, ok := urlSchemes[scheme]
	if !ok {
		return DSN{}, refusef(s, "%s:// is not a database gluon can open", scheme)
	}

	d := DSN{Driver: driver, Shape: ShapeURL, raw: s, Params: map[string]string{}}
	for k, v := range u.Query() {
		if len(v) > 0 {
			d.Params[k] = v[0]
		}
	}

	if driver == SQLite {
		// sqlite:///abs/path, sqlite://relative, file:app.db?cache=shared.
		p := u.Opaque
		if p == "" {
			p = u.Path
			if u.Host != "" && u.Host != "." {
				// sqlite://data/app.db parses Host="data", Path="/app.db".
				p = u.Host + p
			}
		}
		if p == "" {
			return DSN{}, refuse(s, "names no file")
		}
		d.Shape = ShapeURL
		d.File = p
		// A scheme is sufficient intent even when the file does not exist yet;
		// the caller reports that as a concern rather than hiding it.
		return d, nil
	}

	if u.Host == "" {
		return DSN{}, refuse(s, "names no host")
	}
	d.Host = u.Hostname()
	if p := u.Port(); p != "" {
		n, err := strconv.Atoi(p)
		if err != nil {
			return DSN{}, refusef(s, "port %q is not a number", p)
		}
		d.Port = n
	} else {
		d.Port = DefaultPort(driver)
	}
	if u.User != nil {
		d.User = u.User.Username()
		if pw, set := u.User.Password(); set {
			d.password, d.hasPass = pw, true
		}
	}
	d.Database = strings.TrimPrefix(u.Path, "/")
	if len(d.Params) == 0 {
		d.Params = nil
	}
	return d, nil
}

// mysqlNets are the network forms the Go MySQL driver accepts. One of them
// appearing before the final "/" is the discriminator: it is what a value has
// to contain for this grammar rather than something a path could satisfy.
var mysqlNets = []string{"@tcp(", "@unix(", "@cloudsql("}

func isMySQLGo(s string) bool {
	for _, n := range mysqlNets {
		if strings.Contains(s, n) {
			return true
		}
	}
	return false
}

// parseMySQLGo reads user:pass@tcp(host:port)/dbname?params.
//
// The bare /dbname form the driver also accepts is deliberately not recognised
// anywhere in this package. "/tmp" and "/app" are valid MySQL DSNs under that
// rule and are also just filesystem paths, and a detector that cannot tell them
// apart would report every path in a .env as a database. Narrowing here means
// gluon asks instead of guessing, which is the trade this project makes
// everywhere else.
func parseMySQLGo(s string) (DSN, error) {
	d := DSN{Driver: MySQL, Shape: ShapeMySQLGo, raw: s}

	open := strings.Index(s, "(")
	shut := strings.LastIndex(s, ")")
	if open < 0 || shut < open {
		return DSN{}, refuse(s, "unbalanced network address")
	}
	at := strings.LastIndex(s[:open], "@")
	if at < 0 {
		return DSN{}, refuse(s, "no credentials before the network address")
	}

	if cred := s[:at]; cred != "" {
		user, pass, found := strings.Cut(cred, ":")
		d.User = user
		if found {
			d.password, d.hasPass = pass, true
		}
	}

	addr := s[open+1 : shut]
	host, port, found := strings.Cut(addr, ":")
	d.Host = host
	if found {
		n, err := strconv.Atoi(port)
		if err != nil {
			return DSN{}, refusef(s, "port %q is not a number", port)
		}
		d.Port = n
	} else {
		d.Port = DefaultPort(MySQL)
	}

	rest := strings.TrimPrefix(s[shut+1:], "/")
	name, query, hasQuery := strings.Cut(rest, "?")
	d.Database = name
	if hasQuery {
		d.Params = map[string]string{}
		for _, kv := range strings.Split(query, "&") {
			if k, v, ok := strings.Cut(kv, "="); ok {
				d.Params[k] = v
			}
		}
	}
	return d, nil
}

// looksKeyValue reports whether s could be a libpq keyword/value string.
//
// Two pairs are required, every key must be one libpq actually accepts, and one
// of host/dbname/user must be present. One pair is not enough: `user=admin` in
// a .env is a username, not a connection string, and accepting it would make
// every assignment-shaped value a database.
func looksKeyValue(s string) bool {
	fields := strings.Fields(s)
	if len(fields) < 2 {
		return false
	}
	var anchor bool
	for _, f := range fields {
		k, _, ok := strings.Cut(f, "=")
		if !ok || !libpqKeys[strings.ToLower(k)] {
			return false
		}
		switch strings.ToLower(k) {
		case "host", "hostaddr", "dbname", "user":
			anchor = true
		}
	}
	return anchor
}

func parseKeyValue(s string) (DSN, error) {
	d := DSN{Driver: Postgres, Shape: ShapeKeyValue, raw: s, Params: map[string]string{}}
	for _, f := range strings.Fields(s) {
		k, v, _ := strings.Cut(f, "=")
		v = strings.Trim(v, `'"`)
		switch strings.ToLower(k) {
		case "host", "hostaddr":
			d.Host = v
		case "port":
			n, err := strconv.Atoi(v)
			if err != nil {
				return DSN{}, refusef(s, "port %q is not a number", v)
			}
			d.Port = n
		case "user":
			d.User = v
		case "dbname":
			d.Database = v
		case "password":
			d.password, d.hasPass = v, true
		default:
			d.Params[strings.ToLower(k)] = v
		}
	}
	if d.Port == 0 {
		d.Port = DefaultPort(Postgres)
	}
	if len(d.Params) == 0 {
		d.Params = nil
	}
	return d, nil
}

// parseFile accepts a bare path only when the file exists and its first bytes
// say SQLite.
//
// This is the strongest shape rule in the package. LOG_PATH=/var/log/app.log
// cannot be mistaken for a database however it is spelled, and a SQLite
// database called notes.txt is still found — which is the exact inversion of
// the name-matching failure invariant 12 records.
func parseFile(s, dir string) (DSN, error) {
	if strings.ContainsAny(s, " \t\n") {
		return DSN{}, refuse(s, "not a connection string")
	}
	p := s
	if !filepath.IsAbs(p) && dir != "" {
		p = filepath.Join(dir, p)
	}
	if !IsSQLiteFile(p) {
		return DSN{}, refuse(s, "not a connection string")
	}
	abs, err := filepath.Abs(p)
	if err != nil {
		abs = p
	}
	return DSN{Driver: SQLite, Shape: ShapeFile, File: abs, raw: abs}, nil
}

// IsSQLiteFile reports whether path is a SQLite database, by reading its first
// sixteen bytes.
//
// Never by extension. An app.db full of JSON, a zero-byte placeholder and a
// BoltDB store all end in .db, and a detector that trusted the name would open
// all three.
func IsSQLiteFile(path string) bool {
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer f.Close()
	buf := make([]byte, len(sqliteMagic))
	n, err := f.Read(buf)
	if err != nil || n < len(sqliteMagic) {
		return false
	}
	return string(buf) == sqliteMagic
}

// FromFields assembles a DSN from configured fields, for the config form where
// there never was a connection string to parse.
func FromFields(driver, host string, port int, user, database, password string, params map[string]string) DSN {
	d := DSN{
		Driver: driver, Host: host, Port: port, User: user,
		Database: database, Params: params, Shape: ShapeFields,
	}
	if password != "" {
		d.password, d.hasPass = password, true
	}
	if d.Port == 0 {
		d.Port = DefaultPort(driver)
	}
	return d
}

// FromFile is a SQLite DSN for a known path.
func FromFile(path string, params map[string]string) DSN {
	return DSN{Driver: SQLite, File: path, Params: params, Shape: ShapeFile, raw: path}
}

// WithPassword returns a copy carrying a secret resolved at connect time.
// Detection never calls it: a candidate holds a reference to where a password
// lives, not the password.
func (d DSN) WithPassword(pw string) DSN {
	if pw == "" {
		return d
	}
	d.password, d.hasPass = pw, true
	d.raw = "" // the raw string predates the password, so rebuild from fields
	return d
}
