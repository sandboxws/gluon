package rpc

import (
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/sandboxws/gluon/internal/plugin"
	"github.com/sandboxws/gluon/internal/pretty"
)

// The wire between the child and gluon.
//
// The child describes; gluon formats — the same split :query and :http run on,
// and the reason the listing below states facts rather than inferring them. The
// child holds the library, so it is the only thing that can speak reflection;
// the layout is gluon's, so it does not have to be generated into Go.
const (
	sep = "\x1f"

	tagErr       = "err"       // the whole thing failed, with the cause
	tagNoReflect = "noreflect" // the server answered, without a reflection service
	tagMethod    = "mth"       // service, method, streaming shape, input, output
	tagStatus    = "status"    // code, its name, the message
	tagBadReq    = "badreq"    // the request did not match the descriptor
	tagUnknown   = "unknown"   // a kind ("service" or "method") and the name
	tagStream    = "stream"    // the named method is not unary, with its shape
)

// Source-literal forms of the markers, for the reason internal/db and :http
// quote their own: \x1f has to reach the generated program as an escape
// sequence. A raw control byte compiles, and is invisible in the :src that
// gofmt just ran over.
var (
	litErr       = strconv.Quote(tagErr + sep)
	litNoReflect = strconv.Quote(tagNoReflect + sep)
	litMethod    = strconv.Quote(tagMethod + sep)
	litStatus    = strconv.Quote(tagStatus + sep)
	litBadReq    = strconv.Quote(tagBadReq + sep)
	litUnknown   = strconv.Quote(tagUnknown + sep)
	litStream    = strconv.Quote(tagStream + sep)
	litSep       = strconv.Quote(sep)
)

// The streaming shapes, as the child spells them. A method is callable from one
// evaluation exactly when it is unary, which is the whole distinction the
// listing exists to draw.
const (
	kindUnary  = "u"
	kindClient = "cs"
	kindServer = "ss"
	kindBidi   = "bs"
)

// method is one row of a listing.
type method struct {
	Service, Name, Kind, In, Out string
}

// callable reports whether one evaluation can serve it.
func (m method) callable() bool { return m.Kind == kindUnary }

// shape is how the listing names what the method is.
func (m method) shape() string {
	switch m.Kind {
	case kindUnary:
		return "unary"
	case kindClient:
		return "client stream"
	case kindServer:
		return "server stream"
	case kindBidi:
		return "bidi stream"
	}
	return m.Kind
}

// report is what the child said.
type report struct {
	Err       string
	NoReflect string
	Methods   []method
	// Status is an error the *server* returned, which is a different thing
	// from the call failing: the call worked and the answer was a refusal.
	StatusCode string
	StatusName string
	StatusMsg  string
	BadRequest string
	// Unknown is what was not found, and UnknownKind says whether that was a
	// service or a method on one. Without the kind the two collapse into a
	// message that is wrong for whichever case it was not written for.
	Unknown     string
	UnknownKind string
	Stream      string
}

// parseReport reads back what the generated program printed.
func parseReport(out string) report {
	var r report
	for _, line := range strings.Split(out, "\n") {
		tag, rest, ok := strings.Cut(line, sep)
		if !ok {
			continue
		}
		switch tag {
		case tagErr:
			r.Err = rest
		case tagNoReflect:
			r.NoReflect = rest
		case tagMethod:
			f := strings.Split(rest, sep)
			if len(f) == 5 {
				r.Methods = append(r.Methods, method{f[0], f[1], f[2], f[3], f[4]})
			}
		case tagStatus:
			f := strings.Split(rest, sep)
			if len(f) == 3 {
				r.StatusCode, r.StatusName, r.StatusMsg = f[0], f[1], f[2]
			}
		case tagBadReq:
			r.BadRequest = rest
		case tagUnknown:
			if kind, name, ok := strings.Cut(rest, sep); ok {
				r.UnknownKind, r.Unknown = kind, name
			}
		case tagStream:
			r.Stream = rest
		}
	}
	return r
}

// reportList is the answer to `:grpc <target>`.
//
// Reflection being unavailable is reported as its own sentence rather than as
// an empty list. They are the difference between "the server exposes nothing"
// and "the server would not say", which is the distinction
// internal/db/detect.go draws between found nothing and did not look — and
// getting it wrong here sends somebody to look for a bug in a server that is
// working.
func reportList(target string) func(string, []pretty.Value) plugin.Answer {
	return func(out string, _ []pretty.Value) plugin.Answer {
		r := parseReport(out)
		switch {
		case r.Err != "":
			return plugin.Answer{
				Text:   "error: " + target + " could not be reached — " + r.Err,
				Failed: true,
			}
		case r.NoReflect != "":
			return plugin.Answer{
				Text: "error: " + target + " answered, but exposes no server reflection " +
					"service, so its method set cannot be discovered.\n" +
					"       This is not the same as exposing nothing. Register it with " +
					"reflection.Register(s)\n" +
					"       in the server, or use the generated client, which does not " +
					"need reflection.",
				Failed: true,
			}
		case len(r.Methods) == 0:
			return plugin.Answer{Text: target + " exposes reflection and no services"}
		}
		return plugin.Answer{Text: listing(target, r.Methods)}
	}
}

