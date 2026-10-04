package inspect

import (
	"errors"
	"go/types"
	"strings"
	"testing"

	"github.com/sandboxws/gluon/internal/check"
)

// probe analyzes the session with the given arguments appended as one
// expression, the way a two-argument command does, and returns them resolved.
func probe(t *testing.T, args []string, lines ...string) (*check.Result, []Operand) {
	t.Helper()
	r := analyze(t, append(lines, ProbeExpr(args))...)
	ops, err := Operands(r, args)
	if err != nil {
		t.Fatalf("operands: %v", err)
	}
	return r, ops
}

// TestResolveInterface covers the four ways a second argument can go: a name
// the session declared, a qualified stdlib name, an alias for one, and a name
// that resolves to something that is not an interface at all.
func TestResolveInterface(t *testing.T) {
	decls := []string{
		"type Shape interface{ Area() float64 }",
		"type Alias = io.Reader",
		"type Point struct{ X, Y int }",
	}
	tests := []struct {
		name    string
		arg     string
		wantErr error
		methods int
	}{
		{name: "session-local", arg: "Shape", methods: 1},
		{name: "qualified stdlib", arg: "io.Writer", methods: 1},
		{name: "alias", arg: "Alias", methods: 1},
		{name: "not an interface", arg: "Point", wantErr: ErrNotInterface},
		{name: "unresolved", arg: "Nope", wantErr: ErrUnresolved},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, ops := probe(t, []string{tc.arg}, decls...)
			in, err := ops[0].Iface()
			if tc.wantErr != nil {
				if !errors.Is(err, tc.wantErr) {
					t.Fatalf("err = %v, want %v", err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if in.NumMethods() != tc.methods {
				t.Errorf("%d methods, want %d", in.NumMethods(), tc.methods)
			}
		})
	}
}

// TestSatisfies covers the four answers the report has to tell apart. The
// pointer-only case is the one the command exists for: the compiler's error
// names neither the method nor the receiver rule.
func TestSatisfies(t *testing.T) {
	decls := []string{
		"type Shape interface{ Area() float64 }",
		"type Sq struct{ side float64 }",
		"func (s Sq) Area() float64 { return s.side * s.side }",
		"type Tri struct{ b, h float64 }",
		"func (t *Tri) Area() float64 { return t.b * t.h / 2 }",
		"type Bad struct{}",
		"func (Bad) Area() int { return 0 }",
		"type Empty struct{}",
	}
	tests := []struct {
		name      string
		typ       string
		wantOK    bool
		wantPtr   bool
		wantWrong bool
		method    string
	}{
		{name: "implements", typ: "Sq", wantOK: true},
		{name: "pointer only", typ: "Tri", wantPtr: true, method: "Area"},
		{name: "wrong signature", typ: "Bad", wantWrong: true, method: "Area"},
		{name: "missing", typ: "Empty", method: "Area"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			r, ops := probe(t, []string{tc.typ, "Shape"}, decls...)
			in, err := ops[1].Iface()
			if err != nil {
				t.Fatal(err)
			}
			s := Satisfies(r.Pkg, ops[0].Type, in, ops[1].Name())
			if s.OK != tc.wantOK {
				t.Errorf("OK = %v, want %v", s.OK, tc.wantOK)
			}
			if s.PtrOK != tc.wantPtr {
				t.Errorf("PtrOK = %v, want %v", s.PtrOK, tc.wantPtr)
			}
			if s.Wrong != tc.wantWrong {
				t.Errorf("Wrong = %v, want %v", s.Wrong, tc.wantWrong)
			}
			if s.Method != tc.method {
				t.Errorf("Method = %q, want %q", s.Method, tc.method)
			}
			if tc.wantWrong && (s.Want == "" || s.Have == "" || s.Want == s.Have) {
				t.Errorf("want/have = %q/%q; a mismatch has to show both", s.Want, s.Have)
			}
		})
	}
}

