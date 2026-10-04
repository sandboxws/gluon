package db

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/BurntSushi/toml"
	"go.yaml.in/yaml/v3"

	"github.com/sandboxws/gluon/internal/config"
	"github.com/sandboxws/gluon/internal/dsn"
)

// Resolve turns a configured entry into a connection string, reading the secret
// at the last possible moment.
//
// This is where "gluon never writes a password" becomes true rather than
// aspirational: a config holds the *name* of a variable or a key in a file, and
// the value is fetched here, held in one string, and handed to one evaluation.
// It is never stored on the Evaluator, never in a Result, never in a config,
// and never in history.
func Resolve(d config.Database) (dsn.DSN, error) {
	switch {
	case d.DSNEnv != "":
		v, ok := os.LookupEnv(d.DSNEnv)
		if !ok || v == "" {
			// Falling back to the project's own .env is what makes dsn_env
			// work in a shell that has not sourced anything, which is the
			// common case at a REPL.
			if found, from, ok := lookupInEnvFiles(d.Dir, d.DSNEnv); ok {
				parsed, err := dsn.Parse(found, d.Dir)
				if err != nil {
					return dsn.DSN{}, fmt.Errorf("%s in %s: %w", d.DSNEnv, from, err)
				}
				return parsed, nil
			}
			return dsn.DSN{}, fmt.Errorf("$%s is not set, and no .env under %s defines it",
				d.DSNEnv, shortDir(d.Dir))
		}
		return dsn.Parse(v, d.Dir)

	case d.DSNFile != "":
		v, err := valueAt(d.DSNFile, d.DSNKey)
		if err != nil {
			return dsn.DSN{}, err
		}
		return dsn.Parse(v, filepath.Dir(d.DSNFile))

	case d.DSN != "":
		return dsn.Parse(d.DSN, d.Dir)

	case d.File != "":
		return dsn.FromFile(d.File, nil), nil

	default:
		pw, err := resolvePassword(d)
		if err != nil {
			return dsn.DSN{}, err
		}
		return dsn.FromFields(d.Driver, d.Host, d.Port, d.User, d.DBName, pw, d.Params), nil
	}
}

func resolvePassword(d config.Database) (string, error) {
	if d.PassEnv != "" {
		if v, ok := os.LookupEnv(d.PassEnv); ok && v != "" {
			return v, nil
		}
		if v, _, ok := lookupInEnvFiles(d.Dir, d.PassEnv); ok {
			return v, nil
		}
		return "", fmt.Errorf("$%s is not set, and no .env under %s defines it",
			d.PassEnv, shortDir(d.Dir))
	}
	if d.PassFile != "" {
		b, err := os.ReadFile(d.PassFile)
		if err != nil {
			return "", fmt.Errorf("password_file: %w", err)
		}
		return strings.TrimSpace(string(b)), nil
	}
	// No password is a real answer: trust auth, a unix socket, and sqlite all
	// connect without one.
	return "", nil
}

// envFileNames are the files a project keeps its environment in, nearest-first
// in the order a running app would read them.
var envFileNames = []string{".env.local", ".env.development", ".env.dev", ".env"}

func lookupInEnvFiles(dir, key string) (value, from string, ok bool) {
	if dir == "" {
		return "", "", false
	}
	for _, name := range envFileNames {
		p := filepath.Join(dir, name)
		if v, found := LookupEnvFile(p, key); found && v != "" {
			return v, name, true
		}
	}
	return "", "", false
}

// valueAt reads one value out of a .env, YAML, TOML or JSON file.
//
// The key is dotted for the structured formats — database.url — because that is
// how the file is written and how somebody would describe where the value is.
func valueAt(path, key string) (string, error) {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".yaml", ".yml":
		return valueInTree(path, key, yamlTree)
	case ".toml":
		return valueInTree(path, key, tomlTree)
	case ".json":
		return valueInTree(path, key, jsonTree)
	default:
		v, ok := LookupEnvFile(path, key)
		if !ok {
			return "", fmt.Errorf("%s does not define %s", path, key)
		}
		return v, nil
	}
}

// readTree decodes a structured config file into one normalized shape.
func readTree(path string, decode func([]byte) (any, error)) (any, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	tree, err := decode(b)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return tree, nil
}

func valueInTree(path, key string, decode func([]byte) (any, error)) (string, error) {
	tree, err := readTree(path, decode)
	if err != nil {
		return "", err
	}
	cur := tree
	for _, part := range strings.Split(key, ".") {
		m, ok := cur.(map[string]any)
		if !ok {
			return "", fmt.Errorf("%s: %s is not a path into this file", path, key)
		}
		cur, ok = m[part]
		if !ok {
			return "", fmt.Errorf("%s does not define %s", path, key)
		}
	}
	s, ok := cur.(string)
	if !ok {
		return "", fmt.Errorf("%s: %s is not a string", path, key)
	}
	return s, nil
}

func yamlTree(b []byte) (any, error) {
	var v any
	if err := yaml.Unmarshal(b, &v); err != nil {
		return nil, err
	}
	return normalize(v), nil
}

func tomlTree(b []byte) (any, error) {
	var v map[string]any
	if err := toml.Unmarshal(b, &v); err != nil {
		return nil, err
	}
	return normalize(v), nil
}

func jsonTree(b []byte) (any, error) {
	var v any
	if err := json.Unmarshal(b, &v); err != nil {
		return nil, err
	}
	return normalize(v), nil
}

// normalize turns every mapping into map[string]any.
//
// yaml.v3 decodes a mapping into map[string]any already, but a nested one under
// an `any` can arrive as map[any]any from older documents, and JSON numbers
// arrive as float64. Walking once here means the tree readers below can assume
// one shape.
func normalize(v any) any {
	switch t := v.(type) {
	case map[string]any:
		out := make(map[string]any, len(t))
		for k, val := range t {
			out[k] = normalize(val)
		}
		return out
	case map[any]any:
		out := make(map[string]any, len(t))
		for k, val := range t {
			out[fmt.Sprint(k)] = normalize(val)
		}
		return out
	case []any:
		out := make([]any, len(t))
		for i, val := range t {
			out[i] = normalize(val)
		}
		return out
	default:
		return v
	}
}

func shortDir(d string) string {
	if home, err := os.UserHomeDir(); err == nil && home != "" && strings.HasPrefix(d, home) {
		return "~" + strings.TrimPrefix(d, home)
	}
	return d
}
