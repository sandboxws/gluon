package config

import (
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/sandboxws/gluon/internal/gluonrt"
	"github.com/sandboxws/gluon/internal/pretty"
	"github.com/sandboxws/gluon/internal/theme"
)

// An Option is one setting gluon has, defined once.
//
// Before this, a setting lived in four places that did not reference each
// other: the field on Config, the check in validate, the row `gluon doctor`
// prints, and the paragraph in the README. Three of those fail silently — a
// setting missing from doctor is undiagnosable, and one missing from the README
// is undiscoverable. This is the single place, and the tests below assert that
// the registry and the struct cannot drift apart. It is the argument
// internal/repl/command.go makes about commands, applied to settings.
type Option struct {
	// Key is how :settings names it: "timeout", "theme.name", "value.form".
	Key string
	// Table is the TOML table the value lives under, "" for the top level;
	// Name is the bare key inside it. Together they are the line the writer
	// edits.
	Table, Name string

	// Summary is one line, and it is a column in a table — keep it short.
	// TestOptionSummariesFit pins the width it has to live in.
	Summary string
	// Detail is the longer form, for `:settings <key>`.
	Detail string

	// Default is what gluon does when this is unset, as prose: "30s", or
	// "$VISUAL, $EDITOR, then nvim/vim/vi". Never a value to write.
	Default string
	// Allowed describes an open set in one line: "a Go duration".
	Allowed string
	// Values is the closed set, when there is one. A func rather than a slice
	// because the theme names are a fact about a directory that changes
	// independently of this file — the same reason validate checks a theme
	// name's shape and not its existence.
	Values func() []string
	// About is the line one member of that set describes itself by, for a
	// reader choosing between them rather than checking a name. It lives here
	// for the reason everything else about a setting does: a chooser that
	// carried its own descriptions would be a second registry, and the one
	// that went stale would be the one nobody is testing. Empty is fine —
	// a value whose name is the whole story needs no second line.
	About func(value string) string
	// Sample is a valid value that differs from the default. It is the `e.g.`
	// :settings prints and the input to the round-trip test, so a row that
	// cannot actually be set fails a test rather than only failing a user.
	Sample string

	// Get reads the value out of a loaded Config. It is what makes a write
	// verifiable: after the edit the file is parsed again and this must return
	// what was asked for.
	Get func(*Config) string
	// Apply sets it on an in-memory Config, so the running session agrees with
	// the file without a reload.
	Apply func(*Config, string)
	// Validate is the check LoadFile would make, run early so the message
	// names the setting rather than the file.
	Validate func(string) error

	// Effect says when a change is in force.
	Effect Effect
	// Kind separates what a scalar setter can write from what it cannot.
	Kind Kind
	// Why is filled only when Kind is not Scalar: the reason the setting is
	// listed and not settable. A key :settings claims does not exist is worse
	// than one it refuses with a reason.
	Why string
	// Family marks a key whose Name may be any member of Values(): the sixteen
	// theme roles are one row in the table and sixteen keys to Lookup.
	Family bool
	// Note is a caveat that is true right now — the environment variable
	// currently beating this setting, say.
	Note func(*Config) string
}

// Effect is when a change takes hold. A setting that quietly needed a restart
// would be the same failure as one that quietly did nothing.
type Effect int

const (
	effectUnset Effect = iota
	// Live: in force on the next line of this session.
	Live
	// NextRun: read fresh by whatever reads it next — an editor launch, a
	// `gluon run` — which is a new process either way.
	NextRun
)

func (e Effect) String() string {
	switch e {
	case Live:
		return "this session"
	case NextRun:
		return "the next run"
	}
	return ""
}

// Kind separates a setting one line can hold from one it cannot.
type Kind int

const (
	kindUnset Kind = iota
	// Scalar is one TOML string; :settings writes it.
	Scalar
	// List is an array: listed, never written.
	List
	// TableMap is a map of tables: listed, never written.
	TableMap
)

