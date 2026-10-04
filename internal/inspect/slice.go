package inspect

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/lipgloss/table"

	"github.com/sandboxws/gluon/internal/pretty"
)

// RenderSlices draws slice headers side by side and says which of them share a
// backing array.
//
// Aliasing is the thing about slices that surprises people: two slices with
// different values can be views on the same memory, so appending through one
// changes the other. Nothing in a slice's printed contents shows that, and the
// pointer triple shows it immediately.
func RenderSlices(names []string, vals []pretty.Value, st pretty.Styles, rich bool) string {
	rows := make([][]string, 0, len(vals))
	var notes []string
	for i, v := range vals {
		name := nameAt(names, i)
		if v.Kind != "hdr" && v.Kind != "nil" {
			// Something with no header at all. Saying so in a table cell would
			// stretch every column to the width of the sentence.
			notes = append(notes, name+": "+v.Repr)
			continue
		}
		rows = append(rows, []string{name, v.Type, ptrText(v), lenText(v), capText(v)})
	}
	if len(rows) == 0 {
		return strings.Join(notes, "\n")
	}

	var b strings.Builder
	if rich {
		tbl := table.New().
			Border(lipgloss.RoundedBorder()).
			BorderStyle(st.Border).
			Headers("", "type", "ptr", "len", "cap").
			StyleFunc(func(row, col int) lipgloss.Style {
				if row == table.HeaderRow {
					return st.Header
				}
				switch col {
				case 0:
					return st.Type.Padding(0, 1)
				case 1:
					return st.Annot.Padding(0, 1)
				default:
					return st.Num.Padding(0, 1)
				}
			}).
			Rows(rows...)
		b.WriteString(tbl.String())
	} else {
		b.WriteString(fmt.Sprintf("%-10s %-14s %-18s %5s %5s", "", "type", "ptr", "len", "cap"))
		for _, r := range rows {
			b.WriteString(fmt.Sprintf("\n%-10s %-14s %-18s %5s %5s", r[0], r[1], r[2], r[3], r[4]))
		}
	}

	for _, line := range aliasing(names, vals) {
		b.WriteString("\n  " + st.Note.Render(line))
	}
	for _, line := range notes {
		b.WriteString("\n  " + st.Annot.Render(line))
	}
	return b.String()
}

// aliasing reports which pairs of slices are views on the same memory.
//
// Sharing is not pointer equality: a slice taken from the middle of another
// starts at a different address while still writing into the same array. What
// matters is whether the two [ptr, ptr+cap) ranges overlap.
func aliasing(names []string, vals []pretty.Value) []string {
	var out []string
	for i := range vals {
		for j := i + 1; j < len(vals); j++ {
			a, b := vals[i], vals[j]
			if a.Ptr == nil || b.Ptr == nil || a.Elem == nil || b.Elem == nil {
				continue
			}
			if *a.Elem != *b.Elem || a.Type != b.Type {
				continue // different element types cannot be the same array
			}
			aEnd := *a.Ptr + uint64(span(a))
			bEnd := *b.Ptr + uint64(span(b))
			if *a.Ptr >= bEnd || *b.Ptr >= aEnd {
				continue
			}
			out = append(out, fmt.Sprintf("%s and %s share a backing array%s",
				nameAt(names, i), nameAt(names, j), offsetText(a, b, names, i, j)))
		}
	}
	return out
}

// span is how many bytes of backing array a slice can reach: cap, not len,
// because appending within cap writes into memory another slice can see.
func span(v pretty.Value) int {
	n := 0
	switch {
	case v.Cap != nil:
		n = *v.Cap
	case v.Len != nil:
		n = *v.Len
	}
	if v.Elem == nil {
		return n
	}
	return n * *v.Elem
}

func offsetText(a, b pretty.Value, names []string, i, j int) string {
	if a.Elem == nil || *a.Elem == 0 || *a.Ptr == *b.Ptr {
		return ""
	}
	lo, hi, loName, hiName := a, b, nameAt(names, i), nameAt(names, j)
	if *b.Ptr < *a.Ptr {
		lo, hi, loName, hiName = b, a, hiName, loName
	}
	n := (*hi.Ptr - *lo.Ptr) / uint64(*lo.Elem)
	return fmt.Sprintf(" — %s starts %d %s into %s", hiName, n, unit(lo, n), loName)
}

// unit names what an offset counts. A string's backing array is bytes, which
// is the whole reason s[2:] can land in the middle of a multi-byte rune.
func unit(v pretty.Value, n uint64) string {
	word := "element"
	if v.Type == "string" {
		word = "byte"
	}
	return word + plural(n)
}

func plural(n uint64) string {
	if n == 1 {
		return ""
	}
	return "s"
}

func nameAt(names []string, i int) string {
	if i < len(names) && names[i] != "" {
		return names[i]
	}
	return fmt.Sprintf("#%d", i+1)
}

func ptrText(v pretty.Value) string {
	if v.Ptr == nil {
		if v.Repr != "" {
			return v.Repr
		}
		return "—"
	}
	return fmt.Sprintf("0x%x", *v.Ptr)
}

func lenText(v pretty.Value) string {
	if v.Len == nil {
		return "—"
	}
	return fmt.Sprintf("%d", *v.Len)
}

// A string has no cap: it is immutable, so there is nothing to grow into.
func capText(v pretty.Value) string {
	if v.Cap == nil {
		return "—"
	}
	return fmt.Sprintf("%d", *v.Cap)
}
