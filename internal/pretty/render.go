package pretty

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/lipgloss/table"
)

// maxRows bounds how much of a large collection reaches scrollback. The
// encoder already caps what it sends; this caps what is drawn.
const (
	maxRows = 50
	maxCell = 120
)

// MaxRows is maxRows, exported because :query has to agree with value
// rendering about when a table is too big to print. Two thresholds would mean
// a result set that scrolls and a slice of the same length that does not.
const MaxRows = maxRows

// Plain renders without colour or borders. It is what pipes and scripts get,
// so its shape is a compatibility surface — tests assert on it.
func Plain(vals []Value) string {
	parts := make([]string, 0, len(vals))
	for _, v := range vals {
		parts = append(parts, v.resolveIDs().plain())
	}
	return strings.Join(parts, ", ")
}

func (v Value) plain() string {
	body := v.inline()
	if v.Type == "" { // untyped nil
		if a := v.annotations(); len(a) > 0 {
			return body + "  " + strings.Join(a, ", ")
		}
		return body
	}
	s := "(" + v.Type + ") " + body
	if a := v.annotations(); len(a) > 0 {
		s += "  " + strings.Join(a, ", ")
	}
	if n := v.structureNote(); n != "" {
		s += "  " + n
	}
	return s
}

// Styles is the palette. Kept in one place so the look can be changed without
// hunting through the renderer.
type Styles struct {
	Type   lipgloss.Style
	Annot  lipgloss.Style
	Note   lipgloss.Style
	Str    lipgloss.Style
	Num    lipgloss.Style
	Header lipgloss.Style
	Index  lipgloss.Style
	Border lipgloss.Style
}

// PlainStyles is the palette with no colour: the padding a table needs and
// nothing a pipe would have to strip.
//
// This used to be DefaultStyles, and it used to carry a second copy of the ANSI
// values internal/ui owns — which nothing pinned, so the two could drift. It
// also meant "not rich" did not imply "not coloured": Core.styles handed these
// back on the non-rich path, and the only reason no escapes reached a pipe was
// that lipgloss's global renderer degrades on a non-TTY. That is an implicit
// global, which is exactly what internal/ui's doc says gluon does not rely on,
// and it was reachable — `gluon -e ':db' -json` from a terminal put escape
// sequences inside a JSON string.
//
// There is now exactly one coloured palette in gluon and it lives in
// internal/ui. This is what every other path uses.
//
// The padding is load-bearing rather than decorative: baseTable uses Header and
// Index as cell styles against a Padding(0, 1) default for every other cell, so
// dropping it makes the header and index columns ragged.
func PlainStyles() Styles {
	return Styles{
		Header: lipgloss.NewStyle().Padding(0, 1),
		Index:  lipgloss.NewStyle().Padding(0, 1),
	}
}

// Hook is a plugin's formatting for one type. See internal/plugin.
//
// The two forms exist because the two positions are different. Rich renders a
// value that owns its line and may take several; Inline renders one inside a
// table cell, where the surrounding structure has already supplied the type and
// a single line is all there is room for. A plugin that offers only Rich is
// simply not consulted for cells, which is the old behaviour.
type Hook struct {
	Rich   func(v Value, st Styles) (string, bool)
	Inline func(v Value, st Styles) (string, bool)
}

// Rich renders with colour, and draws a table when a value has enough
// structure to earn one. Scalars stay on a single line — a bordered box
// around the number 2 is worse than the number 2.
func Rich(vals []Value, st Styles) string { return RichIn(vals, st, Options{}) }

// RichWith is Rich with per-type hooks, keyed on the reflect type name the
// child sent. A hook that returns false falls through to the ordinary
// rendering, so a plugin can never break output it does not understand.
//
// Hooks apply to a top-level value only, not to one nested inside a table cell.
// A cell is a single line by construction, and a hook is free to return several;
// letting one run there would break the table it was drawn into.
//
// There is no Plain equivalent, deliberately. Plain is what pipes, gluon -e and
// the tests read — a compatibility surface — so nothing installable may change
// it. Invariant 19.
func RichWith(vals []Value, st Styles, hooks map[string]Hook) string {
	return RichIn(vals, st, Options{Hooks: hooks})
}

// RichIn is RichWith drawn in a chosen form.
//
// Rich and RichWith stay, and stay exactly what they were, because they are
// what every caller outside the TUI passes: a test that does not care which
// shape a value takes should not have to name one. They are RichIn at the zero
// Options, and the zero Options is the bordered table — so nothing that existed
// before this moved a byte, which is what TestTheDefaultFormIsTheOldRenderer
// asserts rather than assumes.
//
// The plugin hooks travel inside Options now, and the rule they came with is
// unchanged: a hook applies to a top-level value only, and a hook that declines
// falls through byte for byte. What is new is that FormLiteral does not consult
// them at all — it answers with source, and a renderer does not produce source.
func RichIn(vals []Value, st Styles, opts Options) string {
	parts := make([]string, 0, len(vals))
	for _, v := range vals {
		v = v.resolveIDs()
		if h, ok := opts.Hooks[v.Type]; ok && h.Rich != nil && opts.formFor(v) != FormLiteral {
			if out, handled := h.Rich(v, st); handled {
				parts = append(parts, out)
				continue
			}
		}
		parts = append(parts, v.rich(st, opts))
	}
	// A multi-value result reads as a tuple, but only while every part is a
	// single line; a table cannot be comma-joined.
	if len(parts) > 1 && !anyMultiline(parts) {
		return strings.Join(parts, ", ")
	}
	return strings.Join(parts, "\n")
}

