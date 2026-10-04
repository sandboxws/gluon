// Package cmdspec is how a meta command says what it takes: what kind of
// argument it reads, its operands, its flags, and lines that use them.
//
// It exists because the answer used to live in four places that did not know
// about each other. The one-line Arg string on the registry was the only
// structured part, and it could not hold flags — :http's -H, -d and -t were in
// a paragraph of prose and in one error message, and nowhere a completion, a
// hint or a docs page could read them. A Spec is that knowledge, declared once
// beside the command, and :help, --help, completion, the usage hint, the MCP
// tool descriptions and the generated docs all read it.
//
// It is a leaf package on purpose. internal/repl owns the command registry and
// internal/plugin owns the plugin command type; both declare a Spec, and neither
// may import the other.
//
// A Spec describes a grammar; it does not parse one. Every command keeps its own
// parser, because their edges are deliberate — flags that must lead so that
// `-nums` stays a negation, flags that follow the operands, flags that are one
// mode of several — and a shared parser would have to reproduce all of them.
// Tests hold each parser to what its Spec declares instead.
package cmdspec

import "strings"

// Kind is what a command's argument is. It decides how the argument is
// painted, whether completion hands it to the Go paths, and which help tokens
// could be mistaken for it.
type Kind uint8

const (
	// GoExpr is an expression. It is the zero value, so a command that declares
	// nothing behaves exactly as every command did before: painted and completed
	// as Go, and `-h` read as the negation of h.
	GoExpr Kind = iota
	// GoName is a type, a function or a symbol. It is Go for painting and
	// completion, and no Go name begins with a minus, so -h cannot be one.
	GoName
	// Words is names, paths, URLs and switches. It is painted plain, and only
	// its declared values are ever completed.
	Words
	// SQL is a statement, painted as SQL and never completed.
	SQL
	// NoArg takes nothing.
	NoArg
)

var kindNames = [...]string{
	GoExpr: "a Go expression",
	GoName: "a Go name",
	Words:  "words",
	SQL:    "SQL",
	NoArg:  "nothing",
}

// String is how :help and the docs name the kind.
func (k Kind) String() string {
	if int(k) < len(kindNames) {
		return kindNames[k]
	}
	return "an argument"
}

// IsGo reports whether the argument is Go source.
func (k Kind) IsGo() bool { return k == GoExpr || k == GoName }

// Place is where a command reads its flags.
type Place uint8

const (
	// Lead reads flags only while they lead. Everything from the first word
	// that is not one of them is the operand, verbatim — which is how
	// `:bench -nums` stays a benchmark of the negation of nums.
	Lead Place = iota
	// After reads flags once the operands are given: :http GET <url> -H ….
	After
	// Anywhere reads a flag wherever it appears.
	Anywhere
)

// A Spec is what one command takes.
type Spec struct {
	Kind    Kind
	FlagsAt Place
	// Params are the operands, in order. Empty means they are read off the
	// command's Arg by FromArg, which is enough for a command whose operands
	// have nothing to complete and nothing to explain.
	Params []Param
	Flags  []Flag
	// Synopsis replaces the long form Line builds, for a grammar Line cannot
	// say: `:settings key value` and `:settings key=value` are the same
	// request. It is written without the command's name.
	Synopsis string
	// Examples are lines to type, the command included. Together they use every
	// visible flag, which is what makes them evidence rather than decoration:
	// the tests parse each one with the command's own parser.
	Examples []Example
	// See names neighbouring commands, colons included.
	See []string
}

// A Param is one operand.
type Param struct {
	Name     string
	Optional bool
	// Repeat says more of the same may follow.
	Repeat bool
	// Sep is what joins this operand to the one before it. Empty is a space;
	// "," is a comma inside one argument, the way :impl <t>, <i> and
	// :bench a, b take two Go operands.
	Sep    string
	Values Values
	Help   string
}

// A Flag is one switch, or one option with a value.
type Flag struct {
	// Name is the flag as typed, with its dash: "-H".
	Name string
	// Value is the placeholder for the flag's value, "" for a switch.
	Value  string
	Values Values
	// Help is one line, and says what the flag does rather than restating
	// its name.
	Help string
	// Repeat says the flag may be given more than once.
	Repeat bool
	// Mode says the flag is one of several ways to ask, and at most one is
	// given: :doc -src and :doc -url are two questions, not two adjustments.
	Mode bool
	// Alone says the flag is the whole argument: :use -off takes no directory.
	Alone bool
	// Hidden flags are accepted and shown nowhere: the confirmation a view
	// submits on the reader's behalf is not something to type.
	Hidden bool
	// Runs says the flag builds or runs code. A static MCP tool never builds,
	// so it cannot honour one.
	Runs bool
}

