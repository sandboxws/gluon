package inspect

import (
	"go/types"

	"github.com/sandboxws/gluon/internal/pretty"
)

// Literal itself moved to internal/pretty, where the rest of the value
// formatting lives — it was always a pure function of a pretty.Value. What is
// left here is the half that needs go/types and a *Target, which that package
// must not grow an import of.

// Comparable reports whether a generated assertion may use == on this
// expression's value.
//
// The question is answered from the type and never from the value: a slice
// that happens to hold comparable elements is still not comparable, and a
// value that looks like one is not a counter-example. When the checker could
// not answer at all — it is nil here — the answer is false, which costs a
// reflect.DeepEqual on a value that would have compared directly and is wrong
// in neither direction. Invariant 5: the checker is never an authority, so its
// silence has to fall somewhere safe.
func Comparable(t *Target) bool {
	if t == nil || t.Type == nil || len(t.Tuple) > 1 {
		return false
	}
	return types.Comparable(t.Type)
}

// TypeName is the expression's type as the generated source should spell it:
// the checker's own qualifier, which drops the session's package the way the
// generated file needs. It falls back to the value printer's type — reflect's
// spelling, with the same qualifier stripped — when there is no checker.
func TypeName(t *Target, v pretty.Value) string {
	if t != nil && t.Type != nil && len(t.Tuple) <= 1 {
		// Default first: an untyped constant's type spells "untyped int",
		// which is what the expression *is* and not a type anything can be
		// declared as.
		return t.name(types.Default(t.Type))
	}
	return pretty.GoTypeName(v.Type)
}
