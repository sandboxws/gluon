package repl

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/sandboxws/gluon/internal/pretty"
	"github.com/sandboxws/gluon/internal/syntax"
)

// The theme picker is the second full-screen view, and it works the way modal
// does — alt screen while it is open, nothing printed until it closes, one line
// left in scrollback — for the reasons internal/repl/modal.go gives.
//
// What it does differently is the preview. It does not draw a palette in some
// separate widget: moving the selection installs the theme, so the list, the
// footer, the sample and every colour in them are the theme being considered.
// A swatch of fifteen colours answers "what are these values"; only the session
// itself answers "do I want to read Go in this", which is the actual question.
//
// That is why the picker holds no styles of its own and reads the package-level
// ones on every render, which is the one place in this package where those
// variables changing under a value is the point rather than a hazard.
type picker struct {
	spec ThemeSpec
	// idx is the highlighted row; start is the theme in force when the picker
	// opened, which esc puts back.
	idx   int
	start string
	// warn is a theme that would not load, named in the footer. The palette is
	// still complete — ui.Named says so — so the preview is honest about what
	// you would actually get.
	warn string

	w, h int
}

// A pickAction is what the model has to do about a keystroke. The picker
// changes no palette itself: applying one touches the input, the spinner and
// Core's renderer, and those belong to the model.
type pickAction int

const (
	pickStay pickAction = iota
	// pickPreview: the selection moved, so paint the session in it.
	pickPreview
	// pickKeep: enter. The choice is made and is to be written down.
	pickKeep
	// pickCancel: esc. Put back the theme the picker opened in.
	pickCancel
)

func newPicker(spec ThemeSpec, width, height int) picker {
	p := picker{spec: spec, start: spec.Active, w: width, h: height}
	for i, ch := range spec.Choices {
		if ch.Name == spec.Active {
			p.idx = i
			break
		}
	}
	return p
}

// selected is the theme the highlight is on. Empty only when no theme loaded
// at all, which `gluon doctor` would already be complaining about.
func (p picker) selected() string {
	if p.idx < 0 || p.idx >= len(p.spec.Choices) {
		return ""
	}
	return p.spec.Choices[p.idx].Name
}

func (p picker) update(msg tea.Msg) (picker, pickAction) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		p.w, p.h = msg.Width, msg.Height
		return p, pickStay

	case tea.KeyMsg:
		n := len(p.spec.Choices)
		if n == 0 {
			return p, pickCancel
		}
		switch msg.String() {
		case "q", "esc", "ctrl+c":
			return p, pickCancel
		case "enter":
			return p, pickKeep
		case "j", "down", "ctrl+n", "tab":
			p.idx = (p.idx + 1) % n
			return p, pickPreview
		case "k", "up", "ctrl+p", "shift+tab":
			p.idx = (p.idx - 1 + n) % n
			return p, pickPreview
		case "g", "home":
			p.idx = 0
			return p, pickPreview
		case "G", "end":
			p.idx = n - 1
			return p, pickPreview
		}
	}
	return p, pickStay
}

func (p picker) View() string {
	body := p.h - 2
	if body < 4 {
		body = 4
	}
	listW := p.listWidth()

	var main string
	// Side by side while the preview has room to be a preview. Below that the
	// list goes on top, because a two-column layout on a narrow terminal is
	// two columns of nothing.
	if p.w >= 64 && p.w-listW-2 >= 34 {
		rows := strings.Join(p.rows(body), "\n")
		prev := strings.Join(preview(p.w-listW-2, body), "\n")
		main = lipgloss.JoinHorizontal(lipgloss.Top,
			lipgloss.NewStyle().Width(listW).Render(rows), "  ", prev)
	} else {
		listRows := body / 2
		if listRows > len(p.spec.Choices) {
			listRows = len(p.spec.Choices)
		}
		if listRows < 1 {
			listRows = 1
		}
		lines := append(p.rows(listRows), "")
		lines = append(lines, preview(p.w, body-listRows-1)...)
		main = strings.Join(lines, "\n")
	}

	return p.title() + "\n" + main + "\n" + p.footer()
}

