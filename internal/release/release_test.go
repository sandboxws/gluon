package release

import (
	"strings"
	"testing"
)

const fixture = "testdata/goroot"

func load(t *testing.T) []Release {
	t.Helper()
	rels, err := Load(fixture)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	return rels
}

func TestLoadReadsEveryApiFileNewestFirst(t *testing.T) {
	rels := load(t)
	var got []string
	for _, r := range rels {
		got = append(got, r.Version)
	}
	want := []string{"1.21", "1.20", "1.19", "1.0"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("versions = %v, want %v", got, want)
	}
}

// go1.txt is the Go 1 API surface, not a delta, and a screen that called its
// 30,000 lines "added in 1.0" would be describing the wrong thing.
func TestGo1IsMarkedInitial(t *testing.T) {
	rels := load(t)
	for _, r := range rels {
		if want := r.Version == "1.0"; r.Initial != want {
			t.Errorf("%s: Initial = %v, want %v", r.Version, r.Initial, want)
		}
	}
}

func TestACommentLineIsNotADeclaration(t *testing.T) {
	rels := load(t)
	r, ok := Find(rels, "1.20")
	if !ok {
		t.Fatal("no 1.20 in the fixture")
	}
	for _, s := range r.API {
		if s.Pkg == "" || strings.HasPrefix(s.Pkg, "#") {
			t.Errorf("parsed a comment into %+v", s)
		}
	}
}

// The reason the collapse exists: go1.20.txt is 9,165 lines of which 8,864 are
// one declaration repeated per platform.
func TestPlatformDuplicatesCollapseIntoOneSymbol(t *testing.T) {
	rels := load(t)
	r, _ := Find(rels, "1.20")
	var found int
	for _, s := range r.API {
		if s.Pkg != "syscall" {
			continue
		}
		found++
		want := []string{"freebsd-riscv64", "linux-386", "linux-amd64"}
		if strings.Join(s.Platforms, ",") != strings.Join(want, ",") {
			t.Errorf("Platforms = %v, want %v", s.Platforms, want)
		}
	}
	if found != 1 {
		t.Errorf("syscall symbols = %d, want 1 collapsed entry", found)
	}
}

func TestADeclarationOnEveryPlatformCarriesNoPlatforms(t *testing.T) {
	rels := load(t)
	r, _ := Find(rels, "1.20")
	for _, s := range r.API {
		if s.Pkg == "errors" && len(s.Platforms) != 0 {
			t.Errorf("errors.Join carries platforms %v", s.Platforms)
		}
	}
}

func TestTheIssueNumberBecomesAProposalURL(t *testing.T) {
	rels := load(t)
	r, _ := Find(rels, "1.19")
	for _, s := range r.API {
		if s.Issue == 0 {
			t.Errorf("%s %s has no issue, but 1.19 requires one", s.Pkg, s.Decl)
		}
		if want := "https://go.dev/issue/47005"; s.Pkg == "net/url" && s.URL() != want {
			t.Errorf("URL() = %q, want %q", s.URL(), want)
		}
	}
}

// Before go1.19.txt the suffix was not required, and a URL invented for a line
// that never named an issue would be a citation gluon made up.
func TestAFileWithoutIssueNumbersPrintsNoURL(t *testing.T) {
	rels := load(t)
	r, _ := Find(rels, "1.0")
	for _, s := range r.API {
		if s.Issue != 0 || s.URL() != "" {
			t.Errorf("%s %s: Issue = %d, URL = %q, want none", s.Pkg, s.Decl, s.Issue, s.URL())
		}
	}
}

func TestKindIsTheLeadingWord(t *testing.T) {
	rels := load(t)
	r, _ := Find(rels, "1.19")
	kinds := map[string]int{}
	for _, s := range r.API {
		kinds[s.Kind()]++
	}
	for _, want := range []string{"func", "type", "method"} {
		if kinds[want] == 0 {
			t.Errorf("no %s in 1.19, got %v", want, kinds)
		}
	}
}

func TestPackagesAreSortedAndUnique(t *testing.T) {
	rels := load(t)
	r, _ := Find(rels, "1.19")
	got := r.Packages()
	want := []string{"net/url", "sync/atomic"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("Packages() = %v, want %v", got, want)
	}
}

func TestOnlyTheHistorySectionBecomesBehaviour(t *testing.T) {
	rels := load(t)
	r, _ := Find(rels, "1.21")
	if len(r.Behaviour) != 1 {
		t.Fatalf("Behaviour = %d entries, want 1: %+v", len(r.Behaviour), r.Behaviour)
	}
	if got := r.Behaviour[0].Setting; got != "panicnil" {
		t.Errorf("Setting = %q, want panicnil", got)
	}
	if !strings.Contains(r.Behaviour[0].Text, "panic(nil)") {
		t.Errorf("Text lost the paragraph: %q", r.Behaviour[0].Text)
	}
}

// The two sections above the history describe the mechanism. Folding them into
// a release would attribute them to whichever heading came next.
func TestTheIntroductionIsAttributedToNoRelease(t *testing.T) {
	rels := load(t)
	for _, r := range rels {
		for _, b := range r.Behaviour {
			if strings.Contains(b.Text, "prose about the mechanism") {
				t.Errorf("%s absorbed the introduction", r.Version)
			}
		}
	}
}

func TestCompareOrdersMinorsNumerically(t *testing.T) {
	cases := []struct {
		a, b string
		want int
	}{
		{"1.9", "1.10", -1},
		{"1.10", "1.9", 1},
		{"1.24", "1.24", 0},
		{"1.0", "1.27", -1},
	}
	for _, c := range cases {
		if got := Compare(c.a, c.b); got != c.want {
			t.Errorf("Compare(%q, %q) = %d, want %d", c.a, c.b, got, c.want)
		}
	}
}

func TestFindAcceptsEitherSpelling(t *testing.T) {
	rels := load(t)
	for _, v := range []string{"1.21", "go1.21", " 1.21 "} {
		if _, ok := Find(rels, v); !ok {
			t.Errorf("Find(%q) missed", v)
		}
	}
	if _, ok := Find(rels, "1.99"); ok {
		t.Error("Find(1.99) hit")
	}
}

// A toolchain whose doc/godebug.md is missing still has an api directory, and
// half the data is better than an error naming neither half.
func TestAMissingGodebugFileLosesOnlyThatHalf(t *testing.T) {
	rels, err := Load("testdata/goroot-nodoc")
	if err != nil {
		t.Fatalf("Load without a doc dir: %v", err)
	}
	r, ok := Find(rels, "1.21")
	if !ok {
		t.Fatal("no 1.21")
	}
	if len(r.API) == 0 {
		t.Error("lost the api half too")
	}
	if len(r.Behaviour) != 0 {
		t.Errorf("Behaviour = %+v, want none", r.Behaviour)
	}
}

// A directory that is not a GOROOT is an error rather than an empty list: a
// screen reporting that Go has never added anything would be the wrong answer
// to "your toolchain is somewhere else".
func TestADirectoryWithNoApiFilesIsAnError(t *testing.T) {
	if _, err := Load("testdata"); err == nil {
		t.Error("Load(testdata) succeeded")
	}
}
