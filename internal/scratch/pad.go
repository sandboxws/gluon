package scratch

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/sandboxws/gluon/internal/config"
	"github.com/sandboxws/gluon/internal/session"
)

// A scratchpad is a named session on disk: one directory under Root holding the
// entries as they were typed, plus what it takes to put the session's
// surroundings back.
//
// The three files below are the sidecar, and none of them is a .go file. The
// directory becomes a Go module the moment :save runs there, and a second
// `package main` in it would break `gluon run <name>`, gopls and dlv — the
// three things saving in place exists to buy. So the sidecar carries an
// extension the toolchain ignores.
const (
	// PadName holds the header and the entry stream.
	PadName = "session.gluon"
	// PadSumName is the go.sum the session's requirements were resolved
	// against, kept beside the pad rather than inside it: it is many lines of
	// somebody else's format, and a reader that had to know where it ended
	// would be a second parser.
	PadSumName = "session.sum"
	// PadPrevName is what a shrinking write leaves behind. :reset in a pad is
	// the case that matters — one command became destructive across restarts,
	// and one file answers it.
	PadPrevName = "session.prev"
)

// padFiles is the sidecar, for the readers that must not mistake it for the
// program: newest ignores these when deciding how recently a scratch was
// worked on, so typing into a pad does not push it to the top of List.
var padFiles = map[string]bool{PadName: true, PadSumName: true, PadPrevName: true}

// PadVersion is the format this gluon writes and the highest it can read.
//
// A newer file is refused rather than read as best it can: a directive this
// reader does not understand is a decision the session made and this process
// would drop, and the file is then rewritten without it. Refusing costs the
// user one message; misreading costs them the pins.
const PadVersion = 1

// The header directives, which precede the entry stream. They share
// session.DirectivePrefix so the whole file is one comment syntax, and they are
// consumed by ReadPad before the stream reaches session.Unmarshal.
const (
	padDirective     = session.DirectivePrefix + "pad"
	goDirective      = session.DirectivePrefix + "go"
	hostDirective    = session.DirectivePrefix + "host"
	requireDirective = session.DirectivePrefix + "require"
)

// A Pad is one scratchpad: its entries, and enough about the session's
// surroundings to put them back.
//
// Sum is carried beside Requires rather than derived from it because a module
// in the local cache is not the same thing as a module this session is allowed
// to build against: the sums are what the toolchain verifies, and a restore
// that re-derived them would be fetching.
type Pad struct {
	// Name is the slug, and Dir is where it lives.
	Name string
	Dir  string
	// Go is the toolchain that last wrote this pad, reported when it differs
	// from the one now running — a likely cause of a replay that used to work.
	Go string
	// Host is the directory of the module the session was attached to, empty
	// for a standalone session. A directory rather than a module path, because
	// re-attaching means reading a go.mod and only a path on disk can do that.
	Host string
	// Requires are the "path version" lines the session acquired through :get.
	Requires []string
	// Sum is the recorded go.sum, verbatim.
	Sum []byte
	// Sess is the entry stream.
	Sess *session.Session
}

// PadInfo is one row of the listing: what can be said about a pad without
// replaying it.
type PadInfo struct {
	Name    string
	Dir     string
	Entries int
	Pinned  int
	Host    string
	// Program reports that :save has been run here, so the pad is also a
	// runnable scratch.
	Program bool
	Mod     time.Time
}

// PadDir is where a pad by that name lives. It does not create anything.
func PadDir(name string) string { return filepath.Join(Root(), name) }

// IsPad reports whether dir holds a scratchpad, which is the one question that
// distinguishes it from every other directory in the tree.
func IsPad(dir string) bool {
	fi, err := os.Stat(filepath.Join(dir, PadName))
	return err == nil && !fi.IsDir()
}

