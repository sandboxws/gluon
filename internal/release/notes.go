package release

import (
	"embed"
	"sort"
	"strings"
	"sync"

	"github.com/BurntSushi/toml"
)

// The curated half: changes to the *language*, which no api file describes.
//
// Embedded TOML rather than Go literals, so a shipped note and any file a user
// might one day drop beside it go through exactly one decoder — the argument
// internal/theme/builtin.go makes about themes, and it holds for the same
// reason: a note only the literal path could express is one nobody could write,
// and the test that parses these would then be testing nothing anyone uses.
//
//go:embed notes/*.toml
var notesFS embed.FS

// The rule these files are written under: a note exists only where a compiling
// test can pin it. release_integration_test.go builds every snippet at its own
// `needs` directive and, where `below` is "error", checks it is rejected one
// minor below — so "new in 1.23" is a measurement rather than a recollection.
//
// A release nobody has verified therefore has no file, and Release.Curated is
// false rather than Lang being empty. Empty-means-none would have gluon report
// that Go 1.25 changed no syntax on the strength of nobody having looked, which
// is the distinction :doc -examples already draws between finding nothing and
// not having searched.

// A Note is one language change, in the words someone checked.
type Note struct {
	// Name is the -run selector and the stable identifier: "range-over-func".
	Name string `toml:"name"`
	// Title is the heading a reader sees.
	Title string `toml:"title"`
	// Text is the explanation.
	Text string `toml:"text"`
	// Snippet is Go the REPL can take line by line — declarations and
	// expressions, never a func main. It is fed through Core.submitAll, which
	// is the same path :load and :replay -run use.
	Snippet string `toml:"snippet"`
	// Needs is the go directive the snippet requires, "1.23". It is stated
	// rather than assumed equal to the release, so the test has something to
	// check rather than something to restate.
	Needs string `toml:"needs"`
	// Below is what a lower go directive does, and there are three answers.
	// "error" is a feature the type checker gates: the compiler rejects it.
	// "behaves differently" still builds and means something else — Go 1.22's
	// loop variables, and a note that could not say so would be describing the
	// safer half. "toolchain only" is a change no directive gates at all: it
	// builds under any go line and needs only a new enough toolchain, which is
	// what Go 1.26's self-referential constraints and Go 1.27's generalised
	// inference are. Each is checked a different way; see the integration tier.
	Below string `toml:"below"`
	// Issue is the proposal, and it is optional. The api files carry an issue
	// on every line since Go 1.19 and a language change has no such file — some
	// release notes link a proposal and some do not, and a number invented for
	// one that does not is a fabricated citation. Symbol.URL() returns "" for
	// the same reason.
	Issue int `toml:"issue"`
}

// Gated reports whether a directive below Needs is a compile error.
func (n Note) Gated() bool { return n.Below == errorBelow }

// ToolchainOnly reports a change no go directive gates: the toolchain decides
// alone. Needs then names the release that shipped it rather than a line
// anybody can put in a go.mod, and -run measures it against the toolchain.
func (n Note) ToolchainOnly() bool { return n.Below == toolchainOnly }

const (
	errorBelow    = "error"
	differsBelow  = "behaves differently"
	toolchainOnly = "toolchain only"
)

// BelowValues is the closed set, so the loader and the tests agree on it.
var BelowValues = []string{errorBelow, differsBelow, toolchainOnly}

// noteFile is one notes/go1.N.toml.
type noteFile struct {
	Version string `toml:"version"`
	Note    []Note `toml:"note"`
}

var curated = sync.OnceValue(func() map[string][]Note {
	out := map[string][]Note{}
	entries, err := notesFS.ReadDir("notes")
	if err != nil {
		return out
	}
	for _, e := range entries {
		data, err := notesFS.ReadFile("notes/" + e.Name())
		if err != nil {
			continue
		}
		f, err := decodeNotes(string(data))
		if err != nil {
			// Unreachable in a build that passed its tests:
			// TestNotesParseThroughTheLoader is what holds this.
			continue
		}
		out[f.Version] = f.Note
	}
	return out
})

// decodeNotes is the one decoder, shared by the embedded files and the test
// that reads them.
func decodeNotes(data string) (noteFile, error) {
	var f noteFile
	if _, err := toml.Decode(data, &f); err != nil {
		return noteFile{}, err
	}
	for i := range f.Note {
		f.Note[i].Text = strings.TrimSpace(f.Note[i].Text)
		f.Note[i].Snippet = strings.TrimSpace(f.Note[i].Snippet)
	}
	return f, nil
}

// notesFor is the curated notes for a version, and whether anyone wrote any.
func notesFor(v string) ([]Note, bool) {
	notes, ok := curated()[v]
	return notes, ok
}

// CuratedVersions is every release someone has written language notes for,
// newest first.
func CuratedVersions() []string {
	m := curated()
	out := make([]string, 0, len(m))
	for v := range m {
		out = append(out, v)
	}
	sort.Slice(out, func(i, j int) bool { return Compare(out[i], out[j]) > 0 })
	return out
}
