// Package plugin tailors gluon to one library at a time.
//
// A plugin is compiled in. `-buildmode=plugin` is permanently rejected
// (ROADMAP.md) — it is a dead end on darwin/arm64 and it abandons the premise
// that the real toolchain is the evaluator — so there is no dynamic loading
// here and no ABI to keep. What there is instead is four capabilities, none of
// which require gluon to link the library being described:
//
//   - Imports    a qualifier→path preload, so a command naming json. never
//     pays the ~135ms goimports pass (invariant 17 keeps an unused
//     one free: an import nothing names is not an import).
//   - Commands   a meta command that rewrites its argument into Go source and
//     hands it to the ordinary evaluator. A plugin command can
//     therefore do nothing a typed line could not.
//   - Renders    a formatter for one reflect type name, applied in gluon's own
//     process, after the child has already described the value.
//   - Aliases    a short name for `:get`.
//
// The child program is untouched by all of it. It stays strictly stdlib
// forever, so nothing here can add a dependency or a millisecond to an
// evaluation.
package plugin

import (
	"strings"

	"github.com/sandboxws/gluon/internal/cmdspec"
	"github.com/sandboxws/gluon/internal/pretty"
	"github.com/sandboxws/gluon/internal/syntax"
)

// A Plugin describes one library. Everything beyond Meta is optional: a plugin
// implements only the capability interfaces it needs.
type Plugin interface {
	Meta() Meta
}

// Meta identifies a plugin and says when it is active.
type Meta struct {
	// Name is how :plugins and config refer to it.
	Name string
	// Module is the Go module this plugin describes. An empty Module means the
	// standard library, which is always present, so the plugin is always
	// active.
	Module string
	// Summary is one line for :plugins.
	Summary string
}

// Importer contributes preloaded imports.
type Importer interface{ Imports() []Import }

// Import is a qualifier and the path it resolves to.
type Import struct{ Name, Path string }

// Commander contributes meta commands.
type Commander interface{ Commands() []Command }

// Renderer contributes value formatters.
type Renderer interface{ Renders() []Render }

// Aliaser contributes `:get` shortcuts.
type Aliaser interface{ Aliases() []Alias }

// Alias maps a short name to a module path, so `:get uuid` works.
type Alias struct{ Name, Module string }

// Guider contributes a markdown cheatsheet, shown by `:guide <name>`.
type Guider interface{ Guide() string }

