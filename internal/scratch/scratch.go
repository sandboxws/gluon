// Package scratch owns the scratch tree: where a throwaway program lives, what
// it is scaffolded with, and how it is run.
//
// The shape is one directory per scratch with its own go.mod, not a flat file
// carrying //go:build ignore. The ignore trick does work — cmd/go sets
// UseAllFiles in GoFilesPackage, and $GOROOT/src/cmd/go/internal/test/genflags.go
// is exactly that: an ignore-tagged package main sitting beside a different
// package, run with `go run ./genflags.go`. But gopls greys the file out
// entirely: no completion, no hover, no jump-to-definition. That is precisely
// what someone practising needs most, so it decides the design. The flat form
// stays available as a fallback, and it works only with the file-list form
// (`go run path/file.go`), never `go run ./dir`.
package scratch

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/sandboxws/gluon/internal/host"
	"github.com/sandboxws/gluon/internal/render"
)

// Root follows XDG: scratch files are data, not config or state.
func Root() string {
	if d := os.Getenv("XDG_DATA_HOME"); d != "" {
		return filepath.Join(d, "gluon", "scratch")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return filepath.Join(os.TempDir(), "gluon", "scratch")
	}
	return filepath.Join(home, ".local", "share", "gluon", "scratch")
}

var nonSlug = regexp.MustCompile(`[^a-z0-9]+`)

// Slug normalises a topic into a directory-safe name.
func Slug(s string) string {
	s = nonSlug.ReplaceAllString(strings.ToLower(s), "-")
	s = strings.Trim(s, "-")
	if s == "" {
		return "session"
	}
	return s
}

// Reserve returns a free path under Root for a scratch named topic, dated
// today, and creates nothing.
func Reserve(topic string) string {
	base := filepath.Join(Root(), fmt.Sprintf("%s-%s", time.Now().Format("2006-01-02"), Slug(topic)))
	dir := base
	for i := 2; exists(dir); i++ {
		dir = fmt.Sprintf("%s-%d", base, i)
	}
	return dir
}

func exists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

// Options controls what New scaffolds.
type Options struct {
	// Topic names the scratch; empty becomes "scratch".
	Topic string
	// Flat writes one //go:build ignore file instead of a directory with its
	// own module. It costs every gopls feature in that file, so it is not the
	// default.
	Flat bool
	// Host attaches the scratch module to a project, so it can import that
	// project's packages including the ones under internal/.
	Host *host.Host
	// Body is the main.go source; empty uses the starter.
	Body string
	// Runtime writes the gluonrt files beside main.go, for a scratch grown from a
	// session whose code calls the injected printer.
	Runtime bool
	// Debug writes a debugger launch configuration beside the module, so the
	// directory opens in a debugger without one being written by hand. It adds
	// a file and changes nothing about the program.
	Debug bool
	// Tests is a test file written beside main.go, for the tests :test
	// generated during a session. Like Debug it adds a file and changes
	// nothing about the program — main.go is byte-identical either way, which
	// is what keeps `:save -test` from being a different save.
	Tests string
}

// TestName is the file the generated tests are written as, and TestPath is
// where it lands. Named here rather than at the caller so :save can report the
// path it did not choose.
const TestName = "main_test.go"

func TestPath(dir string) string { return filepath.Join(dir, TestName) }

// New scaffolds a scratch and returns the path to open: the main.go inside a
// module directory, or the flat file itself.
func New(opts Options) (string, error) {
	// Before the first write, so a refused combination leaves nothing behind.
	// The command line refuses this too, with its own exit status; this is the
	// one that holds for every caller of New.
	if opts.Debug && opts.Flat {
		return "", ErrDebugFlat
	}

	topic := opts.Topic
	if topic == "" {
		topic = "scratch"
	}
	body := opts.Body
	if body == "" {
		body = starter(topic)
	}

	if opts.Flat {
		if err := os.MkdirAll(Root(), 0o755); err != nil {
			return "", err
		}
		base := filepath.Join(Root(), fmt.Sprintf("%s-%s", time.Now().Format("2006-01-02"), Slug(topic)))
		p := base + ".go"
		for i := 2; exists(p); i++ {
			p = fmt.Sprintf("%s-%d.go", base, i)
		}
		// The constraint has to precede the package clause, with a blank line
		// between, or it is a plain comment and the file joins the package.
		if err := os.WriteFile(p, []byte("//go:build ignore\n\n"+body), 0o644); err != nil {
			return "", err
		}
		return p, nil
	}

	return Write(Reserve(topic), opts)
}

// Write scaffolds a module into dir, which the caller has chosen.
//
// It is New's second half, split out for :save inside a scratchpad: that saves
// into the pad's own directory rather than a dated one, so the module a
// debugger or gopls opens is the work in progress. Splitting rather than
// copying keeps one writer — a second scaffolder would be a second answer to
// what a saved session contains, and only one of them would be the one tests
// look at.
//
// It overwrites what is in dir, which is why New reaches it only through
// Reserve: a path that does not exist yet cannot be overwritten, and
// TestNewNeverOverwrites is what pins that route.
func Write(dir string, opts Options) (string, error) {
	if opts.Debug && opts.Flat {
		return "", ErrDebugFlat
	}
	topic := opts.Topic
	if topic == "" {
		topic = "scratch"
	}
	body := opts.Body
	if body == "" {
		body = starter(topic)
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	mod, err := Mod(topic, opts.Host)
	if err != nil {
		return "", err
	}
	files := map[string]string{"go.mod": mod, "main.go": body}
	if opts.Runtime {
		// A program that calls the injected printer needs it to travel, or the
		// saved module does not build.
		for name, src := range render.RuntimeFiles() {
			files[name] = src
		}
	}
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
			return "", err
		}
	}
	if opts.Debug {
		if err := writeDebugConfig(dir); err != nil {
			return "", err
		}
	}
	if opts.Tests != "" {
		if err := writeTests(dir, opts.Tests); err != nil {
			return "", err
		}
	}
	return filepath.Join(dir, "main.go"), nil
}

