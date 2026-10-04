package repl

import (
	"reflect"
	"strings"
	"testing"
)

// TestSplitTop: the argument separator is the top-level comma, not the first
// one. A type argument routinely carries commas of its own.
func TestSplitTop(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want []string
	}{
		{name: "plain pair", in: "Sq, Shape", want: []string{"Sq", "Shape"}},
		{name: "no spaces", in: "Sq,Shape", want: []string{"Sq", "Shape"}},
		{
			name: "type argument list",
			in:   "Map[string, int], I",
			want: []string{"Map[string, int]", "I"},
		},
		{
			name: "call arguments",
			in:   "f(a, b), io.Writer",
			want: []string{"f(a, b)", "io.Writer"},
		},
		{
			name: "composite literal",
			in:   "map[string]int{\"a\": 1, \"b\": 2}, error",
			want: []string{"map[string]int{\"a\": 1, \"b\": 2}", "error"},
		},
		{
			name: "comma inside a string",
			in:   "\"a,b\", error",
			want: []string{"\"a,b\"", "error"},
		},
		{
			name: "escaped quote inside a string",
			in:   "\"a\\\",b\", error",
			want: []string{"\"a\\\",b\"", "error"},
		},
		{name: "missing second argument", in: "Sq", want: []string{"Sq"}},
		{name: "trailing comma", in: "Sq,", want: []string{"Sq"}},
		{name: "empty", in: "", want: nil},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := splitTop(tc.in); !reflect.DeepEqual(got, tc.want) {
				t.Errorf("splitTop(%q) = %#v, want %#v", tc.in, got, tc.want)
			}
		})
	}
}

// typeCore is a session with the declarations the command tests ask about.
func typeCore(t *testing.T) *Core {
	t.Helper()
	c := testCore(t)
	for _, line := range []string{
		"type Shape interface{ Area() float64 }",
		"type Sq struct{ side float64 }",
		"func (s Sq) Area() float64 { return s.side * s.side }",
		"type Tri struct{ b, h float64 }",
		"func (t *Tri) Area() float64 { return t.b * t.h / 2 }",
		"type Bad struct{}",
		"func (Bad) Area() int { return 0 }",
		"type Nobody interface{ Fly() }",
		"type Base struct{ N int }",
		"func (Base) Name() string { return \"base\" }",
		"type Other struct{}",
		"func (Other) Name() string { return \"other\" }",
		"type Admin struct{ Base }",
		"type Both struct{ Base; Other }",
		"type List[T any] struct{ items []T }",
		"func (l List[T]) Len() int { return len(l.items) }",
		"func (l List[T]) First() T { var zero T; if len(l.items) > 0 { zero = l.items[0] }; return zero }",
		"func (l *List[T]) Add(v T) { l.items = append(l.items, v) }",
		"type Container[T any] interface{ Len() int; First() T }",
		"type Sized interface{ Len() int }",
		"type Holder struct{ List[int] }",
		"var li List[int]",
		"var buf bytes.Buffer",
		"var r io.Reader = &buf",
		"x := 3",
	} {
		if res := c.Submit(line); res.Err {
			t.Fatalf("setup %q: %s", line, res.Out)
		}
	}
	return c
}