// TestSatisfiesIsOneCheck is the reason every two-argument command resolves its
// operands together. A named type's identity belongs to the check that made it,
// so the same declarations resolved in two runs compare as different types —
// and the report would name a method as mismatched while printing one identical
// signature as both the required and the actual one.
func TestSatisfiesIsOneCheck(t *testing.T) {
	decls := []string{
		"type ID string",
		"type Store interface{ Get(ID) error }",
		"type S struct{}",
		"func (S) Get(ID) error { return nil }",
	}
	r, ops := probe(t, []string{"S", "Store"}, decls...)
	in, err := ops[1].Iface()
	if err != nil {
		t.Fatal(err)
	}
	if s := Satisfies(r.Pkg, ops[0].Type, in, ops[1].Name()); !s.OK {
		t.Fatalf("S should implement Store; got %+v", s)
	}

	// The same pair from two runs, which is what a per-argument Analyze would
	// have produced.
	_, a := probe(t, []string{"S"}, decls...)
	_, b := probe(t, []string{"Store"}, decls...)
	split, err := b[0].Iface()
	if err != nil {
		t.Fatal(err)
	}
	if types.Implements(a[0].Type, split) {
		t.Skip("go/types now compares these across checks; the single-run rule is no longer load-bearing")
	}
}

// TestPlainSatisfaction pins the pipe-friendly form of each answer.
func TestPlainSatisfaction(t *testing.T) {
	tests := []struct {
		name string
		s    Satisfaction
		want []string
	}{
		{
			name: "implements",
			s:    Satisfaction{Type: "bytes.Buffer", Iface: "io.Writer", OK: true},
			want: []string{"bytes.Buffer implements io.Writer"},
		},
		{
			name: "missing",
			s:    Satisfaction{Type: "Empty", Iface: "Shape", Method: "Area", Want: "() float64"},
			want: []string{"Empty does not implement Shape", "missing Area() float64"},
		},
		{
			name: "pointer only",
			s: Satisfaction{Type: "Tri", Iface: "Shape", Method: "Area", Want: "() float64",
				PtrOK: true},
			want: []string{"*Tri implements Shape", "pointer receiver"},
		},
		{
			name: "wrong signature",
			s: Satisfaction{Type: "Bad", Iface: "Shape", Method: "Area", Wrong: true,
				Want: "() float64", Have: "() int"},
			want: []string{"required Area() float64", "has Area() int"},
		},
		{
			name: "value with a note",
			s: Satisfaction{Expr: "r", Type: "io.Reader", Iface: "io.Closer", Method: "Close",
				Want: "() error", Note: "the static type only"},
			want: []string{"r (io.Reader) does not implement io.Closer", "the static type only"},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := PlainSatisfaction(tc.s)
			for _, w := range tc.want {
				if !strings.Contains(got, w) {
					t.Errorf("output missing %q:\n%s", w, got)
				}
			}
		})
	}
}

// TestPlainCast pins the three outcomes apart. "cannot compile" and "compiles"
// have to read as different answers, not different shades of one.
func TestPlainCast(t *testing.T) {
	tests := []struct {
		name string
		c    Cast
		want []string
	}{
		{
			name: "not an interface",
			c:    Cast{Expr: "x", Static: "int", Target: "Shape", Verdict: CastNotInterface},
			want: []string{"applies only to an interface value", "x is int"},
		},
		{
			name: "may fail at run time",
			c:    Cast{Expr: "r", Static: "io.Reader", Target: "*os.File", Verdict: CastRuntime},
			want: []string{"r.(*os.File) compiles", "dynamic type at run time"},
		},
		{
			name: "impossible",
			c: Cast{Expr: "r", Static: "io.Reader", Target: "Sq", Verdict: CastImpossible,
				Method: "Read", Want: "(p []byte) (n int, err error)"},
			want: []string{"cannot compile", "no io.Reader can hold a value of type Sq",
				"missing Read(p []byte)"},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := PlainCast(tc.c)
			for _, w := range tc.want {
				if !strings.Contains(got, w) {
					t.Errorf("output missing %q:\n%s", w, got)
				}
			}
		})
	}
}

