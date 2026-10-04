package repl

import (
	"os"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/sandboxws/gluon/internal/cmdspec"
	"github.com/sandboxws/gluon/internal/plugin"
	"github.com/sandboxws/gluon/internal/plugins"
)

// helpTokens is every spelling of "what does this take?".
var helpTokens = []string{"--help", "?", "-h", "-help"}

// withPlugins is a Core that knows every built-in plugin and has activated
// none of them — the session a reader is in before :get.
func withPlugins() *Core {
	c := &Core{plugins: plugin.NewSet(plugins.Builtin())}
	c.plugins.Activate(nil, nil)
	return c
}

// TestHelpNeverRunsTheCommand: where a token asks for help, the answer is the
// page and Run is never reached. A probe of every kind proves the rule, and
// every builtin proves it holds for the commands that exist — none of them has
// an evaluator here, so one that ran would panic.
func TestHelpNeverRunsTheCommand(t *testing.T) {
	for _, k := range []cmdspec.Kind{cmdspec.NoArg, cmdspec.Words, cmdspec.SQL, cmdspec.GoName, cmdspec.GoExpr} {
		probe := Command{
			Name: ":probe", Group: groupPlugin, Summary: "a command that must not run",
			Usage: cmdspec.Spec{Kind: k},
			Run: func(*Core, string) Result {
				t.Errorf("a %s command ran when asked for help", k)
				return Result{}
			},
		}
		c := &Core{extra: []Command{probe}}
		for _, tok := range helpTokens {
			if !cmdspec.AsksForHelp(k, tok) {
				continue
			}
			res := c.meta(":probe " + tok)
			if res.Err || !strings.Contains(res.Out, "a command that must not run") {
				t.Errorf(":probe %s (%s) did not answer with its page: %q", tok, k, res.Out)
			}
		}
	}

	c := &Core{}
	for _, cmd := range c.Commands() {
		res := c.meta(cmd.Name + " --help")
		if res.Err || !strings.Contains(res.Out, cmd.Summary) {
			t.Errorf("%s --help did not print its page: %q", cmd.Name, res.Out)
		}
		if res.Quit || res.Clear || res.Edit != "" || res.Watch {
			t.Errorf("%s --help asked the driver to act: %+v", cmd.Name, res)
		}
	}
}

// TestHelpHasNoSideEffects is what the interception is for: each of these did
// something before it was asked.
func TestHelpHasNoSideEffects(t *testing.T) {
	data := t.TempDir()
	t.Setenv("XDG_DATA_HOME", data)
	c := testCore(t)
	if res := c.Submit("x := 1"); res.Err {
		t.Fatal(res.Out)
	}

	if res := c.Submit(":undo -h"); res.Err || len(c.sess.Entries) != 1 {
		t.Errorf(":undo -h dropped an entry (%d left): %s", len(c.sess.Entries), res.Out)
	}
	if res := c.Submit(":q -h"); res.Quit {
		t.Error(":q -h quit")
	}
	if res := c.Submit(":bookmark -h"); res.Err || len(c.marks.names()) != 0 {
		t.Errorf(":bookmark -h took a snapshot: %v", c.marks.names())
	}
	for _, line := range []string{":scratch -h", ":scratch --help", ":save -h"} {
		if res := c.Submit(line); res.Err {
			t.Errorf("%s: %s", line, res.Out)
		}
	}
	// Nothing was written under the data directory: no pad named h or help,
	// no scratch module named h.
	if entries, _ := os.ReadDir(data); len(entries) != 0 {
		t.Errorf("asking for help wrote under the data directory: %v", entries)
	}
}

// TestAGoArgumentKeepsItsMinus: for a command taking an expression, -h is the
// negation of h, and a session with an h gets its answer. Without one, the
// error says where the help is.
func TestAGoArgumentKeepsItsMinus(t *testing.T) {
	c := testCore(t)
	if res := c.Submit("h := 3"); res.Err {
		t.Fatal(res.Out)
	}
	res := c.Submit(":t -h")
	if res.Err || !strings.Contains(res.Out, "int") {
		t.Errorf(":t -h with h bound: got %q, want the type of -h", res.Out)
	}
	res = c.Submit(":t -help")
	if !res.Err || !strings.Contains(res.Out, ":t --help is its help") {
		t.Errorf(":t -help with nothing named help: got %q, want the pointer to --help", res.Out)
	}
}