// TestTypeCommands walks each command's branches through Submit, which is the
// only path the REPL and the MCP server share.
func TestTypeCommands(t *testing.T) {
	c := typeCore(t)
	tests := []struct {
		name    string
		cmd     string
		wantErr bool
		want    []string
		notWant []string
	}{
		// :impl
		{
			name: "impl satisfied",
			cmd:  ":impl bytes.Buffer, io.Writer",
			want: []string{"*bytes.Buffer implements io.Writer"},
		},
		{
			name: "impl session types",
			cmd:  ":impl Sq, Shape",
			want: []string{"Sq implements Shape"},
		},
		{
			name: "impl missing method",
			cmd:  ":impl Base, Shape",
			want: []string{"Base does not implement Shape", "missing Area() float64"},
		},
		{
			name: "impl pointer only",
			cmd:  ":impl Tri, Shape",
			want: []string{"*Tri implements Shape", "pointer receiver"},
		},
		{
			name: "impl wrong signature",
			cmd:  ":impl Bad, Shape",
			want: []string{"required Area() float64", "has Area() int"},
		},
		{
			name:    "impl second argument is not an interface",
			cmd:     ":impl Sq, Base",
			wantErr: true,
			want:    []string{"Base is not an interface", "struct"},
			notWant: []string{"implement"},
		},
		{
			name:    "impl unresolved",
			cmd:     ":impl Nope, Shape",
			wantErr: true,
			want:    []string{"Nope does not name anything"},
			notWant: []string{"implement"},
		},
		{
			name:    "impl on a value points at :sat",
			cmd:     ":impl buf, io.Writer",
			wantErr: true,
			want:    []string{":sat buf, io.Writer"},
		},
		{name: "impl bare", cmd: ":impl", wantErr: true, want: []string{"usage:"}},

		// :sat
		{
			name: "sat a value",
			cmd:  ":sat &buf, io.Writer",
			want: []string{"implements io.Writer"},
		},
		{
			name: "sat an interface-typed expression",
			cmd:  ":sat r, io.Closer",
			want: []string{"io.Reader", "does not implement io.Closer",
				"static type only", "without running"},
		},
		{
			name: "sat a scalar",
			cmd:  ":sat x, Shape",
			want: []string{"x (int) does not implement Shape", "missing Area() float64"},
		},
		{name: "sat bare", cmd: ":sat", wantErr: true, want: []string{"usage:"}},

		// :cast
		{
			name: "cast may fail at run time",
			cmd:  ":cast r, *bytes.Buffer",
			want: []string{"compiles", "dynamic type at run time"},
		},
		{
			name:    "cast cannot compile",
			cmd:     ":cast r, Sq",
			want:    []string{"cannot compile", "missing Read"},
			notWant: []string{"dynamic type at run time"},
		},
		{
			name: "cast on a concrete value",
			cmd:  ":cast x, Shape",
			want: []string{"applies only to an interface value", "x is int"},
		},
		{name: "cast bare", cmd: ":cast", wantErr: true, want: []string{"usage:"}},

		// :iface
		{
			name: "iface with implementors",
			cmd:  ":iface Shape",
			want: []string{"Area() float64", "implemented by Sq", "*Tri"},
		},
		{
			name:    "iface nothing implements",
			cmd:     ":iface Nobody",
			want:    []string{"Fly()", "no type in scope implements it", "searched"},
			notWant: []string{"implemented by"},
		},
		{
			name:    "iface not an interface",
			cmd:     ":iface Sq",
			wantErr: true,
			want:    []string{"Sq is not an interface", "struct"},
		},
		{name: "iface bare", cmd: ":iface", wantErr: true, want: []string{"usage:"}},

		// :embeds
		{
			name: "embeds promotes",
			cmd:  ":embeds Admin",
			want: []string{"Admin embeds Base", "Name() string from Base"},
		},
		{
			name: "embeds nothing",
			cmd:  ":embeds Sq",
			want: []string{"Sq embeds nothing"},
		},
		{
			name:    "embeds ambiguously",
			cmd:     ":embeds Both",
			want:    []string{"ambiguous Name", "Base and Other", "does not compile"},
			notWant: []string{"Name() string from Base"},
		},
		{name: "embeds bare", cmd: ":embeds", wantErr: true, want: []string{"usage:"}},

		// :gen
		{
			name: "gen a generic type",
			cmd:  ":gen List",
			want: []string{"1 type parameter", "T any"},
		},
		{
			name: "gen a plain type",
			cmd:  ":gen Sq",
			want: []string{"Sq is not generic"},
		},
		{name: "gen bare", cmd: ":gen", wantErr: true, want: []string{"usage:"}},

		// Generic instantiations. Every one of these asks the question of
		// List[int], so every signature must have been substituted: a T
		// reaching the output means the answer is about the origin, which is
		// a question nobody asked.
		{
			name:    "methods of an instantiation",
			cmd:     ":m List[int]",
			want:    []string{"List[int]", "First() int", "Len() int", "*Add(v int)", "satisfies Sized"},
			notWant: []string{"First() T", "Add(v T)"},
		},
		{
			name:    "implementation by an instantiation",
			cmd:     ":impl List[int], fmt.Stringer",
			want:    []string{"List[int] does not implement fmt.Stringer", "missing String() string"},
			notWant: []string{"List[T]"},
		},
		{
			name:    "embedding an instantiation",
			cmd:     ":embeds Holder",
			want:    []string{"Holder embeds List", "First() int from List", "Len() int from List"},
			notWant: []string{"First() T"},
		},
		{
			name:    "gen an instantiation",
			cmd:     ":gen List[int]",
			want:    []string{"List[int] is List instantiated with int", "T any", "= int"},
			notWant: []string{"is not generic"},
		},
		{
			name: "a generic type is not an implementor",
			cmd:  ":iface Shape",
			// Anchored on both ends: a generic type joining either list would
			// land inside these lines, and the skipped line that follows is
			// the other half of the promise.
			want: []string{"\n  implemented by Sq\n", "\n  by pointer *Tri\n",
				"2 generic types not considered: Container, List",
				"a generic type implements nothing until it is instantiated"},
			notWant: []string{"*List", "*Container"},
		},

		// A bare generic name. Answering from the origin is what go/types
		// will do unasked — a method set with T still in it, and a "required
		// First() T, has First() int" that reads as a verdict — so each of
		// these refuses and shows the form to type.
		{
			name:    "methods of a bare generic",
			cmd:     ":m List",
			wantErr: true,
			want:    []string{"List is generic", ":m List[int]"},
			notWant: []string{"First() T", "Len() int"},
		},
		{
			name:    "implementation by a bare generic",
			cmd:     ":impl List, fmt.Stringer",
			wantErr: true,
			want:    []string{"List is generic", ":impl List[int], fmt.Stringer"},
			notWant: []string{"does not implement", "missing"},
		},
		{
			name:    "implementation of a bare generic interface",
			cmd:     ":impl Holder, Container",
			wantErr: true,
			want:    []string{"Container is generic", ":impl Holder, Container[int]"},
			notWant: []string{"does not implement", "required First() T"},
		},
		{
			name:    "satisfaction against a bare generic interface",
			cmd:     ":sat li, Container",
			wantErr: true,
			want:    []string{"Container is generic", ":sat li, Container[int]"},
			notWant: []string{"does not implement", "required First() T"},
		},
		{
			name:    "a generic struct is still not an interface",
			cmd:     ":impl Sq, List",
			wantErr: true,
			want:    []string{"List is not an interface", "struct"},
			notWant: []string{"is generic"},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			res := c.Submit(tc.cmd)
			if res.Err != tc.wantErr {
				t.Errorf("%s: Err = %v, want %v (output %q)", tc.cmd, res.Err, tc.wantErr, res.Out)
			}
			for _, w := range tc.want {
				if !strings.Contains(res.Out, w) {
					t.Errorf("%s: output missing %q:\n%s", tc.cmd, w, res.Out)
				}
			}
			for _, w := range tc.notWant {
				if strings.Contains(res.Out, w) {
					t.Errorf("%s: output should not contain %q:\n%s", tc.cmd, w, res.Out)
				}
			}
		})
	}
}

