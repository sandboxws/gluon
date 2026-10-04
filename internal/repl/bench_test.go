package repl

import (
	"go/ast"
	"go/parser"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
)

// TestParseBenchArgsReadsLeadingFlags covers each flag alone, all three
// together, and none — the four shapes the argument can take. The expression
// is never rewritten, which is why every case asserts on it verbatim.
func TestParseBenchArgsReadsLeadingFlags(t *testing.T) {
	for _, tc := range []struct {
		arg   string
		want  benchOpts
		expr  string
		label string
	}{
		{arg: "fmt.Sprint(1)", want: benchOpts{count: 1}, expr: "fmt.Sprint(1)", label: "no flags"},
		{arg: "-count 5 fmt.Sprint(1)", want: benchOpts{count: 5}, expr: "fmt.Sprint(1)", label: "count alone"},
		{arg: "-cpu 1,2,4 x", want: benchOpts{count: 1, cpus: []int{1, 2, 4}}, expr: "x", label: "cpu alone"},
		{arg: "-profile x", want: benchOpts{count: 1, profile: true}, expr: "x", label: "profile alone"},
		{
			arg:   "-count 3 -cpu 1,8 -profile fmt.Sprint(1)",
			want:  benchOpts{count: 3, cpus: []int{1, 8}, profile: true},
			expr:  "fmt.Sprint(1)",
			label: "all three",
		},
		{
			// A leading minus in the expression is not a flag, and everything
			// from the first non-flag word on is the expression verbatim.
			arg:   "-count 2 -x + y",
			want:  benchOpts{count: 2},
			expr:  "-x + y",
			label: "a minus that is not a flag",
		},
	} {
		opts, expr, err := parseBenchArgs(tc.arg)
		if err != nil {
			t.Errorf("%s: parseBenchArgs(%q): %v", tc.label, tc.arg, err)
			continue
		}
		if !reflect.DeepEqual(opts, tc.want) {
			t.Errorf("%s: opts = %+v, want %+v", tc.label, opts, tc.want)
		}
		if expr != tc.expr {
			t.Errorf("%s: expression = %q, want %q", tc.label, expr, tc.expr)
		}
	}
}

// TestBenchRejectsInvalidFlags: the error names the flag, and nothing is
// evaluated. The Core here has no evaluator at all, so a bench that reached
// one would panic rather than quietly build — which is the assertion.
func TestBenchRejectsInvalidFlags(t *testing.T) {
	for _, tc := range []struct{ arg, flag string }{
		{"-count 0 x", "-count"},
		{"-count -3 x", "-count"},
		{"-count two x", "-count"},
		{"-count x", "-count"}, // the count swallowed the expression
		{"-count", "-count"},
		{"-cpu 0 x", "-cpu"},
		{"-cpu 1,zero x", "-cpu"},
		{"-cpu -2 x", "-cpu"},
		{"-cpu", "-cpu"},
	} {
		res := (&Core{}).bench(tc.arg)
		if !res.Err {
			t.Errorf(":bench %s: got a result, want an error:\n%s", tc.arg, res.Out)
			continue
		}
		if !strings.Contains(res.Out, tc.flag) {
			t.Errorf(":bench %s: %q does not name %s", tc.arg, res.Out, tc.flag)
		}
	}
}

// TestBenchWithNoExpressionReportsUsage: the flags are not an expression.
func TestBenchWithNoExpressionReportsUsage(t *testing.T) {
	for _, arg := range []string{"", "-count 3", "-profile"} {
		res := (&Core{}).bench(arg)
		if !res.Err || !strings.HasPrefix(res.Out, "usage:") {
			t.Errorf(":bench %q: got %q (err=%v), want a usage line", arg, res.Out, res.Err)
		}
	}
}

