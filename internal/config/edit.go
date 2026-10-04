package config

import (
	"bytes"
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/sandboxws/gluon/internal/atomicfile"
)

// AppendDatabase adds one [[database]] block to path, creating the file if it
// does not exist.
//
// It appends rather than re-encodes, and that is the whole design. BurntSushi's
// encoder serializes a document from a struct: comments dropped, keys
// reordered, inline tables expanded. Running it over a config somebody wrote by
// hand would lose their notes and nothing would fail — silent damage to a file
// gluon was trusted with. The array-of-tables schema exists so that appending
// is always enough, which makes "every byte above the new block is untouched" a
// property of the shape rather than of anyone's care.
//
// The result is checked, not trusted: it is decoded again and compared against
// the original before anything is written. And the write is a temp file plus a
// rename, so an interrupted run cannot truncate a config that was fine.
func AppendDatabase(path string, entry Database, force bool) ([]byte, error) {
	original, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return nil, err
	}
	existed := err == nil

	var before *Config
	if existed {
		before, err = LoadFile(path)
		if err != nil {
			return nil, fmt.Errorf("refusing to write to a config that does not parse: %w", err)
		}
		for _, d := range before.Databases {
			if d.Module == entry.Module && d.Name == entry.Name {
				if !force {
					return nil, fmt.Errorf(
						"%s already has a database named %q for %s — "+
							"gluon does not rewrite a block in place, because that is where the "+
							"comments inside it die. Edit those lines, or pass -force",
						path, entry.Name, moduleLabel(entry.Module))
				}
			}
		}
	}

	block := Stanza(entry)
	next := append(append([]byte(nil), original...), []byte(separatorFor(original)+block)...)

	if err := verifyAppend(path, original, next, before); err != nil {
		return nil, err
	}
	if err := WriteAtomic(path, next, existed); err != nil {
		return nil, err
	}
	return []byte(block), nil
}

func moduleLabel(m string) string {
	if m == "" {
		return "any module"
	}
	return m
}

// separatorFor keeps exactly one blank line between blocks, whatever the file
// ended with.
func separatorFor(original []byte) string {
	if len(original) == 0 {
		return ""
	}
	switch {
	case bytes.HasSuffix(original, []byte("\n\n")):
		return ""
	case bytes.HasSuffix(original, []byte("\n")):
		return "\n"
	default:
		return "\n\n"
	}
}

// verifyAppend proves the edit did what it claimed.
//
// The stance is the differential test that holds invariant 6: do not reason
// about whether the output is right, decode it and compare. Two things must be
// true — every byte of the original survives, and the parsed result differs
// from the parsed original in exactly one new database.
func verifyAppend(path string, original, next []byte, before *Config) error {
	if !bytes.HasPrefix(next, original) {
		return fmt.Errorf("the append would have rewritten existing bytes — refusing")
	}
	tmp, err := os.CreateTemp("", "gluon-verify-*.toml")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(next); err != nil {
		tmp.Close()
		return err
	}
	tmp.Close()

	after, err := LoadFile(tmp.Name())
	if err != nil {
		return fmt.Errorf("the result would not parse, so nothing was written: %w", err)
	}
	want := 1
	if before != nil {
		want = len(before.Databases) + 1
	}
	if len(after.Databases) != want {
		return fmt.Errorf("the result holds %d databases, want %d — nothing was written",
			len(after.Databases), want)
	}
	if before != nil {
		// Everything that is not a database must be identical. A setting that
		// changed would mean the append landed inside an existing table.
		if !sameNonDatabase(before, after) {
			return fmt.Errorf("the append changed a setting outside [[database]] — nothing was written")
		}
	}
	return nil
}

func sameNonDatabase(a, b *Config) bool {
	if strings.Join(a.Imports, "\x00") != strings.Join(b.Imports, "\x00") {
		return false
	}
	if a.Editor != b.Editor || a.Timeout != b.Timeout {
		return false
	}
	if len(a.Theme) != len(b.Theme) || len(a.Hosts) != len(b.Hosts) {
		return false
	}
	for k, v := range a.Theme {
		if b.Theme[k] != v {
			return false
		}
	}
	return true
}