// An Example is a line to type and what it shows.
type Example struct {
	// Line is exactly as typed, the command included.
	Line string
	// Says is what the line shows, when the line does not say it itself.
	Says string
}

// Values is what an operand or a flag's value may be.
type Values struct {
	// Fixed is a closed set, offered as written.
	Fixed []string
	// Source is a set only a session knows: the plugins, the scratchpads.
	Source Source
	// Fold says the parser accepts any case, so completion may offer the case
	// being typed.
	Fold bool
}

// Source names a set of values only a running session can list. The constants
// are written as the phrase :help and the docs print for them.
type Source string

const (
	Commands      Source = "a command — :help lists them"
	Plugins       Source = "a plugin — :plugins lists them"
	Guides        Source = "a plugin that ships a guide"
	Themes        Source = "a theme — :theme lists them"
	Settings      Source = "a setting — :settings lists them"
	SettingValues Source = "a value that setting takes"
	Pads          Source = "a scratchpad — :scratch lists them"
	Bookmarks     Source = "a snapshot — :bookmark lists them"
	Pinned        Source = "a pinned entry — :pin lists them"
	Databases     Source = "a configured database — :db lists them"
	Releases      Source = "a Go release — :since lists them"
	Modules       Source = "a module the session requires — :get lists them"
	GetAliases    Source = "a module, or a plugin's short name for one"
	Entries       Source = "an entry number — :hist lists them"
)

// AsksForHelp reports whether arg, a command's whole argument, is a request for
// the command's help rather than an argument to it.
//
// --help and ? are never Go and never SQL: Go has no prefix decrement, and ? is
// not an operator. -h and -help are the negation of a variable named h or help,
// so for a command that takes an expression they stay what they are. Deciding
// by asking the checker whether h is bound was rejected: invariant 5 keeps the
// type checker from deciding what runs.
func AsksForHelp(k Kind, arg string) bool {
	switch strings.TrimSpace(arg) {
	case "--help", "?":
		return true
	case "-h", "-help":
		return k != GoExpr
	}
	return false
}

// Operands is the declared operands, or the ones Arg spells when none are.
func (s Spec) Operands(arg string) []Param {
	if len(s.Params) > 0 {
		return s.Params
	}
	return FromArg(arg).Params
}

// Visible is the flags a reader is shown: every one but the Hidden.
func (s Spec) Visible() []Flag {
	var out []Flag
	for _, f := range s.Flags {
		if !f.Hidden {
			out = append(out, f)
		}
	}
	return out
}

// Flag finds a declared flag by name, Hidden ones included.
func (s Spec) Flag(name string) (Flag, bool) {
	for _, f := range s.Flags {
		if f.Name == name {
			return f, true
		}
	}
	return Flag{}, false
}

// Line is the long synopsis: the command, its operands and every visible flag.
//
// It is the long form of Arg, not a replacement for it. Arg stays short because
// :help's one-line listing is aligned on it, and because other things read it:
// a `<` in Arg is what makes an MCP tool's argument required.
func (s Spec) Line(name, arg string) string {
	if s.Synopsis != "" {
		return name + " " + s.Synopsis
	}
	ops := operandSynopsis(s.Operands(arg))
	flags := flagSynopsis(s.Visible())
	parts := []string{name}
	if s.FlagsAt == After {
		parts = append(parts, ops, flags)
	} else {
		parts = append(parts, flags, ops)
	}
	var out []string
	for _, p := range parts {
		if p != "" {
			out = append(out, p)
		}
	}
	return strings.Join(out, " ")
}

// operandSynopsis writes operands the way Arg does: <required> and [optional],
// with a comma-joined operand attached to the one before it — <t>, <i> and
// <exp>[, <exp>].
func operandSynopsis(ps []Param) string {
	var b strings.Builder
	for i, p := range ps {
		more := ""
		if p.Repeat {
			more = "..."
		}
		switch {
		case p.Optional && p.Sep != "":
			// The separator belongs to the optional part: <exp>[, <exp>].
			b.WriteString("[" + p.Sep + " <" + p.Name + ">" + more + "]")
			continue
		case p.Sep != "":
			b.WriteString(p.Sep + " ")
		case i > 0:
			b.WriteString(" ")
		}
		if p.Optional {
			b.WriteString("[" + p.Name + more + "]")
		} else {
			b.WriteString("<" + p.Name + ">" + more)
		}
	}
	return b.String()
}

