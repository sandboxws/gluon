// Package web holds the plugins for HTTP routers and CLI frameworks — the
// libraries whose central object is a tree that prints as a struct.
package web

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/sandboxws/gluon/internal/cmdspec"
	"github.com/sandboxws/gluon/internal/plugin"
)

// Each router answers "what routes are registered" through its own API, so
// each gets its own plugin, and every one of them wants the command name
// :routes. Only one is normally in a build list; if two ever are, the first
// active registration keeps the name and :plugins reports the others.
//
// What they share beyond the name is the table. The column set is fixed here
// and each framework is fitted to it, rather than each reporting whatever its
// API happens to hand over. Letting every plugin report what it has is what
// produces seven tables that look alike and cannot be compared — and one
// command name that means seven things is worse than seven names.

// routeFormat is the one route table shape: method, path, handler name.
//
// The widths are constants rather than measured because the rows are built in
// the child, one at a time, by generated source that never holds the whole
// table. Measuring would mean a second pass and a second definition of the
// format, and the format is the only thing keeping the frameworks comparable.
const routeFormat = "%-7s %-30s %s"

// routeUnknown is a column the answering framework cannot supply.
//
// Deliberately not blank. An empty handler cell reads as "this route has no
// handler", which is never true — it has one; chi, fiber and gorilla/mux just
// hand over the value rather than a name. This is the distinction
// internal/db/detect.go draws between "found nothing" and "did not look",
// applied to a cell.
const routeUnknown = "—"

// routeHeader names the columns, so routeUnknown reads as a statement about
// the framework rather than about the route.
var routeHeader = fmt.Sprintf(routeFormat, "METHOD", "PATH", "HANDLER")

// routeCol and routeRow are gluon's copy of what the generated source does,
// which is what makes the table shape testable without a toolchain and seven
// third-party modules. Both sides format through routeFormat and substitute
// routeUnknown, so the two cannot drift on a width or on the marker.
func routeCol(v string) string {
	if v == "" {
		return routeUnknown
	}
	return v
}

func routeRow(method, path, handler string) string {
	return fmt.Sprintf(routeFormat, routeCol(method), routeCol(path), routeCol(handler))
}

// routesExpr wraps a framework's row-collecting statements in the shape every
// :routes shares: __col and __row for the common table, __out to accumulate
// into, and one ending — the sorted rows under the header, or a sentence
// saying there are none. Without that last branch an application with no
// routes and a header over nothing are the same output, and only one of them
// is a fact about the application.
//
// body appends to __out through __row and ends with a semicolon.
func routesExpr(body string) string {
	return "func() string { var __out []string; " +
		"__col := func(__v string) string { if __v == \"\" { return " +
		strconv.Quote(routeUnknown) + " }; return __v }; " +
		"__row := func(__m, __p, __h string) string { return fmt.Sprintf(" +
		strconv.Quote(routeFormat) + ", __col(__m), __col(__p), __col(__h)) }; " +
		body +
		"if len(__out) == 0 { return \"no routes registered\" }; " +
		"sort.Strings(__out); " +
		"return " + strconv.Quote(routeHeader) + " + \"\\n\" + strings.Join(__out, \"\\n\") }()"
}

// routesFromSlice is the body for the five frameworks whose route table is a
// slice of records: range it, take three fields, done. It exists so that the
// next framework of that shape is a call rather than another copy of the loop.
//
// The field arguments are expressions in the child, so a framework that
// records no handler name passes `""` and gets routeUnknown from __col.
func routesFromSlice(arg, call, method, path, handler string) string {
	return "for _, __r := range " + arg + "." + call + " { " +
		"__out = append(__out, __row(" + method + ", " + path + ", " + handler + ")) }; "
}

func routesCommand(detail, example string, rewrite func(string) (string, error)) plugin.Command {
	return plugin.Command{
		Name: ":routes",
		Arg:  "<router>",
		Usage: cmdspec.Spec{
			Kind:   cmdspec.GoExpr,
			Params: []cmdspec.Param{{Name: "router", Help: "the router value your code builds"}},
			Examples: []cmdspec.Example{
				{Line: ":routes " + example, Says: "every route registered on it: method, path and handler"},
			},
			See: []string{":http"},
		},
		Text:    true,
		Summary: "every route registered on the router",
		Detail:  detail,
		Rewrite: rewrite,
	}
}

// needsArg is the usage line. Every framework's is the same sentence with its
// own conventional variable name, because the argument is the only part of
// :routes that differs at the prompt.
func needsArg(example string) error {
	return fmt.Errorf("usage: %s <router>   e.g. %s %s", ":routes", ":routes", example)
}

// Chi is the plugin for github.com/go-chi/chi.
type Chi struct{}

