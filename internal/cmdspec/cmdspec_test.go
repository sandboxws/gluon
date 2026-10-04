package cmdspec

import "testing"

// TestAsksForHelpByKind is the whole interception rule in one table. The case
// that matters is the last column: for a command that takes an expression, -h
// is the negation of h, and help must not take it away.
func TestAsksForHelpByKind(t *testing.T) {
	cases := []struct {
		arg  string
		want map[Kind]bool
	}{
		{"--help", map[Kind]bool{NoArg: true, Words: true, SQL: true, GoName: true, GoExpr: true}},
		{"?", map[Kind]bool{NoArg: true, Words: true, SQL: true, GoName: true, GoExpr: true}},
		{"  ?  ", map[Kind]bool{NoArg: true, Words: true, SQL: true, GoName: true, GoExpr: true}},
		{"-h", map[Kind]bool{NoArg: true, Words: true, SQL: true, GoName: true, GoExpr: false}},
		{"-help", map[Kind]bool{NoArg: true, Words: true, SQL: true, GoName: true, GoExpr: false}},
		// Only the whole argument asks. A flag named -h somewhere in a line is
		// that line's business.
		{"--help x", map[Kind]bool{}},
		{"GET https://x -h", map[Kind]bool{}},
		{"-hx", map[Kind]bool{}},
		{"", map[Kind]bool{}},
	}
	for _, c := range cases {
		for _, k := range []Kind{NoArg, Words, SQL, GoName, GoExpr} {
			if got := AsksForHelp(k, c.arg); got != c.want[k] {
				t.Errorf("AsksForHelp(%s, %q) = %v, want %v", k, c.arg, got, c.want[k])
			}
		}
	}
}

// TestFromArgReadsEveryBuiltinArg covers every Arg shape the registry and the
// plugins use today, by the synopsis each one reads back as.
func TestFromArgReadsEveryBuiltinArg(t *testing.T) {
	cases := []struct{ arg, want string }{
		{"", ":x"},
		{"<exp>", ":x <exp>"},
		{"[name]", ":x [name]"},
		{"a, b", ":x <a>, <b>"},
		{"<t>, <i>", ":x <t>, <i>"},
		{"<exp>, <i>", ":x <exp>, <i>"},
		{"[on|off]", ":x [on|off]"},
		{"<m> <url>", ":x <m> <url>"},
		{"<exp|func>", ":x <exp|func>"},
		{"<name|n>", ":x <name|n>"},
		{"<addr> [m]", ":x <addr> [m]"},
		{"[key=val]", ":x [key=val]"},
		{"[flags] a[,b]", ":x <a>[, <b>]"},
		{"[flags] <sql>", ":x <sql>"},
		{"[flags|topic]", ":x [topic]"},
		{"[flag|name]", ":x [name]"},
		{"[flag]", ":x"},
		{"[-v|-d] <exp>", ":x [-v | -d] <exp>"},
		{"[-n k] <e>", ":x [-n <k>] <e>"},
		{"[-table] <exp>", ":x [-table] <exp>"},
		{"[-rm] [module]", ":x [-rm] [module]"},
		{"[-deps]", ":x [-deps]"},
		{"[dir|-off]", ":x [-off] [dir]"},
	}
	for _, c := range cases {
		s := FromArg(c.arg)
		if got := s.Line(":x", c.arg); got != c.want {
			t.Errorf("FromArg(%q).Line = %q, want %q", c.arg, got, c.want)
		}
	}
}