// WriteAtomic writes through a temp file in the same directory and renames.
//
// The implementation moved to internal/atomicfile when a second caller turned
// out to be unable to import this package. The name stays here because
// `gluon theme import` and the scratchpad writer already call it, and because
// a config that was fine arriving empty after an interrupted run is still the
// worst outcome this guards against.
func WriteAtomic(path string, data []byte, existed bool) error {
	return atomicfile.Write(path, data, existed)
}

// Stanza renders one [[database]] block as text.
//
// Rendered rather than marshalled, because this is also what the wizard shows
// before it writes anything: the bytes on screen and the bytes on disk are the
// same string, so a confirmation cannot promise one thing and do another.
func Stanza(d Database) string {
	var b strings.Builder
	b.WriteString("[[database]]\n")
	write := func(k, v string) {
		if v != "" {
			b.WriteString(k + " = " + strconv.Quote(v) + "\n")
		}
	}
	write("module", d.Module)
	write("name", d.Name)
	write("driver", d.Driver)
	write("driver_module", d.DriverModule)
	write("dsn_env", d.DSNEnv)
	write("dsn_file", d.DSNFile)
	write("dsn_key", d.DSNKey)
	write("file", d.File)
	write("host", d.Host)
	if d.Port != 0 {
		b.WriteString("port = " + strconv.Itoa(d.Port) + "\n")
	}
	write("user", d.User)
	write("database", d.DBName)
	write("password_env", d.PassEnv)
	write("password_file", d.PassFile)
	if d.ReadOnly != nil && !*d.ReadOnly {
		b.WriteString("readonly = false\n")
	}
	return b.String()
}

// SetThemeName writes `[theme] name = "<name>"` into path. It returns the line
// it wrote.
//
// It is SetOption at the theme.name row, and it stays exported and stays named
// this because :theme must keep exactly one write path — invariant 32 names
// this function, and the invariant stays literally true.
func SetThemeName(path, name string) (string, error) {
	opt, ok := Lookup("theme.name")
	if !ok {
		return "", fmt.Errorf("theme.name is not a setting gluon has")
	}
	return SetOption(path, opt, name)
}

// SetOption writes one scalar into path, creating the file, the table or the
// line as needed. It returns the line it wrote.
//
// A line edit rather than a re-encode, for the reason AppendDatabase gives at
// length: the encoder serializes a document from a struct, and every comment
// somebody wrote in their config would quietly die. So exactly one line moves —
// the key under its table, or a new block at the end — and the result is
// checked rather than trusted, twice over. Byte-wise, every other line must be
// identical, which is what says nothing else was damaged. Parse-wise, the file
// must still load and must now read back as the value asked for, which is what
// says the edit did what it claimed.
func SetOption(path string, opt Option, value string) (string, error) {
	if opt.Kind != Scalar {
		return "", fmt.Errorf("%s is listed, not settable: %s", opt.Key, opt.Why)
	}
	return editOption(path, opt, func(src []byte) ([]byte, string, bool, error) {
		next, line, err := setScalar(src, opt.Table, opt.Name, value)
		return next, line, true, err
	}, func(c *Config) error {
		// Against what applying the value produces rather than against the
		// value itself: the loader expands a leading ~, so a path written as
		// "~/lab" reads back absolute, and comparing the raw bytes would call
		// a correct write a failure. Apply is the same normalisation the
		// running session gets, so this asks the question that matters —
		// does the file now mean what setting it in memory would have meant.
		probe := &Config{}
		opt.Apply(probe, value)
		if want, got := opt.Get(probe), opt.Get(c); got != want {
			return fmt.Errorf("the result reads %s as %q rather than %q", opt.Key, got, want)
		}
		return nil
	})
}

// UnsetOption removes the line, so the default applies again. found is false
// when there was nothing to remove, which is not an error: a setting that was
// never set is already at its default.
//
// The table header is left behind even when its last key goes. Removing it
// would be a two-line edit, and one line is what the proof below can hold.
func UnsetOption(path string, opt Option) (line string, found bool, err error) {
	if opt.Kind != Scalar {
		return "", false, fmt.Errorf("%s is listed, not settable: %s", opt.Key, opt.Why)
	}
	line, err = editOption(path, opt, func(src []byte) ([]byte, string, bool, error) {
		next, line, ok := unsetScalar(src, opt.Table, opt.Name)
		found = ok
		return next, line, ok, nil
	}, func(c *Config) error {
		if got := opt.Get(c); got != "" {
			return fmt.Errorf("the result still reads %s as %q", opt.Key, got)
		}
		return nil
	})
	return line, found, err
}