// flagSynopsis writes the flags: the Mode flags as one choice, the rest one
// bracket each, in the order they were declared.
func flagSynopsis(fs []Flag) string {
	var parts []string
	var modes []string
	modeAt := -1
	for _, f := range fs {
		text := f.Name
		if f.Value != "" {
			text += " <" + f.Value + ">"
		}
		if f.Mode {
			if modeAt < 0 {
				modeAt = len(parts)
				parts = append(parts, "")
			}
			modes = append(modes, text)
			continue
		}
		text = "[" + text + "]"
		if f.Repeat {
			text += "..."
		}
		parts = append(parts, text)
	}
	if modeAt >= 0 {
		parts[modeAt] = "[" + strings.Join(modes, " | ") + "]"
	}
	return strings.Join(parts, " ")
}

// FromArg reads the operands and flags an Arg string spells: "<t>, <i>",
// "[-n k] <e>", "a[,b]", "[dir|-off]".
//
// It is the fallback for a command that declares no operands, and it is how a
// user's TOML plugin gets a synopsis without writing one. Placeholders for
// flags declared elsewhere — "[flags]", "[flag|name]" — contribute nothing
// but the operand beside them.
func FromArg(arg string) Spec {
	var s Spec
	sep := ""
	for _, seg := range segments(arg) {
		switch {
		case seg == ",":
			sep = ","
			continue
		case strings.HasPrefix(seg, "[") && strings.HasSuffix(seg, "]"):
			inner := strings.TrimSpace(seg[1 : len(seg)-1])
			if strings.HasPrefix(inner, ",") {
				sep, inner = ",", strings.TrimSpace(inner[1:])
			}
			fromOptional(&s, inner, sep)
		case strings.HasPrefix(seg, "<") && strings.HasSuffix(seg, ">"):
			s.Params = append(s.Params, Param{Name: seg[1 : len(seg)-1], Sep: sep})
		default:
			s.Params = append(s.Params, Param{Name: seg, Sep: sep})
		}
		sep = ""
	}
	return s
}

// fromOptional reads one [bracketed] segment.
func fromOptional(s *Spec, inner, sep string) {
	alts := strings.Split(inner, "|")
	flagsOnly := true
	for _, a := range alts {
		if !strings.HasPrefix(strings.TrimSpace(a), "-") {
			flagsOnly = false
		}
	}
	if flagsOnly {
		// [-v|-d] is a choice of modes; [-n k] is one flag with a value.
		mode := len(alts) > 1
		for _, a := range alts {
			name, value, _ := strings.Cut(strings.TrimSpace(a), " ")
			s.Flags = append(s.Flags, Flag{Name: name, Value: strings.Trim(value, "<>"), Mode: mode})
		}
		return
	}
	var words []string
	for _, a := range alts {
		a = strings.TrimSpace(a)
		switch {
		case strings.HasPrefix(a, "-"):
			s.Flags = append(s.Flags, Flag{Name: a, Mode: true, Alone: true})
		case a == "flag" || a == "flags":
			// A placeholder for the flags declared elsewhere.
		default:
			words = append(words, strings.Trim(a, "<>"))
		}
	}
	if len(words) == 0 {
		return
	}
	p := Param{Name: strings.Join(words, "|"), Optional: true, Sep: sep}
	if len(words) > 1 {
		// [on|off] is a closed set, and its members are what to type.
		p.Values.Fixed = words
	}
	s.Params = append(s.Params, p)
}

// segments splits an Arg string into bracketed groups, <placeholders>, commas
// and bare words.
func segments(arg string) []string {
	var out []string
	for i := 0; i < len(arg); {
		switch c := arg[i]; {
		case c == ' ' || c == '\t':
			i++
		case c == ',':
			out = append(out, ",")
			i++
		case c == '[' || c == '<':
			closer := byte(']')
			if c == '<' {
				closer = '>'
			}
			j := strings.IndexByte(arg[i:], closer)
			if j < 0 {
				out = append(out, arg[i:])
				return out
			}
			out = append(out, arg[i:i+j+1])
			i += j + 1
		default:
			j := i
			for j < len(arg) && !strings.ContainsRune(" \t,[<", rune(arg[j])) {
				j++
			}
			out = append(out, arg[i:j])
			i = j
		}
	}
	return out
}
