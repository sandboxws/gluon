package inspect

import (
	"fmt"
	"go/types"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sandboxws/gluon/internal/check"
	"github.com/sandboxws/gluon/internal/pretty"
	"github.com/sandboxws/gluon/internal/render"
	"github.com/sandboxws/gluon/internal/session"
)

// analyze builds the same temp module eval uses, renders the given lines as a
// session, and type-checks the result — the shortest path to a *check.Result
// that the inspector can be pointed at.
func analyze(t *testing.T, lines ...string) *check.Result {
	t.Helper()
	dir := t.TempDir()

	out, err := exec.Command("go", "env", "GOVERSION").Output()
	if err != nil {
		t.Skip("no go toolchain on PATH")
	}
	parts := strings.SplitN(strings.TrimPrefix(strings.TrimSpace(string(out)), "go"), ".", 3)
	mod := fmt.Sprintf("module gluon.local/session\n\ngo %s.%s\n", parts[0], parts[1])
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte(mod), 0o644); err != nil {
		t.Fatal(err)
	}

	s := &session.Session{}
	for _, line := range lines {
		e, err := session.Classify(line)
		if err != nil {
			t.Fatalf("classify %q: %v", line, err)
		}
		s.Append(e)
	}

	raw, err := render.Main(s, nil)
	if err != nil {
		t.Fatal(err)
	}
	fixed, err := render.FixImports(filepath.Join(dir, "main.go"), []byte(raw))
	if err != nil {
		t.Fatalf("goimports: %v", err)
	}

	c := check.New(check.Options{
		Dir:     dir,
		Env:     append(os.Environ(), "GOFLAGS=", "GOWORK=off", "GOPROXY=off", "GOTOOLCHAIN=local"),
		Runtime: render.RuntimeFiles(),
	})
	res, err := c.Check(fixed)
	if err != nil {
		t.Fatalf("check: %v", err)
	}
	t.Cleanup(func() { _ = c })
	return res
}

func target(t *testing.T, lines ...string) *Target {
	t.Helper()
	tg, err := Resolve(analyze(t, lines...))
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	return tg
}

func TestDescribe(t *testing.T) {
	plain := pretty.Styles{}
	tests := []struct {
		name    string
		lines   []string
		verbose bool
		want    []string
	}{
		{
			name:  "slice",
			lines: []string{"x := []int{3, 1, 2}", "x"},
			want:  []string{"[]int"},
		},
		{
			// byte is numeric — a well-known trip-hazard.
			name:  "indexing a string yields byte",
			lines: []string{`s := "héllo"`, "s[1]"},
			want:  []string{"byte"},
		},
		{
			name:  "multi-value call",
			lines: []string{`strconv.Atoi("12")`},
			want:  []string{"(int, error)", "2 values"},
		},
		{
			name:  "a call with no results has nothing to describe",
			lines: []string{"x := []int{1}", "slices.Sort(x)"},
			want:  []string{"no value"},
		},
		{
			name:  "an interface says so, because the static type is not the whole story",
			lines: []string{"var e error", "e"},
			want:  []string{"error", "dynamic type"},
		},
		{
			name:    "verbose shows the underlying type of a named type",
			lines:   []string{"type Celsius float64", "Celsius(20)"},
			verbose: true,
			want:    []string{"Celsius", "underlying", "float64", "floating point"},
		},
		{
			name:    "verbose describes a map's key and element",
			lines:   []string{`m := map[string][]int{}`, "m"},
			verbose: true,
			want:    []string{"map[string][]int", "key", "string", "element", "[]int"},
		},
		{
			name:  "a type named rather than used says so",
			lines: []string{"type Point struct{ X int }", "Point"},
			want:  []string{"Point", "a type, not a value"},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := target(t, tc.lines...).Describe(tc.verbose, plain)
			for _, want := range tc.want {
				if !strings.Contains(got, want) {
					t.Errorf("Describe() = %q, want it to contain %q", got, want)
				}
			}
		})
	}
}