// title carries the highlighted theme's own line about itself. The list column
// has room for names and nothing else, and "your terminal's own sixteen
// colours" is the sentence that explains what you are looking at.
func (p picker) title() string {
	line := theme.Heading.Render("themes")
	if p.idx >= 0 && p.idx < len(p.spec.Choices) {
		ch := p.spec.Choices[p.idx]
		if ch.About != "" {
			line += theme.Dim.Render("  " + ch.Name + " · " + ch.About)
		}
		// The one thing the preview cannot show. Everything else on this screen
		// is the theme installed and therefore self-evident; which ground it was
		// drawn for is not, because the ground is the terminal's and gluon never
		// touches it — a light theme previewed on a black terminal looks broken
		// rather than light.
		if ch.Appearance != "" {
			line += theme.Note.Render("  " + ch.Appearance)
		}
	}
	return cut(line, p.w)
}

// listWidth is the name column plus its mark, bounded so that one long theme
// name cannot squeeze the preview out.
func (p picker) listWidth() int {
	w := 12
	for _, ch := range p.spec.Choices {
		if n := len([]rune(ch.Name)); n > w {
			w = n
		}
	}
	// An imported theme carries the name of the editor theme it came from, and
	// those are long. This is where a very long one stops taking the preview's
	// room; pad truncates past it.
	if w > 26 {
		w = 26
	}
	return w + 2 // the ✓ or the space before it
}

// rows is the list, windowed so the highlight is always on screen.
//
// The window is derived from the selection rather than remembered, which keeps
// View a function of the picker and nothing else: a list that fits is shown
// whole, and one that does not scrolls with the selection near its middle.
func (p picker) rows(n int) []string {
	if n < 1 {
		n = 1
	}
	top := 0
	if len(p.spec.Choices) > n {
		top = p.idx - n/2
		if limit := len(p.spec.Choices) - n; top > limit {
			top = limit
		}
		if top < 0 {
			top = 0
		}
	}

	var out []string
	for i := top; i < len(p.spec.Choices) && i < top+n; i++ {
		ch := p.spec.Choices[i]
		// ✓ marks the theme the config selects, which is what the session
		// comes back to if this ends in esc.
		mark := " "
		if ch.Name == p.start {
			mark = "✓"
		}
		line := mark + " " + pad(ch.Name, p.listWidth()-2)
		if i == p.idx {
			line = theme.Prompt.Reverse(true).Render(line)
		} else {
			line = theme.Dim.Render(line)
		}
		out = append(out, line)
	}
	return out
}

// pad fits a name to the column, cutting one that is longer than listWidth
// allowed for. Padded before styling, because a lipgloss style makes len() lie
// and the column would walk.
func pad(s string, n int) string {
	r := []rune(s)
	if len(r) > n {
		if n < 1 {
			return ""
		}
		return string(r[:n-1]) + "…"
	}
	return s + strings.Repeat(" ", n-len(r))
}

func (p picker) footer() string {
	key := func(k, what string) string {
		if what == "" {
			return theme.Prompt.Render(k)
		}
		return theme.Prompt.Render(k) + theme.Dim.Render(" "+what)
	}
	keys := func(labelled bool) string {
		label := func(s string) string {
			if labelled {
				return s
			}
			return ""
		}
		return strings.Join([]string{
			key("j/k", label("preview")),
			key("enter", label("keep")),
			key("esc", label("cancel")),
		}, theme.Dim.Render(" · "))
	}
	// The labels go before the keys do: a footer cut in half says less than
	// the shorter one it could have printed instead.
	line := keys(true)
	if lipgloss.Width(line) > p.w {
		line = keys(false)
	}
	out := []string{cut(line, p.w)}
	switch {
	case p.warn != "":
		out = append(out, cut(theme.Err.Render(p.warn), p.w))
	case len(p.spec.Overrides) > 0:
		out = append(out, cut(theme.Note.Render("config.toml overrides "+
			plural(len(p.spec.Overrides), "role")+" on top of this"), p.w))
	}
	return strings.Join(out, "\n")
}

