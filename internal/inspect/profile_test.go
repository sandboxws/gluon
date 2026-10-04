package inspect

import (
	"strings"
	"testing"

	"github.com/sandboxws/gluon/internal/pretty"
)

func sampleProfile() Profile {
	return Profile{
		Label:  "cpu profile",
		Head:   "3 samples",
		Caveat: "too few samples to draw a conclusion from",
		Rows: []ProfileRow{
			{Flat: "900ms", Pct: "75.00%", Fn: "main.fib", Where: "entry 1", Src: "return n"},
			{Flat: "200ms", Pct: "16.67%", Fn: "strings.Repeat", Where: "strings/strings.go:576"},
			{Flat: "10ms", Pct: "0.83%", Fn: "main.veryLongFunctionName"},
		},
	}
}

// TestProfileCaveatComesBeforeTheRanking. A warning printed under a ranking is
// read after the ranking has already been believed.
func TestProfileCaveatComesBeforeTheRanking(t *testing.T) {
	out := PlainProfile(sampleProfile())
	caveat := strings.Index(out, "too few samples")
	first := strings.Index(out, "main.fib")
	if caveat < 0 || first < 0 || caveat > first {
		t.Errorf("the caveat is not above the ranking:\n%s", out)
	}
	if !strings.HasPrefix(out, "cpu profile  3 samples\n") {
		t.Errorf("the head does not lead with the label and the count:\n%s", out)
	}
}

// TestProfileColumnsLineUp: the two numbers are right-aligned so magnitudes
// can be compared down the column, and no line carries trailing whitespace.
func TestProfileColumnsLineUp(t *testing.T) {
	out := PlainProfile(sampleProfile())
	var starts []int
	for _, line := range strings.Split(out, "\n") {
		if !strings.Contains(line, "%") || strings.Contains(line, "too few") {
			continue
		}
		if line != strings.TrimRight(line, " ") {
			t.Errorf("trailing whitespace on %q", line)
		}
		starts = append(starts, strings.Index(line, "%"))
	}
	if len(starts) != 3 {
		t.Fatalf("%d ranking lines, want 3:\n%s", len(starts), out)
	}
	for _, at := range starts[1:] {
		if at != starts[0] {
			t.Errorf("the percentage column does not line up:\n%s", out)
		}
	}
}

// TestProfileWithNoRowsSaysWhatItFound. An empty table is not an answer.
func TestProfileWithNoRowsSaysWhatItFound(t *testing.T) {
	p := Profile{
		Label:   "mem profile, allocated bytes",
		Nothing: "no allocation recorded — nothing in this expression reached the heap",
	}
	out := PlainProfile(p)
	if !strings.Contains(out, "no allocation recorded") {
		t.Errorf("the report does not say what it found:\n%s", out)
	}
	if !strings.Contains(out, "allocated bytes") {
		t.Errorf("the report does not name its measure:\n%s", out)
	}

	// And a report with neither rows nor a message still says something.
	if out := PlainProfile(Profile{Label: "cpu profile"}); !strings.Contains(out, "nothing") {
		t.Errorf("an empty report renders as nothing at all:\n%q", out)
	}
}

// TestProfileTwinsCarryTheSameText. Result.Out always holds the plain form
// when the rich one went to a modal, so a pipe must lose no information —
// invariant 19.
func TestProfileTwinsCarryTheSameText(t *testing.T) {
	for _, p := range []Profile{
		sampleProfile(),
		{Label: "mem profile, allocated bytes", Nothing: "no allocation recorded"},
	} {
		plain := PlainProfile(p)
		rich := RenderProfile(p, pretty.PlainStyles())
		if strings.ContainsRune(plain, 0x1b) {
			t.Errorf("the plain form carries escape codes:\n%q", plain)
		}
		if unstyled(rich) != plain {
			t.Errorf("the two forms differ once styling is stripped:\n%q\n---\n%q", unstyled(rich), plain)
		}
	}
}

func unstyled(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] == 0x1b {
			for i < len(s) && s[i] != 'm' {
				i++
			}
			continue
		}
		b.WriteByte(s[i])
	}
	return b.String()
}
