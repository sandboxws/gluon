package inspect

import (
	"go/types"
	"runtime"
	"sort"
	"strconv"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/lipgloss/table"

	"github.com/sandboxws/gluon/internal/pretty"
)

// sizes is the machine gluon builds for. go/types defaults to amd64 when this
// is left nil, which would report the wrong offsets on any other architecture.
var sizes = types.SizesFor("gc", runtime.GOARCH)

// LayoutField is one field's place in a struct.
type LayoutField struct {
	Name    string
	Type    string
	Offset  int64
	Size    int64
	Padding int64 // bytes of hole between this field and the next
}

// Layout is where a struct's fields sit in memory, and what that costs.
type Layout struct {
	Type   string
	Size   int64
	Align  int64
	Fields []LayoutField
	// Packed is the size the same fields would occupy sorted by descending
	// alignment, Saved the difference, and Order the arrangement that achieves
	// it. All three are zero when reordering buys nothing.
	Packed int64
	Saved  int64
	Order  string
}

// Layout describes where a struct's fields sit. It is a pure type-checker
// answer: go/types knows the sizing rules, so nothing is built and nothing
// runs.
func (t *Target) Layout() (*Layout, bool) {
	if t.Type == nil || sizes == nil {
		return nil, false
	}
	st, ok := t.Type.Underlying().(*types.Struct)
	if !ok {
		return nil, false
	}

	vars := make([]*types.Var, st.NumFields())
	for i := range vars {
		vars[i] = st.Field(i)
	}
	offs := sizes.Offsetsof(vars)
	total := sizes.Sizeof(t.Type)

	out := &Layout{
		Type:  types.TypeString(t.Type, qualifier(t.Pkg)),
		Size:  total,
		Align: sizes.Alignof(t.Type),
	}
	for i, v := range vars {
		f := LayoutField{
			Name:   v.Name(),
			Type:   types.TypeString(v.Type(), qualifier(t.Pkg)),
			Offset: offs[i],
			Size:   sizes.Sizeof(v.Type()),
		}
		// The hole before the next field starts, or before the struct ends.
		next := total
		if i+1 < len(vars) {
			next = offs[i+1]
		}
		f.Padding = next - f.Offset - f.Size
		out.Fields = append(out.Fields, f)
	}

	if packed, order, ok := packedSize(vars); ok && packed < total {
		out.Packed, out.Saved, out.Order = packed, total-packed, order
	}
	return out, true
}

// packedSize is what the same fields would cost sorted by descending
// alignment, then descending size — the ordering that leaves the fewest holes.
// It returns the order too, so what is reported is the order that was measured.
func packedSize(vars []*types.Var) (int64, string, bool) {
	sorted := append([]*types.Var(nil), vars...)
	sort.SliceStable(sorted, func(i, j int) bool {
		ai, aj := sizes.Alignof(sorted[i].Type()), sizes.Alignof(sorted[j].Type())
		if ai != aj {
			return ai > aj
		}
		return sizes.Sizeof(sorted[i].Type()) > sizes.Sizeof(sorted[j].Type())
	})
	names := make([]string, 0, len(sorted))
	for _, v := range sorted {
		names = append(names, v.Name())
	}
	return sizes.Sizeof(types.NewStruct(sorted, nil)), strings.Join(names, ", "), true
}

// PlainLayout is the pipe form.
func PlainLayout(l *Layout) string {
	var b strings.Builder
	b.WriteString(l.Type + "  " + itoa64(l.Size) + " bytes, align " + itoa64(l.Align) + "\n")
	for _, f := range l.Fields {
		b.WriteString("  " + itoa64(f.Offset) + "  " + f.Name + "  " + f.Type +
			"  " + itoa64(f.Size) + "\n")
		if f.Padding > 0 {
			b.WriteString("      (" + itoa64(f.Padding) + " bytes padding)\n")
		}
	}
	if l.Saved > 0 {
		b.WriteString("  reordering as " + l.Order + " would be " +
			itoa64(l.Packed) + " bytes — " + itoa64(l.Saved) + " saved")
	}
	return strings.TrimRight(b.String(), "\n")
}

// RenderLayout is the terminal form.
func RenderLayout(l *Layout, st pretty.Styles) string {
	t := table.New().
		Border(lipgloss.RoundedBorder()).
		BorderStyle(st.Border).
		Headers("offset", "field", "type", "size").
		StyleFunc(func(row, col int) lipgloss.Style {
			s := lipgloss.NewStyle().Padding(0, 1)
			if row == table.HeaderRow {
				return s.Faint(true)
			}
			if col == 0 || col == 3 {
				return s.Align(lipgloss.Right)
			}
			return s
		})
	for _, f := range l.Fields {
		t.Row(itoa64(f.Offset), f.Name, f.Type, itoa64(f.Size))
		if f.Padding > 0 {
			t.Row("", st.Note.Render("← "+itoa64(f.Padding)+" bytes padding"), "", "")
		}
	}
	head := st.Type.Render(l.Type) + " " +
		st.Annot.Render(itoa64(l.Size)+" bytes, align "+itoa64(l.Align))
	out := head + "\n" + t.Render()
	if l.Saved > 0 {
		out += "\n" + st.Note.Render("  reordering as "+l.Order+" would be "+
			itoa64(l.Packed)+" bytes — "+itoa64(l.Saved)+" saved")
	}
	return out
}