func anyMultiline(parts []string) bool {
	for _, p := range parts {
		if strings.Contains(p, "\n") {
			return true
		}
	}
	return false
}

func (v Value) rich(st Styles, opts Options) string {
	// The forms that answer for every kind, tabular or not, decide before
	// tabular() does: a literal of the number 2 is `2`, and a line is a line.
	switch opts.formFor(v) {
	case FormLine:
		return v.lineForm(st, opts)
	case FormLiteral:
		return v.literalForm(st)
	}
	if !v.tabular() {
		return v.scalarLine(st)
	}
	// FormTree is decided after tabular(), because a tree of one node is a
	// node — the same argument scalarLine already makes about a bordered box
	// around the number 2.
	if opts.formFor(v) == FormTree {
		return v.treeForm(st, opts)
	}

	// Unwrap a pointer to a struct so &T{...} still tabulates.
	target, prefix := v, v.label()
	if v.Kind == "ptr" && len(v.Items) == 1 {
		target, prefix = v.Items[0], v.label()+"&"
	}

	head := st.Type.Render("("+v.Type+")") + " " + prefix
	if a := v.annotations(); len(a) > 0 {
		head += st.Annot.Render(strings.Join(a, ", "))
	}

	var t *table.Table
	switch target.Kind {
	case "list":
		// FormColumns is FormTable everywhere but here: a map has two columns
		// by nature, and a struct is already one row per field. The only shape
		// with fields to widen into columns is a list of structs.
		if opts.formFor(v) == FormColumns {
			if ct, ok := columnsTable(target, st, opts); ok {
				t = ct
				break
			}
		}
		t = listTable(target, st, opts)
	case "map":
		t = mapTable(target, st, opts)
	case "struct":
		t = structTable(target, st, opts)
	default:
		return v.scalarLine(st)
	}
	out := strings.TrimRight(head, " ") + "\n" + t.Render()
	if n := v.structureNote(); n != "" {
		out += "\n" + st.Note.Render("  "+n)
	}
	return out
}

func (v Value) scalarLine(st Styles) string {
	body := v.inline()
	if v.Kind == "string" {
		body = st.Str.Render(body)
	}
	s := body
	if v.Type != "" {
		s = st.Type.Render("("+v.Type+")") + " " + body
	}
	var annots []string
	var note string
	for _, a := range v.annotations() {
		if a == v.Note {
			note = a
			continue
		}
		annots = append(annots, a)
	}
	if len(annots) > 0 {
		s += "  " + st.Annot.Render(strings.Join(annots, ", "))
	}
	if note != "" {
		s += "  " + st.Note.Render(note)
	}
	return s
}

func baseTable(st Styles) *table.Table {
	return table.New().
		Border(lipgloss.RoundedBorder()).
		BorderStyle(st.Border).
		StyleFunc(func(row, col int) lipgloss.Style {
			if row == table.HeaderRow {
				return st.Header
			}
			if col == 0 {
				return st.Index
			}
			return lipgloss.NewStyle().Padding(0, 1)
		})
}

func listTable(v Value, st Styles, opts Options) *table.Table {
	t := baseTable(st).Headers("#", "value")
	n := len(v.Items)
	trimmed := 0
	if n > maxRows {
		trimmed = n - maxRows
		n = maxRows
	}
	for i := 0; i < n; i++ {
		t.Row(itoa(i), v.Items[i].cell(st, opts))
	}
	if rest := trimmed + v.More; rest > 0 {
		t.Row("", "… "+itoa(rest)+" more")
	}
	return t
}

func mapTable(v Value, st Styles, opts Options) *table.Table {
	t := baseTable(st).Headers("key", "value")
	n := len(v.Keys)
	trimmed := 0
	if n > maxRows {
		trimmed = n - maxRows
		n = maxRows
	}
	for i := 0; i < n; i++ {
		t.Row(v.Keys[i].cell(st, opts), v.Items[i].cell(st, opts))
	}
	if rest := trimmed + v.More; rest > 0 {
		t.Row("", "… "+itoa(rest)+" more")
	}
	return t
}

func structTable(v Value, st Styles, opts Options) *table.Table {
	t := baseTable(st).Headers("field", "value")
	for _, f := range v.Fields {
		t.Row(f.Name, f.Val.cell(st, opts))
	}
	return t
}

// cell renders a nested value for a table cell: always one line, coloured by
// kind so strings are distinguishable from numbers that look like them.
//
// A plugin's Inline form is consulted first, so a time.Duration inside a struct
// reads the same as one on its own line. The result is truncated and stripped
// of newlines like any other cell — a hook cannot break the table it was drawn
// into, however it misbehaves.
func (v Value) cell(st Styles, opts Options) string {
	if h, ok := opts.Hooks[v.Type]; ok && h.Inline != nil {
		if out, handled := h.Inline(v, st); handled {
			return truncate(oneLine(out), maxCell)
		}
	}
	s := truncate(v.inline(), maxCell)
	if v.Kind == "string" {
		return st.Str.Render(s)
	}
	return s
}

// oneLine flattens whatever a hook returned. A cell is one line by
// construction; a multi-line one silently corrupts every row below it.
func oneLine(s string) string {
	if !strings.ContainsAny(s, "\n\r") {
		return s
	}
	f := strings.FieldsFunc(s, func(r rune) bool { return r == '\n' || r == '\r' })
	return strings.Join(f, " ")
}

// truncate bounds a table cell. maxRows caps how many rows a table has;
// nothing capped how wide one could get, and a nested value rendered inline
// can be arbitrarily long.
func truncate(s string, n int) string {
	if len([]rune(s)) <= n {
		return s
	}
	return string([]rune(s)[:n]) + "…"
}