// TestHelpIsOnePageEverywhere: every way of asking prints the same bytes.
func TestHelpIsOnePageEverywhere(t *testing.T) {
	c := &Core{}
	want := c.meta(":help :t").Out
	for _, line := range []string{":help t", ":t --help", ":type ?", ":help :type"} {
		if got := c.meta(line).Out; got != want {
			t.Errorf("%s printed a different page:\n%s\nwant:\n%s", line, got, want)
		}
	}
}

// TestAnInactivePluginCommandHasAPage: the command a reader cannot try yet is
// the one most worth reading about, and the page says what would turn it on.
func TestAnInactivePluginCommandHasAPage(t *testing.T) {
	c := withPlugins()
	res := c.meta(":help :sql")
	if res.Err {
		t.Fatalf(":help :sql: %s", res.Out)
	}
	for _, want := range []string{"from the gorm plugin", "not active", ":get gorm.io/gorm"} {
		if !strings.Contains(res.Out, want) {
			t.Errorf(":help :sql does not say %q:\n%s", want, res.Out)
		}
	}
	// Seven routers provide :routes; the page says the others exist.
	if out := c.meta(":help :routes").Out; !strings.Contains(out, "(also ") {
		t.Errorf(":help :routes names only one provider:\n%s", out)
	}
	// Asked the other way, it is the same page, not the line that says the
	// command cannot run here.
	for _, ask := range []string{":sql --help", ":sql ?"} {
		if got, want := c.meta(ask), c.meta(":help :sql"); got.Err || got.Out != want.Out {
			t.Errorf("%s is not :help :sql's page:\n%s", ask, got.Out)
		}
	}
}

// TestUnknownCommandNamesItsPlugin: an absent library is not a typo, and the
// answer says which it is.
func TestUnknownCommandNamesItsPlugin(t *testing.T) {
	c := withPlugins()
	res := c.meta(":sql db.Find(&users)")
	if !res.Err || !strings.Contains(res.Out, "comes from the gorm plugin") ||
		!strings.Contains(res.Out, ":get gorm.io/gorm") {
		t.Errorf(":sql without gorm: %q", res.Out)
	}
	if res := c.meta(":nosuch"); !res.Err || !strings.Contains(res.Out, "unknown command") {
		t.Errorf(":nosuch: %q", res.Out)
	}
}

// TestBareHelpNamesTheInactiveCommands: the listing is where a reader looks
// for what exists, so the commands that exist once a library is present are
// named there too.
func TestBareHelpNamesTheInactiveCommands(t *testing.T) {
	out := withPlugins().helpAll()
	if !strings.Contains(out, "not active here") || !strings.Contains(out, ":sql") {
		t.Errorf(":help does not name the inactive plugins' commands:\n%s", out)
	}
	if !strings.Contains(out, "--help") {
		t.Error(":help does not say that a command answers --help")
	}
}

// TestPagesFitEightyColumns: a page is read in a terminal and through a pipe.
// A line to type is exempt, in the examples or in a Detail — wrapping one
// would print two lines that are neither.
func TestPagesFitEightyColumns(t *testing.T) {
	c := withPlugins()
	names := []string{}
	for _, cmd := range c.Commands() {
		names = append(names, cmd.Name)
	}
	names = append(names, c.dormantNames()...)
	for _, name := range names {
		page := c.help(name).Out
		for _, line := range strings.Split(page, "\n") {
			if strings.HasPrefix(strings.TrimSpace(line), ":") {
				continue
			}
			if n := utf8.RuneCountInString(line); n > 80 {
				t.Errorf(":help %s has a %d-column line: %q", name, n, line)
			}
		}
	}
}

// TestAUsageLineSaysWhereTheRestIs: misusing a command is when its grammar is
// wanted, so the usage line points at the page.
func TestAUsageLineSaysWhereTheRestIs(t *testing.T) {
	c := testCore(t)
	res := c.Submit(":http")
	if !res.Err || !strings.Contains(res.Out, "usage:") ||
		!strings.Contains(res.Out, ":http --help lists its flags and examples") {
		t.Errorf(":http with nothing: %q", res.Out)
	}
}