// listing lays out the services and their methods.
//
// Every method is marked with whether one evaluation can serve it, and the
// marked ones are not hidden. A method that is listed and then refused at the
// call is a surprise; a method listed *as* uncallable is a fact the reader has
// before they type anything.
func listing(target string, ms []method) string {
	byService := map[string][]method{}
	var order []string
	for _, m := range ms {
		if _, seen := byService[m.Service]; !seen {
			order = append(order, m.Service)
		}
		byService[m.Service] = append(byService[m.Service], m)
	}
	sort.Strings(order)

	// One width for every row under every service, so the shapes line up down
	// the whole listing rather than per service — a column that restarts is a
	// column you cannot scan.
	width := 0
	streams := 0
	for _, m := range ms {
		if n := len(m.Name); n > width {
			width = n
		}
		if !m.callable() {
			streams++
		}
	}

	var b strings.Builder
	fmt.Fprintf(&b, "%s — %d service", target, len(order))
	if len(order) != 1 {
		b.WriteString("s")
	}
	fmt.Fprintf(&b, ", %d method", len(ms))
	if len(ms) != 1 {
		b.WriteString("s")
	}
	b.WriteString("\n")

	for _, svc := range order {
		b.WriteString("\n  " + svc + "\n")
		rows := byService[svc]
		sort.Slice(rows, func(i, j int) bool { return rows[i].Name < rows[j].Name })
		for _, m := range rows {
			mark := " "
			if !m.callable() {
				mark = "·"
			}
			fmt.Fprintf(&b, "  %s %-*s  %-13s  %s → %s\n",
				mark, width, m.Name, m.shape(), short(m.In), short(m.Out))
		}
	}
	if streams > 0 {
		fmt.Fprintf(&b, "\n  · %d of these need a connection that outlives one evaluation, "+
			"so :grpc cannot call them.\n", streams)
		b.WriteString("    gluon compiles each line into a program that exits; a stream has " +
			"nowhere to live.\n")
	}
	return strings.TrimRight(b.String(), "\n")
}

// short drops the package from a message name.
//
// The package is the service's own in almost every case, so repeating it on
// both sides of every arrow is width spent on nothing. The full name is one
// `:grpc <target>` away when it is not.
func short(name string) string {
	if i := strings.LastIndex(name, "."); i >= 0 {
		return name[i+1:]
	}
	return name
}

// reportCall is the answer to `:grpc <target> <method>`.
//
// The value is the answer on the path where there is one: an empty Answer hands
// the response back to gluon's ordinary value rendering, so every renderer that
// applies to a value anywhere applies to a gRPC response too.
func reportCall(target, name string) func(string, []pretty.Value) plugin.Answer {
	return func(out string, _ []pretty.Value) plugin.Answer {
		r := parseReport(out)
		switch {
		case r.Err != "":
			return plugin.Answer{
				Text:   "error: " + target + " could not be reached — " + r.Err,
				Failed: true,
			}
		case r.NoReflect != "":
			return plugin.Answer{
				Text: "error: " + target + " exposes no server reflection service, so " +
					"gluon cannot discover\n       what " + name + " takes or returns.",
				Failed: true,
			}
		case r.BadRequest != "":
			// Named before the call, not after it. A server's own complaint
			// names its field paths, which is a slow way to find out about a
			// request you got wrong on your own machine.
			return plugin.Answer{
				Text:   "error: the request does not match " + name + " — " + r.BadRequest,
				Failed: true,
			}
		case r.Stream != "":
			// Stated at the call as well as at the listing, because a method
			// name can be typed without ever running the listing — and the
			// refusal has to say why rather than read as a defect.
			return plugin.Answer{
				Text: "error: " + name + " is a " + r.Stream + ", and :grpc calls unary " +
					"methods only.\n" +
					"       A stream needs a connection that outlives the evaluation, and " +
					"gluon compiles\n" +
					"       each line into a program that exits. Reading one message and " +
					"hanging up would\n" +
					"       return a number that looks like an answer and is not.",
				Failed: true,
			}
		case r.Unknown != "":
			return plugin.Answer{Text: unknownMethod(name, r), Failed: true}
		case r.StatusName != "":
			return plugin.Answer{
				Text: fmt.Sprintf("error: %s returned %s (%s) — %s",
					name, r.StatusCode, r.StatusName, r.StatusMsg),
				Failed: true,
			}
		}
		return plugin.Answer{}
	}
}

// unknownMethod says what is there instead.
//
// A bare "no such method" leaves the reader to run the listing themselves, and
// the listing is already in hand — the descriptor had to be fetched to find out
// the method was missing.
func unknownMethod(name string, r report) string {
	var b strings.Builder
	if r.UnknownKind == "method" {
		b.WriteString("error: " + r.Unknown + " exposes no " + shortMethod(name) + ".\n")
		b.WriteString("       It exposes:\n")
		rows := append([]method(nil), r.Methods...)
		sort.Slice(rows, func(i, j int) bool { return rows[i].Name < rows[j].Name })
		for _, m := range rows {
			mark := " "
			if !m.callable() {
				mark = "·"
			}
			fmt.Fprintf(&b, "       %s %s  (%s)\n", mark, m.Name, m.shape())
		}
		return strings.TrimRight(b.String(), "\n")
	}
	return "error: the server exposes no service named " + r.Unknown
}

// shortMethod is the method half of a Service/Method spec.
func shortMethod(name string) string {
	if _, m, ok := strings.Cut(name, "/"); ok {
		return m
	}
	return name
}