func itoa64(n int64) string {
	neg := n < 0
	if neg {
		n = -n
	}
	s := itoa(int(n))
	if neg {
		return "-" + s
	}
	return s
}

// A BenchRun is one measured run: the parallelism it ran at, and the four
// numbers testing.BenchmarkResult has already divided.
//
// CPU is 0 when the run used whatever GOMAXPROCS the child started with, which
// is every run of a :bench that named no -cpu list. Keeping that distinct from
// a real value is what leaves the default report free of a label naming a
// number nobody chose.
type BenchRun struct {
	CPU      int
	NsOp     int64
	BytesOp  int64
	AllocsOp int64
	N        int64
}

// BenchRuns reads the [][5]int64 the child returns.
//
// The five columns are GOMAXPROCS, ns/op, B/op, allocs/op and the iteration
// count, in that order. The generated source in internal/repl is the other
// half of this contract; an array rather than a struct because the encoder
// renders an array as a plain list of scalars, and a struct type declared
// inside the generated expression would show up in :src for no gain.
func BenchRuns(v pretty.Value) ([]BenchRun, bool) {
	if v.Kind != "list" || len(v.Items) == 0 {
		return nil, false
	}
	out := make([]BenchRun, 0, len(v.Items))
	for _, row := range v.Items {
		if row.Kind != "list" || len(row.Items) != 5 {
			return nil, false
		}
		var n [5]int64
		for i, cell := range row.Items {
			p, err := strconv.ParseInt(cell.Repr, 10, 64)
			if err != nil {
				return nil, false
			}
			n[i] = p
		}
		out = append(out, BenchRun{
			CPU: int(n[0]), NsOp: n[1], BytesOp: n[2], AllocsOp: n[3], N: n[4],
		})
	}
	return out, true
}

// RenderBench formats what :bench measured. The child returns the numbers and
// the shaping happens here, the same way values are described there and
// formatted here.
//
// One run at the default parallelism is the overwhelmingly common case and its
// two lines are unchanged — that is the whole of what :bench printed before
// -count and -cpu existed. Anything else is a table, because the question a
// repeated benchmark is asked is "how consistent", which a column of numbers
// answers and a paragraph of them does not.
func RenderBench(runs []BenchRun, st pretty.Styles, rich bool) string {
	var b strings.Builder
	for _, g := range groupByCPU(runs) {
		if b.Len() > 0 {
			b.WriteString("\n\n")
		}
		if g.cpu > 0 {
			// Only a run that was told what parallelism to use gets a label.
			head := "GOMAXPROCS=" + itoa(g.cpu)
			if rich {
				head = st.Type.Render(head)
			}
			b.WriteString(head + "\n")
		}
		b.WriteString(benchGroup(g.runs, st, rich))
	}
	return b.String()
}

// RenderBenchPair formats two expressions measured in one evaluation.
//
// The comparison people reach for is a brute force against an optimised
// version, and doing it with two :bench invocations means holding two numbers
// in your head. Two rows and the ratio between them is the whole feature.
//
// The ratio comes from medians, and the table says so. A single run has one
// value and that value is its own median; past one, an average would let a
// single slow run move a ratio in a way no measured number moved.
//
// A quantity whose left-hand value is zero has no ratio, and says so rather
// than printing an infinity: 16 B/op against 0 B/op is a statement that one of
// them allocates, not a number. It is spelled in ASCII because benchTable pads
// with len() and a multi-byte dash would be padded two columns short.
func RenderBenchPair(a, b string, ar, br []BenchRun, st pretty.Styles, rich bool) string {
	label := ""
	if len(ar) > 1 || len(br) > 1 {
		label = "medians"
	}
	rows := [][]string{{label, "a", "b", "b/a"}}
	for _, q := range []struct {
		name string
		of   func(BenchRun) int64
	}{
		{"ns/op", func(r BenchRun) int64 { return r.NsOp }},
		{"B/op", func(r BenchRun) int64 { return r.BytesOp }},
		{"allocs/op", func(r BenchRun) int64 { return r.AllocsOp }},
	} {
		x, y := benchMedian(ar, q.of), benchMedian(br, q.of)
		rows = append(rows, []string{q.name, itoa64(x), itoa64(y), benchRatio(x, y)})
	}

	head := "a  " + a + "\nb  " + b
	if rich {
		head = st.Annot.Render("a  ") + a + "\n" + st.Annot.Render("b  ") + b
	}
	return head + "\n\n" + benchTable(rows, st, rich)
}

