package inspect

import (
	"strconv"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/lipgloss/table"

	"github.com/sandboxws/gluon/internal/pretty"
)

// The structural comparison behind :diff.
//
// Both values arrive as pretty.Value trees the child already described, so the
// walk happens in gluon's own process and the generated program is untouched —
// :diff adds no imports to the child and nothing about the comparison shows up
// in :src. The alternative, a diff algorithm generated into the child, would
// have to stay standard-library-only and would be re-derived on every call.

// DiffKind says why two values differ, which is what decides how a row reads.
type DiffKind int

const (
	// DiffValue is the ordinary case: the same shape, different contents.
	DiffValue DiffKind = iota
	// DiffType is a type difference, which stops the walk at that path.
	DiffType
	// DiffLen is two sequences of different length.
	DiffLen
	// DiffOnlyA and DiffOnlyB are an element, key or field one side has and
	// the other does not.
	DiffOnlyA
	DiffOnlyB
)

func (k DiffKind) note(names []string) string {
	switch k {
	case DiffType:
		return "type"
	case DiffLen:
		return "length"
	case DiffOnlyA:
		return "only in " + nameAt(names, 0)
	case DiffOnlyB:
		return "only in " + nameAt(names, 1)
	}
	return ""
}

// A Diff is one difference, at one path within the two values.
type Diff struct {
	// Path reads as Go source from the root of the value — .Name, [3],
	// .Ports["ssh"] — and is empty at the root itself.
	Path string
	Kind DiffKind
	// A and B are each side at that path, already rendered. One is empty when
	// the Kind says that side has nothing there.
	A, B string
}

// A DiffResult is everything the walk found, plus whether it could see all of
// it.
type DiffResult struct {
	Diffs []Diff
	// Truncated is set when either side hit the encoder's item cap anywhere
	// the walk reached. It is what keeps "no further differences" from being
	// read as "no differences": a comparison of two prefixes is a comparison
	// of two prefixes, and the report says so.
	Truncated bool

	// targets is what a back-reference refers to, keyed by the id the encoder
	// gave it. See DiffValues.
	targets map[int]pretty.Value
}

// Identical reports that the walk found nothing. It is a method rather than a
// len() at each call site because the answer is worth naming: two values that
// differ nowhere are a result, not an absence of one.
func (r DiffResult) Identical() bool { return len(r.Diffs) == 0 }

// DiffValues compares two described values structurally.
//
// Back-references are resolved first. The encoder streams, so the second time
// an object appears in one payload it is written as a reference to the first
// rather than repeated — and both values here came from one payload, which is
// exactly what makes :diff x, y answerable when y is x. Without this, a value
// would be reported as differing from itself.
func DiffValues(a, b pretty.Value) DiffResult {
	r := DiffResult{targets: map[int]pretty.Value{}}
	collectTargets(a, r.targets)
	collectTargets(b, r.targets)
	r.walk("", a, b, 0)
	return r
}

// collectTargets indexes every object something else may refer back to.
func collectTargets(v pretty.Value, into map[int]pretty.Value) {
	if v.Kind == "cycle" || v.Kind == "shared" {
		return
	}
	if v.ID != 0 {
		into[v.ID] = v
	}
	for _, it := range v.Items {
		collectTargets(it, into)
	}
	for _, k := range v.Keys {
		collectTargets(k, into)
	}
	for _, f := range v.Fields {
		collectTargets(f.Val, into)
	}
}

// deref replaces a back-reference with the object it names.
//
// A cycle is left alone: following one would not terminate, and two cycles
// compare by the id they point at — meaningful precisely because both values
// came from one program, where ids are sequence numbers over one encoding.
func (r *DiffResult) deref(v pretty.Value) pretty.Value {
	if v.Kind != "shared" {
		return v
	}
	if t, ok := r.targets[v.ID]; ok {
		return t
	}
	return v
}

// maxDiffDepth bounds the walk. The encoder caps its own depth at 6, and
// resolving a reference can only add finitely to that — an object that
// contains itself is written as a cycle, which is not followed. This is the
// belt to that argument's braces: a REPL that hangs on :diff would be worse
// than one that stops early and says so.
const maxDiffDepth = 64

func (r *DiffResult) add(path string, kind DiffKind, a, b string) {
	r.Diffs = append(r.Diffs, Diff{Path: path, Kind: kind, A: a, B: b})
}

