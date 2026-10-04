package repl

import (
	"errors"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/sandboxws/gluon/internal/cmdspec"
)

// A command declares what it takes, and its parser decides what it accepts.
// Nothing makes the two agree except these tests, and that is deliberate: the
// parsers' edges are what keep `-nums` a negation and `-srcX` a name, and a
// shared parser rewritten under them would put the risk exactly there. So the
// declaration is held to the parser from both sides — every example parses,
// and every flag a parser speaks is declared.

// argParsers parse a command's argument the way the command does, and nothing
// more: an error is what the command would refuse before doing anything.
var argParsers = map[string]func(arg string) error{
	":t": func(a string) error { _, _, _, err := parseTypeArgs(a); return err },
	":bench": func(a string) error {
		_, rest, err := parseBenchArgs(a)
		if err == nil && rest == "" {
			return errors.New("no expression")
		}
		return err
	},
	":doc": func(a string) error {
		_, rest, err := parseDocArgs(a)
		if err == nil && rest == "" {
			return errors.New("no name")
		}
		return err
	},
	":inspect": func(a string) error {
		_, rest, err := cutInspectItems(a)
		if err == nil && rest == "" {
			return errors.New("no expression")
		}
		return err
	},
	":query": func(a string) error {
		if _, _, _, sql := parseQueryArgs(a); sql == "" || strings.HasPrefix(sql, "-") {
			return fmt.Errorf("no statement after the flags: %q", sql)
		}
		return nil
	},
	":http":  func(a string) error { _, err := parseHTTPArgs(a); return err },
	":since": func(a string) error { _, err := parseSinceArgs(a); return err },
	":save": func(a string) error {
		if _, _, topic := parseSaveArgs(a); strings.HasPrefix(topic, "-") {
			return fmt.Errorf("%s is not a flag :save reads", topic)
		}
		return nil
	},
	":test": func(a string) error {
		if _, exp := parseTestArgs(a); exp == "" {
			return errors.New("no expression")
		}
		return nil
	},
	":scratch": func(a string) error {
		// An unread flag is not an error to :scratch — it is a pad's name,
		// which is exactly the failure worth catching here.
		if flag, rest, _ := parseScratchArgs(a); flag == "" && strings.HasPrefix(rest, "-") {
			return fmt.Errorf("%s is not a flag :scratch reads", rest)
		}
		return nil
	},
}

// parserSources are where each command's flags are spelled: the functions,
// methods, vars and consts whose string literals are its vocabulary.
var parserSources = map[string][]string{
	":t":       {"parseTypeArgs"},
	":bench":   {"parseBenchArgs", "leadingBenchFlag"},
	":doc":     {"docFlags"},
	":inspect": {"cutInspectItems"},
	":query":   {"parseQueryArgs"},
	":http":    {"parseHTTPArgs"},
	":since":   {"parseSinceArgs"},
	":save":    {"parseSaveArgs"},
	":test":    {"parseTestArgs"},
	":scratch": {"parseScratchArgs", "scratchRemove"},
	":buf":     {"buffer"},
	":reset":   {"reset"},
	":get":     {"get"},
	":use":     {"use"},
	":replay":  {"replay"},
	":share":   {"shareConfirmFlag"},
}

// argOf is an example's argument: the line with its command taken off.
func argOf(line string) (name, arg string) {
	name, arg, _ = strings.Cut(strings.TrimSpace(line), " ")
	return name, strings.TrimSpace(arg)
}

// TestEveryExampleParses: an example is evidence, not decoration. Each one is
// read by its command's own parser, and each one names its own command.
func TestEveryExampleParses(t *testing.T) {
	c := &Core{}
	for _, cmd := range c.Commands() {
		parse := argParsers[cmd.Name]
		for _, ex := range cmd.Usage.Examples {
			name, arg := argOf(ex.Line)
			if got, ok := c.lookup(name); !ok || got.Name != cmd.Name {
				t.Errorf("%s's example %q is not a line for %s", cmd.Name, ex.Line, cmd.Name)
				continue
			}
			if parse == nil {
				continue
			}
			if err := parse(arg); err != nil {
				t.Errorf("%s's example %q does not parse: %v", cmd.Name, ex.Line, err)
			}
		}
	}
}

// TestEveryVisibleFlagHasHelpAndAnExample: a flag with no line of help is a
// row with nothing in it, and a flag no example uses is one nothing proves the
// parser accepts.
func TestEveryVisibleFlagHasHelpAndAnExample(t *testing.T) {
	for _, cmd := range (&Core{}).Commands() {
		for _, f := range cmd.Usage.Visible() {
			if f.Help == "" {
				t.Errorf("%s %s has no help", cmd.Name, f.Name)
			}
			used := false
			for _, ex := range cmd.Usage.Examples {
				for _, w := range strings.Fields(ex.Line) {
					if w == f.Name {
						used = true
					}
				}
			}
			if !used {
				t.Errorf("%s %s is in no example, so nothing shows it parses", cmd.Name, f.Name)
			}
		}
	}
}

// TestEveryBuiltinHasAnExample: the page is where somebody learns to type the
// command, and a page without a line to type teaches the grammar only.
func TestEveryBuiltinHasAnExample(t *testing.T) {
	for _, cmd := range builtinCommands() {
		if len(cmd.Usage.Examples) == 0 {
			t.Errorf("%s has no example", cmd.Name)
		}
	}
}

