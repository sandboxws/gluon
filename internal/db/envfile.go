package db

import (
	"bufio"
	"os"
	"strings"
)

// bom is the UTF-8 byte order mark, written as an escape because Go source
// may not contain one.
const bom = "\ufeff"

// Assignment is one KEY=VALUE from a .env file, with the line it was on.
//
// The line number is not decoration: :db reports ".env:3 DATABASE_URL" so
// somebody can open the file and see the thing gluon read, which is the
// difference between a report and an assertion.
type Assignment struct {
	Key   string
	Value string
	Line  int
}

// ReadEnvFile parses a .env file.
//
// There is no standard for this format, so the rules below are the ones every
// implementation agrees on, and each one is a case that appears in real files:
//
//   - `export KEY=value`, because .env files get sourced by shells too.
//   - Double quotes unescape \n, \t, \" and \; single quotes do not, which is
//     the whole reason a file would use one over the other.
//   - An unquoted value drops a trailing comment only when the # follows
//     whitespace. Without that rule PASSWORD=pa#ss silently becomes "pa".
//   - A leading BOM and trailing CRLF, because a file edited on Windows is
//     otherwise read as a key named "<BOM>DATABASE_URL" that matches nothing.
func ReadEnvFile(path string) ([]Assignment, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	var out []Assignment
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	line := 0
	for sc.Scan() {
		line++
		raw := strings.TrimPrefix(sc.Text(), bom)
		raw = strings.TrimRight(raw, "\r")
		t := strings.TrimSpace(raw)
		if t == "" || strings.HasPrefix(t, "#") {
			continue
		}
		t = strings.TrimPrefix(t, "export ")
		key, val, ok := strings.Cut(t, "=")
		if !ok {
			continue
		}
		key = strings.TrimSpace(key)
		if !validEnvKey(key) {
			continue
		}
		out = append(out, Assignment{Key: key, Value: unquoteEnv(strings.TrimSpace(val)), Line: line})
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

// validEnvKey keeps a shell script that happens to be named .env from being
// read as assignments. `if [ -z "$X" ]; then` contains an =, and a key that is
// not an identifier is the cheapest way to notice.
func validEnvKey(k string) bool {
	if k == "" {
		return false
	}
	for i, r := range k {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r == '_':
		case r >= '0' && r <= '9' && i > 0:
		default:
			return false
		}
	}
	return true
}

func unquoteEnv(v string) string {
	if len(v) >= 2 {
		if v[0] == '"' && v[len(v)-1] == '"' {
			body := v[1 : len(v)-1]
			r := strings.NewReplacer(`\n`, "\n", `\t`, "\t", `\"`, `"`, `\\`, `\`)
			return r.Replace(body)
		}
		if v[0] == '\'' && v[len(v)-1] == '\'' {
			return v[1 : len(v)-1]
		}
	}
	// Unquoted: a # ends the value only when whitespace precedes it, so
	// PASSWORD=pa#ss keeps its hash.
	if i := strings.Index(v, " #"); i >= 0 {
		v = v[:i]
	} else if i := strings.Index(v, "\t#"); i >= 0 {
		v = v[:i]
	}
	return strings.TrimSpace(v)
}

// ExpandEnv resolves ${VAR} and $VAR against the process environment and the
// assignments already read from the same file.
//
// postgres://${DB_USER}:${DB_PASS}@localhost/app is everywhere, and a detector
// that reported that string verbatim would be reporting a template rather than
// a database. Unresolved names are returned so the caller can say which ones
// are missing instead of silently producing a broken DSN.
func ExpandEnv(v string, local map[string]string) (string, []string) {
	var missing []string
	out := os.Expand(v, func(name string) string {
		if val, ok := local[name]; ok && val != "" {
			return val
		}
		if val, ok := os.LookupEnv(name); ok && val != "" {
			return val
		}
		missing = append(missing, name)
		return ""
	})
	return out, missing
}

// LookupEnvFile reads one key out of a .env file.
func LookupEnvFile(path, key string) (string, bool) {
	items, err := ReadEnvFile(path)
	if err != nil {
		return "", false
	}
	local := map[string]string{}
	for _, a := range items {
		local[a.Key] = a.Value
	}
	for _, a := range items {
		if a.Key == key {
			v, _ := ExpandEnv(a.Value, local)
			return v, true
		}
	}
	return "", false
}
