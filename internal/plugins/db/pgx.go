package db

import (
	"fmt"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/lipgloss/table"

	"github.com/sandboxws/gluon/internal/plugin"
	"github.com/sandboxws/gluon/internal/pretty"
)

// Pgx is the plugin for github.com/jackc/pgx.
//
// The daily question about pgx is not what SQL a builder produces — pgx has no
// builder — it is what the connection pool is doing. pgxpool.Stat answers it,
// and answers it in the least readable shape available: every field on it is
// unexported, and most of the counters are one level further down, behind a
// pointer to puddle.Stat. Printed structurally that is three depths of names
// like `s`, `newConnsCount` and `acquiredResources`, with the four numbers
// anyone actually wants scattered through them.
//
// This is the change's only renderer, and the reason it is one is that
// everything needed is already in the value the child described. A counter's
// meaning is in its name, and the name is structure — so nothing has to be
// built, run, or connected to. That is the inverse of time.Time, whose meaning
// lives in a String() method the struct encoder walks straight past, and it is
// why a command here would be a program compiled to read numbers gluon is
// already holding.
type Pgx struct{}

func (Pgx) Meta() plugin.Meta {
	return plugin.Meta{
		Name:    "pgx",
		Module:  "github.com/jackc/pgx",
		Summary: "pool statistics as labelled counters, not as three depths of unexported fields",
	}
}

func (Pgx) Imports() []plugin.Import {
	return []plugin.Import{
		{Name: "pgx", Path: "github.com/jackc/pgx/v5"},
		{Name: "pgxpool", Path: "github.com/jackc/pgx/v5/pgxpool"},
	}
}

func (Pgx) Aliases() []plugin.Alias {
	return []plugin.Alias{{Name: "pgx", Module: "github.com/jackc/pgx/v5"}}
}

func (Pgx) Renders() []plugin.Render {
	return []plugin.Render{
		// Pool.Stat() returns a pointer, which is what a session will
		// ordinarily be holding; the value form is registered too so a
		// dereferenced one reads the same.
		{Type: "*pgxpool.Stat", Rich: renderPoolStat, Inline: inlinePoolStat},
		{Type: "pgxpool.Stat", Rich: renderPoolStat, Inline: inlinePoolStat},
	}
}

// poolLabels names what each counter counts.
//
// The names are what a real *pgxpool.Stat carries, read off one from pgx
// v5.10.0 / puddle v2.2.2 rather than assumed. They are pgx's and puddle's own
// and every one of them is unexported, so any of them could be renamed or added
// to without breaking a single build anywhere. That is why an
// unrecognised field is still shown, under its own name: these are counters
// whose meaning is in their naming, so the fallback for a field gluon has not
// heard of is not a shrug, it is the name the library gave it.
//
// The order is the reading order — what the pool is, then what it has done —
// rather than the declaration order, which interleaves the two.
var poolLabels = []struct{ field, label string }{
	{"maxResources", "max"},
	{"acquiredResources", "acquired"},
	{"idleResources", "idle"},
	{"constructingResources", "constructing"},
	{"acquireCount", "acquires"},
	{"emptyAcquireCount", "acquires that waited"},
	{"emptyAcquireWaitTime", "time spent waiting"},
	{"canceledAcquireCount", "acquires canceled"},
	{"acquireDuration", "time spent acquiring"},
	{"newConnsCount", "connections opened"},
	{"lifetimeDestroyCount", "closed — max lifetime"},
	{"idleDestroyCount", "closed — max idle"},
}

// poolCounter is one flattened field: the name the library gave it, and what
// the child said was in it.
type poolCounter struct{ name, value string }

// maxPoolDepth bounds the walk. pgxpool.Stat's counters sit one pointer down,
// so two levels of struct is all there is; the bound is what keeps a value that
// merely shares the type name from being walked forever.
const maxPoolDepth = 3

func renderPoolStat(v pretty.Value, st pretty.Styles) (string, bool) {
	counters, prefix, ok := poolStat(v)
	if !ok {
		return "", false
	}

	t := table.New().
		Border(lipgloss.RoundedBorder()).
		BorderStyle(st.Border).
		StyleFunc(func(row, col int) lipgloss.Style {
			if row == table.HeaderRow {
				return st.Header
			}
			if col == 0 {
				return st.Type.Padding(0, 1)
			}
			return st.Num.Padding(0, 1)
		}).
		Headers("counter", "value")
	for _, c := range counters {
		t.Row(poolLabel(c.name), c.value)
	}
	head := st.Type.Render("("+prefix+"pgxpool.Stat)") + " " +
		st.Annot.Render(fmt.Sprintf("%d counters", len(counters)))
	return head + "\n" + t.Render(), true
}