func (r *DiffResult) walk(path string, a, b pretty.Value, depth int) {
	a, b = r.deref(a), r.deref(b)
	if depth > maxDiffDepth {
		r.Truncated = true
		return
	}
	if a.More > 0 || b.More > 0 {
		// Checked at every node, not once at the top: the cap bites wherever
		// a big collection is, and a truncated slice three fields down makes
		// the whole comparison partial just as surely as a truncated root.
		r.Truncated = true
	}

	if a.Type != b.Type {
		// Two values of different types have no meaningful field-by-field
		// comparison — every field would be reported as differing, which
		// tells the reader nothing they did not already know from the types.
		r.add(path, DiffType, a.Type, b.Type)
		return
	}
	if a.Kind != b.Kind {
		// Same type, different shape: a nil slice against an empty one, or a
		// value the encoder flattened on one side only.
		r.add(path, DiffValue, brief(a), brief(b))
		return
	}

	switch a.Kind {
	case "list":
		r.walkList(path, a, b, depth)
	case "map":
		r.walkMap(path, a, b, depth)
	case "struct":
		r.walkStruct(path, a, b, depth)
	case "ptr":
		// Go's selector derefs on its own, so the path through a pointer is
		// the path through what it points at: .Owner.Name, not (*.Owner).Name.
		if len(a.Items) == 1 && len(b.Items) == 1 {
			r.walk(path, a.Items[0], b.Items[0], depth+1)
			return
		}
		if brief(a) != brief(b) {
			r.add(path, DiffValue, brief(a), brief(b))
		}
	case "cycle", "shared":
		// Ids are sequence numbers within one program, and both values came
		// from one evaluation — which is why they can be compared at all.
		if a.ID != b.ID {
			r.add(path, DiffValue, brief(a), brief(b))
		}
	default:
		if a.Repr != b.Repr {
			r.add(path, DiffValue, brief(a), brief(b))
		}
	}
}

func (r *DiffResult) walkList(path string, a, b pretty.Value, depth int) {
	if la, lb := lengthOf(a), lengthOf(b); la != lb {
		// Reported as the structural fact it is, and then the positions both
		// sides have are still compared: "shorter by one" and "and the third
		// element changed too" are different answers.
		r.add(path, DiffLen, la, lb)
	}
	n := len(a.Items)
	if len(b.Items) < n {
		n = len(b.Items)
	}
	for i := 0; i < n; i++ {
		r.walk(path+"["+strconv.Itoa(i)+"]", a.Items[i], b.Items[i], depth+1)
	}
	for i := n; i < len(a.Items); i++ {
		r.add(path+"["+strconv.Itoa(i)+"]", DiffOnlyA, brief(a.Items[i]), "")
	}
	for i := n; i < len(b.Items); i++ {
		r.add(path+"["+strconv.Itoa(i)+"]", DiffOnlyB, "", brief(b.Items[i]))
	}
}

// walkMap merges two key lists the encoder already sorted, so a key present on
// one side only is found in one pass and the report is deterministic.
func (r *DiffResult) walkMap(path string, a, b pretty.Value, depth int) {
	i, j := 0, 0
	for i < len(a.Keys) && j < len(b.Keys) {
		ka, kb := brief(a.Keys[i]), brief(b.Keys[j])
		switch {
		case ka == kb:
			r.walk(path+"["+ka+"]", a.Items[i], b.Items[j], depth+1)
			i, j = i+1, j+1
		case ka < kb:
			r.add(path+"["+ka+"]", DiffOnlyA, brief(a.Items[i]), "")
			i++
		default:
			r.add(path+"["+kb+"]", DiffOnlyB, "", brief(b.Items[j]))
			j++
		}
	}
	for ; i < len(a.Keys); i++ {
		r.add(path+"["+brief(a.Keys[i])+"]", DiffOnlyA, brief(a.Items[i]), "")
	}
	for ; j < len(b.Keys); j++ {
		r.add(path+"["+brief(b.Keys[j])+"]", DiffOnlyB, "", brief(b.Items[j]))
	}
}

// walkStruct pairs fields by name. Two values of one type have one field list,
// so the only way this finds a name on one side alone is a value the encoder
// described differently on each side — which is worth reporting rather than
// assuming away.
func (r *DiffResult) walkStruct(path string, a, b pretty.Value, depth int) {
	inB := make(map[string]pretty.Value, len(b.Fields))
	for _, f := range b.Fields {
		inB[f.Name] = f.Val
	}
	seen := make(map[string]bool, len(a.Fields))
	for _, f := range a.Fields {
		seen[f.Name] = true
		other, ok := inB[f.Name]
		if !ok {
			r.add(path+"."+f.Name, DiffOnlyA, brief(f.Val), "")
			continue
		}
		r.walk(path+"."+f.Name, f.Val, other, depth+1)
	}
	for _, f := range b.Fields {
		if !seen[f.Name] {
			r.add(path+"."+f.Name, DiffOnlyB, "", brief(f.Val))
		}
	}
}

// lengthOf is what a sequence reports as its length, which is the encoder's
// count and not the number of items that survived the cap.
func lengthOf(v pretty.Value) string {
	if v.Len != nil {
		return strconv.Itoa(*v.Len)
	}
	return strconv.Itoa(len(v.Items))
}