// Options is every setting gluon has, in the order a config.toml reads.
//
// The array and table-of-tables settings are here too, with a Why rather than a
// writer. A key :settings did not mention would look like a key gluon does not
// have; one it lists and refuses with a reason is a question answered.
func Options() []Option {
	opts := []Option{
		{
			Key: "imports", Name: "imports", Kind: List, Effect: Live,
			Summary: "imports a line may name without a goimports pass",
			Detail: "A preloaded import is not written into every program: it is a name-to-path\n" +
				"map consulted only when a line actually names the qualifier. So one that\n" +
				"goes unused costs nothing, and one that is used skips ~135ms.",
			Default: "none", Allowed: "import paths",
			Why: "an array — :settings writes one line, and rewriting a list wholesale takes " +
				"the comments inside it. Edit config.toml, or use :get",
			Get: func(c *Config) string { return plural(len(c.Imports), "import") },
		},
		{
			Key: "editor", Name: "editor", Kind: Scalar, Effect: NextRun,
			Summary: "what :edit opens, over $VISUAL and $EDITOR",
			Detail: "Both of those are commonly unset, which is the only reason this exists.\n\n" +
				"builtin is the one value that names no program: gluon's own\n" +
				"full-screen editor, with vim keys, a check key and a key that\n" +
				"runs what you wrote without closing it. It is read from here\n" +
				"alone — an exported $EDITOR does not veto it, because whether\n" +
				"to launch a program is not a question anyone answers with an\n" +
				"environment variable.",
			Default: "$VISUAL, then $EDITOR, then a terminal editor on $PATH",
			Allowed: "a command with its flags, or builtin", Sample: "code --wait",
			Get:   func(c *Config) string { return c.Editor },
			Apply: func(c *Config, v string) { c.Editor = expandPath(v) },
			Note: func(c *Config) string {
				if c.Editor == "" {
					return ""
				}
				for _, env := range []string{"VISUAL", "EDITOR"} {
					if os.Getenv(env) != "" {
						return "$" + env + " is set, and this wins over it"
					}
				}
				return ""
			},
		},
		{
			Key: "timeout", Name: "timeout", Kind: Scalar, Effect: Live,
			Summary: "how long one evaluation may take",
			Detail: "The whole session replays on every line, so this bounds the replay and not\n" +
				"just the newest expression.",
			Default: "30s", Allowed: "a Go duration, positive", Sample: "2m",
			Get:   func(c *Config) string { return c.Timeout },
			Apply: func(c *Config, v string) { c.Timeout = v },
			Validate: func(v string) error {
				d, err := time.ParseDuration(v)
				if err != nil {
					return fmt.Errorf("%q: %w", v, err)
				}
				if d <= 0 {
					return fmt.Errorf("%q must be positive", v)
				}
				return nil
			},
		},
		{
			Key: "banner", Name: "banner", Kind: Scalar, Effect: NextRun,
			Summary: "how much the startup screen says",
			Detail: "full is the wordmark, the version beside it, and a row for each thing the\n" +
				"session starts with: the scratchpad it landed in, the module it attached to,\n" +
				"the database it can see, the plugins this project's build list activated. A\n" +
				"row appears only where there is something to say, so a bare directory on\n" +
				"defaults shows one row and a project shows as many as it has facts.\n\n" +
				"compact is the version and the toolchain on one line, and the hints line\n" +
				"under it. off prints neither.\n\n" +
				"Below sixty columns the wordmark is dropped on its own, because it is a\n" +
				"picture and a picture that wraps is torn in half. The rows stay: they are\n" +
				"the half that is about this session, and they are thirty columns wide.\n\n" +
				"A scratchpad that could not be opened is reported whatever this says. A\n" +
				"session that believes it is being saved and is not is the failure that whole\n" +
				"feature exists to avoid, and it is not decoration to turn down.\n\n" +
				"Only an interactive session has one. A piped script, gluon -e and the MCP\n" +
				"server print none of this whatever this says.",
			Default: BannerFull, Allowed: strings.Join(BannerForms(), ", "),
			Values: BannerForms, About: bannerAbout, Sample: BannerCompact,
			Get:   func(c *Config) string { return c.Banner },
			Apply: func(c *Config, v string) { c.Banner = v },
			Validate: func(v string) error {
				_, err := ParseBanner(v)
				return err
			},
		},
		{
			Key: "hosts", Name: "hosts", Kind: TableMap, Effect: Live,
			Summary: "per-module imports the host's own index cannot offer",
			Default: "none", Allowed: "a table per module path",
			Why: "a table per module path, each holding a list — neither is one line",
			Get: func(c *Config) string { return plural(len(c.Hosts), "host") },
		},
		{
			Key: "database", Name: "database", Kind: List, Effect: Live,
			Summary: "how to reach a project's database, not its password",
			Default: "none", Allowed: "[[database]] blocks",
			Why: "an array of tables — `gluon init` writes one, and :db reports what it found",
			Get: func(c *Config) string { return plural(len(c.Databases), "database") },
		},
		{
			Key: "input.mode", Table: "input", Name: "mode", Kind: Scalar, Effect: Live,
			Summary: "how the prompt reads keys",
			Detail: "emacs is the readline subset gluon has always bound — ctrl-a, ctrl-e,\n" +
				"alt-f, alt-b. vim adds a normal mode: esc commands, i inserts, and the\n" +
				"motions, operators, counts and registers that go with them.\n\n" +
				"In vim mode k and j walk history, because a one-line prompt has no line\n" +
				"above; ctrl-r is redo in normal mode and reverse search in insert; and u\n" +
				"undoes your typing, where :undo drops a session entry.\n\n" +
				"The prompt says which mode it is in — gluon[i]> while you type and\n" +
				"gluon[n]> while you command, in the colour theme.mode gives it.\n\n" +
				"It changes which keystrokes produce which line and nothing else. A pipe,\n" +
				"`gluon -e` and the MCP server have no keyboard and are unaffected.",
			Default: InputEmacs, Allowed: strings.Join(InputModes(), ", "),
			Values: InputModes, About: inputModeAbout, Sample: InputVim,
			Get:   func(c *Config) string { return c.Input.Mode },
			Apply: func(c *Config, v string) { c.Input.Mode = v },
			Validate: func(v string) error {
				_, err := ParseInputMode(v)
				return err
			},
		},
		{
			Key: "value.form", Table: "value.form", Name: "default", Kind: Scalar, Effect: Live,
			Summary: "the shape a value is drawn in",
			Detail: "table is the bordered box gluon has always drawn. The others answer shapes\n" +
				"it serves badly: columns gives a list of structs a column per field, tree\n" +
				"recurses where a table cell truncates, line keeps a small value on its line,\n" +
				"and literal is source you could paste into a test.\n\n" +
				"It changes what a terminal draws and nothing else: a pipe, `gluon -e` and the\n" +
				"-json envelopes are frozen surfaces and never see a form.",
			Default: pretty.FormTable.String(), Allowed: strings.Join(pretty.FormNames(), ", "),
			Values: pretty.FormNames, About: formAbout, Sample: pretty.FormTree.String(),
			Get:   func(c *Config) string { return c.Value.DefaultForm },
			Apply: func(c *Config, v string) { c.Value.DefaultForm = v },
			Validate: func(v string) error {
				_, err := pretty.ParseForm(v)
				return err
			},
		},
		{
			Key: "value.items", Table: "value", Name: "items", Kind: Scalar, Effect: Live,
			Summary: "how many elements of a collection are described",
			Detail: "The child describes this many elements of one collection and reports how\n" +
				"many it left out; every renderer already draws the shortfall. Raise it to\n" +
				"see a long slice or map whole, rather than slicing it by hand.\n\n" +
				"A separate budget bounds the whole value — the thing that keeps one\n" +
				"*http.Request from becoming a dump of its transport — and this does not\n" +
				"move it. A large collection of large elements can still stop short of\n" +
				"this number.\n\n" +
				"It reaches the child in its environment, so a shell that already exports\n" +
				"GLUON_MAX_ITEMS sets it for a session that never asked.",
			Default: strconv.Itoa(gluonrt.DefaultMaxItems),
			Allowed: "a whole number, 1 to " + strconv.Itoa(MaxValueLimit),
			Sample:  "500",
			Get:     func(c *Config) string { return c.Value.Items },
			Apply:   func(c *Config, v string) { c.Value.Items = v },
			Validate: func(v string) error {
				_, err := ParseValueLimit(v)
				return err
			},
			Note: envNote(gluonrt.EnvMaxItems),
		},
		{
			Key: "value.depth", Table: "value", Name: "depth", Kind: Scalar, Effect: Live,
			Summary: "how many levels of a value are described",
			Detail: "How far into a nested value the child describes structure before falling\n" +
				"back to a flat line. A pointer unwrap does not consume a level, so this\n" +
				"counts composites and not indirections.\n\n" +
				"A separate budget bounds the whole value and this does not move it, so a\n" +
				"deep value may still flatten before this number. Cycles are detected\n" +
				"independently, so raising it cannot turn a linked list into nonsense.\n\n" +
				"It reaches the child in its environment, so a shell that already exports\n" +
				"GLUON_MAX_DEPTH sets it for a session that never asked.",
			Default: strconv.Itoa(gluonrt.DefaultMaxDepth),
			Allowed: "a whole number, 1 to " + strconv.Itoa(MaxValueLimit),
			Sample:  "10",
			Get:     func(c *Config) string { return c.Value.Depth },
			Apply:   func(c *Config, v string) { c.Value.Depth = v },
			Validate: func(v string) error {
				_, err := ParseValueLimit(v)
				return err
			},
			Note: envNote(gluonrt.EnvMaxDepth),
		},
	}

	// One row per kind that may carry a form of its own, generated from the
	// same list the renderer dispatches on — a kind added there is a setting
	// here without anyone remembering to add it.
	for _, kind := range pretty.FormKinds() {
		opts = append(opts, Option{
			Key: "value.form." + kind, Table: "value.form", Name: kind,
			Kind: Scalar, Effect: Live,
			Summary: "the form a " + kind + " takes, over value.form",
			Detail: "Applies at the top level only. What a value nested inside a table cell or\n" +
				"a tree node gets is decided by the form drawing it, not by a second lookup.",
			Default: "whatever value.form says",
			Allowed: strings.Join(pretty.FormNames(), ", "),
			Values:  pretty.FormNames, About: formAbout, Sample: pretty.FormLine.String(),
			Get: func(c *Config) string { return c.Value.Form[kind] },
			Apply: func(c *Config, v string) {
				if c.Value.Form == nil {
					c.Value.Form = map[string]string{}
				}
				c.Value.Form[kind] = v
			},
			Validate: func(v string) error {
				_, err := pretty.ParseForm(v)
				return err
			},
		})
	}

	opts = append(opts,
		Option{
			Key: "scratch.pad", Table: "scratch", Name: "pad", Kind: Scalar, Effect: NextRun,
			Summary: "the scratchpad an interactive session lands in",
			Detail: "A scratchpad is a named session on disk, so the lines you were in the\n" +
				"middle of are still there tomorrow. `-` or `off` starts in none, which is\n" +
				"the persistent form of `gluon -no-scratch`: a scratchpad puts what you\n" +
				"typed on disk, and that is a decision worth being able to write down.\n\n" +
				"Only an interactive session lands in one. `gluon -e`, a piped script and\n" +
				"the MCP server open none whatever this says.",
			Default: "default", Allowed: "a scratchpad name, or - for none", Sample: "notes",
			Get:   func(c *Config) string { return c.Scratch.Pad },
			Apply: func(c *Config, v string) { c.Scratch.Pad = v },
			Validate: func(v string) error {
				if v == "-" || v == "off" {
					return nil
				}
				if strings.TrimSpace(v) == "" {
					return fmt.Errorf("a scratchpad needs a name, or - for none")
				}
				return nil
			},
		},
		Option{
			Key: "plugins.disable", Table: "plugins", Name: "disable", Kind: List, Effect: Live,
			Summary: "library plugins that must not activate",
			Default: "nothing disabled", Allowed: "plugin names — :plugins lists them",
			Why: "an array — :settings writes one line, and rewriting a list wholesale takes " +
				"the comments inside it. Edit config.toml",
			Get: func(c *Config) string { return plural(len(c.Plugins.Disable), "plugin") },
		},
		Option{
			Key: "theme.name", Table: "theme", Name: "name", Kind: Scalar, Effect: Live,
			Summary: "the colour palette",
			Detail: ":theme is the other way to set this, and the one that shows you the palette\n" +
				"before you choose it. Both write the same line.",
			Default: theme.Default,
			Allowed: "an installed theme — :theme lists them",
			Values:  func() []string { return theme.Names(theme.Dir()) },
			About:   themeAbout,
			Sample:  "terminal",
			Get:     func(c *Config) string { return c.ThemeName },
			Apply:   func(c *Config, v string) { c.ThemeName = v },
			Validate: func(v string) error {
				if err := theme.ValidName(v); err != nil {
					return err
				}
				_, err := theme.Named(v, theme.Dir())
				return err
			},
		},
		Option{
			Key: "theme.<role>", Table: "theme", Name: "<role>", Kind: Scalar, Effect: Live,
			Family:  true,
			Summary: "one colour, on top of whichever theme is on",
			Detail: "A role is a thing gluon paints, not a place: keyword, string, error and the\n" +
				"thirteen others :theme's picker shows in their own colour.",
			Default: "whatever the chosen theme says",
			Allowed: "a hex triple (#5fafd7), an ANSI number (6), or none",
			Values:  theme.Roles, Sample: "#5fafd7",
			Get: func(c *Config) string {
				if len(c.Theme) == 0 {
					return ""
				}
				return plural(len(c.Theme), "role") + " overridden"
			},
		},
	)
	return opts
}