// inlinePoolStat is the cell form: what the pool is right now, on one line.
//
// The history counters are dropped rather than abbreviated. A cell has room for
// one fact, and "how many connections are in use" is the one somebody scanning
// a struct of pools is looking for; a total acquire count in a table column is
// a number with nothing to compare it to.
func inlinePoolStat(v pretty.Value, st pretty.Styles) (string, bool) {
	counters, _, ok := poolStat(v)
	if !ok {
		return "", false
	}
	by := make(map[string]string, len(counters))
	for _, c := range counters {
		by[c.name] = c.value
	}
	acquired, hasAcquired := by["acquiredResources"]
	max, hasMax := by["maxResources"]
	if !hasAcquired || !hasMax {
		// Without the two that make a ratio there is no one-line answer worth
		// giving, and declining leaves the ordinary struct rendering — which
		// is at least complete.
		return "", false
	}
	out := st.Num.Render(acquired+"/"+max) + st.Annot.Render(" acquired")
	if idle, ok := by["idleResources"]; ok {
		out += st.Annot.Render(" · ") + st.Num.Render(idle) + st.Annot.Render(" idle")
	}
	return out, true
}

// poolStat flattens a described Stat into its counters, or declines.
//
// Declining has to be cheap and total: a hook that accepted a value it did not
// understand would change output that has nothing to do with pgx, which is what
// invariant 21 and TestHooksFallThroughByteForByte are about.
func poolStat(v pretty.Value) (counters []poolCounter, prefix string, ok bool) {
	if v.Kind == "ptr" {
		if len(v.Items) != 1 {
			return nil, "", false
		}
		v, prefix = v.Items[0], "*"
	}
	if v.Kind != "struct" {
		return nil, "", false
	}
	counters = sortPoolCounters(poolFields(v, 0))
	if len(counters) == 0 {
		return nil, "", false
	}
	return counters, prefix, true
}

// poolFields collects the scalar leaves of a Stat, at whatever depth they sit.
//
// pgxpool.Stat keeps three counters of its own and reaches the other eight
// through a pointer to puddle.Stat, so a walk is what makes one table out of
// two structs. Only leaves are kept: the pointer field itself holds no counter,
// and its name — `s` — is the one name in the value that says nothing.
func poolFields(v pretty.Value, depth int) []poolCounter {
	if depth >= maxPoolDepth {
		return nil
	}
	var out []poolCounter
	for _, f := range v.Fields {
		switch inner := f.Val; {
		case inner.Kind == "struct":
			out = append(out, poolFields(inner, depth+1)...)
		case inner.Kind == "ptr" && len(inner.Items) == 1:
			out = append(out, poolFields(inner.Items[0], depth+1)...)
		case inner.Kind == "scalar" && inner.Repr != "":
			out = append(out, poolCounter{f.Name, inner.Repr})
		}
	}
	return out
}

// poolLabel is the counter's label, or its own name when gluon has not heard of
// it. Ordering is by poolLabels, so the known counters keep their reading order
// wherever a version puts them in the struct.
func poolLabel(field string) string {
	for _, l := range poolLabels {
		if l.field == field {
			return l.label
		}
	}
	return field
}

// sortPoolCounters puts the recognised counters in poolLabels' order and leaves
// the rest, in the order the struct declared them, after — so a field a new pgx
// adds appears rather than being dropped, and appears somewhere predictable.
func sortPoolCounters(in []poolCounter) []poolCounter {
	rank := make(map[string]int, len(poolLabels))
	for i, l := range poolLabels {
		rank[l.field] = i
	}
	known := make([]poolCounter, 0, len(in))
	unknown := make([]poolCounter, 0, len(in))
	for _, c := range in {
		if _, ok := rank[c.name]; ok {
			known = append(known, c)
		} else {
			unknown = append(unknown, c)
		}
	}
	for i := 1; i < len(known); i++ {
		for j := i; j > 0 && rank[known[j].name] < rank[known[j-1].name]; j-- {
			known[j], known[j-1] = known[j-1], known[j]
		}
	}
	return append(known, unknown...)
}