// editOption is the half both writers share: refuse a config that does not
// parse, apply the byte edit, prove it, and write atomically.
func editOption(path string, opt Option, edit func([]byte) ([]byte, string, bool, error),
	check func(*Config) error) (string, error) {
	if path == "" {
		return "", fmt.Errorf("gluon cannot tell where your config lives — set XDG_CONFIG_HOME")
	}
	original, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return "", err
	}
	existed := err == nil
	if existed {
		if _, err := LoadFile(path); err != nil {
			return "", fmt.Errorf("refusing to write to a config that does not parse: %w", err)
		}
	}

	next, line, changed, err := edit(original)
	if err != nil {
		return "", err
	}
	if !changed {
		return "", nil
	}
	if err := verifyScalar(original, next, check); err != nil {
		return "", err
	}
	if err := WriteAtomic(path, next, existed); err != nil {
		return "", err
	}
	return line, nil
}

// setScalar is the edit itself, on bytes, so it can be tested without a
// filesystem and so the "one line changed" check below has something to
// compare. An empty table means the top level.
func setScalar(src []byte, table, key, value string) (next []byte, line string, err error) {
	line = key + " = " + strconv.Quote(value)
	lines := strings.Split(string(src), "\n")

	if i, ok := findKey(lines, table, key); ok {
		indent, rest := splitIndent(lines[i])
		eq := strings.Index(rest, "=")
		tail, ok := afterValue(rest[eq+1:])
		if !ok {
			return nil, "", fmt.Errorf(
				"the %s in your config is not a plain string — change that line by hand", key)
		}
		lines[i] = indent + line + tail
		return []byte(strings.Join(lines, "\n")), line, nil
	}

	// The table is there but says nothing about this key: the new line goes
	// under its header.
	if table != "" {
		if i, ok := headerIndex(lines, table); ok {
			return []byte(strings.Join(insert(lines, i+1, line), "\n")), line, nil
		}
		block := "[" + table + "]\n" + line + "\n"
		return append(append([]byte(nil), src...), []byte(separatorFor(src)+block)...), line, nil
	}

	// A top-level key may not simply be appended: everything after the first
	// table header belongs to a table, so a key appended to the end of the file
	// lands inside whichever one happens to be last. It goes above them
	// instead.
	return []byte(strings.Join(insert(lines, topLevelInsert(lines), line), "\n")), line, nil
}

// unsetScalar removes one key's line.
func unsetScalar(src []byte, table, key string) (next []byte, line string, found bool) {
	lines := strings.Split(string(src), "\n")
	i, ok := findKey(lines, table, key)
	if !ok {
		return src, "", false
	}
	line = strings.TrimSpace(lines[i])
	return []byte(strings.Join(append(lines[:i:i], lines[i+1:]...), "\n")), line, true
}

// findKey is the index of `key = ...` inside table, or false. An empty table is
// the top level, which is every line before the first header.
func findKey(lines []string, table, key string) (int, bool) {
	inTable := table == ""
	for i, l := range lines {
		if h, ok := tableHeader(l); ok {
			if table == "" {
				// Everything from here down belongs to a table.
				return 0, false
			}
			// A later table of the same name would be a duplicate key, which
			// the file cannot have had and still parsed. The first is it.
			if inTable {
				return 0, false
			}
			inTable = h == table
			continue
		}
		if !inTable {
			continue
		}
		_, rest := splitIndent(l)
		if !strings.HasPrefix(rest, key) {
			continue
		}
		eq := strings.Index(rest, "=")
		if eq < 0 || strings.TrimSpace(rest[:eq]) != key {
			continue
		}
		return i, true
	}
	return 0, false
}

