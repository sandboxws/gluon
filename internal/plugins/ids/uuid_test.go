package ids

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/sandboxws/gluon/internal/pretty"
)

// uuidValue builds the pretty.Value the child sends for a uuid.UUID: an array
// of sixteen bytes, each in the byte renderer's form.
func uuidValue(t *testing.T, b []byte) pretty.Value {
	t.Helper()
	items := make([]string, 0, len(b))
	for _, c := range b {
		items = append(items, fmt.Sprintf(`{"k":"scalar","r":"%d (0x%02x)"}`, c, c))
	}
	blob := `{"t":"uuid.UUID","k":"list","l":16,"i":[` + strings.Join(items, ",") + `]}`
	var v pretty.Value
	if err := json.Unmarshal([]byte(blob), &v); err != nil {
		t.Fatal(err)
	}
	return v
}

func render(t *testing.T, b []byte) (string, bool) {
	t.Helper()
	return UUID{}.Renders()[0].Rich(uuidValue(t, b), pretty.Styles{})
}

// TestUUIDRendersCanonically. The value printer shows sixteen small integers,
// which is the least useful possible rendering of an identifier.
func TestUUIDRendersCanonically(t *testing.T) {
	// ee7a1a96-f9ef-4c40-a295-1dc5984b4292 — a v4, RFC 4122.
	b := []byte{
		0xee, 0x7a, 0x1a, 0x96, 0xf9, 0xef, 0x4c, 0x40,
		0xa2, 0x95, 0x1d, 0xc5, 0x98, 0x4b, 0x42, 0x92,
	}
	out, ok := render(t, b)
	if !ok {
		t.Fatal("declined a UUID")
	}
	if !strings.Contains(out, "ee7a1a96-f9ef-4c40-a295-1dc5984b4292") {
		t.Errorf("not the canonical form: %q", out)
	}
	if !strings.Contains(out, "v4 random") {
		t.Errorf("version not decoded: %q", out)
	}
	if !strings.Contains(out, "RFC 4122") {
		t.Errorf("variant not decoded: %q", out)
	}
}

// TestUUIDVersions covers the versions worth telling apart — v7 sorts by time
// and v4 does not, which is the reason to show this at all.
func TestUUIDVersions(t *testing.T) {
	cases := map[byte]string{
		1: "v1 time-based",
		4: "v4 random",
		7: "v7 unix-time-ordered",
	}
	for version, want := range cases {
		b := make([]byte, 16)
		b[6] = version << 4
		b[8] = 0x80 // RFC 4122 variant
		out, ok := render(t, b)
		if !ok {
			t.Fatalf("declined a v%d", version)
		}
		if !strings.Contains(out, want) {
			t.Errorf("v%d rendered as %q, want %q", version, out, want)
		}
	}
}

// TestNilUUIDIsNamed, because all-zeroes is a value people hit by accident and
// "v0, reserved (NCS)" would be a confusing way to say so.
func TestNilUUIDIsNamed(t *testing.T) {
	out, ok := render(t, make([]byte, 16))
	if !ok {
		t.Fatal("declined the nil UUID")
	}
	if !strings.Contains(out, "the nil UUID") {
		t.Errorf("got %q", out)
	}
	if !strings.Contains(out, "00000000-0000-0000-0000-000000000000") {
		t.Errorf("canonical form wrong: %q", out)
	}
}

// TestUUIDDeclinesWrongShapes — invariant 21's fallthrough.
func TestUUIDDeclinesWrongShapes(t *testing.T) {
	r := UUID{}.Renders()[0].Rich
	for _, blob := range []string{
		`{"t":"uuid.UUID","k":"scalar","r":"nil"}`,
		`{"t":"uuid.UUID","k":"list","i":[{"k":"scalar","r":"1"}]}`,
		`{"t":"uuid.UUID","k":"list","i":[` +
			strings.Repeat(`{"k":"scalar","r":"nope"},`, 15) + `{"k":"scalar","r":"nope"}]}`,
	} {
		var v pretty.Value
		if err := json.Unmarshal([]byte(blob), &v); err != nil {
			t.Fatal(err)
		}
		if out, ok := r(v, pretty.Styles{}); ok {
			t.Errorf("accepted %s and produced %q", blob, out)
		}
	}
}

// TestUUIDInlineIsTheCanonicalFormAlone. A table of UUIDs is scanned for which
// one, not for what kind, so the version is dropped and the id kept.
func TestUUIDInlineIsTheCanonicalFormAlone(t *testing.T) {
	b := []byte{
		0xee, 0x7a, 0x1a, 0x96, 0xf9, 0xef, 0x4c, 0x40,
		0xa2, 0x95, 0x1d, 0xc5, 0x98, 0x4b, 0x42, 0x92,
	}
	out, ok := UUID{}.Renders()[0].Inline(uuidValue(t, b), pretty.Styles{})
	if !ok {
		t.Fatal("the inline uuid form declined")
	}
	if out != "ee7a1a96-f9ef-4c40-a295-1dc5984b4292" {
		t.Errorf("inline form = %q, want the canonical id alone", out)
	}
}
