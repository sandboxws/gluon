package release

import (
	"slices"
	"strings"
	"testing"
)

// The embedded notes go through the same decoder any other file would, so this
// is a real test of the loading path rather than of a literal. It is also what
// makes the skip-on-error branch in curated() unreachable.
func TestNotesParseThroughTheLoader(t *testing.T) {
	entries, err := notesFS.ReadDir("notes")
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	if len(entries) == 0 {
		t.Fatal("no notes are embedded")
	}
	for _, e := range entries {
		data, err := notesFS.ReadFile("notes/" + e.Name())
		if err != nil {
			t.Fatalf("%s: %v", e.Name(), err)
		}
		f, err := decodeNotes(string(data))
		if err != nil {
			t.Errorf("%s: %v", e.Name(), err)
			continue
		}
		if f.Version == "" {
			t.Errorf("%s: no version", e.Name())
		}
		if want := "go" + f.Version + ".toml"; e.Name() != want {
			t.Errorf("%s declares version %q, so it should be named %s", e.Name(), f.Version, want)
		}
		// A file with no notes is deliberate: it is somebody having checked and
		// found the language unchanged, which is what makes Curated true and
		// the column read "none" instead of "—".
	}
}

func TestEveryNoteIsWellFormed(t *testing.T) {
	seen := map[string]bool{}
	for _, v := range CuratedVersions() {
		notes, _ := notesFor(v)
		for _, n := range notes {
			key := v + "/" + n.Name
			if seen[key] {
				t.Errorf("%s: two notes with the same name", key)
			}
			seen[key] = true

			if n.Name == "" || strings.ContainsAny(n.Name, " \t") {
				t.Errorf("%s: Name %q must be one word — it is the -run selector", v, n.Name)
			}
			for field, got := range map[string]string{
				"title": n.Title, "text": n.Text, "snippet": n.Snippet, "needs": n.Needs,
			} {
				if strings.TrimSpace(got) == "" {
					t.Errorf("%s: %s is empty", key, field)
				}
			}
			if _, _, ok := split(n.Needs); !ok {
				t.Errorf("%s: needs = %q is not a version", key, n.Needs)
			}
			// Issue is optional on purpose: not every release note links a
			// proposal, and a number invented for one that does not is a
			// fabricated citation.
			if n.Issue < 0 {
				t.Errorf("%s: issue = %d", key, n.Issue)
			}
		}
	}
}

// Below is the field the integration tier reads to decide which way to check a
// snippet, so a third spelling would silently check nothing.
func TestBelowIsOneOfTwoWords(t *testing.T) {
	for _, v := range CuratedVersions() {
		notes, _ := notesFor(v)
		for _, n := range notes {
			if !slices.Contains(BelowValues, n.Below) {
				t.Errorf("%s/%s: below = %q, want one of %v", v, n.Name, n.Below, BelowValues)
			}
		}
	}
}

// A snippet is fed to the REPL line by line, so a leading tab or a stray blank
// first line changes what gets classified.
func TestASnippetIsTrimmedAndImportsNothing(t *testing.T) {
	for _, v := range CuratedVersions() {
		notes, _ := notesFor(v)
		for _, n := range notes {
			if n.Snippet != strings.TrimSpace(n.Snippet) {
				t.Errorf("%s/%s: snippet has surrounding space", v, n.Name)
			}
			if strings.Contains(n.Snippet, "import ") {
				t.Errorf("%s/%s: a note imports something — the subject is the "+
					"language, and an import puts goimports in front of it", v, n.Name)
			}
		}
	}
}

// Empty Lang means nobody has written notes, never "this release changed no
// syntax". Curated is the difference, and a release with a file must report it.
func TestCuratedIsAbsenceOfAFileAndNotAbsenceOfNotes(t *testing.T) {
	rels, err := Load(fixture)
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range rels {
		notes, ok := notesFor(r.Version)
		if r.Curated != ok {
			t.Errorf("%s: Curated = %v, but notesFor says %v", r.Version, r.Curated, ok)
		}
		if ok && len(r.Lang) != len(notes) {
			t.Errorf("%s: Lang has %d notes, want %d", r.Version, len(r.Lang), len(notes))
		}
	}
}

func TestNoteLooksUpByName(t *testing.T) {
	r := Release{Lang: []Note{{Name: "range-over-func"}}, Curated: true}
	if _, ok := r.Note("range-over-func"); !ok {
		t.Error("Note missed a name it has")
	}
	if _, ok := r.Note("nope"); ok {
		t.Error("Note hit a name it does not have")
	}
}

func TestGatedReadsBelow(t *testing.T) {
	if !(Note{Below: "error"}).Gated() {
		t.Error(`below "error" is gated`)
	}
	if (Note{Below: "behaves differently"}).Gated() {
		t.Error(`below "behaves differently" is not gated`)
	}
}

func TestCuratedVersionsAreNewestFirst(t *testing.T) {
	got := CuratedVersions()
	for i := 1; i < len(got); i++ {
		if Compare(got[i-1], got[i]) <= 0 {
			t.Errorf("CuratedVersions() = %v, not newest first", got)
			break
		}
	}
}