// TestBenchSourceIsParseableStandardLibrary: the generated expression parses,
// and the only packages it names are the four standard-library ones :bench
// depends on. Invariant B — the child program is strictly standard library —
// is what this pins.
func TestBenchSourceIsParseableStandardLibrary(t *testing.T) {
	for _, tc := range []struct {
		label string
		opts  benchOpts
		path  string
		want  []string
	}{
		{"plain", benchOpts{count: 1}, "", []string{"runtime", "testing"}},
		{"count", benchOpts{count: 7}, "", []string{"runtime", "testing"}},
		{"cpu", benchOpts{count: 1, cpus: []int{1, 4}}, "", []string{"runtime", "testing"}},
		{"profile", benchOpts{count: 1, profile: true}, "/tmp/x.pprof",
			[]string{"os", "pprof", "runtime", "testing"}},
	} {
		src := benchSource([]string{"1 + 1"}, tc.opts, tc.path)
		got := freeQualifiers(t, src)
		if !reflect.DeepEqual(got, tc.want) {
			t.Errorf("%s: packages %v, want %v\n%s", tc.label, got, tc.want, src)
		}
	}
}

// TestBenchSourceRepeatsWithoutAllocatingInTheMeasuredRegion: the results
// slice is sized before the loop and the closure is untouched by the count.
// The trap this guards is the one recorded on benchSource — a harness that
// allocates reports its own allocation as the expression's.
func TestBenchSourceRepeatsWithoutAllocatingInTheMeasuredRegion(t *testing.T) {
	src := benchSource([]string{"1 + 1"}, benchOpts{count: 4}, "")
	if !strings.Contains(src, "out := make([][5]int64, 0, 4)") {
		t.Errorf("the results slice is not sized up front:\n%s", src)
	}
	if !strings.Contains(src, "for k := 0; k < 4; k++ {") {
		t.Errorf("the count does not become a loop:\n%s", src)
	}
	// The measured closure is character for character what a single run runs.
	one := benchSource([]string{"1 + 1"}, benchOpts{count: 1}, "")
	if closureOf(t, src) != closureOf(t, one) {
		t.Errorf("the measured closure changed with the count:\n%s\n---\n%s", one, src)
	}
}

// TestBenchSourceVariesParallelism: one run per value, each labelled with the
// value it used, and GOMAXPROCS put back before the expression returns.
func TestBenchSourceVariesParallelism(t *testing.T) {
	src := benchSource([]string{"1 + 1"}, benchOpts{count: 2, cpus: []int{1, 2, 8}}, "")
	for _, want := range []string{
		"cpus := []int{1, 2, 8}",
		"out := make([][5]int64, 0, len(cpus)*2)",
		"prev := runtime.GOMAXPROCS(0)",
		"runtime.GOMAXPROCS(p)",
		"[5]int64{int64(p), r.NsPerOp()",
		"runtime.GOMAXPROCS(prev)",
	} {
		if !strings.Contains(src, want) {
			t.Errorf("generated source lacks %q:\n%s", want, src)
		}
	}
	if closureOf(t, src) != closureOf(t, benchSource([]string{"1 + 1"}, benchOpts{count: 1}, "")) {
		t.Errorf("the measured closure changed with the parallelism list:\n%s", src)
	}
}

// TestBenchWritesNoProfileWithoutTheFlag: without -profile the generated
// program cannot create a file — it names no file-creating call at all — and
// the evaluator's own directory holds no profile. Whether a real run leaves
// one behind is the integration tier's question, since it takes a build;
// this is the half that can be answered without one.
func TestBenchWritesNoProfileWithoutTheFlag(t *testing.T) {
	for _, opts := range []benchOpts{{count: 1}, {count: 5}, {count: 2, cpus: []int{1, 4}}} {
		src := benchSource([]string{"1 + 1"}, opts, "")
		for _, forbidden := range []string{"os.Create", "pprof", ".pprof"} {
			if strings.Contains(src, forbidden) {
				t.Errorf("%+v: generated source names %q:\n%s", opts, forbidden, src)
			}
		}
	}

	c := testCore(t)
	if res := c.bench("-count 0 1 + 1"); !res.Err {
		t.Fatalf("expected the invalid count to be refused, got %q", res.Out)
	}
	entries, err := os.ReadDir(c.ev.Dir())
	if err != nil {
		t.Fatalf("reading %s: %v", c.ev.Dir(), err)
	}
	for _, e := range entries {
		if filepath.Ext(e.Name()) == ".pprof" {
			t.Errorf("%s left a profile behind", e.Name())
		}
	}
}

