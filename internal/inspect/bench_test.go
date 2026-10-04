package inspect

import (
	"strings"
	"testing"

	"github.com/sandboxws/gluon/internal/pretty"
)

func benchVal(rows ...[5]int64) pretty.Value {
	out := pretty.Value{Type: "[][5]int64", Kind: "list"}
	for _, r := range rows {
		row := pretty.Value{Type: "[5]int64", Kind: "list"}
		for _, n := range r {
			row.Items = append(row.Items, pretty.Value{Type: "int64", Kind: "scalar", Repr: itoa64(n)})
		}
		out.Items = append(out.Items, row)
	}
	return out
}

// TestBenchRunsReadsTheChildsTable pins the five-column contract the generated
// source in internal/repl writes and this reads.
func TestBenchRunsReadsTheChildsTable(t *testing.T) {
	runs, ok := BenchRuns(benchVal([5]int64{0, 79, 208, 1, 14999101}, [5]int64{4, 80, 208, 1, 14000000}))
	if !ok {
		t.Fatal("BenchRuns refused a well-formed table")
	}
	if len(runs) != 2 {
		t.Fatalf("got %d runs, want 2", len(runs))
	}
	want := BenchRun{CPU: 0, NsOp: 79, BytesOp: 208, AllocsOp: 1, N: 14999101}
	if runs[0] != want {
		t.Errorf("first run = %+v, want %+v", runs[0], want)
	}
	if runs[1].CPU != 4 {
		t.Errorf("second run's parallelism = %d, want 4", runs[1].CPU)
	}

	for _, bad := range []pretty.Value{
		{Kind: "scalar", Repr: "7"},
		{Kind: "list"},
		{Kind: "list", Items: []pretty.Value{{Kind: "list", Items: []pretty.Value{{Repr: "1"}}}}},
		benchValNonNumeric(),
	} {
		if _, ok := BenchRuns(bad); ok {
			t.Errorf("BenchRuns accepted %+v", bad)
		}
	}
}

func benchValNonNumeric() pretty.Value {
	v := benchVal([5]int64{0, 1, 2, 3, 4})
	v.Items[0].Items[1].Repr = "1.19s"
	return v
}

// TestRenderBenchSingleRunIsUnchanged pins the two lines :bench printed before
// -count and -cpu existed, byte for byte. They are what the Measuring guide's
// first :bench shows, and a flag that shifted them would be a flag that changed the
// default.
func TestRenderBenchSingleRunIsUnchanged(t *testing.T) {
	runs := []BenchRun{{CPU: 0, NsOp: 79, BytesOp: 208, AllocsOp: 1, N: 14999101}}
	const want = "14,999,101 iterations\n79 ns/op     208 B/op     1 allocs/op"
	if got := RenderBench(runs, pretty.PlainStyles(), false); got != want {
		t.Errorf("plain form:\n%q\nwant:\n%q", got, want)
	}
	// The rich form is the same text through two styles and nothing else, so
	// stripping the styling has to give the plain form back.
	st := pretty.PlainStyles()
	rich := RenderBench(runs, st, true)
	if got := stripANSI(rich); got != want {
		t.Errorf("rich form once unstyled:\n%q\nwant:\n%q", got, want)
	}
}

