package db

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/sandboxws/gluon/internal/pretty"
)

func parse(t *testing.T, blob string) pretty.Value {
	t.Helper()
	var v pretty.Value
	if err := json.Unmarshal([]byte(blob), &v); err != nil {
		t.Fatal(err)
	}
	return v
}

func renderFor(t *testing.T, typ string) func(pretty.Value, pretty.Styles) (string, bool) {
	t.Helper()
	for _, r := range (SQL{}).Renders() {
		if r.Type == typ {
			return r.Rich
		}
	}
	t.Fatalf("no renderer for %s", typ)
	return nil
}

// TestNullIsRenderedAsNull. The struct form shows the field that does not
// matter as prominently as the one that does.
func TestNullIsRenderedAsNull(t *testing.T) {
	v := parse(t, `{"t":"sql.NullString","k":"struct","f":[
		{"n":"String","v":{"k":"string","r":"leftover"}},
		{"n":"Valid","v":{"k":"scalar","r":"false"}}]}`)
	out, ok := renderFor(t, "sql.NullString")(v, pretty.Styles{})
	if !ok {
		t.Fatal("declined a NullString")
	}
	if !strings.Contains(out, "NULL") {
		t.Errorf("did not say NULL: %q", out)
	}
	// The carried value is whatever was in the struct when the scan found
	// NULL. Showing it is the bug this rendering exists to prevent.
	if strings.Contains(out, "leftover") {
		t.Errorf("showed the value behind an invalid null: %q", out)
	}
}

// TestTheTextNULLIsNotANull: a valid NullString holding the four letters is
// quoted, as any string is, so it does not read as the NULL it is not.
func TestTheTextNULLIsNotANull(t *testing.T) {
	v := parse(t, `{"t":"sql.NullString","k":"struct","f":[
		{"n":"String","v":{"k":"string","r":"NULL"}},
		{"n":"Valid","v":{"k":"scalar","r":"true"}}]}`)
	null := parse(t, `{"t":"sql.NullString","k":"struct","f":[
		{"n":"String","v":{"k":"string","r":""}},
		{"n":"Valid","v":{"k":"scalar","r":"false"}}]}`)
	r := renderFor(t, "sql.NullString")
	text, _ := r(v, pretty.Styles{})
	sqlNull, _ := r(null, pretty.Styles{})
	if text == sqlNull || !strings.Contains(text, `"NULL"`) {
		t.Errorf("the text NULL drew as %q and a NULL as %q", text, sqlNull)
	}
}

func TestValidNullShowsItsValue(t *testing.T) {
	v := parse(t, `{"t":"sql.NullInt64","k":"struct","f":[
		{"n":"Int64","v":{"k":"scalar","r":"42"}},
		{"n":"Valid","v":{"k":"scalar","r":"true"}}]}`)
	out, ok := renderFor(t, "sql.NullInt64")(v, pretty.Styles{})
	if !ok {
		t.Fatal("declined a valid NullInt64")
	}
	if !strings.Contains(out, "42") {
		t.Errorf("did not show the value: %q", out)
	}
	if strings.Contains(out, "Valid") {
		t.Errorf("leaked the Valid field: %q", out)
	}
}

// TestNullRenderersDecline on anything malformed — invariant 21's fallthrough.
func TestNullRenderersDecline(t *testing.T) {
	junk := []string{
		`{"t":"sql.NullString","k":"scalar","r":"nil"}`,
		`{"t":"sql.NullString","k":"struct","f":[{"n":"String","v":{"k":"string","r":"x"}}]}`,
		`{"t":"sql.NullString","k":"struct","f":[{"n":"Valid","v":{"k":"scalar","r":"yes"}}]}`,
	}
	r := renderFor(t, "sql.NullString")
	for _, blob := range junk {
		if out, ok := r(parse(t, blob), pretty.Styles{}); ok {
			t.Errorf("accepted %s and produced %q", blob, out)
		}
	}
}

// TestEveryNullTypeHasARenderer keeps the map honest against database/sql.
func TestEveryNullTypeHasARenderer(t *testing.T) {
	want := []string{
		"sql.NullString", "sql.NullBool", "sql.NullByte", "sql.NullFloat64",
		"sql.NullInt16", "sql.NullInt32", "sql.NullInt64", "sql.NullTime",
	}
	have := map[string]bool{}
	for _, r := range (SQL{}).Renders() {
		have[r.Type] = true
	}
	for _, typ := range want {
		if !have[typ] {
			t.Errorf("no renderer for %s", typ)
		}
	}
}