// brief is one side of one row: short enough for a table cell, and specific
// enough to be the answer. A composite is named by its type and length rather
// than expanded, because the walk has already gone inside it and anything that
// differed in there has a row of its own.
func brief(v pretty.Value) string {
	switch v.Kind {
	case "string":
		return strconv.Quote(v.Repr)
	case "nil":
		return "nil"
	case "list", "map":
		if v.Len != nil {
			return v.Type + " len=" + strconv.Itoa(*v.Len)
		}
		return v.Type
	case "struct", "ptr":
		return v.Type
	case "cycle":
		return "<cycle #" + strconv.Itoa(v.ID) + ">"
	case "shared":
		return "<shared #" + strconv.Itoa(v.ID) + ">"
	}
	return v.Repr
}

// absent is what a cell holds when that side has nothing at this path. An
// em dash rather than an empty cell, so a missing key reads as missing rather
// than as a value that happened to render blank.
const absent = "—"

// RenderDiff draws what differs, with the path to each difference.
//
// Only the differences: a struct of twenty fields that differs in one produces
// one row, which is the whole reason to have the command rather than print
// both values and read them.
func RenderDiff(names []string, r DiffResult, st pretty.Styles) string {
	if s, ok := diffHeadline(names, r, st, true); ok {
		return s
	}
	rows := diffRows(names, r)
	tbl := table.New().
		Border(lipgloss.RoundedBorder()).
		BorderStyle(st.Border).
		Headers("path", nameAt(names, 0), nameAt(names, 1), "").
		StyleFunc(func(row, col int) lipgloss.Style {
			if row == table.HeaderRow {
				return st.Header
			}
			switch col {
			case 0:
				return st.Type.Padding(0, 1)
			case 3:
				return st.Annot.Padding(0, 1)
			default:
				return st.Str.Padding(0, 1)
			}
		}).
		Rows(rows...)

	out := tbl.String()
	if note := truncationNote(r); note != "" {
		out += "\n  " + st.Note.Render(note)
	}
	return out
}

// PlainDiff is the same report through a pipe, where the shape is what a
// script reads.
func PlainDiff(names []string, r DiffResult) string {
	if s, ok := diffHeadline(names, r, pretty.Styles{}, false); ok {
		return s
	}
	rows := append([][]string{{"path", nameAt(names, 0), nameAt(names, 1), ""}}, diffRows(names, r)...)
	widths := make([]int, 4)
	for _, row := range rows {
		for i, cell := range row {
			if w := lipgloss.Width(cell); w > widths[i] {
				widths[i] = w
			}
		}
	}
	var b strings.Builder
	for _, row := range rows {
		if b.Len() > 0 {
			b.WriteByte('\n')
		}
		var line strings.Builder
		for i, cell := range row {
			if i > 0 {
				line.WriteString("  ")
			}
			line.WriteString(cell + strings.Repeat(" ", widths[i]-lipgloss.Width(cell)))
		}
		b.WriteString(strings.TrimRight(line.String(), " "))
	}
	if note := truncationNote(r); note != "" {
		b.WriteString("\n" + note)
	}
	return b.String()
}

// diffHeadline is the two reports that are a sentence rather than a table.
//
// Identical values are one of them because empty output would read as "the
// command did nothing". A type difference at the root is the other: it is the
// whole answer, and a one-row table would be a frame around a sentence.
func diffHeadline(names []string, r DiffResult, st pretty.Styles, rich bool) (string, bool) {
	line := ""
	switch {
	case r.Identical():
		line = nameAt(names, 0) + " and " + nameAt(names, 1) + " are identical"
	case len(r.Diffs) == 1 && r.Diffs[0].Path == "" && r.Diffs[0].Kind == DiffType:
		d := r.Diffs[0]
		line = "types differ: " + nameAt(names, 0) + " is " + d.A + ", " +
			nameAt(names, 1) + " is " + d.B
	default:
		return "", false
	}
	note := truncationNote(r)
	if rich {
		line = st.Annot.Render(line)
		if note != "" {
			note = st.Note.Render(note)
		}
	}
	if note != "" {
		line += "\n" + note
	}
	return line, true
}

func diffRows(names []string, r DiffResult) [][]string {
	rows := make([][]string, 0, len(r.Diffs))
	for _, d := range r.Diffs {
		a, b := d.A, d.B
		switch d.Kind {
		case DiffOnlyA:
			b = absent
		case DiffOnlyB:
			a = absent
		case DiffLen:
			// The walk reports the two lengths as numbers; saying which
			// number this is belongs here, where a bare 2 in a column of
			// values would otherwise read as one.
			a, b = "len "+a, "len "+b
		}
		rows = append(rows, []string{diffPath(d.Path), a, b, d.Kind.note(names)})
	}
	return rows
}

// diffPath names the root. Every other path is Go source you could type after
// the operand; the root is the operand itself, and "." is what jq and every
// path syntax after it calls the whole value.
func diffPath(p string) string {
	if p == "" {
		return "."
	}
	return p
}

func truncationNote(r DiffResult) string {
	if !r.Truncated {
		return ""
	}
	return "one side was capped by the value printer — this compares only the part that was described"
}
