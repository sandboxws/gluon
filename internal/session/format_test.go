package session

import (
	"reflect"
	"strings"
	"testing"
)

// TestAPadRoundTripsEveryFieldTheSessionHolds is the whole contract of the
// format: what goes in comes back, including the two decisions a build taught
// the session and the one the user made.
//
// Binds is asserted equal rather than stored, because Classify recomputes it —
// a field written to the file twice is a field that can disagree with itself.
func TestAPadRoundTripsEveryFieldTheSessionHolds(t *testing.T) {
	in := &Session{Entries: []Entry{
		{Kind: KindDecl, Src: "func f() int { return 1 }"},
		{Kind: KindStmt, Src: "x := f()", Binds: []string{"x"}, Pinned: true},
		{Kind: KindExpr, Src: "fmt.Println(x)", NoValue: true},
		{Kind: KindExpr, Src: "strings.Cut(\"a=b\", \"=\")", Values: 3},
	}}

	out, err := Unmarshal(Marshal(in))
	if err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if len(out.Entries) != len(in.Entries) {
		t.Fatalf("got %d entries, want %d:\n%s", len(out.Entries), len(in.Entries), Marshal(in))
	}
	for i, want := range in.Entries {
		got := out.Entries[i]
		if !reflect.DeepEqual(got, want) {
			t.Errorf("entry %d: got %+v, want %+v", i, got, want)
		}
	}
}

// TestABlankLineInsideAConstructIsNotAnEntryBoundary pins the accumulation rule
// against the obvious wrong one. A reader that split on blank lines would turn
// one function into two entries, neither of which parses.
func TestABlankLineInsideAConstructIsNotAnEntryBoundary(t *testing.T) {
	src := "func f() int {\n\n\treturn 1\n\n}\n"
	s, err := Unmarshal([]byte(src))
	if err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if len(s.Entries) != 1 {
		t.Fatalf("got %d entries, want 1: %+v", len(s.Entries), s.Entries)
	}
	if !strings.Contains(s.Entries[0].Src, "return 1") {
		t.Errorf("the body did not survive: %q", s.Entries[0].Src)
	}
}

// TestADirectiveInsideAConstructIsSourceNotADirective is the one rule that
// decides whether a comment somebody typed survives being written down.
func TestADirectiveInsideAConstructIsSourceNotADirective(t *testing.T) {
	src := "//gluon:pin\nfunc f() int {\n//gluon:pin\n\treturn 1\n}\n"
	s, err := Unmarshal([]byte(src))
	if err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if len(s.Entries) != 1 {
		t.Fatalf("got %d entries, want 1: %+v", len(s.Entries), s.Entries)
	}
	if !s.Entries[0].Pinned {
		t.Error("the directive before the entry was not read as a pin")
	}
	if !strings.Contains(s.Entries[0].Src, "//gluon:pin\n\treturn 1") {
		t.Errorf("the comment inside the body did not survive: %q", s.Entries[0].Src)
	}
}

// TestALineThatWillNotClassifyIsReportedWithItsNumber: a file being read back
// is a file somebody may have edited, so the answer has to say where to look.
func TestALineThatWillNotClassifyIsReportedWithItsNumber(t *testing.T) {
	src := "x := 1\ny := 2\nfunc (\n"
	_, err := Unmarshal([]byte(src))
	if err == nil {
		t.Fatal("a truncated construct was accepted")
	}
	if !strings.Contains(err.Error(), "line 3") {
		t.Errorf("the error does not name the line: %v", err)
	}

	_, err = Unmarshal([]byte("x := 1\n)\n"))
	if err == nil {
		t.Fatal("a line that cannot be classified was accepted")
	}
	if !strings.Contains(err.Error(), "line 2") {
		t.Errorf("the error does not name the line: %v", err)
	}
}

// TestTheSerialiserSeesEveryEntryField asks the next person rather than
// trusting them. A field added to Entry is either written to the file or
// recomputed from Src, and both answers are fine — what is not fine is a field
// that is neither, which is a decision the session made and the file forgets.
//
// The same argument Clone's comment makes, enforced instead of stated.
func TestTheSerialiserSeesEveryEntryField(t *testing.T) {
	serialised := map[string]bool{"Src": true, "Pinned": true, "NoValue": true, "Values": true}
	derived := map[string]bool{"Kind": true, "Binds": true}

	et := reflect.TypeOf(Entry{})
	for i := range et.NumField() {
		name := et.Field(i).Name
		if !serialised[name] && !derived[name] {
			t.Errorf("Entry.%s is neither serialised by Marshal nor derived by Classify — "+
				"a scratchpad reopened in a later process will not have it. Add it to "+
				"format.go, or to the derived list here with the reason.", name)
		}
	}
	for name := range serialised {
		if _, ok := et.FieldByName(name); !ok {
			t.Errorf("Entry.%s is pinned as serialised but no such field — update this list", name)
		}
	}
	for name := range derived {
		if _, ok := et.FieldByName(name); !ok {
			t.Errorf("Entry.%s is pinned as derived but no such field — update this list", name)
		}
	}
}

// TestAnUnknownDirectiveIsRefused: a directive this reader does not understand
// says something about the entry it precedes, and dropping it silently is how a
// pin goes missing between one version and the next.
func TestAnUnknownDirectiveIsRefused(t *testing.T) {
	_, err := Unmarshal([]byte("//gluon:frobnicate\nx := 1\n"))
	if err == nil {
		t.Fatal("an unknown directive was accepted")
	}
	if !strings.Contains(err.Error(), "frobnicate") {
		t.Errorf("the error does not name the directive: %v", err)
	}
}