// A Command is a plugin's meta command.
//
// Rewrite is the whole design: it turns the argument into Go source that the
// ordinary evaluator runs, transiently, so the command has exactly the
// authority of a line the user could have typed and cannot mutate the session.
// That is also why a plugin never links the library it describes — the user's
// own module supplies it.
type Command struct {
	Name    string
	Aliases []string
	Arg     string
	Summary string
	Detail  string
	// Usage is what the command takes, declared the way a builtin declares
	// it, so :help, completion and the docs treat both alike. A command that
	// declares none is read off Arg (cmdspec.FromArg), and its argument is Go.
	Usage cmdspec.Spec
	// Rewrite produces the Go expression to evaluate. Returning an error is how
	// a command rejects its argument.
	Rewrite func(arg string) (string, error)
	// Text says the expression evaluates to a string that is the answer, and
	// should be printed as-is rather than rendered as a value.
	//
	// Without it, :json shows (string) "{\n  \"name\": \"Ada\"\n}" — the value
	// printer is correct and useless, because the whole point of the command
	// is the formatting inside the string.
	Text bool
	// Query says the answer comes from the project's configured database, so
	// gluon runs it through the same path :query uses instead of through
	// Rewrite.
	//
	// A plugin cannot reach the config, the resolved connection or the
	// evaluator — Rewrite sees its argument and nothing else — and that is
	// deliberate: it is what keeps a plugin command unable to do anything a
	// typed line could not. A command that must open the project's database
	// therefore declares its statement here and lets gluon own the connection,
	// so invariants 24 and 25 keep exactly one implementation rather than one
	// per plugin that wants a row back.
	//
	// A command with a Query has no Rewrite. The two are the two ways a
	// command can be answered, and no command is answered both ways.
	Query *DBQuery
	// Live says the answer comes from a resource that is dialled, so gluon
	// runs it through EvalLive rather than EvalTransient and owns the
	// environment the child reads.
	//
	// It exists for the reason Query does, and the reason :http is a builtin
	// rather than a plugin command: Rewrite sees its argument and nothing
	// else. It cannot look an environment variable up to find out whether it
	// is set, it cannot name the values that must not appear in what the child
	// prints, and it cannot reach the evaluator to keep the answer out of a
	// cache keyed on program text alone. A command that dials therefore
	// declares what it needs and gluon resolves it, so invariants 24 and 25
	// keep exactly one implementation rather than one per plugin that wants a
	// socket.
	//
	// A command with a Live has no Rewrite and no Query. The three are the
	// three ways a command can be answered, and no command is answered two
	// ways.
	Live *LiveCall
	// Redact says the answer is a `key = value` listing whose values may carry
	// secrets, so gluon applies its shape-based redactor before display.
	//
	// It is a declaration rather than something the plugin does, deliberately.
	// A Rewrite that redacted would have to generate Go that inspects strings:
	// fragile, invisible to :src, and a second definition of "secret" that
	// nothing tests against real connection strings. The one definition lives
	// in internal/dsn, and this is how a command asks for it. Only Text
	// commands can carry it — there is nothing to redact in a rendered value.
	Redact bool
	// Lang names the language a Text command's answer is written in, so a
	// terminal can colour it. ":sql" answers in SQL, ":json" in JSON.
	//
	// It changes nothing else. Result.Out is the same bytes with it and
	// without, so a pipe, gluon -e, the -json envelopes and the MCP tool made
	// from this command all see exactly what they saw before. Only a Text
	// command may set one: a rendered value is already coloured by its own
	// renderer, and a table of them is not source.
	Lang syntax.Lang
}

// A DBQuery is a statement gluon runs against the project's own database on a
// plugin's behalf.
//
// The statement is declared, not built: it takes no argument and nothing the
// user types reaches it, so the whole of what runs is written here in Go by the
// plugin that knows the schema. What the user can say is which database — and
// never a connection string, which is what `-dsn` and `:db connect <url>` are
// both permanently rejected for.
type DBQuery struct {
	// Table is what the statement reads. It is declared separately from SQL
	// because a scan failure has to name it: a tracking schema that has moved
	// on surfaces as a driver error about a column, and the diagnosis is one
	// step only if the report says which table was being read.
	Table string
	// SQL is the statement. It passes db.CheckStatement with the write flag
	// unset, so the read-only guarantee is the allowlist that already exists
	// rather than a second promise made here.
	SQL string
	// Empty is what to say when the statement succeeds and returns no rows.
	// "no rows" is true and useless — for a migration table it means either
	// nothing has ever been applied or the table belongs to another tool, and
	// the plugin is the only thing that knows which sentence to write.
	Empty string
}

// A LiveCall is a command answered by dialling something.
//
// The split between its two halves is the split :query and :http already run
// on: the child does what only the child can do — it holds the library, so it
// speaks the protocol — and gluon formats what came back. Neither half is in
// the other's process, which is what keeps the generated program readable in
// :src and keeps the layout out of generated Go.
type LiveCall struct {
	// Plan turns the argument into the program to run. Returning an error is
	// how the command rejects its argument, before anything is dialled.
	Plan func(arg string) (Call, error)
}

// A Call is the program a Plan produced, and what running it needs.
type Call struct {
	// Source is the Go expression to evaluate, exactly as a Rewrite's is: the
	// command still has the authority of a line the user could have typed.
	Source string
	// Imports are what Source names. They are supplied rather than left to
	// goimports, which costs ~135ms, and exact because an import nothing names
	// does not compile.
	Imports []Import
	// Refs are the environment variables Source reads by name.
	//
	// gluon resolves them in its own process, hands them to the child alone,
	// and masks their values in what the child prints. A variable that is not
	// set is reported rather than passed through empty — an empty credential
	// reads at the far end as a wrong one, which sends somebody to rotate a
	// secret that was fine.
	Refs []string
	// Report turns what the child printed into the answer.
	//
	// It belongs to the plan rather than to the command because it needs what
	// the plan read — which form ran, and what was named — and carrying that
	// across the evaluation some other way would be a second parse that can
	// disagree with the first.
	//
	// It runs in gluon's process, after the child's output has been masked, so
	// a value the plan named as a secret is already *** by the time a plugin
	// sees it. A nil Report means the value the program evaluated to is the
	// whole answer.
	Report func(out string, vals []pretty.Value) Answer
}