// topLevelInsert is the line a new top-level key goes before: the first table
// header, walking back over the comments and blank lines directly above it,
// because a comment sitting on top of [theme] is somebody's note about [theme]
// and not about the line being inserted.
func topLevelInsert(lines []string) int {
	for i, l := range lines {
		if _, ok := tableHeader(l); !ok {
			continue
		}
		j := i
		for j > 0 {
			prev := strings.TrimSpace(lines[j-1])
			if prev == "" || strings.HasPrefix(prev, "#") {
				j--
				continue
			}
			break
		}
		return j
	}
	// No tables at all: the end of the file is the top level.
	n := len(lines)
	for n > 0 && strings.TrimSpace(lines[n-1]) == "" {
		n--
	}
	return n
}

func insert(lines []string, at int, line string) []string {
	out := append([]string{}, lines[:at]...)
	out = append(out, line)
	return append(out, lines[at:]...)
}

// verifyScalar proves the edit moved one line and nothing else, and that the
// file it produced still parses and now says what it was asked to say.
func verifyScalar(original, next []byte, check func(*Config) error) error {
	if err := changedOneLine(original, next); err != nil {
		return err
	}
	tmp, err := os.CreateTemp("", "gluon-verify-*.toml")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(next); err != nil {
		tmp.Close()
		return err
	}
	tmp.Close()

	after, err := LoadFile(tmp.Name())
	if err != nil {
		return fmt.Errorf("the result would not parse, so nothing was written: %w", err)
	}
	if err := check(after); err != nil {
		return fmt.Errorf("%w — nothing was written", err)
	}
	return nil
}

// tableHeader reports the name of the table a line opens, for the plain
// `[name]` and `[[name]]` forms this only has to recognise well enough to know
// when it has left [theme].
func tableHeader(l string) (string, bool) {
	t := strings.TrimSpace(l)
	if !strings.HasPrefix(t, "[") {
		return "", false
	}
	if i := strings.Index(t, "]"); i > 0 {
		return strings.TrimSpace(strings.Trim(t[:i+1], "[]")), true
	}
	return "", false
}

func headerIndex(lines []string, want string) (int, bool) {
	for i, l := range lines {
		if h, ok := tableHeader(l); ok && h == want {
			return i, true
		}
	}
	return 0, false
}

func splitIndent(l string) (indent, rest string) {
	i := 0
	for i < len(l) && (l[i] == ' ' || l[i] == '\t') {
		i++
	}
	return l[:i], l[i:]
}

// afterValue skips one quoted TOML string and returns whatever followed it —
// a trailing comment, usually, which is somebody's note about why they chose
// that theme and is not ours to delete.
func afterValue(s string) (tail string, ok bool) {
	i := 0
	for i < len(s) && (s[i] == ' ' || s[i] == '\t') {
		i++
	}
	if i >= len(s) {
		return "", false
	}
	quote := s[i]
	if quote != '"' && quote != '\'' {
		return "", false
	}
	// A multi-line string would end this scan at its opening delimiter and
	// leave the rest as "tail", producing a line that does not parse. The
	// verification below catches that; nothing is written.
	for j := i + 1; j < len(s); j++ {
		switch s[j] {
		case '\\':
			if quote == '"' {
				j++
			}
		case quote:
			return s[j+1:], true
		}
	}
	return "", false
}

// changedOneLine is the byte-level half of the check: an edit that replaced a
// line may differ from the original on exactly that line, an edit that added a
// block may only have added, and an unset may only have removed one line.
func changedOneLine(original, next []byte) error {
	if bytes.HasPrefix(next, original) {
		return nil
	}
	a, b := strings.Split(string(original), "\n"), strings.Split(string(next), "\n")
	if d := len(b) - len(a); d < -1 || d > 1 {
		return fmt.Errorf("the edit would have moved %d lines — refusing", d)
	}
	diff := 0
	for i, j := 0, 0; i < len(a) && j < len(b); i, j = i+1, j+1 {
		if a[i] == b[j] {
			continue
		}
		diff++
		if diff > 1 {
			return fmt.Errorf("the edit would have rewritten more than one line — refusing")
		}
		// An inserted line leaves the rest of the file shifted by one, and a
		// removed line shifts it the other way.
		switch len(b) - len(a) {
		case 1:
			i--
		case -1:
			j--
		}
	}
	return nil
}
