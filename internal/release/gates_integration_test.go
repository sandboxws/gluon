//go:build integration

package release

import (
	"strings"
	"testing"
)

// This is the test that answers "are we missing a language feature?" — the
// question that found generic methods absent from Go 1.27 after the first
// version of this package shipped.
//
// The curated notes are hand-written, so nothing about them is automatic; what
// can be automatic is noticing that a release gates syntax nobody wrote up. The
// type checker in $GOROOT/src/go/types is the authority: if it admits something
// only at go1.N, then go1.N changed the language, whatever anybody remembered.
//
// It is keyed on the version rather than on the feature string. All three
// gating helpers name a version, so the set of gated versions is exact; only two
// of the three name a feature, so a feature-keyed check would be built on a list
// that is known to be short — see the comment at the top of gates.go.

// waived are the gated versions deliberately left without a note, with why.
// Adding a version here is a decision, which is the point of it being a literal
// somebody has to edit rather than a rule that quietly covers a range.
var waived = map[string]string{
	"1.9":  "type alias, before the 1.18 curation window this dataset starts at",
	"1.13": "numeric literal spellings — binary, 0o octal, hex floats, underscores",
	"1.14": "overlapping method sets in embedded interfaces, pre-generics",
	"1.17": "unsafe.Add and unsafe.Slice, which are library calls in all but name",
}

func TestEveryGatedVersionIsWrittenUpOrWaived(t *testing.T) {
	gates := Gates(GOROOT())
	if len(gates) == 0 {
		t.Skip("no toolchain release data: $GOROOT/src/go/types is not readable")
	}
	needed := map[string]bool{}
	for _, v := range CuratedVersions() {
		notes, _ := notesFor(v)
		for _, n := range notes {
			needed[n.Needs] = true
		}
	}
	for _, v := range GatedVersions(gates) {
		if needed[v] || waived[v] != "" {
			continue
		}
		var named []string
		for _, g := range GatesAt(gates, v) {
			if g.Named() {
				named = append(named, g.Feature)
			} else {
				named = append(named, "an unnamed gate at "+g.Where)
			}
		}
		t.Errorf("go %s gates language syntax and no note requires it: %s\n"+
			"\twrite internal/release/notes/go%s.toml, or add %q to waived with a reason",
			v, strings.Join(named, ", "), v, v)
	}
}

// The other direction, and the one that already caught a mistake: a note
// claiming a directive nothing gates. The first draft filed generic type
// aliases at needs = "1.24" because that is the release they shipped in, and
// the checker gates them at go1.23 — so the number a reader would have put in
// their go.mod was wrong by one.
func TestAGatedNoteRequiresAVersionTheCheckerActuallyGates(t *testing.T) {
	gates := Gates(GOROOT())
	if len(gates) == 0 {
		t.Skip("no toolchain release data: $GOROOT/src/go/types is not readable")
	}
	gated := map[string]bool{}
	for _, v := range GatedVersions(gates) {
		gated[v] = true
	}
	for _, v := range CuratedVersions() {
		notes, _ := notesFor(v)
		for _, n := range notes {
			if !n.Gated() || gated[n.Needs] {
				continue
			}
			t.Errorf("%s/%s needs go %s and is marked %q, but the checker gates "+
				"nothing at go %s — the release it shipped in is not always the "+
				"language version it is gated at",
				v, n.Name, n.Needs, n.Below, n.Needs)
		}
	}
}

// A release with no gate and no note is unwritten; a release with no gate and an
// empty note file is checked. The second is a claim, so it is worth holding to
// the toolchain: an empty file for a version the checker does gate is somebody
// having looked and missed something.
func TestAnEmptyNoteFileMeansTheCheckerGatesNothingThere(t *testing.T) {
	gates := Gates(GOROOT())
	if len(gates) == 0 {
		t.Skip("no toolchain release data: $GOROOT/src/go/types is not readable")
	}
	gated := map[string]bool{}
	for _, v := range GatedVersions(gates) {
		gated[v] = true
	}
	for _, v := range CuratedVersions() {
		notes, _ := notesFor(v)
		if len(notes) == 0 && gated[v] {
			t.Errorf("go%s.toml says the language did not change, but the checker "+
				"gates something at go %s", v, v)
		}
	}
}
