package pretty

import (
	"strings"

	"github.com/charmbracelet/lipgloss/table"
	"github.com/charmbracelet/lipgloss/tree"
)

// The four forms that are not the bordered table.
//
// Each one is bounded, and each bound has the same reason maxRows and maxCell
// have: what reaches scrollback is finite because a person scrolls past it. The
// numbers differ because the shapes do — one line holds more than one cell, and
// a row of eight columns holds less per column than a row of two.
const (
	// maxLine bounds FormLine. Value.inline is unbounded by design — only cell
	// truncated it, because only a cell had a width to respect. A form that
	// puts inline at the top level has to supply one, or a 200-element slice
	// is a wall of wrapped text.
	maxLine = 240
	// maxColCell bounds one cell of FormColumns. maxCell is 120 because two
	// columns can afford it; a column per field multiplies that by the field
	// count, and eight fields at 120 is a table no terminal will show.
	maxColCell = 40
	// maxCols bounds how many field columns are drawn, with the rest named
	// rather than dropped silently.
	maxCols = 8
)

// lineForm is one line per value, however deep the value goes.
//
// It is Plain's shape with colour and with the plugin Inline hooks that Plain
// may never have — invariant 21 — which is the whole reason it is not simply
// Plain in a terminal. A three-field struct costs six lines as a table and one
// here, and it is the only form under which a multi-value result still reads as
// a tuple, because RichIn comma-joins parts that hold no newline.
func (v Value) lineForm(st Styles, opts Options) string {
	body := truncate(v.inlineWith(st, opts), maxLine)
	if v.Kind == "string" {
		body = st.Str.Render(body)
	}
	s := body
	if v.Type != "" {
		s = st.Type.Render("("+v.Type+")") + " " + body
	}
	return s + v.tail(st)
}

// tail is the annotations and the structure note, in the order and the styles
// scalarLine puts them in. Shared so a form cannot accidentally drop the
// bytes-versus-runes split or the typed-nil warning, which are the facts a bare
// value dump leaves out and gluon exists to add.
func (v Value) tail(st Styles) string {
	var out strings.Builder
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
		out.WriteString("  " + st.Annot.Render(strings.Join(annots, ", ")))
	}
	if note != "" {
		out.WriteString("  " + st.Note.Render(note))
	}
	if n := v.structureNote(); n != "" {
		out.WriteString("  " + st.Note.Render(n))
	}
	return out.String()
}

// treeForm is indentation instead of borders, and it is the only form that
// shows nesting.
//
// Under the table form a struct field that is itself a struct becomes a cell:
// one line, truncated at maxCell. That information is currently unrecoverable
// without opening :inspect over the value. Here the walk recurses instead, so
// what a table flattens is what a tree shows.
//
// Drawn with lipgloss/tree, which ships inside the lipgloss already required —
// no dependency added, and no widget written. The enumerator is styled with
// st.Border because a branch character is structurally the same thing as a
// table border; giving it a role of its own would mean editing internal/theme,
// internal/ui, config.validate and all twenty-three theme files to say
// something the border role already says.
func (v Value) treeForm(st Styles, opts Options) string {
	target, prefix := v, v.label()
	if v.Kind == "ptr" && len(v.Items) == 1 {
		target, prefix = v.Items[0], v.label()+"&"
	}
	head := strings.TrimRight(st.Type.Render("("+v.Type+")")+" "+prefix, " ")
	if a := v.annotations(); len(a) > 0 {
		head += "  " + st.Annot.Render(strings.Join(a, ", "))
	}

	// One budget for the whole tree rather than one per level, so a wide value
	// and a deep one cost the same and MaxRows stays the single threshold
	// :query agrees with about when a collection is too big to print.
	budget := maxRows
	t := tree.Root(head).EnumeratorStyle(st.Border)
	target.treeChildren(t, st, opts, &budget)

	out := t.String()
	if n := v.structureNote(); n != "" {
		out += "\n" + st.Note.Render("  "+n)
	}
	return out
}

// treeChildren hangs one composite's parts off a node, recursing where a table
// cell would have truncated.
func (v Value) treeChildren(parent *tree.Tree, st Styles, opts Options, budget *int) {
	add := func(label string, child Value) bool {
		if *budget <= 0 {
			return false
		}
		*budget--
		// A composite becomes a subtree whose root carries the label and the
		// type; anything else is a leaf, because a tree of one node is a node.
		if child.treeWorthy() {
			sub := tree.Root(label + "  " + st.Type.Render(child.typeLabel())).
				EnumeratorStyle(st.Border)
			child.treeChildren(sub, st, opts, budget)
			parent.Child(sub)
			return true
		}
		parent.Child(label + "  " + child.cell(st, opts))
		return true
	}

	more := v.More
	switch v.Kind {
	case "list":
		for i, it := range v.Items {
			if !add(itoa(i), it) {
				more += len(v.Items) - i
				break
			}
		}
	case "map":
		for i, k := range v.Keys {
			if !add(k.cell(st, opts), v.Items[i]) {
				more += len(v.Keys) - i
				break
			}
		}
	case "struct":
		for i, f := range v.Fields {
			if !add(f.Name, f.Val) {
				more += len(v.Fields) - i
				break
			}
		}
	case "ptr":
		if len(v.Items) == 1 {
			add("&", v.Items[0])
		}
	}
	if more > 0 {
		parent.Child("… " + itoa(more) + " more")
	}
}

