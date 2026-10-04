package inspect

import (
	"go/types"
	"sort"
	"strings"

	"github.com/sandboxws/gluon/internal/check"
)

// stdInterfaces are the interfaces worth testing every type against. They are
// the ones a person meets first in Go, and the ones whose accidental
// satisfaction is most often the point: a type grows a String() method and
// suddenly fmt prints it differently.
var stdInterfaces = []struct{ Pkg, Name string }{
	{"fmt", "Stringer"},
	{"sort", "Interface"},
	{"io", "Reader"},
	{"io", "Writer"},
	{"encoding/json", "Marshaler"},
}

// Interfaces returns what satisfaction should be tested against: error, the
// stdlib handful above, and every interface the session itself declares.
//
// The handful is the set a reader actually asks about; any other interface is
// one :sat away.
func Interfaces(r *check.Result, lookup func(string) (*types.Package, error)) []Named {
	var out []Named

	// error lives in the universe scope, not in any package.
	if obj := types.Universe.Lookup("error"); obj != nil {
		if in, ok := obj.Type().Underlying().(*types.Interface); ok {
			out = append(out, Named{Name: "error", Iface: in})
		}
	}

	for _, want := range stdInterfaces {
		pkg, err := lookup(want.Pkg)
		if err != nil || pkg == nil {
			// A package that will not load is not worth failing the command
			// over; the rest of the answer is still useful.
			continue
		}
		obj := pkg.Scope().Lookup(want.Name)
		if obj == nil {
			continue
		}
		if in, ok := obj.Type().Underlying().(*types.Interface); ok {
			out = append(out, Named{Name: pkg.Name() + "." + want.Name, Iface: in})
		}
	}

	out = append(out, sessionInterfaces(r)...)
	return out
}

// sessionInterfaces finds interfaces the user declared this session. Answering
// "does this satisfy the interface I just wrote" is the whole reason to ask.
func sessionInterfaces(r *check.Result) []Named {
	if r == nil || r.Pkg == nil {
		return nil
	}
	scope := r.Pkg.Scope()
	names := scope.Names()
	sort.Strings(names)

	var out []Named
	for _, name := range names {
		if strings.HasPrefix(name, "__gluon") {
			continue // gluon's own injected machinery
		}
		obj, ok := scope.Lookup(name).(*types.TypeName)
		if !ok {
			continue
		}
		if in, ok := obj.Type().Underlying().(*types.Interface); ok && in.NumMethods() > 0 {
			out = append(out, Named{Name: name, Iface: in})
		}
	}
	return out
}
