package db

import (
	"fmt"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/sandboxws/gluon/internal/dsn"
)

// fieldGroupKeys are the names a connection block is spelled with.
var fieldGroupKeys = map[string]string{
	"host": "host", "hostname": "host", "server": "host", "address": "host",
	"port": "port",
	"user": "user", "username": "user",
	"dbname": "database", "database": "database", "name": "database", "schema": "database",
	"password": "password", "pass": "password",
	"driver": "driver", "dialect": "driver", "adapter": "driver",
}

// scanConfFile reads a YAML, TOML or JSON config for a connection.
//
// Two shapes qualify, and neither is a key name on its own. A leaf string that
// parses as a connection string is one. The other is a *field group*: host,
// port and a database name sitting together, which is what a connection block
// looks like in every configuration format — where `database: myapp` alone is
// just a word.
func scanConfFile(path string, depth int) []Candidate {
	var decode func([]byte) (any, error)
	switch strings.ToLower(filepath.Ext(path)) {
	case ".yaml", ".yml":
		decode = yamlTree
	case ".toml":
		decode = tomlTree
	case ".json":
		decode = jsonTree
	default:
		return nil
	}
	tree, err := readTree(path, decode)
	if err != nil {
		return nil
	}
	var out []Candidate
	walkTree(tree, nil, func(keyPath []string, node any) {
		switch v := node.(type) {
		case string:
			if v == "" {
				return
			}
			parsed, err := dsn.Parse(v, filepath.Dir(path))
			if err != nil {
				return
			}
			out = append(out, Candidate{
				DSN:   parsed,
				Rank:  RankApp,
				Depth: depth,
				Secret: SecretRef{
					File: path, Key: strings.Join(keyPath, "."),
				},
				From: []Provenance{{
					File: path, Key: strings.Join(keyPath, "."), Kind: KindConfFile,
				}},
			})
		case map[string]any:
			if c, ok := fieldGroup(v, keyPath, path, depth); ok {
				out = append(out, c)
			}
		}
	})
	return out
}

// fieldGroup recognises a connection block by the fields present together.
//
// Three or more of the connection fields, and one of them naming a database, is
// the threshold. Below it the evidence is a coincidence: a `name` beside a
// `port` in a service definition is not a database, and reporting one would be
// the roaming-detector failure with a different input.
func fieldGroup(m map[string]any, keyPath []string, path string, depth int) (Candidate, bool) {
	got := map[string]string{}
	for k, v := range m {
		canon, ok := fieldGroupKeys[strings.ToLower(k)]
		if !ok {
			continue
		}
		s, ok := scalarString(v)
		if !ok || s == "" {
			continue
		}
		got[canon] = s
	}
	if len(got) < 3 || got["database"] == "" {
		return Candidate{}, false
	}
	family := familyOf(got["driver"])
	port, _ := strconv.Atoi(got["port"])
	if family == "" {
		// Without a driver named, the port is the only thing that says which
		// database this is. A group with neither is not identifiable, and
		// guessing postgres because it is common is exactly the guess this
		// project refuses.
		switch port {
		case 5432:
			family = dsn.Postgres
		case 3306:
			family = dsn.MySQL
		case 1433:
			family = dsn.SQLServer
		default:
			return Candidate{}, false
		}
	}
	host := got["host"]
	if host == "" {
		host = "127.0.0.1"
	}
	c := Candidate{
		DSN:   dsn.FromFields(family, host, port, got["user"], got["database"], "", nil),
		Rank:  RankApp,
		Depth: depth,
		From: []Provenance{{
			File: path, Key: strings.Join(keyPath, "."), Kind: KindConfFile,
			Note: fmt.Sprintf("%d connection fields together", len(got)),
		}},
	}
	// A password written in a config is a secret gluon reports the location of
	// and never the value of.
	if got["password"] != "" {
		c.Secret = SecretRef{File: path, Key: strings.Join(append(keyPath, "password"), ".")}
	}
	return c, true
}

func familyOf(driver string) string {
	switch strings.ToLower(driver) {
	case "postgres", "postgresql", "pgx", "pq":
		return dsn.Postgres
	case "mysql", "mariadb":
		return dsn.MySQL
	case "sqlite", "sqlite3":
		return dsn.SQLite
	case "sqlserver", "mssql":
		return dsn.SQLServer
	}
	return ""
}

func scalarString(v any) (string, bool) {
	switch t := v.(type) {
	case string:
		return t, true
	case int:
		return strconv.Itoa(t), true
	case int64:
		return strconv.FormatInt(t, 10), true
	case float64:
		if t == float64(int64(t)) {
			return strconv.FormatInt(int64(t), 10), true
		}
		return strconv.FormatFloat(t, 'f', -1, 64), true
	}
	return "", false
}

// walkTree visits every node, mappings included, so both shapes get a look.
func walkTree(node any, keyPath []string, fn func([]string, any)) {
	fn(keyPath, node)
	switch t := node.(type) {
	case map[string]any:
		for k, v := range t {
			walkTree(v, append(append([]string(nil), keyPath...), k), fn)
		}
	case []any:
		for i, v := range t {
			walkTree(v, append(append([]string(nil), keyPath...), strconv.Itoa(i)), fn)
		}
	}
}