// treeWorthy reports whether a child earns a subtree rather than a leaf. It is
// tabular() plus the pointer that tabular() defers to its target, because a
// pointer to a struct is drawn as the struct.
func (v Value) treeWorthy() bool { return v.tabular() }

// typeLabel is the type a subtree root carries, with the length a table would
// have put in the annotation column. Without it a nested slice reads as a
// nameless indent.
func (v Value) typeLabel() string {
	t := v.Type
	if t == "" {
		t = v.Kind
	}
	if a := v.annotations(); len(a) > 0 {
		return t + "  " + strings.Join(a, ", ")
	}
	return t
}

// columnsTable gives a list of uniform structs a column per field.
//
// browse.go already knows how to compute those columns; until now the only way
// to see them was to open the :inspect modal over the value. This puts the same
// capability in scrollback, and reuses structColumns rather than restating the
// uniformity rule — a column that exists for some rows and not others is a
// worse table than one column of rendered values, and there should be one place
// that decides so.
//
// It reports false when the list is not uniform, and the caller falls back to
// the ordinary #/value table. Bounded harder than that table, because the width
// of a row here is the field count times the cell width rather than one cell.
func columnsTable(v Value, st Styles, opts Options) (*table.Table, bool) {
	names, uniform := structColumns(v.Items)
	if !uniform {
		return nil, false
	}
	trimmedCols := 0
	if len(names) > maxCols {
		trimmedCols = len(names) - maxCols
		names = names[:maxCols]
	}

	headers := append([]string{"#"}, names...)
	if trimmedCols > 0 {
		// Named rather than dropped: a table that quietly holds some of the
		// fields is a table somebody reads as holding all of them.
		headers = append(headers, "… "+itoa(trimmedCols)+" more")
	}
	t := baseTable(st).Headers(headers...)

	n := len(v.Items)
	trimmed := 0
	if n > maxRows {
		trimmed = n - maxRows
		n = maxRows
	}
	for i := 0; i < n; i++ {
		row := []string{itoa(i)}
		for _, name := range names {
			row = append(row, truncate(columnCell(v.Items[i], name, st, opts), maxColCell))
		}
		if trimmedCols > 0 {
			row = append(row, "")
		}
		t.Row(row...)
	}
	if rest := trimmed + v.More; rest > 0 {
		t.Row(append([]string{"", "… " + itoa(rest) + " more"},
			make([]string, len(headers)-2)...)...)
	}
	return t, true
}

// columnCell is one struct field rendered for a column.
//
// Through cell rather than browse.go's fieldCell, so a plugin's Inline form
// reaches a column exactly as it reaches an ordinary table cell — fieldCell
// calls inline directly and sees no hooks, which is right for the modal (which
// has none) and wrong here.
func columnCell(v Value, name string, st Styles, opts Options) string {
	if v.Kind == "ptr" && len(v.Items) == 1 {
		v = v.Items[0]
	}
	for _, f := range v.Fields {
		if f.Name == name {
			return f.Val.cell(st, opts)
		}
	}
	return ""
}

// literalForm is the value as Go source you could paste back.
//
// It is the only form whose output is input somewhere else — `want := …` in a
// test, or a line typed at the prompt — which is what it earns its place for.
//
// A value with no literal form falls back to a line and says why. That rule is
// what makes it a form at all: :test may refuse to generate a test, because
// that is a judgement about a test, but a form that sometimes declines to draw
// is not a form. NoLiteral already names the component that has no literal —
// ".Sock is a channel" — so the fallback is never silent, which is the same
// distinction between "found nothing" and "did not look" that gluon draws
// everywhere else.
//
// Hooks are not consulted, deliberately: a plugin renderer answers with a
// rendering and this form answers with source. TestLiteralFormIgnoresHooks
// pins that as a decision rather than an omission.
func (v Value) literalForm(st Styles) string {
	src, err := Literal(v)
	if err != nil {
		plain := v.lineForm(st, Options{})
		return plain + "  " + st.Note.Render("no literal form: "+err.Error())
	}
	return st.Str.Render(src) + v.tail(st)
}