// preview is a session in the theme that is currently installed: two lines
// typed at the prompt, the value one of them answered with, an error, and then
// the roles a transcript this short does not otherwise reach.
//
// The two lines are chosen to carry every role a line of Go can — a comment, a
// keyword, a string, a number, a predeclared type, a builtin call, punctuation
// and plain identifiers — and they are rendered through the same functions the
// REPL renders with, syntax.Highlight and pretty.RichWith. What it shows is
// what you will get rather than an impression of it.
func preview(width, height int) []string {
	if height < 1 {
		return nil
	}
	var out []string
	add := func(s string) {
		for _, line := range strings.Split(s, "\n") {
			out = append(out, cut(line, width))
		}
	}

	typed := func(src string) {
		add(promptStyle.Render(prompt) + syntax.Highlight(syntax.Go, src, syntaxPal))
	}
	// A transcript, in the order one happens.
	typed(`u := User{ID: 7, Tags: []string{"admin"}} // a binding`)
	add(pretty.RichWith([]pretty.Value{previewValue}, theme.Styles(), nil))
	typed(`if len(u.Tags) > 0 { fmt.Println("hello", u.ID) }`)
	add("hello 7") // the program's own output, which gluon never paints
	add(errStyle.Render("error: undefined: usrs"))
	add("")
	// Last, so that a short window cuts the swatch rather than the session.
	add(roleSwatch(width))

	if len(out) > height {
		out = out[:height]
	}
	return out
}

// previewValue is what the first of those lines answers with, as the child
// would have described it. Written out rather than evaluated: the picker opens
// in a session that may have no module, and a preview that depended on the
// toolchain would sometimes be empty.
var previewValue = pretty.Value{
	Type: "main.User", Kind: "struct",
	Fields: []pretty.Field{
		{Name: "ID", Val: pretty.Value{Type: "int", Kind: "scalar", Repr: "7"}},
		{Name: "Tags", Val: pretty.Value{Type: "[]string", Kind: "list", Len: intp(1), Cap: intp(1),
			Items: []pretty.Value{{Type: "string", Kind: "string", Repr: "admin"}}}},
	},
}

func intp(n int) *int { return &n }

// cut trims a line to the pane it is drawn in.
//
// Through lipgloss rather than by slicing, because these lines are full of
// escape sequences by the time they get here and cutting one in half would
// leave the rest of the screen painted in whatever colour it opened with.
// MaxWidth is the one property that truncates without reflowing.
func cut(s string, width int) string {
	if width <= 0 || lipgloss.Width(s) <= width {
		return s
	}
	return lipgloss.NewStyle().MaxWidth(width).TabWidth(lipgloss.NoTabConversion).Render(s)
}

// roleSwatch names every role in its own colour, which is the only way the
// three that a transcript cannot show — search, border, the annotation grey —
// get seen before they are chosen.
func roleSwatch(width int) string {
	styles := theme.RoleStyles()
	var line, out strings.Builder
	flush := func() {
		if line.Len() > 0 {
			if out.Len() > 0 {
				out.WriteString("\n")
			}
			out.WriteString(line.String())
			line.Reset()
		}
	}
	w := 0
	for _, role := range swatchOrder {
		if w > 0 && w+1+len(role) > width {
			flush()
			w = 0
		}
		if w > 0 {
			line.WriteString(" ")
			w++
		}
		line.WriteString(styles[role].Render(role))
		w += len(role)
	}
	flush()
	return out.String()
}

// swatchOrder is source roles first, then the ones that describe the program
// itself, because that is the order somebody comparing two themes reads them.
var swatchOrder = []string{
	"keyword", "string", "type", "comment", "number", "builtin", "punctuation", "ident",
	"prompt", "error", "note", "annotation", "dim", "search", "border",
}
