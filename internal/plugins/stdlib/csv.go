package stdlib

import (
	"fmt"
	"strings"

	"github.com/sandboxws/gluon/internal/cmdspec"
	"github.com/sandboxws/gluon/internal/plugin"
)

// CSV is the plugin for encoding/csv.
//
// encoding/csv writes rows of strings and knows nothing about types, so the
// interesting half of ":csv x" is the part it does not provide: deciding what
// the records are, and what the columns are called. That decision is made in
// the child, by reflecting over the value, and not here by asking the type
// checker — the checker must never be an authority (invariant 5), and here it
// would be an authority on whether the command runs at all.
//
// The other decision is that a value with no table shape is refused rather than
// bent into one. A scalar could be a single cell and a map could be two
// columns; both are guesses, and a confident guess is worse than a refusal that
// names what the command accepts.
type CSV struct{}

func (CSV) Meta() plugin.Meta {
	return plugin.Meta{
		Name:    "csv",
		Summary: ":csv to see a sequence of records as the table it becomes",
	}
}

func (CSV) Imports() []plugin.Import {
	return []plugin.Import{
		{Name: "csv", Path: "encoding/csv"},
		{Name: "reflect", Path: "reflect"},
	}
}

func (CSV) Commands() []plugin.Command {
	return []plugin.Command{{
		Name: ":csv",
		Arg:  "<exp>",
		Usage: cmdspec.Spec{
			Kind: cmdspec.GoExpr,
			Examples: []cmdspec.Example{
				{Line: ":csv rows", Says: "one record per element, the header from the fields or their tags"},
			},
			See: []string{":json"},
		},
		Text:    true,
		Summary: "a sequence of records as CSV, header included",
		Detail: "Takes a slice of structs — one row each, with the header read from the\n" +
			"field names or from their `csv:\"...\"` tags — or a slice of slices, where\n" +
			"each element is already a row.\n\n" +
			"Anything else is refused by name rather than rendered as a guess: a CSV is\n" +
			"a table, and a scalar or a map is not one. The refusal names the shapes it\n" +
			"does take.\n\n" +
			"Quoting is encoding/csv's own, so a value containing a comma or a newline\n" +
			"comes out as the file would hold it.",
		Rewrite: func(arg string) (string, error) {
			if strings.TrimSpace(arg) == "" {
				return "", fmt.Errorf("usage: :csv <expression>   e.g. :csv users")
			}
			return strings.Replace(csvSource, "__EXPR", arg, 1), nil
		},
	}}
}

// csvSource reflects over the value in the child.
//
// It deliberately uses nothing newer than Go 1.13: when a host is attached the
// session builds against the host's own go.mod, whose `go` directive decides
// the language version, so strings.Cut and StructField.IsExported would turn a
// working command into a build error in an older project.
const csvSource = `func() string {
	__refuse := func(__what string) string {
		return "cannot render " + __what + " as CSV: a CSV is a table of records.\n" +
			":csv takes a slice of structs — one row each, header from the field names\n" +
			"or from their csv struct tags — or a slice of slices, each element a row."
	}
	__v := reflect.ValueOf(__EXPR)
	for __v.IsValid() && (__v.Kind() == reflect.Ptr || __v.Kind() == reflect.Interface) && !__v.IsNil() {
		__v = __v.Elem()
	}
	if !__v.IsValid() {
		return __refuse("nil")
	}
	if __v.Kind() != reflect.Slice && __v.Kind() != reflect.Array {
		return __refuse(__v.Type().String())
	}
	__rec := __v.Type().Elem()
	for __rec.Kind() == reflect.Ptr {
		__rec = __rec.Elem()
	}
	var __head []string
	var __cols []int
	switch __rec.Kind() {
	case reflect.Struct:
		for __i := 0; __i < __rec.NumField(); __i++ {
			__f := __rec.Field(__i)
			if __f.PkgPath != "" {
				continue
			}
			__name := __f.Name
			if __tag, __ok := __f.Tag.Lookup("csv"); __ok {
				__tag = strings.SplitN(__tag, ",", 2)[0]
				if __tag == "-" {
					continue
				}
				if __tag != "" {
					__name = __tag
				}
			}
			__head = append(__head, __name)
			__cols = append(__cols, __i)
		}
		if len(__cols) == 0 {
			return "cannot render " + __v.Type().String() +
				" as CSV: its record type has no exported field."
		}
	case reflect.Slice, reflect.Array:
	default:
		return __refuse(__v.Type().String())
	}
	var __b strings.Builder
	__w := csv.NewWriter(&__b)
	if len(__head) > 0 {
		__w.Write(__head)
	}
	for __i := 0; __i < __v.Len(); __i++ {
		__e := __v.Index(__i)
		for __e.IsValid() && (__e.Kind() == reflect.Ptr || __e.Kind() == reflect.Interface) && !__e.IsNil() {
			__e = __e.Elem()
		}
		__row := make([]string, 0, len(__cols))
		switch {
		case len(__cols) > 0 && __e.Kind() == reflect.Struct:
			for _, __c := range __cols {
				__row = append(__row, fmt.Sprint(__e.Field(__c).Interface()))
			}
		case len(__cols) > 0:
			__row = make([]string, len(__cols))
		case __e.Kind() == reflect.Slice || __e.Kind() == reflect.Array:
			for __j := 0; __j < __e.Len(); __j++ {
				__row = append(__row, fmt.Sprint(__e.Index(__j).Interface()))
			}
		}
		__w.Write(__row)
	}
	__w.Flush()
	__out := strings.TrimRight(__b.String(), "\n")
	if __v.Len() > 0 {
		return __out
	}
	if __out == "" {
		return "no records"
	}
	return __out + "\nno records"
}()`
