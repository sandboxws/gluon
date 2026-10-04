package scratch

// The debugger written for here is Delve, because on Go there is no second
// one: gdb cannot read goroutines, and every editor that debugs Go drives dlv
// underneath. The file is VS Code's launch.json, which is the format its forks
// read too and the one the JetBrains Go plugin imports.
//
// gluon neither links nor runs a debugger. It writes this file and names the
// command, which is also why the command is named: an editor whose integration
// does not pick the file up still leaves the developer a working path, so the
// configuration going stale against a future Delve release costs a convenience
// rather than the feature.

import (
	"encoding/json"
	"errors"
	"path/filepath"
)

// ErrDebugFlat refuses the two options that cannot both hold.
//
// The flat form is one //go:build ignore file with no go.mod, and a debugger
// has to build a package. resolveTarget already encodes the same asymmetry for
// running one — the flat form must be `go run <file>`, never `./dir`. Naming
// the conflict is what this codebase does everywhere two options are
// exclusive, rather than silently picking one.
var ErrDebugFlat = errors.New("a debugger has to build a module, and the flat //go:build ignore form has no go.mod — -debug cannot be combined with -flat")

// debugConfigDir is the directory the configuration goes in, relative to the
// scratch. It is always inside the scratch: invariants 1 and 25 both turn on
// gluon never writing into a product repo, and the scratch directory is under
// Root() even when -host nests the module *path* under a project.
const debugConfigDir = ".vscode"

// DebugConfigPath is where the launch configuration for the scratch in dir is.
func DebugConfigPath(dir string) string {
	return filepath.Join(dir, debugConfigDir, "launch.json")
}

// DebugCommand is the one command that starts a debug session from a terminal.
//
// The cd is part of it. `dlv debug <dir>` looks tidier and does not work from
// outside the module: cmd/go resolves a directory pattern against the module it
// is standing in, so naming a scratch from elsewhere fails with "outside main
// module or its dependencies".
func DebugCommand(dir string) string { return "cd " + dir + " && dlv debug" }

// launchFile is VS Code's launch.json. Written through structs rather than a
// map so the keys keep the order a human wrote them in.
type launchFile struct {
	Version        string            `json:"version"`
	Configurations []launchConfigure `json:"configurations"`
}

type launchConfigure struct {
	Name    string `json:"name"`
	Type    string `json:"type"`
	Request string `json:"request"`
	Mode    string `json:"mode"`
	Program string `json:"program"`
}

// debugConfig is the launch configuration for the scratch module at dir.
//
// The path is marshalled, not concatenated. It comes from Slug and Reserve and
// is not attacker-controlled, but it is the one field here that varies, and a
// home directory containing a quote would otherwise produce a file no editor
// can parse — a failure that would look like the feature being broken.
func debugConfig(dir string) ([]byte, error) {
	f := launchFile{
		Version: "0.2.0",
		Configurations: []launchConfigure{{
			// Named from the directory rather than the topic: the basename is
			// already the slug, so nothing new is interpolated.
			Name:    "Debug " + filepath.Base(dir),
			Type:    "go",
			Request: "launch",
			Mode:    "debug",
			Program: dir,
		}},
	}
	b, err := json.MarshalIndent(f, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(b, '\n'), nil
}