func (Chi) Meta() plugin.Meta {
	return plugin.Meta{
		Name:    "chi",
		Module:  "github.com/go-chi/chi",
		Summary: ":routes walks a chi mux, including everything mounted under it",
	}
}

func (Chi) Imports() []plugin.Import {
	return []plugin.Import{{Name: "chi", Path: "github.com/go-chi/chi/v5"}}
}

func (Chi) Aliases() []plugin.Alias {
	return []plugin.Alias{{Name: "chi", Module: "github.com/go-chi/chi/v5"}}
}

func (Chi) Commands() []plugin.Command {
	return []plugin.Command{routesCommand(
		"chi.Walk descends into mounted sub-routers, so this is the whole tree\n"+
			"rather than the routes registered directly on the mux you named.\n"+
			"The walk hands over the http.Handler itself and never its name, so the\n"+
			"handler column is unavailable rather than blank.",
		"r", // the router, as its own docs name it
		func(arg string) (string, error) {
			if strings.TrimSpace(arg) == "" {
				return "", needsArg("r")
			}
			return routesExpr(
				"__err := chi.Walk(" + arg + ", func(__m string, __p string, " +
					"__h http.Handler, __mw ...func(http.Handler) http.Handler) error { " +
					"__out = append(__out, __row(__m, __p, \"\")); return nil }); " +
					"if __err != nil { return \"walk failed: \" + __err.Error() }; "), nil
		},
	)}
}

// Gin is the plugin for github.com/gin-gonic/gin.
type Gin struct{}

func (Gin) Meta() plugin.Meta {
	return plugin.Meta{
		Name:    "gin",
		Module:  "github.com/gin-gonic/gin",
		Summary: ":routes lists a gin engine's routes with their handlers",
	}
}

func (Gin) Imports() []plugin.Import {
	return []plugin.Import{{Name: "gin", Path: "github.com/gin-gonic/gin"}}
}

func (Gin) Aliases() []plugin.Alias {
	return []plugin.Alias{{Name: "gin", Module: "github.com/gin-gonic/gin"}}
}

func (Gin) Commands() []plugin.Command {
	return []plugin.Command{routesCommand(
		"gin records the handler's function name at registration, which is the\n"+
			"column a route table usually cannot fill.",
		"r", // the router, as its own docs name it
		func(arg string) (string, error) {
			if strings.TrimSpace(arg) == "" {
				return "", needsArg("r")
			}
			return routesExpr(routesFromSlice(
				arg, "Routes()", "__r.Method", "__r.Path", "__r.Handler")), nil
		},
	)}
}

// Echo is the plugin for github.com/labstack/echo.
type Echo struct{}

func (Echo) Meta() plugin.Meta {
	return plugin.Meta{
		Name:    "echo",
		Module:  "github.com/labstack/echo",
		Summary: ":routes lists an echo instance's routes with their handlers",
	}
}

func (Echo) Imports() []plugin.Import {
	return []plugin.Import{{Name: "echo", Path: "github.com/labstack/echo/v4"}}
}

func (Echo) Aliases() []plugin.Alias {
	return []plugin.Alias{{Name: "echo", Module: "github.com/labstack/echo/v4"}}
}

func (Echo) Commands() []plugin.Command {
	return []plugin.Command{routesCommand(
		"echo's Routes() reports the handler name it captured at registration,\n"+
			"which fills the handler column.",
		"e", // the router, as its own docs name it
		func(arg string) (string, error) {
			if strings.TrimSpace(arg) == "" {
				return "", needsArg("e")
			}
			return routesExpr(routesFromSlice(
				arg, "Routes()", "__r.Method", "__r.Path", "__r.Name")), nil
		},
	)}
}

// Fiber is the plugin for github.com/gofiber/fiber.
type Fiber struct{}

func (Fiber) Meta() plugin.Meta {
	return plugin.Meta{
		Name:    "fiber",
		Module:  "github.com/gofiber/fiber",
		Summary: ":routes lists a fiber app's routes, HEAD pairs included",
	}
}

func (Fiber) Imports() []plugin.Import {
	return []plugin.Import{{Name: "fiber", Path: "github.com/gofiber/fiber/v2"}}
}

func (Fiber) Aliases() []plugin.Alias {
	return []plugin.Alias{{Name: "fiber", Module: "github.com/gofiber/fiber/v2"}}
}

func (Fiber) Commands() []plugin.Command {
	return []plugin.Command{routesCommand(
		"fiber registers a HEAD alongside every GET, so the table has rows the\n"+
			"application did not write — they are real routes and are shown.\n"+
			"Route.Name is the route's own name, not the handler's, so the handler\n"+
			"column is unavailable rather than filled with a near-miss.",
		"app", // the router, as its own docs name it
		func(arg string) (string, error) {
			if strings.TrimSpace(arg) == "" {
				return "", needsArg("app")
			}
			return routesExpr(routesFromSlice(
				arg, "GetRoutes()", "__r.Method", "__r.Path", `""`)), nil
		},
	)}
}

