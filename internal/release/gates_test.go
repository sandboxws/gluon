package release

import (
	"strings"
	"testing"
)

func fixtureGates(t *testing.T) []Gate {
	t.Helper()
	g := Gates(fixture)
	if len(g) == 0 {
		t.Fatal("no gates parsed from the fixture")
	}
	return g
}

func TestGatesReadAllThreeHelpers(t *testing.T) {
	got := map[string]string{}
	for _, g := range fixtureGates(t) {
		got[g.Version+"/"+g.Feature] = g.Where
	}
	for _, want := range []string{
		"1.27/generic method", // verifyVersionf
		"1.13/binary literal", // versionErrorf
		"1.14/",               // allowVersion, unnamed
		"1.27/use of promoted field … in struct literal of type …", // verbs cleaned
	} {
		if _, ok := got[want]; !ok {
			t.Errorf("missing gate %q; got %v", want, got)
		}
	}
}

// The bug this holds: matching only check.allowVersion(...) and not a bare
// allowVersion(...) drops range-over-int and range-over-func, which are the two
// gates a release explorer most needs to see.
func TestABareAllowVersionCallIsAGateToo(t *testing.T) {
	versions := map[string]bool{}
	for _, g := range fixtureGates(t) {
		versions[g.Version] = true
	}
	for _, v := range []string{"1.22", "1.23"} {
		if !versions[v] {
			t.Errorf("go %s gate missed — it is called as a function parameter", v)
		}
	}
}

func TestACallWithNoVersionIsNotAGate(t *testing.T) {
	for _, g := range fixtureGates(t) {
		if g.Version == "" {
			t.Errorf("parsed a gate with no version: %+v", g)
		}
	}
}

func TestGatesAreDedupedAndCarryTheirLocation(t *testing.T) {
	var generic int
	for _, g := range fixtureGates(t) {
		if g.Feature == "generic method" {
			generic++
			if !strings.HasPrefix(g.Where, "fake.go:") {
				t.Errorf("Where = %q, want a file and line", g.Where)
			}
		}
	}
	if generic != 1 {
		t.Errorf("the same gate appears %d times, want 1", generic)
	}
}

// A verb stands for a name the checker fills in at the error site and this
// package cannot know. Dropping it would claim the feature is called "built-in".
func TestCleanFeatureKeepsAPlaceForEachVerb(t *testing.T) {
	cases := map[string]string{
		"generic method": "generic method",
		"built-in %s":    "built-in …",
		"new(%s)":        "new(…)",
		"predeclared %s": "predeclared …",
		"invalid operation: signed shift count %s": "signed shift count …",
		"100%% sure": "100% sure",
	}
	for in, want := range cases {
		if got := cleanFeature(in); got != want {
			t.Errorf("cleanFeature(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestNamedSeparatesTheTwoKinds(t *testing.T) {
	if (Gate{Feature: "generic method"}).Named() != true {
		t.Error("a gate with a feature is named")
	}
	if (Gate{}).Named() != false {
		t.Error("a gate with no feature is not named")
	}
}

func TestGatedVersionsAreOldestFirstAndUnique(t *testing.T) {
	got := GatedVersions(fixtureGates(t))
	for i := 1; i < len(got); i++ {
		if Compare(got[i-1], got[i]) >= 0 {
			t.Errorf("GatedVersions = %v, not oldest first and unique", got)
			break
		}
	}
}

// A distribution without a src tree loses the gates and nothing else, the way a
// missing doc/godebug.md loses only its own half.
func TestNoSrcTreeIsNotAnError(t *testing.T) {
	if g := Gates("testdata/goroot-nodoc"); g != nil {
		t.Errorf("Gates without a src tree = %v, want none", g)
	}
	rels, err := Load("testdata/goroot-nodoc")
	if err != nil {
		t.Fatalf("Load without a src tree: %v", err)
	}
	if len(rels) == 0 {
		t.Error("lost the api half too")
	}
}

func TestReleasesCarryTheirOwnGates(t *testing.T) {
	rels, err := Load(fixture)
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range rels {
		for _, g := range r.Gates {
			if g.Version != r.Version {
				t.Errorf("go %s carries a gate for go %s", r.Version, g.Version)
			}
		}
	}
}
