package release

import (
	"bytes"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// The language half of a release has no data file. api/go1.N.txt is the standard
// library, doc/godebug.md is behaviour, and doc/initial/2-language.md ships as an
// empty heading — the finished release notes are not in the distribution.
//
// What the distribution does ship is $GOROOT/src/go/types, and the type checker
// knows exactly which syntax it admits at which language version. Three helpers
// carry that: verifyVersionf and versionErrorf name the feature in a format
// string, and allowVersion is a silent permission check whose caller names the
// feature in its own words — range.go's "cannot range over %s: requires go1.23
// or later" is one of those, and it is how range-over-func is gated.
//
// So a list built from the named helpers alone would omit range-over-func, which
// is the headline change of the release it belongs to. That is the reason Gates
// is not a display of what a release changed: a list that confidently omits the
// thing you came for is worse than no list. It is read for two narrower jobs
// that it can do honestly — reporting which *versions* gate something, which is
// exact because all three helpers are collected, and naming the features the
// named helpers do name, which is a labelled subset.
//
// TestEveryGatedVersionIsWrittenUpOrWaived is what this exists for: when a Go
// release lands and its language change has no note, the build says so.

// A Gate is the type checker admitting some syntax only at or above a version.
type Gate struct {
	// Version is the language version required, "1.27".
	Version string
	// Feature is what the checker calls it, when the call names it at all.
	// Empty for an allowVersion gate, whose caller words the error itself.
	Feature string
	// Where is the file and line, so an unnamed gate can still be looked at.
	Where string
}

// Named reports whether the gate carries a feature name.
func (g Gate) Named() bool { return g.Feature != "" }

// gateFuncs are the three ways go/types gates on the language version. The
// first two name the feature in a format string; the third does not.
var gateFuncs = map[string]bool{
	"verifyVersionf": true,
	"versionErrorf":  true,
	"allowVersion":   true,
}

// Gates reads the language version gates out of $GOROOT/src/go/types.
//
// A distribution with no src directory is not an error — the api files still
// answer everything else, and the caller gets an empty list, the way a missing
// doc/godebug.md loses only its own half.
func Gates(goroot string) []Gate {
	dir := filepath.Join(goroot, "src", "go", "types")
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	fset := token.NewFileSet()
	// A named gate is deduped on the name, not on the call site: the checker
	// gates "function instantiation" from three places and "type parameter"
	// from two, and a list repeating those is describing the implementation
	// rather than the language. An unnamed gate has only its location to be
	// told apart by, so that is its key.
	seen := map[[2]string]bool{}
	var out []Gate
	for _, e := range entries {
		name := e.Name()
		if !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		path := filepath.Join(dir, name)
		data, err := os.ReadFile(path)
		// A byte scan before parsing: most of go/types names no version at all,
		// and parsing every file to find the ten that do is the cost this
		// avoids. "go1_" is how the constants are spelled, so a file without it
		// cannot hold a gate.
		if err != nil || !bytes.Contains(data, []byte("go1_")) {
			continue
		}
		f, err := parser.ParseFile(fset, path, data, parser.SkipObjectResolution)
		if err != nil {
			continue
		}
		ast.Inspect(f, func(node ast.Node) bool {
			g, ok := gateOf(node, fset, name)
			if !ok {
				return true
			}
			key := [2]string{g.Version, g.Feature}
			if !g.Named() {
				key[1] = g.Where
			}
			if seen[key] {
				return true
			}
			seen[key] = true
			out = append(out, g)
			return true
		})
	}
	sort.Slice(out, func(i, j int) bool {
		if c := Compare(out[i].Version, out[j].Version); c != 0 {
			return c < 0
		}
		if out[i].Feature != out[j].Feature {
			return out[i].Feature < out[j].Feature
		}
		return out[i].Where < out[j].Where
	})
	return out
}

// gateOf reads one call, if it is a gate.
func gateOf(node ast.Node, fset *token.FileSet, file string) (Gate, bool) {
	call, ok := node.(*ast.CallExpr)
	if !ok || len(call.Args) == 0 {
		return Gate{}, false
	}
	// Either check.allowVersion(go1_22) or a bare allowVersion(go1_22): inside
	// rangeKeyVal the check is passed in as a function parameter, and matching
	// only the method form is how a first attempt missed range-over-int and
	// range-over-func — the two gates this file most needs to see.
	switch fun := call.Fun.(type) {
	case *ast.SelectorExpr:
		if !gateFuncs[fun.Sel.Name] {
			return Gate{}, false
		}
	case *ast.Ident:
		if !gateFuncs[fun.Name] {
			return Gate{}, false
		}
	default:
		return Gate{}, false
	}
	version, feature := "", ""
	for i, arg := range call.Args {
		switch a := arg.(type) {
		case *ast.Ident:
			if v, ok := goVersionIdent(a.Name); ok && version == "" {
				version = v
			}
		case *ast.BasicLit:
			// The format string follows the version argument. A literal before
			// it belongs to the position, not to the feature.
			if a.Kind == token.STRING && version != "" && feature == "" && i > 0 {
				if s, err := strconv.Unquote(a.Value); err == nil {
					feature = cleanFeature(s)
				}
			}
		}
	}
	if version == "" {
		return Gate{}, false
	}
	pos := fset.Position(call.Pos())
	return Gate{Version: version, Feature: feature, Where: fmt.Sprintf("%s:%d", file, pos.Line)}, true
}

// goVersionIdent turns the constant name go1_27 into "1.27".
func goVersionIdent(name string) (string, bool) {
	rest, ok := strings.CutPrefix(name, "go1_")
	if !ok || rest == "" {
		return "", false
	}
	v := "1." + rest
	if _, _, ok := split(v); !ok {
		return "", false
	}
	return v, true
}

// cleanFeature makes a checker format string readable. The verbs stand for a
// name the checker fills in at the error site and this package cannot know, so
// they become an ellipsis rather than being dropped — "built-in …" is honest
// about there being a word there, and "built-in" alone is not.
func cleanFeature(format string) string {
	// invalidOp and friends are concatenated prefixes, not part of the name.
	format = strings.TrimPrefix(format, "invalid operation: ")
	var b strings.Builder
	for i := 0; i < len(format); i++ {
		if format[i] != '%' || i+1 >= len(format) {
			b.WriteByte(format[i])
			continue
		}
		i++
		if format[i] == '%' {
			b.WriteByte('%')
			continue
		}
		b.WriteString("…")
	}
	return strings.TrimSpace(b.String())
}

// GatedVersions is every language version the checker gates something at,
// oldest first. It is exact where the feature list is not: all three helpers
// name a version, and only two of them name a feature.
func GatedVersions(gates []Gate) []string {
	seen := map[string]bool{}
	var out []string
	for _, g := range gates {
		if !seen[g.Version] {
			seen[g.Version] = true
			out = append(out, g.Version)
		}
	}
	sort.Slice(out, func(i, j int) bool { return Compare(out[i], out[j]) < 0 })
	return out
}

// GatesAt is the gates for one version.
func GatesAt(gates []Gate, version string) []Gate {
	var out []Gate
	for _, g := range gates {
		if g.Version == version {
			out = append(out, g)
		}
	}
	return out
}
