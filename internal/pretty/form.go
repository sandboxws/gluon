package pretty

import (
	"fmt"
	"sort"
	"strings"
)

// A Form is one of the shapes a value can be drawn in.
//
// gluon drew exactly one until this existed: a bordered table for anything with
// structure, a single line for anything without. That is still the default and
// still the right one, but it is not the only useful answer. A list of structs
// wants a column per field; a value nested three deep wants indentation rather
// than a cell truncated at 120 runes; a working session often wants one line.
//
// It is called Form rather than Style because Styles in this package is the
// palette, and two names one letter apart in the same signature is a reader's
// trap. Form is also the word the rest of the package already uses — "the two
// forms exist because the two positions are different" (Hook), "inline is the
// one-line form of a value" (Value.inline).
type Form uint8

const (
	// FormTable is the bordered table gluon has always drawn, and it is the
	// zero value deliberately: a caller that knows nothing about forms gets
	// exactly what it got before this existed. That is the whole compatibility
	// argument, and TestTheDefaultFormIsTheOldRenderer is what holds it.
	FormTable Form = iota
	// FormLine is one painted line per value.
	FormLine
	// FormTree is indented and borderless: the only form that shows nesting.
	FormTree
	// FormColumns gives a list of uniform structs a column per field.
	FormColumns
	// FormLiteral is Go source you can paste back.
	FormLiteral
)

// formNames is the wire spelling of each form, indexed by the form. One list,
// so String and ParseForm cannot disagree about what a form is called.
var formNames = [...]string{"table", "line", "tree", "columns", "literal"}

// formAbout is the line each form describes itself by in `:settings`.
var formAbout = [...]string{
	"a bordered table per collection",
	"one line per value, however deep it goes",
	"indented and borderless — the only form that shows nesting",
	"a column per field, for a list of structs of one shape",
	"Go source you could paste into a test",
}

// String is the name config.toml and :settings use.
func (f Form) String() string {
	if int(f) >= len(formNames) {
		return "table"
	}
	return formNames[f]
}

// About is the one line the form describes itself by.
func (f Form) About() string {
	if int(f) >= len(formAbout) {
		return ""
	}
	return formAbout[f]
}

// Forms is every form, in the order :settings lists them.
func Forms() []Form {
	out := make([]Form, len(formNames))
	for i := range formNames {
		out[i] = Form(i)
	}
	return out
}

// FormNames is every form's name, for the allowed-values column.
func FormNames() []string { return append([]string(nil), formNames[:]...) }

// ParseForm reads a form by name.
//
// An unknown name is an error naming what is available, never a silent fall
// back to the default. internal/config's doc gives the reason: a setting that
// quietly does nothing is worse than one that says so, because the only signal
// the user gets is "gluon ignored my config".
func ParseForm(s string) (Form, error) {
	for i, n := range formNames {
		if n == s {
			return Form(i), nil
		}
	}
	return FormTable, fmt.Errorf("unknown form %q — try one of %s",
		s, strings.Join(formNames[:], ", "))
}

// formKinds are the kinds that may name a form of their own.
//
// Exactly the kinds Value.tabular answers true for, and for the same reason:
// they are the only ones with more than one drawing. A per-kind override for a
// scalar would be a setting that does nothing under four forms out of five,
// which is the setting-that-silently-does-nothing this codebase refuses at load
// rather than ships. A ptr unwraps to its target and takes that kind's form,
// exactly as rich already unwraps it.
var formKinds = []string{"list", "map", "struct"}

// FormKinds is the kinds a per-kind override may name.
func FormKinds() []string { return append([]string(nil), formKinds...) }

// IsFormKind reports whether a name is a kind that may carry its own form.
func IsFormKind(s string) bool {
	for _, k := range formKinds {
		if k == s {
			return true
		}
	}
	return false
}

// Options is what a caller may vary about a rich rendering.
//
// A struct rather than more parameters, because the next thing added here
// should not be a fourth Rich signature — and because the zero value has to go
// on meaning "what gluon drew before", which is what lets Rich and RichWith
// stay exactly as they were.
type Options struct {
	// Form is the shape every kind takes unless Kinds names another. The zero
	// value is FormTable.
	Form Form
	// Kinds overrides Form for one kind, keyed by the kind name the child
	// sent. It applies at the top level only: what a value nested inside a
	// table cell or a tree node gets is decided by the form drawing it, not by
	// a second lookup three levels down.
	Kinds map[string]Form
	// Hooks are the per-type plugin renderers, keyed on the reflect type name
	// the child sent. Nil is no hooks, which is what Rich passes.
	Hooks map[string]Hook
}

// formFor is the form one value is drawn in.
//
// A pointer is asked about its target, because a *User is drawn as the User it
// points at — rich already unwraps it, so a per-kind rule that stopped at "ptr"
// would name a kind the renderer never reaches.
func (o Options) formFor(v Value) Form {
	if len(o.Kinds) > 0 {
		k := v.Kind
		if k == "ptr" && len(v.Items) == 1 {
			k = v.Items[0].Kind
		}
		if f, ok := o.Kinds[k]; ok {
			return f
		}
	}
	return o.Form
}

// KindForms is the per-kind table as config records it, sorted so the caller
// prints it in one order.
func KindForms(m map[string]Form) []string {
	out := make([]string, 0, len(m))
	for k, f := range m {
		out = append(out, k+"="+f.String())
	}
	sort.Strings(out)
	return out
}
