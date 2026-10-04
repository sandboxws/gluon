package db

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// ConfEntry is one leaf of a configuration file: the dotted path that reaches
// it, and its value as written.
//
// Dotted rather than nested because a value's full path has to be readable on
// one line — `database.primary.url` says where it is, where an indented tree
// makes the reader carry the parent names in their head.
type ConfEntry struct {
	Key   string
	Value string
}

// ConfFormats is what ReadConf reads, for a message that has to say so.
var ConfFormats = []string{".toml", ".yaml", ".yml", ".json"}

// ErrConfFormat is an extension ReadConf does not read. It is a distinct error
// because "gluon cannot read .ini" and "this .yaml is broken" are different
// answers and only one of them is worth editing the file over.
type ErrConfFormat struct{ Ext string }

func (e *ErrConfFormat) Error() string {
	ext := e.Ext
	if ext == "" {
		ext = "a file with no extension"
	}
	return fmt.Sprintf("%s is not a format gluon reads — it reads %s",
		ext, strings.Join(ConfFormats, ", "))
}

// ReadConf reads a TOML, YAML or JSON file into its leaves.
//
// It decodes through the same three functions detection uses, so a file :db
// read and a file :conf shows are read by one parser rather than two that can
// disagree. A parse failure returns no entries at all: a configuration shown as
// half read is worse than one reported as unread, because the half that is
// missing is invisible.
func ReadConf(path string) ([]ConfEntry, error) {
	var decode func([]byte) (any, error)
	switch ext := strings.ToLower(filepath.Ext(path)); ext {
	case ".yaml", ".yml":
		decode = yamlTree
	case ".toml":
		decode = tomlTree
	case ".json":
		decode = jsonTree
	default:
		return nil, &ErrConfFormat{Ext: ext}
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	tree, err := decode(b)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", filepath.Base(path), confPosition(err, b))
	}

	var out []ConfEntry
	walkTree(tree, nil, func(keyPath []string, node any) {
		if len(keyPath) == 0 {
			return
		}
		switch node.(type) {
		case map[string]any, []any:
			return // a branch; its leaves arrive on their own
		}
		out = append(out, ConfEntry{
			Key:   strings.Join(keyPath, "."),
			Value: leafString(node),
		})
	})
	sort.Slice(out, func(i, j int) bool { return out[i].Key < out[j].Key })
	return out, nil
}

// leafString renders a scalar the way the file wrote it, near enough.
//
// A nil is spelled the way each format spells it rather than as Go's "<nil>",
// which would be the renderer talking about itself instead of the file.
func leafString(v any) string {
	if v == nil {
		return "null"
	}
	if s, ok := v.(string); ok {
		return s
	}
	return fmt.Sprint(v)
}

// confPosition adds a line and column to a parse failure that only knows a byte
// offset.
//
// encoding/json reports one; the TOML and YAML decoders already say "line N" in
// their own words and are left alone. Without this, a JSON error reads "invalid
// character '}'" with no way to find which one.
func confPosition(err error, src []byte) error {
	var syn *json.SyntaxError
	if !errors.As(err, &syn) {
		return err
	}
	line, col := 1, 1
	for i := int64(0); i < syn.Offset-1 && int(i) < len(src); i++ {
		if src[i] == '\n' {
			line, col = line+1, 1
			continue
		}
		col++
	}
	return fmt.Errorf("line %d, column %d: %w", line, col, err)
}