// formAbout is how a form describes itself, from the same list the renderer
// dispatches on — so a form added there arrives here described rather than
// bare.
// inputModeAbout answers for both members of the set, because a chooser showing
// one explained value and one bare one reads as an oversight.
// bannerAbout answers for all three, because a chooser that explained one value
// and left two bare would read as an oversight rather than as a default.
func bannerAbout(v string) string {
	switch v {
	case BannerFull:
		return "the wordmark, and a row for each thing the session starts with"
	case BannerCompact:
		return "one line — the version and the toolchain — and the hints"
	case BannerOff:
		return "nothing at all: the prompt, and that is it"
	}
	return ""
}

func inputModeAbout(v string) string {
	switch v {
	case InputEmacs:
		return "readline bindings — ctrl-a, ctrl-e, alt-f, alt-b"
	case InputVim:
		return "modal editing — esc commands, i inserts"
	}
	return ""
}

func formAbout(v string) string {
	f, err := pretty.ParseForm(v)
	if err != nil {
		return ""
	}
	return f.About()
}

// themeAbout is a palette's own line about itself, and the ground it was drawn
// for. `:theme` shows both while previewing the palette, which is the better
// way to choose one; this is what is left for a chooser that cannot preview.
func themeAbout(v string) string {
	f, err := theme.Named(v, theme.Dir())
	if err != nil {
		return ""
	}
	switch {
	case f.About == "":
		return f.Appearance
	case f.Appearance == "":
		return f.About
	}
	return f.About + " · " + f.Appearance
}

