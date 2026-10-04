// Package pretty renders the values a generated program describes. The child
// process only encodes structure — see internal/gluonrt — so all formatting
// choices, colour and layout live here, in gluon's own process where lipgloss
// is available and where nothing is paid for per evaluation.
package pretty

import (
	"encoding/json"
	"strings"

	"github.com/sandboxws/gluon/internal/gluonrt"
)

// Value mirrors the encoder's output. Keys are terse because they cross a
// process boundary on every evaluation.
type Value struct {
	Type   string  `json:"t"`
	Kind   string  `json:"k"`
	Repr   string  `json:"r"`
	Note   string  `json:"n"`
	Len    *int    `json:"l"`
	Cap    *int    `json:"c"`
	Runes  *int    `json:"u"`
	Items  []Value `json:"i"`
	Keys   []Value `json:"ky"`
	Fields []Field `json:"f"`
	More   int     `json:"m"`
	// Ptr and Elem describe a slice or string header: the address of the
	// backing array and the size of one element. They are only set by the
	// header encoder, which :slice uses to show aliasing.
	Ptr  *uint64 `json:"p"`
	Elem *int    `json:"z"`
	// ID labels an object that something else refers to. On a cycle or shared
	// value it is the id being referred to; on the target it is the target's
	// own. Ids are sequence numbers rather than addresses, so the same program
	// always produces the same bytes.
	ID int `json:"d"`
}

type Field struct {
	Name string `json:"n"`
	Val  Value  `json:"v"`
}

// Parse separates whatever the user's own code printed from the encoded
// values that follow it, flattened into one list. One submitted line
// produces one payload, so for everything but a batch this is the whole
// story; a batch-aware caller uses ParseGroups instead, because flattening
// erases the line between "two entries" and "one entry returning two values"
// — a tuple renders on one line, two entries render on two.
func Parse(raw string) (userOut string, vals []Value) {
	userOut, groups := ParseGroups(raw)
	for _, g := range groups {
		vals = append(vals, g...)
	}
	return userOut, vals
}

// ParseGroups separates the user's own output from the encoded payloads
// interleaved with it — one group per payload, in entry order.
func ParseGroups(raw string) (userOut string, groups [][]Value) {
	orig := raw
	var out strings.Builder
	for {
		i := strings.Index(raw, gluonrt.Marker)
		if i < 0 {
			out.WriteString(raw)
			return out.String(), groups
		}
		out.WriteString(raw[:i])
		rest := raw[i+len(gluonrt.Marker):]
		// Anything after a payload line is output too: a goroutine that
		// outlives the printed expression writes there, and so does the drain
		// note — and in a batch, so does the next entry's own printing.
		line := rest
		if nl := strings.IndexByte(rest, '\n'); nl >= 0 {
			line, rest = rest[:nl], rest[nl+1:]
		} else {
			rest = ""
		}
		var vals []Value
		if err := json.Unmarshal([]byte(line), &vals); err != nil {
			// A malformed payload should not swallow the program's own output.
			return orig, nil
		}
		groups = append(groups, vals)
		raw = rest
	}
}

// annotations are the type-specific facts a bare value dump leaves out: the
// bytes-vs-runes split, len/cap, and the typed-nil-in-an-interface trap.
func (v Value) annotations() []string {
	var a []string
	switch {
	case v.Kind == "string" && v.Len != nil && v.Runes != nil:
		if *v.Len == *v.Runes {
			a = append(a, "len="+itoa(*v.Len))
		} else {
			a = append(a, "len="+itoa(*v.Len)+" bytes, "+itoa(*v.Runes)+" runes")
		}
	case v.Len != nil && v.Cap != nil:
		a = append(a, "len="+itoa(*v.Len)+" cap="+itoa(*v.Cap))
	case v.Len != nil:
		a = append(a, "len="+itoa(*v.Len))
	}
	if v.Note != "" {
		a = append(a, v.Note)
	}
	return a
}

// inline is the one-line form of a value, used for scalars and for nested
// values inside a table cell.
//
// It is inlineWith at the zero Options, so there is one definition of the
// one-line form and Plain still cannot reach a plugin hook: Plain never builds
// an Options, and a nil hook map is not a hook that declined. Invariant 21 is
// held by there being no path rather than by a check.
func (v Value) inline() string { return v.inlineWith(Styles{}, Options{}) }

