package pretty

import (
	"fmt"
	"go/ast"
	"go/parser"
	"regexp"
	"strconv"
	"strings"
)

// This half of the file used to live in internal/inspect, beside :test, which
// was its only caller. It moved when the `go` form arrived: a Go literal is a
// way of drawing a Value, and this package's doc says every formatting
// choice lives here. The move also cost a dependency edge rather than adding
// one — nothing here ever needed go/types or a *Target, and internal/inspect
// imports this package, so the form could not have reached a renderer from
// there at all.
//
// What stayed behind is Comparable and TypeName, which do need go/types.

// Turning an observed value back into a Go literal is what separates a test
// gluon can generate from one it must refuse.
//
// The child already described the value structurally, so the walk below reads
// that description rather than reflecting again: a Value carries the
// type, the kind and — for a composite — its parts. What it cannot carry is
// identity. The value printer tells the truth about that (ROADMAP.md §v2.5):
// a pointer is an identity, not contents, and ids are sequence numbers rather
// than addresses. So a pointer, a channel, a func and a back-reference have no
// literal form at all, and the honest answer is to say which part of the value
// that was — not to emit `/* TODO */` and let a test that cannot pass look
// like one that was generated.

// NoLiteral names the component of a value that has no Go literal form.
type NoLiteral struct {
	// Path is the component's position inside the value: "" for the value
	// itself, then ".Field", "[0]", "[\"key\"]" as the walk descends.
	Path string
	// Why is the reason, phrased so it can be printed after the path.
	Why string
}

func (e *NoLiteral) Error() string {
	if e.Path == "" {
		return e.Why
	}
	return e.Path + " " + e.Why
}

// Literal renders an observed value as Go source, or reports the first
// component that cannot be written as one.
//
// The source is self-typed: a composite carries its own type, and a scalar
// whose type is not the one an untyped constant would default to is wrapped in
// a conversion. That is what lets the result be dropped into `want := …` and
// compared against the expression's own value without a mismatch.
func Literal(v Value) (string, error) {
	return literal(v, "")
}

func literal(v Value, path string) (string, error) {
	no := func(why string) (string, error) { return "", &NoLiteral{Path: path, Why: why} }

	switch v.Kind {
	// Both before the type test below, which would otherwise answer "pointer"
	// for a back-reference — true, and not the fact worth reporting.
	case "cycle":
		return no("refers back into the value it is part of, which no literal can express")
	case "shared":
		return no("is the same object as one already in this value — a literal would make two")
	}

	// Decided before the kind, because the kind of a pointer to a struct is
	// "ptr" and the kind of a pointer to anything else is "scalar": the type
	// is the one place both are the same fact.
	if why, ok := unwritable(v.Type); ok && v.Kind != "nil" {
		return no(why)
	}

	switch v.Kind {
	case "nil":
		if v.Type == "" {
			// An interface that arrived nil has had its type erased by the
			// conversion to any, so there is nothing to convert to.
			return "nil", nil
		}
		return "(" + GoTypeName(v.Type) + ")(nil)", nil

	case "string":
		return strconv.Quote(v.Repr), nil

	case "scalar":
		return scalarLiteral(v, path)

	case "list":
		if v.Items == nil {
			return no("is nested deeper than the value printer describes")
		}
		if v.More > 0 {
			return no(fmt.Sprintf("holds %d more items than were sent — the encoder caps a collection, so a literal built from it would assert a prefix", v.More))
		}
		parts := make([]string, 0, len(v.Items))
		for i, it := range v.Items {
			s, err := literal(it, path+"["+strconv.Itoa(i)+"]")
			if err != nil {
				return "", err
			}
			parts = append(parts, s)
		}
		return GoTypeName(v.Type) + "{" + strings.Join(parts, ", ") + "}", nil

	case "map":
		if v.Keys == nil {
			return no("is nested deeper than the value printer describes")
		}
		if v.More > 0 {
			return no(fmt.Sprintf("holds %d more keys than were sent — the encoder caps a collection, so a literal built from it would assert a prefix", v.More))
		}
		parts := make([]string, 0, len(v.Keys))
		for i, k := range v.Keys {
			// The key is walked first so the value's path can name it the way
			// the source will read — .Ports["ssh"] rather than .Ports[0].
			ks, err := literal(k, path+"["+strconv.Itoa(i)+"]")
			if err != nil {
				return "", err
			}
			vs, err := literal(v.Items[i], path+"["+ks+"]")
			if err != nil {
				return "", err
			}
			parts = append(parts, ks+": "+vs)
		}
		return GoTypeName(v.Type) + "{" + strings.Join(parts, ", ") + "}", nil

	case "struct":
		if v.Fields == nil {
			return no("is nested deeper than the value printer describes")
		}
		foreign := strings.Contains(GoTypeName(v.Type), ".")
		parts := make([]string, 0, len(v.Fields))
		for _, f := range v.Fields {
			at := path + "." + f.Name
			if foreign && !ast.IsExported(f.Name) {
				return "", &NoLiteral{Path: at, Why: "is unexported — a composite literal cannot name it outside its own package"}
			}
			s, err := literal(f.Val, at)
			if err != nil {
				return "", err
			}
			parts = append(parts, f.Name+": "+s)
		}
		return GoTypeName(v.Type) + "{" + strings.Join(parts, ", ") + "}", nil

	case "ptr":
		return no("is a pointer — what was observed is an identity, and a literal would be a different object")
	}
	return no("has no literal form")
}

