package stdlib

import (
	"go/parser"
	"strings"
	"testing"
)

// The shape decision :csv makes — table or refusal — happens in the child, by
// reflecting over the value, so these fast tests can only assert on the source
// that gets sent there. What each shape actually prints is
// TestCSVRendersATableAndRefusesWhatIsNotOne, in the integration tier, where
// the toolchain really runs.

func csvRewrite(t *testing.T) func(string) (string, error) {
	t.Helper()
	return CSV{}.Commands()[0].Rewrite
}

// TestCSVRewriteParsesForEveryShape: the rewrite is one template, so the
// argument is the only thing that can make it unparseable — and a slice of
// structs, a scalar, a map and an empty slice all have to reach the child,
// which is where the shape is decided.
func TestCSVRewriteParsesForEveryShape(t *testing.T) {
	rewrite := csvRewrite(t)
	for _, arg := range []string{
		"users",
		"[]User{{Name: \"Ada\"}}",
		"42",
		"map[string]int{\"a\": 1}",
		"[]User{}",
		"[][]string{{\"a\"}}",
	} {
		src, err := rewrite(arg)
		if err != nil {
			t.Errorf(":csv %s was rejected: %v", arg, err)
			continue
		}
		if _, err := parser.ParseExpr(src); err != nil {
			t.Errorf(":csv %s produced source that does not parse: %v", arg, err)
		}
		if !strings.Contains(src, arg) {
			t.Errorf(":csv %s did not reach the generated source", arg)
		}
	}
}

// TestCSVSourceRefusesRatherThanGuesses. The refusal is the design decision —
// a scalar as a one-cell table and a map as two columns are both guesses — so
// the source must carry both the refusal and the shapes it does accept.
func TestCSVSourceRefusesRatherThanGuesses(t *testing.T) {
	src, err := csvRewrite(t)("x")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"cannot render ",
		" as CSV: a CSV is a table of records.",
		"slice of structs",
		"slice of slices",
	} {
		if !strings.Contains(src, want) {
			t.Errorf("the refusal does not carry %q", want)
		}
	}
	// The header reads the column tag, and the shape is decided by the child
	// rather than by the type checker (invariant 5).
	if !strings.Contains(src, `Tag.Lookup("csv")`) {
		t.Error("the column tag is not read")
	}
	if !strings.Contains(src, "reflect.ValueOf") {
		t.Error("the shape is not decided by reflection in the child")
	}
}

// TestCSVEvaluatesItsArgumentOnce is what the local binding is for: `:csv
// fetch()` must not call fetch once per row.
func TestCSVEvaluatesItsArgumentOnce(t *testing.T) {
	src, err := csvRewrite(t)("fetch()")
	if err != nil {
		t.Fatal(err)
	}
	if n := strings.Count(src, "fetch()"); n != 1 {
		t.Errorf("the argument appears %d times in the generated source, want 1", n)
	}
}

// TestCSVGeneratesNothingNewerThanTheHostMayAllow. An attached session builds
// against the host's own go.mod, and its `go` directive decides the language
// version — so a rewrite reaching for a recent stdlib addition is a build error
// in an older project rather than a missing feature.
func TestCSVGeneratesNothingNewerThanTheHostMayAllow(t *testing.T) {
	src, err := csvRewrite(t)("x")
	if err != nil {
		t.Fatal(err)
	}
	for _, recent := range []string{"strings.Cut", "IsExported", "reflect.Pointer", "any("} {
		if strings.Contains(src, recent) {
			t.Errorf("the generated source uses %s, which an older host cannot build", recent)
		}
	}
}

// TestStdlibEncodingsRejectAnEmptyArgument, with a usage line rather than a
// build error naming gluon.
func TestStdlibEncodingsRejectAnEmptyArgument(t *testing.T) {
	for _, c := range append(XML{}.Commands(), CSV{}.Commands()...) {
		_, err := c.Rewrite("   ")
		if err == nil {
			t.Errorf("%s accepted an empty argument", c.Name)
			continue
		}
		if !strings.HasPrefix(err.Error(), "usage:") {
			t.Errorf("%s: %q does not begin with usage:", c.Name, err)
		}
	}
}

// TestXMLMarshalsIndented. The whole reason the command exists is the tags, so
// what runs must be the encoder and not a printer.
func TestXMLMarshalsIndented(t *testing.T) {
	src, err := XML{}.Commands()[0].Rewrite("u")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(src, "xml.MarshalIndent(u") {
		t.Errorf("the encoder is not what runs: %s", src)
	}
	// A type encoding/xml refuses is exactly what someone runs this on to find
	// out, so the error is returned as the answer rather than dropped.
	if !strings.Contains(src, "cannot marshal: ") {
		t.Errorf("the marshal error is dropped: %s", src)
	}
}