// inlineWith is inline with the plugin Inline hooks consulted at every node.
//
// The hooks recurse where inline could not: today a time.Duration inside a
// struct inside a slice reads as its Repr, because only cell consults a hook
// and only the outermost value is a cell. With no hooks this is inline byte for
// byte whatever Styles it is handed — nothing here paints, because painting is
// the top level's job and doing it twice would nest escape sequences inside a
// table cell.
func (v Value) inlineWith(st Styles, opts Options) string {
	if h, ok := opts.Hooks[v.Type]; ok && h.Inline != nil {
		if out, handled := h.Inline(v, st); handled {
			return oneLine(out)
		}
	}
	switch v.Kind {
	case "string":
		return quote(v.Repr)
	case "list":
		if len(v.Items) == 0 {
			return "[]"
		}
		parts := make([]string, 0, len(v.Items))
		for _, it := range v.Items {
			parts = append(parts, it.inlineWith(st, opts))
		}
		if v.More > 0 {
			parts = append(parts, "…")
		}
		return "[" + strings.Join(parts, " ") + "]"
	case "map":
		if len(v.Keys) == 0 {
			return "map[]"
		}
		parts := make([]string, 0, len(v.Keys))
		for i, k := range v.Keys {
			parts = append(parts, k.inlineWith(st, opts)+":"+v.Items[i].inlineWith(st, opts))
		}
		return "map[" + strings.Join(parts, " ") + "]"
	case "struct":
		parts := make([]string, 0, len(v.Fields))
		for _, f := range v.Fields {
			parts = append(parts, f.Name+":"+f.Val.inlineWith(st, opts))
		}
		return v.label() + "{" + strings.Join(parts, " ") + "}"
	case "ptr":
		if len(v.Items) == 1 {
			return v.label() + "&" + v.Items[0].inlineWith(st, opts)
		}
	case "cycle":
		return "<cycle #" + itoa(v.ID) + ">"
	case "shared":
		return "<shared #" + itoa(v.ID) + ">"
	}
	return v.Repr
}

// label is the #N marker on a value something else points back at. resolveIDs
// clears the id on everything nothing refers to, so ordinary output carries no
// labels at all.
func (v Value) label() string {
	if v.ID == 0 {
		return ""
	}
	return "#" + itoa(v.ID)
}

// structureNote describes what the ids mean, once, at the top level. The child
// reports the structure; saying what it means in English is gluon's job.
func (v Value) structureNote() string {
	var cyclic, shared bool
	v.walk(func(c Value) {
		switch c.Kind {
		case "cycle":
			cyclic = true
		case "shared":
			shared = true
		}
	})
	switch {
	case cyclic:
		return "cyclic — it is reachable from inside itself"
	case shared:
		return "the same object appears more than once — not a copy"
	}
	return ""
}

func (v Value) walk(f func(Value)) {
	f(v)
	for _, it := range v.Items {
		it.walk(f)
	}
	for _, k := range v.Keys {
		k.walk(f)
	}
	for _, fl := range v.Fields {
		fl.Val.walk(f)
	}
}

// tabular reports whether a value has enough structure that a table beats a
// single line. Scalars never do.
func (v Value) tabular() bool {
	switch v.Kind {
	case "list":
		return len(v.Items) > 0
	case "map":
		return len(v.Keys) > 0
	case "struct":
		return len(v.Fields) > 0
	case "ptr":
		return len(v.Items) == 1 && v.Items[0].tabular()
	}
	// A back-reference is a scalar: there is nothing under it to tabulate.
	return false
}

// Quote is how the printer writes a string, for a renderer that draws one: a
// string a plugin shows reads the way every other string does, quoted — which
// is what tells the text NULL from a SQL NULL once colour has been stripped.
func Quote(s string) string { return quote(s) }

func quote(s string) string {
	var b strings.Builder
	b.WriteByte('"')
	for _, r := range s {
		switch r {
		case '"':
			b.WriteString(`\"`)
		case '\\':
			b.WriteString(`\\`)
		case '\n':
			b.WriteString(`\n`)
		case '\t':
			b.WriteString(`\t`)
		default:
			b.WriteRune(r)
		}
	}
	b.WriteByte('"')
	return b.String()
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}

// resolveIDs clears the label on every object nothing refers to.
//
// The child cannot make this call: it streams, and a shared object is fully
// written before the reference to it is reached, so it labels everything it
// tracks. Here the whole tree exists, so the labels can be reduced to the ones
// that are actually pointed at — which is what keeps output that has no cycles
// and no sharing looking exactly as it did.
func (v Value) resolveIDs() Value {
	referenced := map[int]bool{}
	v.walk(func(c Value) {
		if c.Kind == "cycle" || c.Kind == "shared" {
			referenced[c.ID] = true
		}
	})
	return v.clearIDs(referenced)
}

func (v Value) clearIDs(keep map[int]bool) Value {
	if v.Kind != "cycle" && v.Kind != "shared" && !keep[v.ID] {
		v.ID = 0
	}
	if v.Items != nil {
		items := make([]Value, len(v.Items))
		for i, it := range v.Items {
			items[i] = it.clearIDs(keep)
		}
		v.Items = items
	}
	if v.Keys != nil {
		keys := make([]Value, len(v.Keys))
		for i, k := range v.Keys {
			keys[i] = k.clearIDs(keep)
		}
		v.Keys = keys
	}
	if v.Fields != nil {
		fields := make([]Field, len(v.Fields))
		for i, f := range v.Fields {
			fields[i] = Field{Name: f.Name, Val: f.Val.clearIDs(keep)}
		}
		v.Fields = fields
	}
	return v
}