// TestRenderBenchReportsSpread: past one run every quantity comes with the
// three numbers that were actually seen.
func TestRenderBenchReportsSpread(t *testing.T) {
	runs := []BenchRun{
		{NsOp: 84, BytesOp: 208, AllocsOp: 1, N: 14000000},
		{NsOp: 79, BytesOp: 208, AllocsOp: 1, N: 15000000},
		{NsOp: 81, BytesOp: 216, AllocsOp: 1, N: 14500000},
	}
	got := RenderBench(runs, pretty.PlainStyles(), false)
	for _, want := range []string{
		"3 runs", "min", "median", "max",
		"ns/op", "B/op", "allocs/op", "iterations",
		"79", "81", "84", // the spread of ns/op, in that order
		"14,000,000", "15,000,000", // iterations keep their grouping
	} {
		if !strings.Contains(got, want) {
			t.Errorf("spread report lacks %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "GOMAXPROCS") {
		t.Errorf("runs at the default parallelism should carry no label:\n%s", got)
	}
	// The median is a value that was measured, not an average of two that
	// were not: 81 sits between 79 and 84 on its own line.
	if !strings.Contains(got, "ns/op") || !strings.Contains(got, "79") {
		t.Errorf("ns/op row missing:\n%s", got)
	}
}

// TestRenderBenchLabelsEachParallelism: one block per value, in the order the
// user listed them, and no ratio or winner derived from them.
func TestRenderBenchLabelsEachParallelism(t *testing.T) {
	runs := []BenchRun{
		{CPU: 1, NsOp: 79, BytesOp: 208, AllocsOp: 1, N: 15000000},
		{CPU: 4, NsOp: 92, BytesOp: 208, AllocsOp: 1, N: 13000000},
	}
	got := RenderBench(runs, pretty.PlainStyles(), false)
	i, j := strings.Index(got, "GOMAXPROCS=1"), strings.Index(got, "GOMAXPROCS=4")
	if i < 0 || j < 0 {
		t.Fatalf("both parallelism values should be labelled:\n%s", got)
	}
	if i > j {
		t.Errorf("the labels are out of the order they were asked for:\n%s", got)
	}
	// Each block is one run, so each keeps the single-run two-line form.
	if !strings.Contains(got, "15,000,000 iterations") || !strings.Contains(got, "92 ns/op") {
		t.Errorf("a labelled single run lost its ordinary form:\n%s", got)
	}
	if strings.Contains(got, "faster") || strings.Contains(got, "x ") {
		t.Errorf("the report should not derive a comparison:\n%s", got)
	}
}

// TestRenderBenchSpreadPerParallelism is both flags together: a spread table
// under each label.
func TestRenderBenchSpreadPerParallelism(t *testing.T) {
	runs := []BenchRun{
		{CPU: 1, NsOp: 79, N: 15000000}, {CPU: 1, NsOp: 83, N: 15000001},
		{CPU: 8, NsOp: 92, N: 13000000}, {CPU: 8, NsOp: 90, N: 13000002},
	}
	got := RenderBench(runs, pretty.PlainStyles(), false)
	if n := strings.Count(got, "2 runs"); n != 2 {
		t.Errorf("got %d spread tables, want 2:\n%s", n, got)
	}
	if !strings.Contains(got, "GOMAXPROCS=1") || !strings.Contains(got, "GOMAXPROCS=8") {
		t.Errorf("both blocks should be labelled:\n%s", got)
	}
}

// stripANSI removes the escape sequences lipgloss writes, so a styled string
// can be compared with the text it styles.
func stripANSI(s string) string {
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

// TestRenderBenchPair is the golden form of the two-expression report: the two
// expressions named, then one row per quantity with the ratio between them.
func TestRenderBenchPair(t *testing.T) {
	a := []BenchRun{{NsOp: 4, BytesOp: 0, AllocsOp: 0, N: 100}}
	b := []BenchRun{{NsOp: 79, BytesOp: 208, AllocsOp: 1, N: 20}}

	want := "a  1 + 1\n" +
		"b  fmt.Sprint(1)\n" +
		"\n" +
		"           a    b     b/a\n" +
		"ns/op      4   79  19.75x\n" +
		"B/op       0  208     n/a\n" +
		"allocs/op  0    1     n/a"
	got := RenderBenchPair("1 + 1", "fmt.Sprint(1)", a, b, pretty.PlainStyles(), false)
	if got != want {
		t.Errorf("plain form:\n%q\nwant:\n%q", got, want)
	}
	// Rich is the same text through styles and nothing else.
	if s := stripANSI(RenderBenchPair("1 + 1", "fmt.Sprint(1)", a, b, pretty.PlainStyles(), true)); s != want {
		t.Errorf("rich form once unstyled:\n%q\nwant:\n%q", s, want)
	}
}

// Past one run each, the ratio is computed from medians and the table says so
// — an average would let a single slow run move a number nothing measured.
func TestRenderBenchPairRatioIsFromMedians(t *testing.T) {
	a := []BenchRun{{NsOp: 10}, {NsOp: 20}, {NsOp: 300}}
	b := []BenchRun{{NsOp: 40}, {NsOp: 60}, {NsOp: 900}}

	got := RenderBenchPair("a", "b", a, b, pretty.PlainStyles(), false)
	if !strings.Contains(got, "medians") {
		t.Errorf("the table does not say what the ratio was computed from:\n%s", got)
	}
	// Medians are 20 and 60, so the ratio is 3.00 — an average would be 3.25.
	if !strings.Contains(got, "3.00x") {
		t.Errorf("ratio is not the medians':\n%s", got)
	}
}