// writeTests puts the generated tests beside main.go, with their imports
// resolved in the module that now exists around them.
//
// goimports rather than a computed import block: a generated test carries the
// expression the user typed, so it needs whatever that line needed — strings,
// time, a package from the attached host — and the session's own import block
// is the wrong answer because an import a test file does not use is a compile
// error. Resolution is done here because it needs the file's real path inside
// the module, which is only true once the module has been written.
//
// A failure leaves the unresolved source in place rather than nothing: the
// tests are still the thing worth keeping, and a missing import line is a
// one-line fix where a missing file is not.
func writeTests(dir, src string) error {
	path := TestPath(dir)
	if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
		return err
	}
	fixed, err := render.FixImports(path, []byte(src))
	if err != nil {
		return nil
	}
	return os.WriteFile(path, fixed, 0o644)
}

// writeDebugConfig puts the launch configuration inside the scratch, and only
// there. Every path it touches is built from dir, which Reserve produced under
// Root, so a hosted scratch cannot reach the project it is nested under.
func writeDebugConfig(dir string) error {
	cfg, err := debugConfig(dir)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Join(dir, debugConfigDir), 0o755); err != nil {
		return err
	}
	return os.WriteFile(DebugConfigPath(dir), cfg, 0o644)
}

// Mod is the go.mod for a scratch, nested under h when one is given so the
// scratch can import the host's internal packages the same way a session can.
func Mod(topic string, h *host.Host) (string, error) {
	if h == nil {
		return fmt.Sprintf("module gluon.local/scratch/%s\n\ngo %s\n", Slug(topic), GoMinor()), nil
	}
	return h.ModFor("scratch/" + Slug(topic))
}

func starter(topic string) string {
	return fmt.Sprintf(`package main

import "fmt"

// %s
func main() {
	fmt.Println("hello")
}
`, topic)
}

// List returns every scratch under Root, newest first.
//
// Ordering is by modification time, not by name. The names carry a date but no
// clock, so three scratches made on one afternoon sort alphabetically — which
// would make `gluon run` with no argument reach for whichever of them happens
// to sort last rather than the one just edited. Modification time is also what
// keeps a scratch at the top after it has been worked on, which is what "most
// recent" means to someone iterating on it.
func List() []string {
	ents, err := os.ReadDir(Root())
	if err != nil {
		return nil
	}
	type entry struct {
		path string
		mod  time.Time
	}
	var found []entry
	for _, e := range ents {
		n := e.Name()
		if !e.IsDir() && !strings.HasSuffix(n, ".go") {
			continue
		}
		p := filepath.Join(Root(), n)
		// A scratchpad that has never been saved holds a session and no
		// program, and this list exists to answer what there is to run —
		// resolveTarget cannot run one. It joins the moment :save gives it a
		// main.go, like any other scratch.
		if e.IsDir() && IsPad(p) && !exists(filepath.Join(p, "main.go")) {
			continue
		}
		mod := newest(p, e)
		found = append(found, entry{p, mod})
	}
	sort.SliceStable(found, func(i, j int) bool { return found[i].mod.After(found[j].mod) })
	out := make([]string, len(found))
	for i, f := range found {
		out[i] = f.path
	}
	return out
}

// newest is the latest modification time in a scratch. A directory's own mtime
// only changes when an entry is added or removed, so editing main.go in place
// would otherwise leave the scratch looking untouched.
func newest(p string, e os.DirEntry) time.Time {
	fi, err := e.Info()
	if err != nil {
		return time.Time{}
	}
	latest := fi.ModTime()
	if !e.IsDir() {
		return latest
	}
	if IsPad(p) {
		// A pad's own mtime is the sidecar's, not the program's: the session
		// file is written through temp-then-rename on every line typed into it,
		// and creating and removing that temp entry moves the directory's
		// timestamp. So a pad is dated by the program files inside it and
		// nothing else — zero when there are none, which is a pad List already
		// skips.
		latest = time.Time{}
	}
	inner, err := os.ReadDir(p)
	if err != nil {
		return latest
	}
	for _, in := range inner {
		// The sidecar is not part of the program. A scratchpad is written on
		// every line somebody types into it, so counting its session file here
		// would park it at the top of List permanently — and `gluon run` with
		// no argument, which takes Latest, would stop meaning "the scratch you
		// were last editing". That property is the whole argument this function
		// exists to make.
		if padFiles[in.Name()] {
			continue
		}
		ifi, err := in.Info()
		if err != nil {
			continue
		}
		if ifi.ModTime().After(latest) {
			latest = ifi.ModTime()
		}
	}
	return latest
}

// Latest is the most recent scratch, for `gluon run` with no argument.
func Latest() (string, error) {
	all := List()
	if len(all) == 0 {
		return "", fmt.Errorf("no scratches under %s — try `gluon new`", Root())
	}
	return all[0], nil
}
