package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/BurntSushi/toml"

	"github.com/sandboxws/gluon/internal/find"
)

// ProjectFile is the config a project may carry in its own tree.
const ProjectFile = "gluon.toml"

// Project is a gluon.toml sitting in a project rather than in ~/.config.
//
// It deliberately understands almost nothing. A gluon.toml arrives with `git
// clone`, from whoever wrote the repository — so it may say where a database
// is, and it may preload imports, and that is all. `editor` would run an
// arbitrary command on someone else's machine, which is not a thing a
// downloaded file should decide, and refusing it with that reason stated is
// better than a schema that quietly never had it.
type Project struct {
	Imports   []string   `toml:"imports"`
	Databases []Database `toml:"database"`

	// The rest exist only to be refused, so the error can say why rather than
	// "unknown setting editor".
	Editor  string         `toml:"editor"`
	Timeout string         `toml:"timeout"`
	Theme   map[string]any `toml:"theme"`
	Hosts   map[string]any `toml:"hosts"`
	Plugins *Plugins       `toml:"plugins"`

	// Path is the file this was read from.
	Path string `toml:"-"`
}

// refusedInProject maps a setting a project file may not carry to the reason.
var refusedInProject = []struct {
	name, why string
	set       func(*Project) bool
}{
	{"editor", "it would run a command chosen by whoever wrote the repository",
		func(p *Project) bool { return p.Editor != "" }},
	{"theme", "colour is yours, not the project's",
		func(p *Project) bool { return len(p.Theme) > 0 }},
	{"hosts", "a project file already applies to one project",
		func(p *Project) bool { return len(p.Hosts) > 0 }},
	{"plugins", "which plugins run is a property of your gluon, not of a checkout",
		func(p *Project) bool { return p.Plugins != nil }},
	{"timeout", "how long your REPL waits is yours to set",
		func(p *Project) bool { return p.Timeout != "" }},
}

// FindProject locates the nearest gluon.toml at or above dir, stopping at the
// repository root.
//
// The ceiling is invariant 12 again, and it matters more here than it does for
// a search: a gluon.toml found above the repository belongs to some other
// project, and following it would point a session at a database that has
// nothing to do with the code in front of it.
func FindProject(dir string) (string, bool) {
	root, ok := find.Repo(dir)
	if !ok {
		// No repository means no upward walk. Only the directory itself.
		p := filepath.Join(dir, ProjectFile)
		if _, err := os.Stat(p); err == nil {
			return p, true
		}
		return "", false
	}
	for _, d := range find.Ancestors(dir, root) {
		p := filepath.Join(d, ProjectFile)
		if _, err := os.Stat(p); err == nil {
			return p, true
		}
	}
	return "", false
}

// LoadProject reads a gluon.toml. A missing file is not an error; a malformed
// one is, and so is one that sets something a project file may not set.
func LoadProject(path string) (*Project, error) {
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var p Project
	md, err := toml.Decode(string(data), &p)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	// The named refusals come first. A setting this file may not carry has a
	// specific reason, and reporting it as merely "unknown" would send someone
	// looking for a typo in a key they spelled correctly.
	for _, r := range refusedInProject {
		if r.set(&p) {
			return nil, fmt.Errorf("%s: %s is not allowed in %s — %s",
				path, r.name, ProjectFile, r.why)
		}
	}
	if und := md.Undecoded(); len(und) > 0 {
		keys := make([]string, len(und))
		for i, k := range und {
			keys[i] = k.String()
		}
		return nil, fmt.Errorf("%s: unknown setting %s", path, strings.Join(keys, ", "))
	}
	p.Path = path
	dir := filepath.Dir(path)
	for i := range p.Databases {
		p.Databases[i].Dir = dir
		p.Databases[i].From = path
		p.Databases[i].DSNFile = expandPath(p.Databases[i].DSNFile)
		p.Databases[i].File = expandPath(p.Databases[i].File)
		p.Databases[i].PassFile = expandPath(p.Databases[i].PassFile)
		p.Databases[i] = p.Databases[i].resolvePaths()
	}
	if err := validateDatabases(p.Databases); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return &p, nil
}
