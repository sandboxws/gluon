// Package stdlib holds the plugins for the standard library. They are always
// active, because the standard library is always there — which also makes them
// the working example of what a plugin is.
package stdlib

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/sandboxws/gluon/internal/cmdspec"
	"github.com/sandboxws/gluon/internal/plugin"
	"github.com/sandboxws/gluon/internal/pretty"
)

// Time is the plugin for the time package.
//
// It is also where the shape of the whole plugin model shows: a renderer sees
// the *structure* the child described, never the value, so it can add the
// nanosecond count to a Duration — which is a scalar whose Repr is already its
// String() — but it cannot read a time.Time, whose meaning lives entirely in a
// String() method the struct encoder walks straight past:
//
//	gluon> time.Date(2026, 8, 28, 10, 0, 0, 0, time.UTC)
//	(time.Time) {wall:0 ext:63923508000 loc:nil}
//
// Decoding wall/ext here would mean reimplementing time's internals against a
// representation that is explicitly not part of its API — the exact class of
// mistake this project rejected an interpreter to avoid. So the answer to
// time.Time is a command: :when rewrites to source the child runs, where the
// real value still exists and String() is just a call.
type Time struct{}

func (Time) Meta() plugin.Meta {
	return plugin.Meta{
		Name:    "time",
		Summary: "durations with their nanosecond count, and :when for a time.Time",
	}
}

func (Time) Imports() []plugin.Import {
	return []plugin.Import{{Name: "time", Path: "time"}}
}

func (Time) Commands() []plugin.Command {
	return []plugin.Command{{
		Name: ":when",
		Arg:  "<exp>",
		Usage: cmdspec.Spec{
			Kind: cmdspec.GoExpr,
			Examples: []cmdspec.Example{
				{Line: ":when time.Now()", Says: "RFC 3339, your local time, the zone and Unix seconds"},
			},
			See: []string{":t"},
		},
		Summary: "a time.Time in the forms you actually want to read",
		Detail: "The printed form of a time.Time is {wall:… ext:… loc:…}, because the value\n" +
			"printer reports structure and time.Time keeps its meaning in String().\n" +
			":when asks the child instead, where the value is still real.",
		Rewrite: func(arg string) (string, error) {
			if strings.TrimSpace(arg) == "" {
				return "", fmt.Errorf("usage: :when <expression>   e.g. :when time.Now()")
			}
			// Bound to a local first so the expression is evaluated once: an
			// argument like time.Now() would otherwise differ between fields.
			return "func() any { __t := " + arg + "; return struct {\n" +
				"\tRFC3339 string\n\tLocal   string\n\tZone    string\n\tUnix    int64\n" +
				"}{__t.Format(time.RFC3339Nano), __t.Local().Format(\"2006-01-02 15:04:05\"), __t.Format(\"MST-07:00\"), __t.Unix()} }()", nil
		},
	}}
}

func (Time) Renders() []plugin.Render {
	return []plugin.Render{{
		Type: "time.Duration",
		Rich: func(v pretty.Value, st pretty.Styles) (string, bool) {
			// The child already rendered String(), which is the readable half.
			// What it hides is the total: "1h30m0s" is ninety minutes and
			// "2h15m0.000001s" is not "2h15m0s", and neither reads as a number
			// you can compare.
			ns, ok := durationNanos(v.Repr)
			if !ok {
				return "", false
			}
			total := totalOf(ns)
			if total == "" || total == v.Repr {
				// Nothing to add. Declining is how a hook says so, and the
				// value falls through to gluon's own rendering unchanged.
				return "", false
			}
			return st.Type.Render("(time.Duration)") + " " + v.Repr + "  " +
				st.Annot.Render("= "+total), true
		},
		Inline: func(v pretty.Value, st pretty.Styles) (string, bool) {
			ns, ok := durationNanos(v.Repr)
			if !ok {
				return "", false
			}
			total := totalOf(ns)
			if total == "" || total == v.Repr {
				return "", false
			}
			// No type prefix: the column heading already said which field this
			// is, and the table has no room to repeat it.
			return v.Repr + st.Annot.Render("  = "+total), true
		},
	}}
}

// durationNanos parses Go's own duration form back to nanoseconds. It is
// deliberately a parse of the string the child sent rather than a second
// encoding of the value: there is only one number here, and the child already
// formatted it the way the language does.
func durationNanos(s string) (int64, bool) {
	if s == "0s" {
		return 0, true
	}
	neg := strings.HasPrefix(s, "-")
	s = strings.TrimPrefix(s, "-")

	units := []struct {
		suffix string
		scale  float64
	}{
		{"ns", 1}, {"µs", 1e3}, {"us", 1e3}, {"ms", 1e6},
		{"h", 3.6e12}, {"m", 6e10}, {"s", 1e9},
	}

	var total float64
	for s != "" {
		// Take the number, then the longest unit that follows it.
		i := 0
		for i < len(s) && (s[i] == '.' || (s[i] >= '0' && s[i] <= '9')) {
			i++
		}
		if i == 0 {
			return 0, false
		}
		n, err := strconv.ParseFloat(s[:i], 64)
		if err != nil {
			return 0, false
		}
		rest := s[i:]
		matched := false
		for _, u := range units {
			if strings.HasPrefix(rest, u.suffix) {
				// "m" must not swallow the "m" of "ms"; the table is ordered so
				// the two-character units are tried first.
				total += n * u.scale
				s = rest[len(u.suffix):]
				matched = true
				break
			}
		}
		if !matched {
			return 0, false
		}
	}
	if neg {
		total = -total
	}
	return int64(total), true
}

// totalOf is the duration as one number rather than a carry chain.
//
// Which unit depends on the scale, because the useful comparison does. Below a
// second, String() has already broken it into µs or ms and the total in
// nanoseconds is the exact figure; at or above one, minutes and hours are what
// String() spends its characters on, and seconds is the number nobody wants to
// do the arithmetic for.
func totalOf(ns int64) string {
	if ns == 0 {
		return "" // "0s = 0ns" is not information
	}
	abs := ns
	if abs < 0 {
		abs = -abs
	}
	if abs < 1e9 {
		return commas(ns) + "ns"
	}
	secs := float64(ns) / 1e9
	return strings.TrimRight(strings.TrimRight(strconv.FormatFloat(secs, 'f', 9, 64), "0"), ".") + "s"
}

// commas groups digits, because the whole point of showing the raw count is
// that its magnitude is readable at a glance.
func commas(n int64) string {
	s := strconv.FormatInt(n, 10)
	sign := ""
	if strings.HasPrefix(s, "-") {
		sign, s = "-", s[1:]
	}
	var parts []string
	for len(s) > 3 {
		parts = append([]string{s[len(s)-3:]}, parts...)
		s = s[:len(s)-3]
	}
	parts = append([]string{s}, parts...)
	return sign + strings.Join(parts, ",")
}