// freeQualifiers returns the sorted package names the expression selects
// through, excluding every identifier it declares itself.
func freeQualifiers(t *testing.T, src string) []string {
	t.Helper()
	expr, err := parser.ParseExpr(src)
	if err != nil {
		t.Fatalf("generated source does not parse: %v\n%s", err, src)
	}

	declared := map[string]bool{}
	ast.Inspect(expr, func(n ast.Node) bool {
		switch n := n.(type) {
		case *ast.AssignStmt:
			for _, lhs := range n.Lhs {
				if id, ok := lhs.(*ast.Ident); ok {
					declared[id.Name] = true
				}
			}
		case *ast.RangeStmt:
			for _, v := range []ast.Expr{n.Key, n.Value} {
				if id, ok := v.(*ast.Ident); ok {
					declared[id.Name] = true
				}
			}
		case *ast.FuncType:
			if n.Params == nil {
				return true
			}
			for _, f := range n.Params.List {
				for _, id := range f.Names {
					declared[id.Name] = true
				}
			}
		}
		return true
	})

	seen := map[string]bool{}
	ast.Inspect(expr, func(n ast.Node) bool {
		sel, ok := n.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		if id, ok := sel.X.(*ast.Ident); ok && !declared[id.Name] {
			seen[id.Name] = true
		}
		return true
	})
	out := make([]string, 0, len(seen))
	for name := range seen {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

// closureOf returns the text of the func literal handed to testing.Benchmark —
// the measured region, and the only part of the generated source that must not
// move when a flag is added.
func closureOf(t *testing.T, src string) string {
	t.Helper()
	const open = "testing.Benchmark(func(b *testing.B) {"
	i := strings.Index(src, open)
	if i < 0 {
		t.Fatalf("no measured closure in:\n%s", src)
	}
	rest := src[i+len(open):]
	j := strings.Index(rest, "})")
	if j < 0 {
		t.Fatalf("unterminated closure in:\n%s", src)
	}
	// Indentation moves with the loop nesting; the statements are what must
	// not change.
	var out []string
	for _, line := range strings.Split(rest[:j], "\n") {
		if s := strings.TrimSpace(line); s != "" {
			out = append(out, s)
		}
	}
	return strings.Join(out, "\n")
}

// --- two expressions ------------------------------------------------------

// The pair is split by the same top-level splitter :diff and :impl use, so a
// comma inside brackets, parentheses, braces or a literal is not the separator.
func TestBenchSplitsTheTopLevelCommaOnly(t *testing.T) {
	for _, tc := range []struct {
		arg  string
		want []string
	}{
		{"fmt.Sprint(1)", []string{"fmt.Sprint(1)"}},
		{"a, b", []string{"a", "b"}},
		{"fmt.Sprint(1, 2)", []string{"fmt.Sprint(1, 2)"}},
		{"[]int{1, 2}[0]", []string{"[]int{1, 2}[0]"}},
		{`strings.Join(xs, ", ")`, []string{`strings.Join(xs, ", ")`}},
		{"m[[2]int{1, 2}]", []string{"m[[2]int{1, 2}]"}},
		{"fmt.Sprint(1, 2), fmt.Sprintf(\"%d%d\", 1, 2)",
			[]string{"fmt.Sprint(1, 2)", `fmt.Sprintf("%d%d", 1, 2)`}},
	} {
		if got := splitTop(tc.arg); !reflect.DeepEqual(got, tc.want) {
			t.Errorf("splitTop(%q) = %q, want %q", tc.arg, got, tc.want)
		}
	}

	// Three is neither form, and the refusal says which two it wanted.
	res := (&Core{}).bench("a, b, c")
	if !res.Err || !strings.Contains(res.Out, "one expression or two") {
		t.Errorf(":bench a, b, c = %q (err=%v)", res.Out, res.Err)
	}
}

// Two expressions, one generated program, two sinks — and neither of them an
// interface. Assigning a non-pointer to an interface allocates, which would
// corrupt the allocs/op being measured; a single shared sink would have to be
// one to hold both values, which is the trap ROADMAP.md records.
func TestBenchSourceMeasuresBothWithConcreteSinks(t *testing.T) {
	src := benchSource([]string{"1 + 1", "fmt.Sprint(1)"}, benchOpts{count: 1}, "")
	if got := strings.Count(src, "testing.Benchmark("); got != 2 {
		t.Errorf("the program calls testing.Benchmark %d times, want 2:\n%s", got, src)
	}
	if got := strings.Count(src, "s := "); got != 2 {
		t.Errorf("the program declares %d sinks, want one per closure:\n%s", got, src)
	}
	for _, forbidden := range []string{"var s any", "interface{}", "any =", "[]any"} {
		if strings.Contains(src, forbidden) {
			t.Errorf("the generated source has an interface-typed sink (%q):\n%s", forbidden, src)
		}
	}
	if !strings.Contains(src, "out := make([][5]int64, 0, 2)") {
		t.Errorf("the results slice is not sized for both expressions:\n%s", src)
	}
	if _, err := parser.ParseExpr(src); err != nil {
		t.Fatalf("generated source does not parse: %v\n%s", err, src)
	}

	// With flags, still one program: one cpus list, one saved GOMAXPROCS, one
	// restore — two of any of them would not compile.
	withFlags := benchSource([]string{"a", "b"}, benchOpts{count: 3, cpus: []int{1, 4}}, "")
	for _, once := range []string{"cpus := []int{1, 4}", "prev := runtime.GOMAXPROCS(0)", "runtime.GOMAXPROCS(prev)"} {
		if got := strings.Count(withFlags, once); got != 1 {
			t.Errorf("%q appears %d times, want 1:\n%s", once, got, withFlags)
		}
	}
	if !strings.Contains(withFlags, "out := make([][5]int64, 0, len(cpus)*6)") {
		t.Errorf("the results slice is not sized for both expressions at every parallelism:\n%s", withFlags)
	}
	if _, err := parser.ParseExpr(withFlags); err != nil {
		t.Fatalf("generated source does not parse: %v\n%s", err, withFlags)
	}
}

// Constraint I: the single-expression form's generated program is what it was.
// The pair is an addition, and an addition that moved the existing bytes would
// be a change to a surface that is frozen.
func TestBenchSourceForOneExpressionIsUnchanged(t *testing.T) {
	want := "func() [][5]int64 {\n" +
		"\tout := make([][5]int64, 0, 1)\n" +
		"\tfor k := 0; k < 1; k++ {\n" +
		"\t\tr := testing.Benchmark(func(b *testing.B) {\n" +
		"\t\t\ts := 1 + 1\n" +
		"\t\t\tb.ReportAllocs()\n" +
		"\t\t\tb.ResetTimer()\n" +
		"\t\t\tfor i := 0; i < b.N; i++ {\n" +
		"\t\t\t\ts = 1 + 1\n" +
		"\t\t\t}\n" +
		"\t\t\truntime.KeepAlive(s)\n" +
		"\t\t})\n" +
		"\t\tout = append(out, [5]int64{0, r.NsPerOp(), r.AllocedBytesPerOp(), r.AllocsPerOp(), int64(r.N)})\n" +
		"\t}\n" +
		"\treturn out\n}()"
	if got := benchSource([]string{"1 + 1"}, benchOpts{count: 1}, ""); got != want {
		t.Errorf("the one-expression program changed:\n--- got\n%s\n--- want\n%s", got, want)
	}
}
