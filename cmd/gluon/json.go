package main

import (
	"encoding/json"
	"os"

	"github.com/sandboxws/gluon/internal/config"
	"github.com/sandboxws/gluon/internal/pretty"
	"github.com/sandboxws/gluon/internal/repl"
)

// The -json envelopes. These are a contract: a justfile that reads them should
// not have to change because gluon's terminal output was reworded, so they are
// defined here rather than derived from whatever the human-readable path
// happens to print.
//
// Every envelope carries `ok`, meaning "the thing you asked for turned out the
// way it should". That is not the same as "the command ran": `doctor -json`
// prints its whole report, with `ok` false, when the config does not load.

// metaJSON is the -e -json envelope for a meta command — `gluon -e ':t x'
// -json` — one object per command, in execution order. A new envelope rather
// than new bytes in evalJSON: that one is a frozen surface, and this one
// starts its own add-only life.
type metaJSON struct {
	OK      bool   `json:"ok"`
	Command string `json:"command"`
	Text    string `json:"text,omitempty"`
	Error   string `json:"error,omitempty"`
}

// queryJSON is `gluon -e ':query -json <sql>' -json`, and the one meta command
// that answers with something other than metaJSON.
//
// A rendered table inside metaJSON's `text` is a string a script has to
// re-parse, and one that cannot say whether a cell was SQL NULL or the four
// letters NULL. This envelope says it: a null cell is JSON `null`, a text one
// is `"NULL"`. New rather than grown, for the reason metaJSON itself was new —
// `rows` on a frozen envelope would be add-only in letter and a second shape in
// practice, absent from every other command's answer.
//
// The type is repl's own because two drivers emit it: `gluon -e`, here, and the
// piped loop inside internal/repl, which cannot import a main package. One
// definition, one set of bytes.
type queryJSON = repl.QueryJSON

type evalJSON struct {
	OK bool `json:"ok"`
	// Stdout is what the evaluated program itself printed.
	Stdout string `json:"stdout,omitempty"`
	// Values are the expressions gluon auto-printed.
	Values []valueJSON `json:"values,omitempty"`
	// ExitCode is the program's, not gluon's: a session that panics reports
	// the panic here and still exits 1 overall.
	ExitCode int    `json:"exitCode"`
	Error    string `json:"error,omitempty"`
}

type valueJSON struct {
	Type string `json:"type"`
	Kind string `json:"kind"`
	// Repr is the scalar representation, empty for composites.
	Repr string `json:"repr,omitempty"`
	// Text is the full plain rendering — the same bytes `gluon -e` prints.
	Text string `json:"text"`
	Len  *int   `json:"len,omitempty"`
	Cap  *int   `json:"cap,omitempty"`
}

func newValueJSON(v pretty.Value) valueJSON {
	return valueJSON{
		Type: v.Type,
		Kind: v.Kind,
		Repr: v.Repr,
		Text: pretty.Plain([]pretty.Value{v}),
		Len:  v.Len,
		Cap:  v.Cap,
	}
}

// doctorJSON is `gluon doctor -json`. It carries every answer the text form
// prints, because the two are rendered from this one struct — a -json that
// reported less than the report would be the more annoying kind of wrong.
type doctorJSON struct {
	OK          bool   `json:"ok"`
	Version     string `json:"version"`
	Go          string `json:"go"`
	GoToolchain string `json:"goToolchain"`
	GoFlags     string `json:"goFlags"`
	// MaxItems and MaxDepth are GLUON_MAX_ITEMS and GLUON_MAX_DEPTH, reported
	// only when the environment sets them. They are how value.items and
	// value.depth reach the child, so one exported in a shell is in force for
	// every session — including one whose config.toml says otherwise, and that
	// is exactly the confusion doctor exists to end.
	//
	// omitempty: the field is add-only on a frozen envelope, and a reader that
	// has never seen it must see the same bytes it always did.
	MaxItems   string                `json:"maxItems,omitempty"`
	MaxDepth   string                `json:"maxDepth,omitempty"`
	Editor     string                `json:"editor"`
	Gatekeeper *doctorGatekeeperJSON `json:"gatekeeper,omitempty"`
	Config     *doctorConfigJSON     `json:"config"`
	Plugins    []doctorPluginJSON    `json:"plugins,omitempty"`
	Host       *doctorHostJSON       `json:"host"`
	Database   *doctorDatabaseJSON   `json:"database,omitempty"`
	// Scratch is where the scratchpads live and how many there are. omitempty
	// for the reason MaxItems is: the envelope is a frozen compatibility
	// surface, so a field is add-only and a reader that has never seen this one
	// must see the bytes it always did.
	Scratch *doctorScratchJSON `json:"scratch,omitempty"`

	// cfg is the parsed config, kept for the theme the text form renders with.
	cfg *config.Config
}

// doctorScratchJSON is the scratchpad tree: where it is, and how many named
// sessions are in it. Reported because a scratchpad is the one thing gluon
// writes that a user is expected to come back to, and "where did yesterday go"
// is a question with a filesystem answer.
type doctorScratchJSON struct {
	Root string `json:"root"`
	Pads int    `json:"pads"`
}

// doctorGatekeeperJSON is measured, never asked: Assessed comes from timing a
// freshly linked binary, because `spctl --status` reports the global setting
// and not whether this terminal is exempt through Developer Tools.
type doctorGatekeeperJSON struct {
	Status      string `json:"status"`
	FreshExecMS int64  `json:"freshExecMs,omitempty"`
	Assessed    bool   `json:"assessed"`
	Error       string `json:"error,omitempty"`
}

type doctorConfigJSON struct {
	Path    string              `json:"path"`
	Imports []string            `json:"imports,omitempty"`
	Timeout string              `json:"timeout,omitempty"`
	Editor  string              `json:"editor,omitempty"`
	Hosts   map[string][]string `json:"hosts,omitempty"`
	Theme   map[string]string   `json:"theme,omitempty"`
	// ThemeName is the palette [theme] name selects, and ThemeError is what
	// went wrong resolving it. Add-only: the envelope is a frozen surface, and
	// a reader that does not know these fields sees exactly what it saw before.
	ThemeName  string `json:"theme_name,omitempty"`
	ThemeError string `json:"theme_error,omitempty"`
	Error      string `json:"error,omitempty"`
}

type doctorHostJSON struct {
	Module      string          `json:"module,omitempty"`
	Dir         string          `json:"dir,omitempty"`
	GoDirective string          `json:"goDirective,omitempty"`
	SessionPath string          `json:"sessionPath,omitempty"`
	Importable  int             `json:"importable"`
	Packages    []doctorPkgJSON `json:"packages,omitempty"`
	Error       string          `json:"error,omitempty"`
	IndexError  string          `json:"indexError,omitempty"`
}

// doctorPluginJSON says which plugins apply here and why. "Why" is the part
// worth reporting: a command that is absent because its module is not in the
// build list looks exactly like a broken install unless something says which.
type doctorPluginJSON struct {
	Name    string `json:"name"`
	Module  string `json:"module,omitempty"`
	Active  bool   `json:"active"`
	Why     string `json:"why"`
	Summary string `json:"summary,omitempty"`
}

type doctorPkgJSON struct {
	Name string `json:"name"`
	Path string `json:"path"`
}

// emit writes one envelope and reports the exit code that matches it.
//
// A failure is still valid JSON on stdout: a caller piping into jq should not
// have to distinguish "gluon printed an object" from "gluon printed an error to
// stderr instead".
func emit(v any, ok bool) int {
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	if err := enc.Encode(v); err != nil {
		return 3
	}
	if ok {
		return 0
	}
	return 1
}
