//go:build integration

package repl

import (
	"strings"
	"testing"
)

// The serialization commands, with the toolchain really running. What a value
// becomes under an encoder cannot be asserted from the generated source: the
// encoder is the answer, and it runs in the child.

// TestEncodingCommandDoesNotMutateTheSession is invariant 14 for the pair added
// to plugins/stdlib. :xml goes through EvalTransient like every plugin command,
// so the session must gain neither an entry nor the encoding/xml import — an
// import the session never asked for is exactly what a command that rewrote
// into the program would leave behind.
func TestEncodingCommandDoesNotMutateTheSession(t *testing.T) {
	c, err := NewCore()
	if err != nil {
		t.Skip("no evaluator:", err)
	}
	defer c.Close()

	if res := c.Submit("type Point struct { X int `xml:\"x,attr\"`; Y int }"); res.Err {
		t.Fatalf("setup failed: %s", res.Out)
	}
	if res := c.Submit(`p := Point{1, 2}`); res.Err {
		t.Fatalf("setup failed: %s", res.Out)
	}
	entriesBefore := len(c.sess.Entries)
	importsBefore := len(c.ev.Imports())

	res := c.Submit(`:xml p`)
	if res.Err {
		t.Fatalf(":xml failed: %s", res.Out)
	}
	// The tag is the point: X is an attribute because the tag says so, and the
	// value printer cannot show that.
	if !strings.Contains(res.Out, `x="1"`) {
		t.Errorf(":xml did not apply the tags: %q", res.Out)
	}
	if !strings.Contains(res.Out, "<Y>2</Y>") {
		t.Errorf(":xml did not marshal the value: %q", res.Out)
	}

	if got := len(c.sess.Entries); got != entriesBefore {
		t.Errorf("the session gained %d entries", got-entriesBefore)
	}
	if got := len(c.ev.Imports()); got != importsBefore {
		t.Errorf("the session gained %d imports; encoding/xml must not leak in",
			got-importsBefore)
	}
	for _, im := range c.ev.Imports() {
		if im.Path == "encoding/xml" {
			t.Error("encoding/xml was written into the session")
		}
	}

	// The session still works, and p still means what it did.
	if res := c.Submit(`p.X`); res.Err || !strings.Contains(res.Out, "1") {
		t.Errorf("session broken after :xml: %q", res.Out)
	}
}

// TestCSVRendersATableAndRefusesWhatIsNotOne covers the four shapes the design
// argues about: a sequence of records, a scalar, a map, and an empty sequence
// of a known record type. The refusal is the decision worth pinning — a scalar
// as a one-cell table and a map as two columns are both guesses, and a
// confident guess is worse than a refusal that names what the command takes.
func TestCSVRendersATableAndRefusesWhatIsNotOne(t *testing.T) {
	c, err := NewCore()
	if err != nil {
		t.Skip("no evaluator:", err)
	}
	defer c.Close()

	if res := c.Submit("type Row struct { Name string `csv:\"name\"`; Age int; Note string `csv:\"-\"` }"); res.Err {
		t.Fatalf("setup failed: %s", res.Out)
	}
	if res := c.Submit(`rows := []Row{{"Ada", 36, "skip"}, {"Grace, G", 45, "skip"}}`); res.Err {
		t.Fatalf("setup failed: %s", res.Out)
	}

	// A sequence of records: a header from the tags and the field names, one
	// row each, and encoding/csv's own quoting for the value with a comma.
	res := c.Submit(`:csv rows`)
	if res.Err {
		t.Fatalf(":csv failed: %s", res.Out)
	}
	for _, want := range []string{"name,Age", "Ada,36", `"Grace, G",45`} {
		if !strings.Contains(res.Out, want) {
			t.Errorf(":csv rows is missing %q:\n%s", want, res.Out)
		}
	}
	if strings.Contains(res.Out, "Note") || strings.Contains(res.Out, "skip") {
		t.Errorf("a csv:\"-\" column was rendered anyway:\n%s", res.Out)
	}

	// A scalar and a map have no table shape. Both name the shapes :csv takes,
	// which is what makes the refusal actionable.
	for _, arg := range []string{`42`, `map[string]int{"a": 1}`} {
		res := c.Submit(`:csv ` + arg)
		if res.Err {
			t.Fatalf(":csv %s errored rather than answering: %s", arg, res.Out)
		}
		if !strings.Contains(res.Out, "as CSV") || !strings.Contains(res.Out, "slice of structs") {
			t.Errorf(":csv %s did not refuse by naming the shapes it takes:\n%s", arg, res.Out)
		}
		if strings.Contains(res.Out, "\n42") {
			t.Errorf(":csv %s invented a row:\n%s", arg, res.Out)
		}
	}

	// An empty sequence still has a record type, so the header is the useful
	// half of the answer.
	res = c.Submit(`:csv []Row{}`)
	if res.Err {
		t.Fatalf(":csv []Row{} failed: %s", res.Out)
	}
	if !strings.Contains(res.Out, "name,Age") || !strings.Contains(res.Out, "no records") {
		t.Errorf(":csv on an empty sequence gave:\n%s", res.Out)
	}

	// A slice of slices is already rows, so there is no header to invent.
	res = c.Submit(`:csv [][]string{{"a", "b"}, {"c", "d"}}`)
	if res.Err {
		t.Fatalf(":csv on a slice of slices failed: %s", res.Out)
	}
	if !strings.Contains(res.Out, "a,b") || !strings.Contains(res.Out, "c,d") {
		t.Errorf(":csv on a slice of slices gave:\n%s", res.Out)
	}
	if strings.Contains(res.Out, "no records") {
		t.Errorf(":csv reported no records for two rows:\n%s", res.Out)
	}
}