// benchMedian is the upper middle of the runs, for benchSpreadRow's reason: it
// is a value that was actually measured rather than an average of two that
// were not.
func benchMedian(runs []BenchRun, of func(BenchRun) int64) int64 {
	if len(runs) == 0 {
		return 0
	}
	ns := make([]int64, 0, len(runs))
	for _, r := range runs {
		ns = append(ns, of(r))
	}
	sort.Slice(ns, func(i, j int) bool { return ns[i] < ns[j] })
	return ns[len(ns)/2]
}

// benchRatio is b over a, to two decimals, and "—" where there is none.
func benchRatio(a, b int64) string {
	if a == 0 {
		return "n/a"
	}
	return strconv.FormatFloat(float64(b)/float64(a), 'f', 2, 64) + "x"
}

// benchCPUGroup is the runs that shared one parallelism value, in the order
// the user listed them.
type benchCPUGroup struct {
	cpu  int
	runs []BenchRun
}

func groupByCPU(runs []BenchRun) []benchCPUGroup {
	var out []benchCPUGroup
	for _, r := range runs {
		if n := len(out); n > 0 && out[n-1].cpu == r.CPU {
			out[n-1].runs = append(out[n-1].runs, r)
			continue
		}
		out = append(out, benchCPUGroup{cpu: r.CPU, runs: []BenchRun{r}})
	}
	return out
}

// benchGroup renders the runs at one parallelism value.
func benchGroup(runs []BenchRun, st pretty.Styles, rich bool) string {
	if len(runs) == 1 {
		r := runs[0]
		head := commas(itoa64(r.N)) + " iterations"
		body := itoa64(r.NsOp) + " ns/op     " + itoa64(r.BytesOp) + " B/op     " +
			itoa64(r.AllocsOp) + " allocs/op"
		if !rich {
			return head + "\n" + body
		}
		return st.Annot.Render(head) + "\n" + st.Num.Render(body)
	}

	rows := [][]string{{itoa(len(runs)) + " runs", "min", "median", "max"}}
	rows = append(rows,
		benchSpreadRow("ns/op", runs, func(r BenchRun) int64 { return r.NsOp }, false),
		benchSpreadRow("B/op", runs, func(r BenchRun) int64 { return r.BytesOp }, false),
		benchSpreadRow("allocs/op", runs, func(r BenchRun) int64 { return r.AllocsOp }, false),
		benchSpreadRow("iterations", runs, func(r BenchRun) int64 { return r.N }, true),
	)
	return benchTable(rows, st, rich)
}

// benchSpreadRow is one quantity's min, median and max across the runs.
func benchSpreadRow(label string, runs []BenchRun, of func(BenchRun) int64, group bool) []string {
	ns := make([]int64, 0, len(runs))
	for _, r := range runs {
		ns = append(ns, of(r))
	}
	sort.Slice(ns, func(i, j int) bool { return ns[i] < ns[j] })
	fmtN := itoa64
	if group {
		fmtN = func(n int64) string { return commas(itoa64(n)) }
	}
	// The upper middle at an even count, which is the value actually measured
	// rather than an average of two that were not. A spread reported as three
	// numbers that were all seen says only what happened; a standard deviation
	// over the three to ten runs :bench is used with would invite a reader to
	// treat too few samples as a distribution.
	return []string{label, fmtN(ns[0]), fmtN(ns[len(ns)/2]), fmtN(ns[len(ns)-1])}
}

// benchTable lays the spread out in columns, the label left and the numbers
// right. It pads before styling, since a lipgloss style makes len() lie.
func benchTable(rows [][]string, st pretty.Styles, rich bool) string {
	widths := make([]int, len(rows[0]))
	for _, r := range rows {
		for i, cell := range r {
			if len(cell) > widths[i] {
				widths[i] = len(cell)
			}
		}
	}
	var b strings.Builder
	for i, r := range rows {
		if i > 0 {
			b.WriteByte('\n')
		}
		for j, cell := range r {
			if j > 0 {
				b.WriteString("  ")
			}
			pad := strings.Repeat(" ", widths[j]-len(cell))
			text := pad + cell
			if j == 0 {
				text = cell + pad // the label column reads left-aligned
			}
			if rich {
				if i == 0 || j == 0 {
					text = st.Annot.Render(text)
				} else {
					text = st.Num.Render(text)
				}
			}
			b.WriteString(text)
		}
	}
	return b.String()
}

// commas groups an iteration count, which routinely runs to eight digits.
func commas(s string) string {
	if len(s) <= 3 {
		return s
	}
	var b strings.Builder
	lead := len(s) % 3
	if lead > 0 {
		b.WriteString(s[:lead])
	}
	for i := lead; i < len(s); i += 3 {
		if b.Len() > 0 {
			b.WriteByte(',')
		}
		b.WriteString(s[i : i+3])
	}
	return b.String()
}