// An Answer is what a Report decided to show.
type Answer struct {
	// Text is what to print instead of the value. Empty means the value is the
	// answer, and gluon renders it the way it renders any other — so the
	// renderers that apply to a value anywhere apply to this one too.
	Text string
	// Failed marks the answer an error.
	Failed bool
}

// A Render formats one type. Type is the reflect.Type.String() form the child
// encoder emits — "*http.Response", "time.Duration", "uuid.UUID".
//
// Either form returns false to decline, and the value falls through to gluon's
// own renderer unchanged. That is what makes a plugin unable to break output it
// does not understand — and it is also how a formatter says "I have nothing to
// add here", which is why the duration renderer declines on "3s".
//
// The two forms exist because the two positions are different:
//
//   - Rich renders a value that owns its line. It supplies its own type prefix
//     and may take several lines, which is how a response draws a header table.
//   - Inline renders one inside a table cell — a field of a struct, an element
//     of a slice. The surrounding structure has already said what the type is,
//     so Inline supplies the value alone, on one line.
//
// A plugin with only Rich is not consulted for cells, so a []time.Duration
// would show its elements plainly. Supplying both is what makes a value read
// the same wherever it appears.
//
// There is deliberately no Plain counterpart to either. pretty.Plain and the
// -json envelopes are compatibility surfaces that pipes, `gluon -e`, justfiles
// and tests read; a plugin that could change them could break a script by being
// installed. Invariant 21.
type Render struct {
	Type   string
	Rich   func(v pretty.Value, st pretty.Styles) (string, bool)
	Inline func(v pretty.Value, st pretty.Styles) (string, bool)
}

// Set is the plugins gluon knows about, and which of them are active.
type Set struct {
	all []Plugin
	// active is recomputed only when the build list changes — :get and :use.
	active []Plugin
	why    map[string]string
	// conflicts records commands two active plugins both claimed.
	conflicts []Conflict
}

// NewSet builds a set from every plugin gluon ships with plus any loaded from
// config. Nothing is active until Activate runs.
func NewSet(all []Plugin) *Set {
	return &Set{all: all, why: map[string]string{}}
}

// All is every known plugin, active or not.
func (s *Set) All() []Plugin { return s.all }

// Active is the plugins whose module the session can actually see.
func (s *Set) Active() []Plugin { return s.active }

// Why explains a plugin's state, for :plugins and gluon doctor.
func (s *Set) Why(name string) string { return s.why[name] }

// Activate decides which plugins apply.
//
// A stdlib plugin is always active. A module plugin is active when its module
// is in the session's build list — what `:get` added, or what the attached host
// requires. Anything else contributes only its alias and its guide, so a
// session with no gorm has no :sql rather than a command that cannot work.
//
// disabled names are never activated, whatever the build list says.
func (s *Set) Activate(requires []string, disabled map[string]bool) {
	have := make(map[string]bool, len(requires))
	for _, r := range requires {
		// Requirements arrive as "path v1.2.3"; the path is what matters.
		if p, _, ok := strings.Cut(r, " "); ok {
			have[p] = true
		} else {
			have[r] = true
		}
	}

	s.active = s.active[:0]
	for _, p := range s.all {
		m := p.Meta()
		switch {
		case disabled[m.Name]:
			s.why[m.Name] = "disabled in config"
		case m.Module == "":
			s.why[m.Name] = "standard library — always active"
			s.active = append(s.active, p)
		case moduleIn(have, m.Module):
			s.why[m.Name] = m.Module + " is in the build list"
			s.active = append(s.active, p)
		default:
			s.why[m.Name] = "not active — :get " + m.Module + " to use it"
		}
	}
}

