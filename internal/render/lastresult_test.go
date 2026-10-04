package render

import (
	"go/format"
	"strings"
	"testing"

	"github.com/sandboxws/gluon/internal/session"
)

func TestSynthetic(t *testing.T) {
	for _, tc := range []struct {
		name string
		want bool
	}{
		{"it", true}, {"_1", true}, {"_10", true},
		{"_", false}, {"_x", false}, {"_0", false}, {"_1a", false},
		{"item", false}, {"x", false},
	} {
		if got := Synthetic(tc.name); got != tc.want {
			t.Errorf("Synthetic(%q) = %v, want %v", tc.name, got, tc.want)
		}
	}
}

// Nothing is bound unless something asks for it, which is what keeps every
// session that never says it or _N rendering exactly as it did before.
func TestNoBindingWithoutAReference(t *testing.T) {
	got := build(t, "1+1", "2+2")
	if strings.Contains(got, "_1") {
		t.Errorf("unreferenced value was bound anyway:\n%s", got)
	}
}

func TestBindingEmittedOnReference(t *testing.T) {
	got := stripDirectives(build(t, "1+1", "_1 + 1"))
	if !strings.Contains(got, "_1 := 1+1") {
		t.Errorf("missing binding:\n%s", got)
	}
	if !strings.Contains(got, PrintFunc+"(_1)") {
		t.Errorf("bound value is not printed:\n%s", got)
	}
	// The print call is itself a use, so the _ = suppression would be noise.
	if strings.Contains(got, "_ = _1") {
		t.Errorf("redundant blank assignment:\n%s", got)
	}
}

// The newest entry keeps __gluonPrint(expr) rather than being bound, which is
// what preserves the constant fast path: check.Printed reads the type of the
// argument, and an identifier bound to a constant is a variable.
func TestNewestPrintKeepsItsShape(t *testing.T) {
	got := stripDirectives(build(t, "1+1", "_1 + 1"))
	if !strings.Contains(got, PrintFunc+"(_1 + 1)") {
		t.Errorf("newest entry was not printed directly:\n%s", got)
	}
	if strings.Contains(got, "_2 :=") {
		t.Errorf("newest entry was bound:\n%s", got)
	}
}

func TestItRewritesToTheOrdinal(t *testing.T) {
	got := stripDirectives(build(t, "1+1", "it * 2"))
	if !strings.Contains(got, PrintFunc+"(_1 * 2)") {
		t.Errorf("it was not resolved:\n%s", got)
	}
	if strings.Contains(got, "it") {
		t.Errorf("it reached the program:\n%s", got)
	}
}

// The session replays, so an entry that said it is still in the program long
// after it stopped being newest — and each one resolves to its own predecessor,
// which is why it cannot be a variable.
func TestItChainsAcrossEntries(t *testing.T) {
	got := stripDirectives(build(t, "1+1", "it * 2", "it + 1"))
	for _, want := range []string{"_1 := 1+1", "_1 * 2", "_2 + 1"} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q:\n%s", want, got)
		}
	}
}

func TestItInStringOrCommentIsNotRewritten(t *testing.T) {
	got := stripDirectives(build(t, "1+1", `"wait for it"`))
	if !strings.Contains(got, `"wait for it"`) {
		t.Errorf("it was rewritten inside a string literal:\n%s", got)
	}
}

func TestOrdinalsSkipVoidCallsAndNonExpressions(t *testing.T) {
	s := &session.Session{}
	s.Append(session.Entry{Kind: session.KindDecl, Src: "type T int"})
	s.Append(session.Entry{Kind: session.KindStmt, Src: "x := 1", Binds: []string{"x"}})
	s.Append(session.Entry{Kind: session.KindExpr, Src: "x + 1"})
	s.Append(session.Entry{Kind: session.KindExpr, Src: "close(ch)", NoValue: true})
	s.Append(session.Entry{Kind: session.KindExpr, Src: "it * 2"})

	got, err := Main(s, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stripDirectives(got), "_1 * 2") {
		t.Errorf("ordinal did not skip the decl, stmt and void call:\n%s", got)
	}
}

// Popping only ever truncates, so an ordinal never changes meaning under :undo.
func TestOrdinalsAreStableUnderPop(t *testing.T) {
	s := sessionOf(t, "1+1", "2+2", "3+3")
	before := Ordinals(s)[1]
	s.Pop()
	if after := Ordinals(s)[1]; after != before {
		t.Errorf("ordinal moved under Pop: was %d, now %d", before, after)
	}
}

func TestUserBoundItWins(t *testing.T) {
	got := stripDirectives(build(t, "1+1", `it := "mine"`, `it + "!"`))
	if strings.Contains(got, "_1 :=") {
		t.Errorf("gluon bound a value the user had shadowed:\n%s", got)
	}
	if !strings.Contains(got, `it + "!"`) {
		t.Errorf("the user's own it was rewritten:\n%s", got)
	}
}

func TestItWithNoPreviousValueIsAnError(t *testing.T) {
	s := sessionOf(t, "it + 1")
	if _, err := Main(s, nil); err == nil {
		t.Fatal("want an error naming it, got none")
	}
}

func TestForwardOrdinalIsAnError(t *testing.T) {
	s := sessionOf(t, "1+1", "_9 + 1")
	_, err := Main(s, nil)
	if err == nil || !strings.Contains(err.Error(), "_9") {
		t.Fatalf("want an error naming _9, got %v", err)
	}
}

func TestDeclCannotUseIt(t *testing.T) {
	s := &session.Session{}
	s.Append(session.Entry{Kind: session.KindExpr, Src: "1+1"})
	s.Append(session.Entry{Kind: session.KindDecl, Src: "func f() int { return it }"})
	if _, err := Main(s, nil); err == nil {
		t.Fatal("want an error, got none")
	}
}

// A multi-value expression cannot be bound to one name. Refusing by ordinal
// names what the user typed; the compiler would report a mismatch against the
// earlier line instead.
func TestMultiValueOrdinalIsRefused(t *testing.T) {
	s := &session.Session{}
	s.Append(session.Entry{Kind: session.KindExpr, Src: `strconv.Atoi("12")`, Values: 2})
	s.Append(session.Entry{Kind: session.KindExpr, Src: "it + 1"})
	_, err := Main(s, nil)
	if err == nil || !strings.Contains(err.Error(), "_1") {
		t.Fatalf("want a refusal naming _1, got %v", err)
	}
}

// :src and :save must stay compilable with bindings in play.
func TestDisplayKeepsBindingsAndStillCompiles(t *testing.T) {
	got := Display(build(t, "x := 1", "x + 1", "it * 2"))
	if !strings.Contains(got, "_1 := x + 1") {
		t.Errorf("Display dropped the binding:\n%s", got)
	}
	if _, err := format.Source([]byte(got)); err != nil {
		t.Errorf("Display output does not parse: %v\n%s", err, got)
	}
}