// Lookup resolves a dotted key, materialising a Family member: Lookup
// ("theme.keyword") returns the theme row with Name "keyword" and a Get that
// reads that one role.
func Lookup(key string) (Option, bool) {
	var family []Option
	for _, o := range Options() {
		if o.Key == key {
			return o, true
		}
		if o.Family {
			family = append(family, o)
		}
	}
	for _, o := range family {
		prefix := strings.TrimSuffix(o.Key, "<role>")
		if !strings.HasPrefix(key, prefix) {
			continue
		}
		member := strings.TrimPrefix(key, prefix)
		if member == "" || !contains(o.Values(), member) {
			continue
		}
		o.Key, o.Name, o.Family = key, member, false
		o.Summary = "the " + member + " colour, on top of whichever theme is on"
		o.Get = func(c *Config) string { return c.Theme[member] }
		o.Apply = func(c *Config, v string) {
			if c.Theme == nil {
				c.Theme = map[string]string{}
			}
			c.Theme[member] = v
		}
		o.Values = nil
		return o, true
	}
	return Option{}, false
}

// Keys is every settable key, expanded, for a "did you mean" list.
func Keys() []string {
	var out []string
	for _, o := range Options() {
		if !o.Family {
			out = append(out, o.Key)
			continue
		}
		prefix := strings.TrimSuffix(o.Key, "<role>")
		for _, m := range o.Values() {
			out = append(out, prefix+m)
		}
	}
	sort.Strings(out)
	return out
}

func contains(xs []string, s string) bool {
	for _, x := range xs {
		if x == s {
			return true
		}
	}
	return false
}

// plural is "1 import" / "3 imports", for the value column of a setting whose
// value is a count rather than a string.
func plural(n int, what string) string {
	if n == 0 {
		return "none"
	}
	if n == 1 {
		return "1 " + what
	}
	return fmt.Sprintf("%d %ss", n, what)
}

// envNote reports the environment variable a setting shares its meaning with,
// when it is set. The variable is how the bound reaches the child, so one
// exported in the user's own shell is in force for every session — including
// one whose config says something else, since gluon only adds a variable when
// the setting is not the default.
func envNote(name string) func(*Config) string {
	return func(*Config) string {
		v := os.Getenv(name)
		if v == "" {
			return ""
		}
		return name + "=" + v + " is set in the environment"
	}
}