// The value/pointer receiver rule is a real Go stumbling block, so a method
// only reachable through *T must be marked rather than silently merged in.
func TestMethodsMarkPointerReceivers(t *testing.T) {
	tg := target(t,
		"type Point struct{ X, Y int }",
		"func (p Point) Dist() int { return p.X*p.X + p.Y*p.Y }",
		"func (p *Point) Move(dx int) { p.X += dx }",
		"Point{}",
	)
	ms, _, _ := tg.Methods(nil)
	got := map[string]bool{}
	for _, m := range ms {
		got[m.Name] = m.PointerOnly
	}
	if len(ms) != 2 {
		t.Fatalf("got %d methods, want 2: %+v", len(ms), ms)
	}
	if got["Dist"] {
		t.Error("Dist has a value receiver but was marked pointer-only")
	}
	if !got["Move"] {
		t.Error("Move has a pointer receiver but was not marked")
	}
}

// Promoted methods from an embedded type come along for free.
func TestMethodsIncludePromoted(t *testing.T) {
	tg := target(t,
		"type Base struct{}",
		"func (Base) Hello() string { return \"hi\" }",
		"type Derived struct{ Base }",
		"Derived{}",
	)
	ms, _, _ := tg.Methods(nil)
	if len(ms) != 1 || ms[0].Name != "Hello" {
		t.Errorf("methods = %+v, want the promoted Hello", ms)
	}
}

func TestInterfaceSatisfaction(t *testing.T) {
	res := analyze(t,
		"type ByLen []string",
		"func (b ByLen) Len() int { return len(b) }",
		"func (b ByLen) Less(i, j int) bool { return len(b[i]) < len(b[j]) }",
		"func (b ByLen) Swap(i, j int) { b[i], b[j] = b[j], b[i] }",
		"ByLen{}",
	)
	tg, err := Resolve(res)
	if err != nil {
		t.Fatal(err)
	}
	_, sat, _ := tg.Methods([]Named{{Name: "sort.Interface", Iface: sortInterface(t, res)}})
	if len(sat) != 1 || sat[0] != "sort.Interface" {
		t.Errorf("satisfied = %v, want [sort.Interface]", sat)
	}
}

// An interface trivially implements itself; reporting that is noise.
func TestInterfaceDoesNotReportSatisfyingItself(t *testing.T) {
	res := analyze(t, "type Shape interface{ Area() float64 }", "var s Shape", "s")
	tg, err := Resolve(res)
	if err != nil {
		t.Fatal(err)
	}
	ifaces := sessionInterfaces(res)
	if len(ifaces) != 1 {
		t.Fatalf("expected the session's own Shape, got %v", ifaces)
	}
	_, sat, ptrSat := tg.Methods(ifaces)
	if len(sat) != 0 || len(ptrSat) != 0 {
		t.Errorf("Shape reported satisfying %v / %v, want neither", sat, ptrSat)
	}
}

// sortInterface digs sort.Interface out of the checked package's imports,
// which the injected runtime guarantees are loaded.
func sortInterface(t *testing.T, res *check.Result) *types.Interface {
	t.Helper()
	for _, p := range res.Pkg.Imports() {
		if p.Path() != "sort" {
			continue
		}
		if obj := p.Scope().Lookup("Interface"); obj != nil {
			if in, ok := obj.Type().Underlying().(*types.Interface); ok {
				return in
			}
		}
	}
	t.Skip("sort.Interface not reachable from the checked package")
	return nil
}

// The rich form is what a terminal sees, so its content is worth pinning even
// though its exact borders are not.
func TestRenderMethodsShowsThePointerRule(t *testing.T) {
	tg := target(t,
		"type Point struct{ X int }",
		"func (p *Point) Move(dx int) { p.X += dx }",
		"Point{}",
	)
	ms, _, _ := tg.Methods(nil)
	got := RenderMethods(tg, ms, nil, nil, pretty.PlainStyles())
	for _, want := range []string{"Point", "Move", "pointer receiver"} {
		if !strings.Contains(got, want) {
			t.Errorf("RenderMethods() = %q, want it to contain %q", got, want)
		}
	}
}