// TestFromArgFindsTheFlagsArgSpells: a flag written into Arg is a flag, with
// its value placeholder, and a choice of several is a choice.
func TestFromArgFindsTheFlagsArgSpells(t *testing.T) {
	s := FromArg("[-v|-d] <exp>")
	if len(s.Flags) != 2 || !s.Flags[0].Mode || !s.Flags[1].Mode {
		t.Fatalf("[-v|-d] read as %+v, want two Mode flags", s.Flags)
	}
	s = FromArg("[-n k] <e>")
	if len(s.Flags) != 1 || s.Flags[0].Name != "-n" || s.Flags[0].Value != "k" {
		t.Fatalf("[-n k] read as %+v, want -n with value k", s.Flags)
	}
	s = FromArg("[dir|-off]")
	if len(s.Flags) != 1 || !s.Flags[0].Alone {
		t.Fatalf("[dir|-off] read as %+v, want -off alone", s.Flags)
	}
	s = FromArg("[on|off]")
	if len(s.Params) != 1 || len(s.Params[0].Values.Fixed) != 2 {
		t.Fatalf("[on|off] read as %+v, want a closed set of two", s.Params)
	}
}

// TestLineOrdersFlagsByPlace: flags lead unless the command reads them after
// its operands, and a hidden flag is never part of the synopsis.
func TestLineOrdersFlagsByPlace(t *testing.T) {
	http := Spec{
		Kind: Words, FlagsAt: After,
		Params: []Param{{Name: "method"}, {Name: "url"}},
		Flags: []Flag{
			{Name: "-H", Value: "header", Repeat: true},
			{Name: "-d", Value: "body"},
			{Name: "-t", Value: "duration"},
		},
	}
	if got, want := http.Line(":http", "<m> <url>"),
		":http <method> <url> [-H <header>]... [-d <body>] [-t <duration>]"; got != want {
		t.Errorf("got  %s\nwant %s", got, want)
	}

	bench := Spec{
		Params: []Param{{Name: "exp"}, {Name: "exp", Optional: true, Sep: ","}},
		Flags: []Flag{
			{Name: "-count", Value: "n"},
			{Name: "-cpu", Value: "list"},
			{Name: "-profile"},
		},
	}
	if got, want := bench.Line(":bench", "[flags] a[,b]"),
		":bench [-count <n>] [-cpu <list>] [-profile] <exp>[, <exp>]"; got != want {
		t.Errorf("got  %s\nwant %s", got, want)
	}

	scratch := Spec{
		Kind:   Words,
		Params: []Param{{Name: "name", Optional: true}},
		Flags: []Flag{
			{Name: "-off", Mode: true, Alone: true},
			{Name: "-rm", Mode: true},
			{Name: "-force", Hidden: true},
		},
	}
	if got, want := scratch.Line(":scratch", "[flag|name]"),
		":scratch [-off | -rm] [name]"; got != want {
		t.Errorf("got  %s\nwant %s", got, want)
	}

	set := Spec{Kind: Words, Synopsis: "[key [value | -]]"}
	if got, want := set.Line(":settings", "[key=val]"), ":settings [key [value | -]]"; got != want {
		t.Errorf("a Synopsis replaces the generated form: got %q", got)
	}
}

// TestVisibleLeavesOutHidden and Flag finds either.
func TestVisibleLeavesOutHidden(t *testing.T) {
	s := Spec{Flags: []Flag{{Name: "-rm"}, {Name: "-force", Hidden: true}}}
	if v := s.Visible(); len(v) != 1 || v[0].Name != "-rm" {
		t.Errorf("Visible = %+v, want -rm alone", v)
	}
	if _, ok := s.Flag("-force"); !ok {
		t.Error("Flag does not find a hidden flag, so a parser test could not declare one")
	}
}

// TestKindNamesItself: the docs print a kind, so every kind has a name.
func TestKindNamesItself(t *testing.T) {
	for _, k := range []Kind{GoExpr, GoName, Words, SQL, NoArg} {
		if k.String() == "an argument" {
			t.Errorf("kind %d has no name", k)
		}
	}
	if !GoExpr.IsGo() || !GoName.IsGo() || Words.IsGo() || SQL.IsGo() || NoArg.IsGo() {
		t.Error("IsGo disagrees with the kinds that are Go")
	}
}