// moduleIn reports whether the build list has the module, or something under
// it. A plugin for gorm.io/gorm should activate on gorm.io/gorm, and one named
// for a module whose driver lives beneath it should too.
func moduleIn(have map[string]bool, module string) bool {
	if have[module] {
		return true
	}
	for p := range have {
		if strings.HasPrefix(p, module+"/") {
			return true
		}
	}
	return false
}

// Imports is every active plugin's preloads.
func (s *Set) Imports() []Import {
	var out []Import
	for _, p := range s.active {
		if im, ok := p.(Importer); ok {
			out = append(out, im.Imports()...)
		}
	}
	return out
}

// Commands is every active plugin's commands, each tagged with the plugin that
// supplied it.
//
// Two plugins can want the same name — three web frameworks all want :routes —
// so the first active registration keeps it and the rest are recorded as
// conflicts. First-wins matches Renders, and the registry order is declared, so
// the outcome is the same on every run. Silently dropping the loser would leave
// someone typing a command that belongs to a framework they are not using.
func (s *Set) Commands() []TaggedCommand {
	var out []TaggedCommand
	taken := map[string]string{}
	s.conflicts = s.conflicts[:0]
	for _, p := range s.active {
		cm, ok := p.(Commander)
		if !ok {
			continue
		}
		name := p.Meta().Name
		for _, c := range cm.Commands() {
			if owner, dup := taken[c.Name]; dup {
				s.conflicts = append(s.conflicts,
					Conflict{Command: c.Name, Kept: owner, Dropped: name})
				continue
			}
			taken[c.Name] = name
			out = append(out, TaggedCommand{Plugin: name, Command: c})
		}
	}
	return out
}

// Conflict is a command two active plugins both wanted.
type Conflict struct{ Command, Kept, Dropped string }

// Conflicts is what the last call to Commands had to resolve.
func (s *Set) Conflicts() []Conflict { return s.conflicts }

// TaggedCommand is a command and the plugin it came from, so :help and an
// error message can say which plugin is responsible.
type TaggedCommand struct {
	Plugin string
	Command
}

// Renders is every active plugin's formatters, indexed by type name. A later
// plugin does not silently win: the first registration for a type keeps it, so
// the order in the registry is the tie-break and it is stable.
func (s *Set) Renders() map[string]Render {
	out := map[string]Render{}
	for _, p := range s.active {
		r, ok := p.(Renderer)
		if !ok {
			continue
		}
		for _, rd := range r.Renders() {
			if _, taken := out[rd.Type]; !taken {
				out[rd.Type] = rd
			}
		}
	}
	return out
}

// Aliases is every known plugin's `:get` shortcuts — known, not active,
// because the whole point of `:get uuid` is to reach a module the session does
// not have yet.
func (s *Set) Aliases() map[string]string {
	out := map[string]string{}
	for _, p := range s.all {
		a, ok := p.(Aliaser)
		if !ok {
			continue
		}
		for _, al := range a.Aliases() {
			if _, taken := out[al.Name]; !taken {
				out[al.Name] = al.Module
			}
		}
	}
	return out
}

// Guide is a plugin's cheatsheet, by name. A plugin that implements Guider and
// has nothing to say ships no guide: every file-backed plugin is a Guider,
// because its Go type has to be, and reporting an empty one as present printed
// nothing and called it an answer.
func (s *Set) Guide(name string) (string, bool) {
	for _, p := range s.all {
		if p.Meta().Name != name {
			continue
		}
		if g, ok := p.(Guider); ok {
			text := g.Guide()
			return text, text != ""
		}
		return "", false
	}
	return "", false
}

// Known reports whether any plugin, active or not, has the name — so a guide
// that is missing and a plugin that does not exist are two different answers.
func (s *Set) Known(name string) bool {
	for _, p := range s.all {
		if p.Meta().Name == name {
			return true
		}
	}
	return false
}

// Guides is the name of every plugin with a guide, active or not, without
// reading a guide file: completion asks this on a keystroke.
func (s *Set) Guides() []string {
	var out []string
	for _, p := range s.all {
		g, ok := p.(Guider)
		if !ok {
			continue
		}
		if h, ok := g.(interface{ HasGuide() bool }); ok && !h.HasGuide() {
			continue
		}
		out = append(out, p.Meta().Name)
	}
	return out
}