// TestDescribeInterface checks the implementor search, including the case that
// has to stay distinguishable from a search that never ran.
func TestDescribeInterface(t *testing.T) {
	decls := []string{
		"type Shape interface{ Area() float64 }",
		"type Sq struct{ side float64 }",
		"func (s Sq) Area() float64 { return s.side * s.side }",
		"type Tri struct{ b, h float64 }",
		"func (t *Tri) Area() float64 { return t.b * t.h / 2 }",
		"type Nobody interface{ Fly() }",
	}

	r, ops := probe(t, []string{"Shape"}, decls...)
	in, err := ops[0].Iface()
	if err != nil {
		t.Fatal(err)
	}
	f := DescribeInterface(r, ops[0], in, nil)
	if len(f.Methods) != 1 || f.Methods[0].Name != "Area" {
		t.Errorf("methods = %+v, want just Area", f.Methods)
	}
	if !contains(f.Value, "Sq") {
		t.Errorf("value implementors = %v, want Sq", f.Value)
	}
	if !contains(f.Pointer, "Tri") {
		t.Errorf("pointer implementors = %v, want Tri", f.Pointer)
	}

	r, ops = probe(t, []string{"Nobody"}, decls...)
	in, err = ops[0].Iface()
	if err != nil {
		t.Fatal(err)
	}
	f = DescribeInterface(r, ops[0], in, nil)
	if len(f.Value)+len(f.Pointer) != 0 {
		t.Fatalf("nothing implements Nobody; got %v %v", f.Value, f.Pointer)
	}
	if f.Scanned == 0 {
		t.Error("Scanned = 0: nothing distinguishes 'nothing implements it' from 'nothing was searched'")
	}
	if got := PlainIface(f); !strings.Contains(got, "no type in scope implements it") ||
		!strings.Contains(got, "searched") {
		t.Errorf("empty implementor list must say so:\n%s", got)
	}
	// Nothing here is generic, so the skipped line must not appear at all:
	// every :iface output that skipped nothing reads exactly as it did.
	if len(f.SkippedGeneric) > 0 || strings.Contains(PlainIface(f), "not considered") {
		t.Errorf("nothing was skipped, so nothing may be reported as skipped: %v", f.SkippedGeneric)
	}
}

// TestSkippedGenericsAreNamed: a generic type cannot be checked without an
// instantiation, so it is not an implementor — but dropping it silently makes
// "it does not implement this" and "it was never asked" the same output, which
// is the distinction Scanned already exists to keep.
func TestSkippedGenericsAreNamed(t *testing.T) {
	decls := []string{
		"type Sized interface{ Len() int }",
		"type Sq struct{ side float64 }",
		"func (s Sq) Len() int { return 1 }",
		"type List[T any] struct{ items []T }",
		"func (l List[T]) Len() int { return len(l.items) }",
	}
	r, ops := probe(t, []string{"Sized"}, decls...)
	in, err := ops[0].Iface()
	if err != nil {
		t.Fatal(err)
	}
	f := DescribeInterface(r, ops[0], in, nil)
	if contains(f.Value, "List") || contains(f.Value, "List[T any]") ||
		contains(f.Pointer, "List") || contains(f.Pointer, "List[T any]") {
		t.Errorf("a generic type was listed as an implementor: %v %v", f.Value, f.Pointer)
	}
	if !contains(f.Value, "Sq") {
		t.Errorf("value implementors = %v, want Sq", f.Value)
	}
	if len(f.SkippedGeneric) != 1 || f.SkippedGeneric[0] != "List" {
		t.Fatalf("SkippedGeneric = %v, want [List]", f.SkippedGeneric)
	}
	got := PlainIface(f)
	for _, w := range []string{"1 generic type not considered: List", "until it is instantiated"} {
		if !strings.Contains(got, w) {
			t.Errorf("output missing %q:\n%s", w, got)
		}
	}
}

