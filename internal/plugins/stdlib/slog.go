package stdlib

import (
	"fmt"
	"strings"

	"github.com/sandboxws/gluon/internal/cmdspec"
	"github.com/sandboxws/gluon/internal/plugin"
)

// Slog is the plugin for log/slog.
//
// It contributes no renderer on purpose. slog's types keep their meaning behind
// methods — slog.Value is a struct of an interface, a uint64 and a pointer —
// and a renderer only ever sees the structure the child described. Decoding
// that here would be guessing at an internal representation, so the answer is a
// command that asks the child, where the value is still real.
type Slog struct{}

func (Slog) Meta() plugin.Meta {
	return plugin.Meta{
		Name:    "slog",
		Summary: ":slog shows how a value comes out of a structured logger",
	}
}

func (Slog) Imports() []plugin.Import {
	return []plugin.Import{{Name: "slog", Path: "log/slog"}}
}

func (Slog) Commands() []plugin.Command {
	return []plugin.Command{{
		Name: ":slog",
		Arg:  "<exp>",
		Usage: cmdspec.Spec{
			Kind: cmdspec.GoExpr,
			Examples: []cmdspec.Example{
				{Line: ":slog req", Says: "the key=value form a TextHandler writes for it"},
			},
			See: []string{":json"},
		},
		Text:    true,
		Summary: "how the value appears in a structured log line",
		Detail: "Runs the value through a real slog.TextHandler. What a type looks like in\n" +
			"logs is a question about its LogValuer and its String method, not about its\n" +
			"fields, so the printed form cannot answer it.",
		Rewrite: func(arg string) (string, error) {
			if strings.TrimSpace(arg) == "" {
				return "", fmt.Errorf("usage: :slog <expression>   e.g. :slog time.Now()")
			}
			// The time is removed so the line is stable: a REPL that printed a
			// different answer every call would be unreadable, and the
			// timestamp is not what is being asked about.
			return "func() string { var __b strings.Builder; " +
				"__h := slog.NewTextHandler(&__b, &slog.HandlerOptions{ReplaceAttr: " +
				"func(_ []string, __a slog.Attr) slog.Attr { if __a.Key == \"time\" { return slog.Attr{} }; return __a }}); " +
				"slog.New(__h).Info(\"value\", \"v\", " + arg + "); " +
				"return strings.TrimRight(__b.String(), \"\\n\") }()", nil
		},
	}}
}
