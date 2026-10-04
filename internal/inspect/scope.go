package inspect

import (
	"go/types"
	"sort"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/lipgloss/table"

	"github.com/sandboxws/gluon/internal/check"
	"github.com/sandboxws/gluon/internal/pretty"
)

// Binding is one name the session has bound, with the type the checker gave
// it. Ord is the ordinal that also addresses it, or 0.
type Binding struct {
	Name string
	Type string
	Ord  int
}

// Scope is everything the session has in scope: the variables bound in main's
// body, and the funcs, types and consts hoisted above it.
type Scope struct {
	Vars   []Binding
	Funcs  []Binding
	Types  []Binding
	Consts []Binding
	// Values maps an ordinal to the expression that produced it, so :ls can
	// say what _2 refers to rather than only that it exists.
	Values []Binding
}

// Names reports whether anything is in scope at all.
func (s *Scope) Empty() bool {
	return len(s.Vars) == 0 && len(s.Funcs) == 0 && len(s.Types) == 0 &&
		len(s.Consts) == 0 && len(s.Values) == 0
}

// Bindings reads the session's scope out of a completed check.
//
// Variables come from main's body scope and the rest from the package scope,
// which is the same split render uses when it decides what to hoist.
func Bindings(r *check.Result) *Scope {
	out := &Scope{}
	qual := qualifier(r.Pkg)

	if sc := r.MainScope(); sc != nil {
		for _, name := range sc.Names() {
			if strings.HasPrefix(name, "__gluon") || isSynthetic(name) {
				continue
			}
			v, ok := sc.Lookup(name).(*types.Var)
			if !ok || !r.DeclaredHere(v) {
				continue
			}
			out.Vars = append(out.Vars, Binding{Name: name, Type: types.TypeString(v.Type(), qual)})
		}
	}

	if r.Pkg != nil {
		sc := r.Pkg.Scope()
		for _, name := range sc.Names() {
			if strings.HasPrefix(name, "__gluon") || name == "main" || isSynthetic(name) {
				continue
			}
			o := sc.Lookup(name)
			if !r.DeclaredHere(o) {
				continue
			}
			switch o := o.(type) {
			case *types.Func:
				out.Funcs = append(out.Funcs, Binding{
					Name: name,
					Type: strings.TrimPrefix(types.TypeString(o.Type(), qual), "func"),
				})
			case *types.TypeName:
				out.Types = append(out.Types, Binding{
					Name: name,
					Type: types.TypeString(o.Type().Underlying(), qual),
				})
			case *types.Const:
				out.Consts = append(out.Consts, Binding{
					Name: name,
					Type: types.TypeString(o.Type(), qual) + " = " + o.Val().String(),
				})
			}
		}
	}
	for _, g := range [][]Binding{out.Vars, out.Funcs, out.Types, out.Consts} {
		sort.Slice(g, func(i, j int) bool { return g[i].Name < g[j].Name })
	}
	return out
}

// isSynthetic hides the names gluon binds on the user's behalf. They are
// reported as ordinals in their own block rather than as variables.
func isSynthetic(name string) bool {
	if !strings.HasPrefix(name, "_") || len(name) < 2 {
		return false
	}
	for _, r := range name[1:] {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

// PlainScope is the one-value-per-line form pipes and scripts read.
func PlainScope(s *Scope) string {
	var b strings.Builder
	section := func(title string, bs []Binding) {
		if len(bs) == 0 {
			return
		}
		b.WriteString(title + "\n")
		for _, bd := range bs {
			b.WriteString("  " + bd.Name)
			if bd.Type != "" {
				b.WriteString("  " + bd.Type)
			}
			if bd.Ord > 0 {
				b.WriteString("  _" + itoa(bd.Ord))
			}
			b.WriteString("\n")
		}
	}
	section("variables", s.Vars)
	section("funcs", s.Funcs)
	section("types", s.Types)
	section("consts", s.Consts)
	section("values", s.Values)
	return strings.TrimRight(b.String(), "\n")
}

// RenderScope is the terminal form: one table per kind of thing.
func RenderScope(s *Scope, st pretty.Styles) string {
	var out []string
	section := func(title string, bs []Binding, ordCol bool) {
		if len(bs) == 0 {
			return
		}
		t := table.New().
			Border(lipgloss.RoundedBorder()).
			BorderStyle(st.Border).
			StyleFunc(func(row, col int) lipgloss.Style {
				if col == 0 {
					return lipgloss.NewStyle().Padding(0, 1)
				}
				return lipgloss.NewStyle().Padding(0, 1).Faint(true)
			})
		for _, bd := range bs {
			row := []string{bd.Name, bd.Type}
			if ordCol {
				ord := ""
				if bd.Ord > 0 {
					ord = "_" + itoa(bd.Ord)
				}
				row = append(row, ord)
			}
			t.Row(row...)
		}
		out = append(out, st.Header.Render(title)+"\n"+t.Render())
	}
	section("variables", s.Vars, false)
	section("funcs", s.Funcs, false)
	section("types", s.Types, false)
	section("consts", s.Consts, false)
	section("values", s.Values, true)
	return strings.Join(out, "\n")
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	return string(b[i:])
}
