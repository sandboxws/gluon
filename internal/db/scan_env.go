package db

import (
	"path/filepath"
	"strings"

	"github.com/sandboxws/gluon/internal/dsn"
)

// scanEnvFile reads a .env and keeps the values that are connection strings.
//
// The key's name is not evidence. DATABASE_URL=https://api.example.com is a
// URL, and FOO_BAR=postgres://… is a database — invariant 23, which is
// invariant 12 applied to a value rather than a directory.
func scanEnvFile(path string, depth int) []Candidate {
	items, err := ReadEnvFile(path)
	if err != nil {
		return nil
	}
	// An example file's values are placeholders. Reporting
	// postgres://user:password@localhost/dbname as the project's database would
	// be confidently wrong, so the file contributes the variable *name* — which
	// is the useful half — and never a value.
	example := strings.HasSuffix(path, ".example")

	local := map[string]string{}
	for _, a := range items {
		local[a.Key] = a.Value
	}

	var out []Candidate
	for _, a := range items {
		if a.Value == "" {
			continue
		}
		value, missing := ExpandEnv(a.Value, local)
		if value == "" {
			continue
		}
		parsed, err := dsn.Parse(value, filepath.Dir(path))
		if err != nil {
			continue
		}
		if example {
			continue
		}
		c := Candidate{
			DSN:    parsed,
			Rank:   RankApp,
			Depth:  depth,
			Secret: SecretRef{Env: a.Key, File: path, Key: a.Key},
			From: []Provenance{{
				File: path, Line: a.Line, Key: a.Key, Kind: KindEnvFile,
			}},
		}
		if len(missing) > 0 {
			// A DSN assembled from variables that are not set is a template,
			// and saying which names are missing is the difference between a
			// fixable report and a connection that fails later for no visible
			// reason.
			c.Concerns = append(c.Concerns,
				"unset: $"+strings.Join(missing, ", $"))
		}
		out = append(out, c)
	}
	return out
}

// ExampleKeys is the variable names an example file documents.
//
// It is the one genuinely useful thing such a file holds: it says what the
// project expects to be set, which is exactly what the wizard should offer when
// nothing is set yet.
func ExampleKeys(root string) []string {
	var out []string
	for _, name := range []string{".env.example", ".env.sample", ".env.template"} {
		items, err := ReadEnvFile(filepath.Join(root, name))
		if err != nil {
			continue
		}
		for _, a := range items {
			if _, err := dsn.Parse(a.Value, root); err == nil {
				out = append(out, a.Key)
				continue
			}
			// A placeholder that does not parse still names a variable, and an
			// obviously database-shaped name is worth offering.
			if looksLikeDatabaseName(a.Key) {
				out = append(out, a.Key)
			}
		}
	}
	return out
}

// looksLikeDatabaseName is used only to offer a name in the wizard, never to
// decide that a value is a database. That distinction is the whole of invariant
// 23: a name may rank or suggest, and may never qualify.
func looksLikeDatabaseName(k string) bool {
	u := strings.ToUpper(k)
	for _, hint := range []string{"DATABASE", "DB_", "POSTGRES", "MYSQL", "SQLITE", "PG"} {
		if strings.Contains(u, hint) {
			return true
		}
	}
	return false
}