// TestParsersSpeakOnlyDeclaredFlags reads each parser's source for the flags it
// spells, and holds them to the command's declaration in both directions: a
// flag added to a parser and not declared is one no page, completion or tool
// would ever show, and a flag declared and not parsed is a page that lies.
func TestParsersSpeakOnlyDeclaredFlags(t *testing.T) {
	spoken := flagLiterals(t)
	c := &Core{}
	for _, cmd := range c.Commands() {
		sources, ok := parserSources[cmd.Name]
		if !ok {
			if len(cmd.Usage.Flags) > 0 {
				t.Errorf("%s declares flags, and parserSources does not say where it reads them", cmd.Name)
			}
			continue
		}
		heard := map[string]bool{}
		for _, src := range sources {
			lits, found := spoken[src]
			if !found {
				t.Errorf("%s: %s is not a function, var or const in this package", cmd.Name, src)
			}
			for _, l := range lits {
				heard[l] = true
			}
		}
		for f := range heard {
			if _, ok := cmd.Usage.Flag(f); !ok {
				t.Errorf("%s's parser reads %s, and %s does not declare it", cmd.Name, f, cmd.Name)
			}
		}
		for _, f := range cmd.Usage.Flags {
			if !heard[f.Name] {
				t.Errorf("%s declares %s, and its parser never reads it", cmd.Name, f.Name)
			}
		}
	}
}

// flagRe is a string literal that is a flag and nothing else: "-H", "-w ".
var flagRe = regexp.MustCompile(`^-[A-Za-z][A-Za-z0-9]* ?$`)

// flagLiterals maps each top-level function, method, var and const in this
// package to the flag-shaped string literals inside it.
func flagLiterals(t *testing.T) map[string][]string {
	t.Helper()
	fset := token.NewFileSet()
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	out := map[string][]string{}
	collect := func(name string, n ast.Node) {
		if _, seen := out[name]; !seen {
			out[name] = nil
		}
		ast.Inspect(n, func(n ast.Node) bool {
			lit, ok := n.(*ast.BasicLit)
			if !ok || lit.Kind != token.STRING {
				return true
			}
			s, err := strconv.Unquote(lit.Value)
			if err == nil && flagRe.MatchString(s) {
				out[name] = append(out[name], strings.TrimSpace(s))
			}
			return true
		})
	}
	for _, path := range files {
		if strings.HasSuffix(path, "_test.go") {
			continue
		}
		src, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		f, err := parser.ParseFile(fset, path, src, 0)
		if err != nil {
			t.Fatal(err)
		}
		for _, d := range f.Decls {
			switch d := d.(type) {
			case *ast.FuncDecl:
				collect(d.Name.Name, d)
			case *ast.GenDecl:
				for _, sp := range d.Specs {
					if vs, ok := sp.(*ast.ValueSpec); ok {
						for _, n := range vs.Names {
							collect(n.Name, vs)
						}
					}
				}
			}
		}
	}
	return out
}

// TestSeeAlsoNamesRealCommands: a neighbour that does not exist is a dead
// link on the page and in the docs. A plugin's command counts, active or not.
func TestSeeAlsoNamesRealCommands(t *testing.T) {
	c := withPlugins()
	for _, cmd := range c.Commands() {
		for _, s := range cmd.Usage.See {
			if s == cmd.Name {
				t.Errorf("%s names itself as a neighbour", cmd.Name)
			}
			if _, ok := c.lookup(s); ok {
				continue
			}
			if _, ok := c.dormant(s); !ok {
				t.Errorf("%s's see-also names %s, which no command or plugin provides", cmd.Name, s)
			}
		}
	}
}

// TestArgAgreesWithUsage: Arg is the short form and the Spec the long one, and
// the two must not contradict each other. A `<` in Arg is a required operand
// to the MCP schema and the usage test, so the Spec must have one; a flag Arg
// spells must be one the Spec declares.
func TestArgAgreesWithUsage(t *testing.T) {
	for _, cmd := range (&Core{}).Commands() {
		sp := cmd.Usage
		if strings.Contains(cmd.Arg, "<") {
			required := false
			for _, p := range sp.Operands(cmd.Arg) {
				if !p.Optional {
					required = true
				}
			}
			if !required {
				t.Errorf("%s's Arg %q requires an operand and its Usage declares none", cmd.Name, cmd.Arg)
			}
		}
		for _, f := range cmdspec.FromArg(cmd.Arg).Flags {
			if _, ok := sp.Flag(f.Name); !ok {
				t.Errorf("%s's Arg %q spells %s, and its Usage does not declare it", cmd.Name, cmd.Arg, f.Name)
			}
		}
		if (sp.Kind == cmdspec.NoArg) != (cmd.Arg == "") {
			t.Errorf("%s takes %s by its Usage and %q by its Arg", cmd.Name, sp.Kind, cmd.Arg)
		}
	}
}

// TestHiddenFlagsAreNeverShown: a hidden flag is a confirmation a view submits
// on the reader's behalf. Printed anywhere, it becomes a way past the question.
func TestHiddenFlagsAreNeverShown(t *testing.T) {
	c := &Core{}
	for _, cmd := range c.Commands() {
		page := c.helpPage(cmd)
		for _, f := range cmd.Usage.Flags {
			if !f.Hidden {
				continue
			}
			if strings.Contains(page, f.Name) {
				t.Errorf(":help %s shows the hidden %s", cmd.Name, f.Name)
			}
			for _, ex := range cmd.Usage.Examples {
				if strings.Contains(ex.Line, f.Name) {
					t.Errorf("%s's example %q uses the hidden %s", cmd.Name, ex.Line, f.Name)
				}
			}
		}
	}
}