// TestPlainEmbeds covers promotion, the absence of it, and the ambiguity that
// must never be resolved to a winner.
func TestPlainEmbeds(t *testing.T) {
	decls := []string{
		"type Base struct{ N int }",
		"func (Base) Name() string { return \"base\" }",
		"type Other struct{}",
		"func (Other) Name() string { return \"other\" }",
		"type Admin struct{ Base }",
		"type Both struct{ Base; Other }",
		"type Plain struct{ X int }",
		"type Deep struct{ Admin }",
	}
	tests := []struct {
		name    string
		typ     string
		want    []string
		notWant []string
	}{
		{name: "embedded", typ: "Admin", want: []string{"Admin embeds Base", "Name() string from Base"}},
		{name: "embeds nothing", typ: "Plain", want: []string{"Plain embeds nothing"}},
		{
			name:    "ambiguous",
			typ:     "Both",
			want:    []string{"ambiguous Name", "Base and Other", "does not compile"},
			notWant: []string{"Name() string from Base"},
		},
		{name: "depth", typ: "Deep", want: []string{"from Admin (depth 2)"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			r, ops := probe(t, []string{tc.typ}, decls...)
			got := PlainEmbeds(Embeds(r.Pkg, ops[0].Type))
			for _, w := range tc.want {
				if !strings.Contains(got, w) {
					t.Errorf("output missing %q:\n%s", w, got)
				}
			}
			for _, w := range tc.notWant {
				if strings.Contains(got, w) {
					t.Errorf("output should not contain %q:\n%s", w, got)
				}
			}
		})
	}
}

// TestPlainGenerics covers a generic type, a generic function, an instantiation
// and a type with no parameters at all.
func TestPlainGenerics(t *testing.T) {
	decls := []string{
		"type Num interface{ ~int | ~float64 }",
		"type Box[T Num] struct{ v T }",
		"type Point struct{ X, Y int }",
		"func Map[T, U any](xs []T, f func(T) U) []U { return nil }",
	}
	tests := []struct {
		name string
		arg  string
		want []string
	}{
		{name: "generic type", arg: "Box", want: []string{"1 type parameter", "T Num"}},
		{name: "generic function", arg: "Map", want: []string{"Map", "2 type parameters", "T any", "U any"}},
		{name: "instantiated", arg: "Box[int]", want: []string{"instantiated with int", "T Num = int"}},
		{name: "not generic", arg: "Point", want: []string{"Point is not generic"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			r, ops := probe(t, []string{tc.arg}, decls...)
			got := PlainGenerics(TypeParams(r.Pkg, ops[0].Type, ops[0].Src))
			for _, w := range tc.want {
				if !strings.Contains(got, w) {
					t.Errorf("output missing %q:\n%s", w, got)
				}
			}
		})
	}
}

// TestInstantiationForm covers the two halves of the suggestion: an
// unconstrained parameter becomes a type the reader can substitute, and a
// constrained one keeps its own name rather than a guess that happens to
// satisfy the constraint.
func TestInstantiationForm(t *testing.T) {
	decls := []string{
		"type Num interface{ ~int | ~float64 }",
		"type List[T any] struct{ items []T }",
		"type Box[T Num] struct{ v T }",
		"type Pair[K comparable, V any] struct{ k K; v V }",
		"type Point struct{ X, Y int }",
	}
	tests := []struct {
		arg            string
		want           string
		uninstantiated bool
	}{
		{arg: "List", want: "List[int]", uninstantiated: true},
		{arg: "Box", want: "Box[T]", uninstantiated: true},
		{arg: "Pair", want: "Pair[K, int]", uninstantiated: true},
		{arg: "List[int]", uninstantiated: false},
		{arg: "Point", uninstantiated: false},
	}
	for _, tc := range tests {
		t.Run(tc.arg, func(t *testing.T) {
			r, ops := probe(t, []string{tc.arg}, decls...)
			if got := Uninstantiated(ops[0].Type); got != tc.uninstantiated {
				t.Fatalf("Uninstantiated(%s) = %v, want %v", tc.arg, got, tc.uninstantiated)
			}
			if !tc.uninstantiated {
				return
			}
			if got := InstantiationForm(r.Pkg, ops[0].Type); got != tc.want {
				t.Errorf("InstantiationForm(%s) = %q, want %q", tc.arg, got, tc.want)
			}
		})
	}
}

// TestOperandsRejectsUnresolved: a name the session cannot see comes back with
// no type, so every caller reports it rather than treating an empty method set
// as a verdict.
func TestOperandsRejectsUnresolved(t *testing.T) {
	_, ops := probe(t, []string{"Nope", "io.Writer"})
	if ops[0].Type != nil {
		t.Errorf("Nope resolved to %v, want nothing", ops[0].Type)
	}
	if ops[1].Type == nil {
		t.Error("io.Writer should still resolve beside an unresolved name")
	}
}

func contains(xs []string, s string) bool {
	for _, x := range xs {
		if x == s {
			return true
		}
	}
	return false
}