// unwritable reports the types whose observed value is an identity rather than
// contents. It reads the type rather than the rendering because the rendering
// of a pointer past the depth cap is an address, which is a perfectly valid
// integer literal and would otherwise pass every other check here.
func unwritable(t string) (string, bool) {
	switch {
	case strings.HasPrefix(t, "*"):
		return "is a pointer — what was observed is an identity, and a literal would be a different object", true
	case t == "unsafe.Pointer", t == "uintptr":
		return "is an address, which says nothing that would still be true on a second run", true
	case strings.HasPrefix(t, "chan "), strings.HasPrefix(t, "<-chan "), strings.HasPrefix(t, "chan<- "):
		return "is a channel — it has no literal form, and what it holds is not what it is", true
	case strings.HasPrefix(t, "func("):
		return "is a func — it has no literal form", true
	}
	return "", false
}

// byteRepr and runeRepr are the two shapes the encoder gives a number that is
// also a character: `195 (0xc3)` for a byte and `99 'c'` for a rune. Both lead
// with the number, and the rest is the gloss that makes the display readable —
// so the literal is the leading field.
var (
	byteRepr = regexp.MustCompile(`^(-?\d+) \(0x[0-9a-f]+\)`)
	runeRepr = regexp.MustCompile(`^(-?\d+) '`)
)

func scalarLiteral(v Value, path string) (string, error) {
	repr := v.Repr
	if m := byteRepr.FindStringSubmatch(repr); m != nil {
		repr = m[1]
	} else if m := runeRepr.FindStringSubmatch(repr); m != nil {
		repr = m[1]
	}

	// The last gate, and the one that catches everything the encoder writes
	// for a value it could not describe: `<unexported>`, `<unprintable: …>`,
	// the `%+v` of a struct past the node budget, and the +Inf and NaN a float
	// has no literal for. A constant expression is the only thing that gets
	// through, so anything naming an identifier is out — with true and false
	// spelled back in, because Go's parser reads those as identifiers too.
	if !isConstantExpr(repr) {
		return "", &NoLiteral{Path: path, Why: "was printed as " + strconv.Quote(v.Repr) + ", which is not a Go literal"}
	}
	if t := GoTypeName(v.Type); !defaultTyped(t) {
		// `want := 36.6` is a float64 and `c` is a Celsius; the conversion is
		// what keeps the comparison from being a type error.
		return t + "(" + repr + ")", nil
	}
	return repr, nil
}

// defaultTyped names the types an untyped constant already becomes on its own,
// so a conversion around the literal would be noise.
func defaultTyped(t string) bool {
	switch t {
	case "int", "float64", "string", "bool", "complex128":
		return true
	}
	return false
}

// isConstantExpr reports whether src parses as an expression built only from
// literals — no identifier but true and false, no call, no selector.
func isConstantExpr(src string) bool {
	e, err := parser.ParseExpr(src)
	if err != nil {
		return false
	}
	var ok func(ast.Expr) bool
	ok = func(e ast.Expr) bool {
		switch e := e.(type) {
		case *ast.BasicLit:
			return true
		case *ast.Ident:
			return e.Name == "true" || e.Name == "false"
		case *ast.ParenExpr:
			return ok(e.X)
		case *ast.UnaryExpr:
			return ok(e.X)
		case *ast.BinaryExpr:
			return ok(e.X) && ok(e.Y)
		}
		return false
	}
	return ok(e)
}

// mainQualifier matches the package qualifier reflect gives a type the session
// itself declared. The generated test is package main beside the session's own
// program, so `main.Point` has to read as `Point` there — and reflect is the
// only place the qualifier appears, because the type checker's qualifier drops
// it already.
var mainQualifier = regexp.MustCompile(`\bmain\.`)

// GoTypeName is reflect's spelling of a type with the session's own package
// qualifier stripped.
//
// Exported because inspect.TypeName falls back to it when there is no checker,
// and two copies of the \bmain\. regexp is the drift this repository keeps
// deleting rather than tolerating.
func GoTypeName(t string) string { return mainQualifier.ReplaceAllString(t, "") }