// TestCheckerFailureIsNeverAVerdict is invariant 5 for these six commands: with
// the checker off, every one of them has to say it could not tell, and none may
// say yes or no.
func TestCheckerFailureIsNeverAVerdict(t *testing.T) {
	t.Setenv("GLUON_NO_TYPECHECK", "1")
	c := testCore(t)
	for _, cmd := range []string{
		":impl Sq, Shape",
		":sat x, Shape",
		":cast r, Sq",
		":iface Shape",
		":embeds Admin",
		":gen List",
	} {
		t.Run(cmd, func(t *testing.T) {
			res := c.Submit(cmd)
			if !res.Err {
				t.Errorf("%s: expected an error result, got %q", cmd, res.Out)
			}
			if !strings.Contains(res.Out, "unavailable") {
				t.Errorf("%s: output does not say the answer is unavailable:\n%s", cmd, res.Out)
			}
			for _, verdict := range []string{"implements", "does not implement",
				"compiles", "cannot compile", "embeds", "is not generic"} {
				if strings.Contains(res.Out, verdict) {
					t.Errorf("%s: output states %q, which is a verdict the checker could not reach:\n%s",
						cmd, verdict, res.Out)
				}
			}
		})
	}
}

// TestUnavailableReadsDifferentlyFromNo pins the distinction the spec turns on:
// "I could not tell" and "no" are different facts and must not share wording.
func TestUnavailableReadsDifferentlyFromNo(t *testing.T) {
	c := typeCore(t)
	no := c.Submit(":impl Base, Shape")
	if no.Err {
		t.Fatalf("a negative verdict is an answer, not an error: %s", no.Out)
	}
	if strings.Contains(no.Out, "unavailable") {
		t.Errorf("a negative verdict must not read as unavailable:\n%s", no.Out)
	}
}
