package ids

import (
	"fmt"
	"strings"

	"github.com/sandboxws/gluon/internal/cmdspec"
	"github.com/sandboxws/gluon/internal/plugin"
)

// Decimal is the plugin for github.com/shopspring/decimal.
//
// It contributes a command rather than a renderer, and the reason is the same
// one that governs time.Time: decimal.Decimal is a *big.Int and an exponent,
// both unexported, and a big.Int is itself a sign and a slice of machine words.
// Reassembling a decimal from that here would be reimplementing arbitrary
// precision arithmetic against a representation that is not part of the API.
// The child can just call String().
type Decimal struct{}

func (Decimal) Meta() plugin.Meta {
	return plugin.Meta{
		Name:    "decimal",
		Module:  "github.com/shopspring/decimal",
		Summary: ":dec reads a decimal, which prints as a big.Int and an exponent",
	}
}

func (Decimal) Imports() []plugin.Import {
	return []plugin.Import{{Name: "decimal", Path: "github.com/shopspring/decimal"}}
}

func (Decimal) Aliases() []plugin.Alias {
	return []plugin.Alias{{Name: "decimal", Module: "github.com/shopspring/decimal"}}
}

func (Decimal) Commands() []plugin.Command {
	return []plugin.Command{{
		Name: ":dec",
		Arg:  "<exp>",
		Usage: cmdspec.Spec{
			Kind: cmdspec.GoExpr,
			Examples: []cmdspec.Example{
				{Line: ":dec decimal.NewFromFloat(0.1)", Says: "the exact decimal, and the float64 it would have been"},
			},
			See: []string{":t"},
		},
		Text:    true,
		Summary: "a decimal's exact value, and what float64 would have done to it",
		Detail: "Prints the exact decimal alongside the float64 round-trip, because the\n" +
			"reason to reach for this package at all is that the two differ.",
		Rewrite: func(arg string) (string, error) {
			if strings.TrimSpace(arg) == "" {
				return "", fmt.Errorf("usage: :dec <expression>   e.g. :dec decimal.NewFromFloat(0.1)")
			}
			return "func() string { __d := " + arg + "; __f, _ := __d.Float64(); " +
				"return __d.String() + \"   exponent \" + strconv.Itoa(int(__d.Exponent())) + " +
				"\"\\nfloat64  \" + strconv.FormatFloat(__f, 'g', -1, 64) }()", nil
		},
	}}
}
