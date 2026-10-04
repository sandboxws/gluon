package main

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/sandboxws/gluon/internal/pretty"
)

func decode(t *testing.T, v any) map[string]any {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatal(err)
	}
	return m
}

func TestValueJSONCarriesReprAndText(t *testing.T) {
	three := 3
	v := pretty.Value{Type: "[]int", Kind: "list", Len: &three, Cap: &three,
		Items: []pretty.Value{
			{Type: "int", Kind: "scalar", Repr: "1"},
			{Type: "int", Kind: "scalar", Repr: "2"},
			{Type: "int", Kind: "scalar", Repr: "3"},
		}}
	m := decode(t, newValueJSON(v))
	if m["type"] != "[]int" || m["kind"] != "list" {
		t.Errorf("type/kind = %v/%v", m["type"], m["kind"])
	}
	// text is the same rendering `gluon -e` prints, which is a documented
	// compatibility surface.
	text, _ := m["text"].(string)
	if !strings.Contains(text, "[1 2 3]") || !strings.Contains(text, "len=3 cap=3") {
		t.Errorf("text = %q", text)
	}
	if m["len"] != float64(3) || m["cap"] != float64(3) {
		t.Errorf("len/cap = %v/%v", m["len"], m["cap"])
	}
	// A composite has no scalar repr, and the key is omitted rather than empty.
	if _, ok := m["repr"]; ok {
		t.Errorf("repr should be omitted for a composite: %v", m["repr"])
	}

	scalar := decode(t, newValueJSON(pretty.Value{Type: "int", Kind: "scalar", Repr: "2"}))
	if scalar["repr"] != "2" {
		t.Errorf("scalar repr = %v", scalar["repr"])
	}
}

// An error is still a JSON object on stdout: a caller piping into jq should not
// have to read stderr to find out what happened.
func TestEvalJSONReportsErrorsInBand(t *testing.T) {
	m := decode(t, evalJSON{Error: "undefined: bogus", ExitCode: 1})
	if m["ok"] != false {
		t.Errorf("ok = %v", m["ok"])
	}
	if m["error"] != "undefined: bogus" {
		t.Errorf("error = %v", m["error"])
	}
	// No values and no stdout are omitted rather than emitted as null.
	for _, k := range []string{"values", "stdout"} {
		if _, ok := m[k]; ok {
			t.Errorf("%s should be omitted when empty", k)
		}
	}
}

// The frozen surfaces, byte for byte.
//
// evalJSON and metaJSON are compatibility surfaces (constraint I): a field may
// be added, and no existing byte may move. The
// assertions below are the whole document rather than a key at a time, because
// a reordered field and a renamed key are both things a shape test misses and a
// justfile does not.
//
// A new command's envelopes go in new types for exactly this reason. If adding
// one made this test fail, the answer is a new envelope, not a new golden.
func TestFrozenEnvelopeBytes(t *testing.T) {
	cases := []struct {
		name string
		v    any
		want string
	}{
		{"evalJSON", evalJSON{OK: true, Stdout: "hi\n", ExitCode: 0,
			Values: []valueJSON{{Type: "int", Kind: "int", Repr: "1", Text: "1"}}}, `{
  "ok": true,
  "stdout": "hi\n",
  "values": [
    {
      "type": "int",
      "kind": "int",
      "repr": "1",
      "text": "1"
    }
  ],
  "exitCode": 0
}`},
		{"metaJSON", metaJSON{OK: true, Command: ":t x", Text: "int"}, `{
  "ok": true,
  "command": ":t x",
  "text": "int"
}`},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			b, err := json.MarshalIndent(tc.v, "", "  ")
			if err != nil {
				t.Fatal(err)
			}
			if string(b) != tc.want {
				t.Errorf("%s changed:\n got %s\nwant %s", tc.name, b, tc.want)
			}
		})
	}
}