// datedName is the shape Reserve writes: a scratch made on a day, which the
// tree already means as "throwaway".
var datedName = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}-`)

// ErrDatedPad is what ValidPadName answers for a name in that shape.
var ErrDatedPad = errors.New("a name beginning with a date is what `gluon new` writes for a throwaway scratch — pick another")

// ValidPadName slugs a name and refuses the ones that would collide with what
// the tree already writes.
//
// The slug is returned as well as the error, because the answer has to name the
// pad it actually opened: `Parser Bug` and `parser-bug` are one scratchpad, and
// a message that echoed what was typed would suggest they are two.
func ValidPadName(name string) (string, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return "", errors.New("a scratchpad needs a name")
	}
	slug := Slug(name)
	if datedName.MatchString(slug) {
		return slug, ErrDatedPad
	}
	return slug, nil
}

// ErrNotAPad is what ReadPad answers when the directory is there and holds a
// program rather than a session. Distinct from "no such pad" because the two
// are answered differently: one is created, the other must not be.
var ErrNotAPad = errors.New("that name is a saved scratch, not a scratchpad")

// ErrNewerPad is what ReadPad answers for a file this gluon cannot read. It
// carries the version so the message can name it.
type ErrNewerPad struct{ Version int }

func (e *ErrNewerPad) Error() string {
	return fmt.Sprintf("this scratchpad was written by a newer gluon (format %d, this one reads %d)",
		e.Version, PadVersion)
}

// ReadPad reads the pad named name. A directory that does not exist is not an
// error: a pad exists once somebody names it, and the empty one is what the
// first `gluon` lands on.
func ReadPad(name string) (*Pad, error) {
	slug, err := ValidPadName(name)
	if err != nil {
		return nil, err
	}
	dir := PadDir(slug)
	p := &Pad{Name: slug, Dir: dir, Sess: &session.Session{}}

	data, err := os.ReadFile(filepath.Join(dir, PadName))
	if err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			return nil, err
		}
		// A directory holding a program and no session file is a scratch
		// somebody saved, and converting it into a pad would put a second
		// writer in a directory that already has one.
		if exists(dir) {
			if _, serr := os.Stat(filepath.Join(dir, "main.go")); serr == nil {
				return nil, ErrNotAPad
			}
		}
		return p, nil
	}

	parsed, err := ParsePad(data)
	if err != nil {
		return nil, err
	}
	parsed.Name, parsed.Dir = p.Name, p.Dir
	p = parsed
	if sum, err := os.ReadFile(filepath.Join(dir, PadSumName)); err == nil {
		p.Sum = sum
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	return p, nil
}

// ParsePad reads a pad's bytes: the header, and then the entry stream under it.
//
// Exported because the session file is also something an editor hands back. A
// pad opened with :scratch -edit is reloaded from these same bytes, and the
// header is the half session.Unmarshal does not know — it refuses any directive
// it was not taught, which is exactly right for an entry stream and exactly
// wrong for a file that opens with //gluon:pad. One parser for the file rather
// than two, so a directive added to the header cannot be understood in one
// place and rejected in the other.
func ParsePad(data []byte) (*Pad, error) {
	p := &Pad{Sess: &session.Session{}}
	rest, err := p.readHeader(data)
	if err != nil {
		return nil, err
	}
	if p.Sess, err = session.Unmarshal(rest); err != nil {
		return nil, err
	}
	return p, nil
}

// readHeader consumes the leading directives and returns the entry stream.
//
// It stops at the first line that is not a header directive, so an entry
// beginning with a comment is still an entry: only the directives this function
// knows are eaten, and session.Unmarshal refuses any it does not.
func (p *Pad) readHeader(data []byte) ([]byte, error) {
	lines := strings.Split(string(data), "\n")
	for i, line := range lines {
		field, value, _ := strings.Cut(line, " ")
		switch field {
		case padDirective:
			n, err := strconv.Atoi(strings.TrimSpace(value))
			if err != nil {
				return nil, fmt.Errorf("line %d: %s takes a version: %q", i+1, padDirective, line)
			}
			if n > PadVersion {
				return nil, &ErrNewerPad{Version: n}
			}
		case goDirective:
			p.Go = strings.TrimSpace(value)
		case hostDirective:
			p.Host = strings.TrimSpace(value)
		case requireDirective:
			p.Requires = append(p.Requires, strings.TrimSpace(value))
		default:
			return []byte(strings.Join(lines[i:], "\n")), nil
		}
	}
	return nil, nil
}

// Marshal renders the pad: the header, then the entry stream.
func (p *Pad) Marshal() []byte {
	var b strings.Builder
	fmt.Fprintf(&b, "%s %d\n", padDirective, PadVersion)
	if p.Go != "" {
		fmt.Fprintf(&b, "%s %s\n", goDirective, p.Go)
	}
	if p.Host != "" {
		fmt.Fprintf(&b, "%s %s\n", hostDirective, p.Host)
	}
	for _, r := range p.Requires {
		fmt.Fprintf(&b, "%s %s\n", requireDirective, r)
	}
	b.Write(session.Marshal(p.Sess))
	return []byte(b.String())
}

// padMode is the mode every file under a pad is written with. It matches the
// history file's: a pad is a transcript, and a transcript can hold whatever
// somebody typed.
const padMode = 0o600

// WritePad writes the pad and the go.sum beside it.
//
// Through config.WriteAtomic, which is temp-then-rename: an interrupted write
// must not turn a session somebody has been keeping for a week into a truncated
// one. It is exported for exactly this reason.
func WritePad(p *Pad) error {
	if err := os.MkdirAll(p.Dir, 0o755); err != nil {
		return err
	}
	if err := config.WriteAtomic(filepath.Join(p.Dir, PadName), p.Marshal(), false); err != nil {
		return err
	}
	sumPath := filepath.Join(p.Dir, PadSumName)
	if len(p.Sum) == 0 {
		// Removed rather than left: sums for modules the pad no longer records
		// would be the one piece of the previous session left behind, which is
		// the argument writeSum already makes.
		if err := os.Remove(sumPath); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		return nil
	}
	return config.WriteAtomic(sumPath, p.Sum, false)
}

// PadNames is every scratchpad's name, read off the directory and nothing more.
// Completion asks this, and Pads parses every pad to sort them.
func PadNames() []string {
	ents, err := os.ReadDir(Root())
	if err != nil {
		return nil
	}
	var out []string
	for _, e := range ents {
		if e.IsDir() && IsPad(filepath.Join(Root(), e.Name())) {
			out = append(out, e.Name())
		}
	}
	return out
}

// Pads is every scratchpad, newest worked on first.
//
// Ordering matches List's and for the same reason: the names carry no clock, so
// three pads opened in one afternoon would otherwise be listed in whatever
// order the directory happens to be read in.
func Pads() []PadInfo {
	ents, err := os.ReadDir(Root())
	if err != nil {
		return nil
	}
	var out []PadInfo
	for _, e := range ents {
		if !e.IsDir() {
			continue
		}
		dir := filepath.Join(Root(), e.Name())
		if !IsPad(dir) {
			continue
		}
		info := PadInfo{Name: e.Name(), Dir: dir}
		if fi, err := os.Stat(filepath.Join(dir, PadName)); err == nil {
			info.Mod = fi.ModTime()
		}
		if _, err := os.Stat(filepath.Join(dir, "main.go")); err == nil {
			info.Program = true
		}
		// Read rather than counted from the outside: the entry count and what
		// is pinned are the two things the listing exists to say, and deriving
		// them from the file is the only way they are true.
		if p, err := ReadPad(e.Name()); err == nil {
			info.Host = p.Host
			info.Entries = len(p.Sess.Entries)
			for _, en := range p.Sess.Entries {
				if en.Pinned {
					info.Pinned++
				}
			}
		}
		out = append(out, info)
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Mod.After(out[j].Mod) })
	return out
}

// RemovePad deletes a pad's directory, and only a pad's.
//
// The IsPad check is what keeps this the tree's first delete rather than a
// general one: a name that is a saved scratch reaches this function the same
// way a pad does, and `rm -rf` on the wrong one is not a mistake a message can
// undo.
func RemovePad(name string) error {
	slug, err := ValidPadName(name)
	if err != nil {
		return err
	}
	dir := PadDir(slug)
	if !IsPad(dir) {
		if exists(dir) {
			return ErrNotAPad
		}
		return fmt.Errorf("no scratchpad named %s", slug)
	}
	return os.RemoveAll(dir)
}

// RenamePad moves a pad, refusing when the target is taken. Refusing rather
// than merging: two sessions under one name is not a state this format can
// describe, and the loser would be the one nobody looked at.
func RenamePad(from, to string) (string, error) {
	fromSlug, err := ValidPadName(from)
	if err != nil {
		return "", err
	}
	toSlug, err := ValidPadName(to)
	if err != nil {
		return "", err
	}
	if fromSlug == toSlug {
		return toSlug, nil
	}
	src := PadDir(fromSlug)
	if !IsPad(src) {
		return "", fmt.Errorf("no scratchpad named %s", fromSlug)
	}
	dst := PadDir(toSlug)
	if exists(dst) {
		if IsPad(dst) {
			return "", fmt.Errorf("%s is already a scratchpad", toSlug)
		}
		return "", fmt.Errorf("%s is already a saved scratch", toSlug)
	}
	if err := os.Rename(src, dst); err != nil {
		return "", err
	}
	return toSlug, nil
}