// Hertz is the plugin for github.com/cloudwego/hertz.
type Hertz struct{}

func (Hertz) Meta() plugin.Meta {
	return plugin.Meta{
		Name:    "hertz",
		Module:  "github.com/cloudwego/hertz",
		Summary: ":routes lists a hertz engine's routes with their handlers",
	}
}

func (Hertz) Imports() []plugin.Import {
	return []plugin.Import{
		{Name: "server", Path: "github.com/cloudwego/hertz/pkg/app/server"},
		{Name: "app", Path: "github.com/cloudwego/hertz/pkg/app"},
	}
}

func (Hertz) Aliases() []plugin.Alias {
	return []plugin.Alias{{Name: "hertz", Module: "github.com/cloudwego/hertz"}}
}

func (Hertz) Commands() []plugin.Command {
	return []plugin.Command{routesCommand(
		"hertz's RouteInfo carries the handler name, so all three columns are\n"+
			"filled. *server.Hertz embeds *route.Engine, so this takes the value the\n"+
			"application actually holds.",
		"h", // the router, as its own docs name it
		func(arg string) (string, error) {
			if strings.TrimSpace(arg) == "" {
				return "", needsArg("h")
			}
			return routesExpr(routesFromSlice(
				arg, "Routes()", "__r.Method", "__r.Path", "__r.Handler")), nil
		},
	)}
}

// Iris is the plugin for github.com/kataras/iris.
type Iris struct{}

func (Iris) Meta() plugin.Meta {
	return plugin.Meta{
		Name:    "iris",
		Module:  "github.com/kataras/iris",
		Summary: ":routes lists an iris application's routes with their handlers",
	}
}

func (Iris) Imports() []plugin.Import {
	return []plugin.Import{{Name: "iris", Path: "github.com/kataras/iris/v12"}}
}

func (Iris) Aliases() []plugin.Alias {
	return []plugin.Alias{{Name: "iris", Module: "github.com/kataras/iris/v12"}}
}

func (Iris) Commands() []plugin.Command {
	return []plugin.Command{routesCommand(
		"iris records MainHandlerName per route, which fills the handler column.\n"+
			"The paths are the router's own form — {id} registered reads back as :id.\n"+
			"GetRoutes() is populated at registration, so this works before Build().",
		"app", // the router, as its own docs name it
		func(arg string) (string, error) {
			if strings.TrimSpace(arg) == "" {
				return "", needsArg("app")
			}
			return routesExpr(routesFromSlice(
				arg, "GetRoutes()", "__r.Method", "__r.Path", "__r.MainHandlerName")), nil
		},
	)}
}

// Mux is the plugin for github.com/gorilla/mux.
type Mux struct{}

func (Mux) Meta() plugin.Meta {
	return plugin.Meta{
		Name:    "mux",
		Module:  "github.com/gorilla/mux",
		Summary: ":routes walks a gorilla/mux router, subrouters included",
	}
}

func (Mux) Imports() []plugin.Import {
	return []plugin.Import{{Name: "mux", Path: "github.com/gorilla/mux"}}
}

func (Mux) Aliases() []plugin.Alias {
	return []plugin.Alias{{Name: "mux", Module: "github.com/gorilla/mux"}}
}

func (Mux) Commands() []plugin.Command {
	return []plugin.Command{routesCommand(
		"Walk descends into subrouters, and a PathPrefix subrouter is itself a\n"+
			"route, so its prefix appears as a row — it is one, and it matches.\n"+
			"A route with no method restriction is ANY rather than blank; a route\n"+
			"matched by something other than a path has no template to report.\n"+
			"mux hands over the http.Handler and never its name, so the handler\n"+
			"column is unavailable.",
		"r", // the router, as its own docs name it
		func(arg string) (string, error) {
			if strings.TrimSpace(arg) == "" {
				return "", needsArg("r")
			}
			// Walk takes a callback and returns an error, so this is the one
			// route table that cannot be a one-line expression — the second
			// case, after gorm's :sql, that a TOML rewrite template could not
			// express. That boundary is why plugin commands are Go.
			return routesExpr(
				"__err := " + arg + ".Walk(func(__rt *mux.Route, __rr *mux.Router, " +
					"__an []*mux.Route) error { " +
					"__p, __perr := __rt.GetPathTemplate(); if __perr != nil { __p = \"\" }; " +
					"__ms, __merr := __rt.GetMethods(); " +
					"if __merr != nil || len(__ms) == 0 { __ms = []string{\"ANY\"} }; " +
					"for _, __m := range __ms { __out = append(__out, __row(__m, __p, \"\")) }; " +
					"return nil }); " +
					"if __err != nil { return \"walk failed: \" + __err.Error() }; "), nil
		},
	)}
}
