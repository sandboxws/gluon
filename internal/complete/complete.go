// Package complete turns a partly typed line into whole-line completions.
//
// Whole-line, because that is what bubbles/textinput's suggestion machinery
// takes: it matches a candidate against the entire input and, on accept,
// appends whatever of the candidate extends past it. So a completion for
// `x := strings.To` is the full `x := strings.ToUpper`, not `ToUpper`.
//
// That machinery matches case-insensitively, which is wrong for Go: accepting
// `strings.ToUpper` after typing `strings.tou` would leave `strings.toupper`,
// since the typed prefix is kept verbatim. Filtering case-sensitively here means
// every candidate handed over is a true prefix extension, so the two filters can
// never disagree.
package complete

import (
	"sort"
	"strings"
)

// Context is what the REPL knows at the moment a key is pressed.
//
// The cheap parts are precomputed once per submitted line, because that is when
// they change. The function fields are lazy: enumerating every stdlib package's
// members up front, or type-checking every callee in scope, would cost far more
// than the one lookup a typed dot or parenthesis actually asks for.
type Context struct {
	// Metas are the meta commands, with their leading colon.
	Metas []string
	// Names are the identifiers in scope: variables, funcs, types, consts,
	// and the ordinals the session has bound.
	Names []string
	// Packages maps a qualifier to the import path it stands for.
	Packages map[string]string
	// Members returns a package's exported names, given its import path.
	Members func(importPath string) []string
	// Fields returns the fields and methods of an expression, given its text.
	Fields func(expr string) []string
	// Literal returns the struct fields of a named type, given its text. It is
	// separate from Fields because a composite literal takes fields and nothing
	// else: offering a method inside `Point{...}` would be offering a mistake.
	Literal func(typeExpr string) []string
	// Arguments returns a callee's signature as it should be shown, and the
	// names in scope the parameter at index will take. Both come from the type
	// checker, so both are empty when it has nothing to say.
	Arguments func(callee string, index int) (sig string, cands []string)
	// MetaArgs completes a meta command's argument: its flags, and the values
	// its operands and flag values take — HTTP methods, theme names, a
	// scratchpad for :scratch.
	//
	// It is consulted before every Go path because most of those arguments are
	// not Go. `two-sum` is one name here and a subtraction there, so the
	// identifier path would offer completions for `sum` and never for the
	// slug. ok says the command's grammar owns the position, and then its
	// candidates — even none — are the whole answer; at is where the word
	// being completed starts. A Go operand answers !ok, and the Go paths run
	// exactly as they always did.
	MetaArgs func(line string) (at int, cands []string, ok bool)
}

// Max is how many candidates are offered. Cycling with ctrl-n through four
// hundred names is not completion, and the ghost text only ever shows one.
const Max = 200

// Hint is the callee's signature while the end of line sits inside its argument
// list, or "" when it does not.
//
// It shares callAt with Suggest so the two can never disagree about which call
// the cursor is in, and it is deliberately not a completion: the signature is
// never handed to the suggestion list, so it cannot be accepted and cannot
// change the line (invariant 16 by construction).
func Hint(line string, arguments func(callee string, index int) (string, []string)) string {
	if arguments == nil {
		return ""
	}
	cl, ok := callAt(line)
	if !ok {
		return ""
	}
	sig, _ := arguments(cl.Callee, cl.Index)
	return sig
}

// Suggest returns whole-line completions for line, or nil when there is
// nothing useful to offer.
//
// It only ever completes the token the line ends with, so the caller must not
// call it when the cursor sits elsewhere: textinput appends an accepted
// suggestion at the end regardless of where the cursor is.
func Suggest(line string, ctx Context) []string {
	// A meta command is a different language: `:t` is not an identifier, and
	// the names that follow it are ordinary Go.
	if strings.HasPrefix(line, ":") && !strings.ContainsAny(line, " \t") {
		// The whole line is the prefix here — a meta command is one token, and
		// there is nothing before it to carry through.
		return prefixed("", line, ctx.Metas)
	}

	if ctx.MetaArgs != nil && strings.HasPrefix(line, ":") {
		if at, cands, ok := ctx.MetaArgs(line); ok {
			return prefixed(line[:at], line[at:], cands)
		}
	}

	// Inside a composite literal, the names that make sense are the fields that
	// are not set yet — and nothing else does, so this comes before the
	// identifier path rather than after it.
	if lit, ok := literalAt(line); ok && ctx.Literal != nil {
		return literalSuggestions(line, lit, ctx)
	}

	// Inside an open call, the names that make sense are the ones the parameter
	// will take. Like the literal path this comes before identifier completion
	// rather than after it: when nothing in scope fits, the answer is nothing,
	// not every name that happens to share a prefix.
	//
	// A partly typed argument carrying a dot is the exception, and it is the
	// selector path's, not this one's. Arguments offers names the session has
	// bound, and none of them is `os.Std` — swallowing that case here would
	// take package member completion away inside every call, which is a
	// capability this one has no business ending.
	if cl, ok := callAt(line); ok && ctx.Arguments != nil && !strings.Contains(cl.Prefix, ".") {
		_, cands := ctx.Arguments(cl.Callee, cl.Index)
		return prefixed(line[:len(line)-len(cl.Prefix)], cl.Prefix, cands)
	}

	start := tokenStart(line)
	token := line[start:]
	head := line[:start]

	if qual, prefix, ok := splitSelector(token); ok {
		if path, isPkg := ctx.Packages[qual]; isPkg && ctx.Members != nil {
			return prefixed(head+qual+".", prefix, ctx.Members(path))
		}
		if ctx.Fields != nil {
			return prefixed(head+qual+".", prefix, ctx.Fields(qual))
		}
		return nil
	}

	// A bare identifier: everything nameable here, plus the packages that
	// could be qualified.
	if token == "" {
		return nil
	}
	cands := append([]string(nil), ctx.Names...)
	for name := range ctx.Packages {
		cands = append(cands, name)
	}
	cands = append(cands, keywords...)
	return prefixed(head, token, cands)
}

// literalSuggestions offers the unset fields of a composite literal.
//
// Each candidate carries its colon, because that is the next thing that has to
// be typed and completion that stops one character short is completion that
// still needs a keystroke.
func literalSuggestions(line string, lit literal, ctx Context) []string {
	fields := ctx.Literal(lit.Type)
	if len(fields) == 0 {
		return nil
	}
	set := make(map[string]bool, len(lit.Set))
	for _, k := range lit.Set {
		set[k] = true
	}

	head := line[:len(line)-len(lit.Prefix)]
	var out []string
	for _, f := range fields {
		if set[f] || !strings.HasPrefix(f, lit.Prefix) || f == lit.Prefix {
			continue
		}
		out = append(out, head+f+": ")
	}
	// Declaration order, not alphabetical: it is the order the fields are
	// written in, so it is the order someone filling a literal expects.
	if len(out) > Max {
		out = out[:Max]
	}
	return out
}

// tokenStart is the index where the identifier or selector at the end of line
// begins. A dot is part of the token, since `strings.To` is completed as one.
func tokenStart(line string) int {
	i := len(line)
	for i > 0 {
		c := line[i-1]
		if c == '.' || c == '_' ||
			(c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') {
			i--
			continue
		}
		break
	}
	return i
}

// splitSelector divides a token at its last dot. A token with no dot, or one
// whose qualifier is empty, is not a selector.
func splitSelector(token string) (qual, prefix string, ok bool) {
	i := strings.LastIndex(token, ".")
	if i <= 0 {
		return "", "", false
	}
	return token[:i], token[i+1:], true
}

// prefixed keeps the candidates that extend prefix — case-sensitively, because
// Go is — and returns each as a whole line.
func prefixed(head, prefix string, cands []string) []string {
	if len(cands) == 0 {
		return nil
	}
	seen := map[string]bool{}
	var out []string
	for _, c := range cands {
		if !strings.HasPrefix(c, prefix) || c == prefix || seen[c] {
			continue
		}
		seen[c] = true
		out = append(out, head+c)
	}
	sort.Strings(out)
	if len(out) > Max {
		out = out[:Max]
	}
	return out
}

// keywords are offered because a REPL is where people try syntax out, and
// because the alternative is a completion list that mysteriously omits `range`.
// The builtins are here for the same reason.
var keywords = []string{
	"break", "case", "chan", "const", "continue", "default", "defer", "else",
	"fallthrough", "for", "func", "go", "goto", "if", "import", "interface",
	"map", "package", "range", "return", "select", "struct", "switch", "type",
	"var",
	"append", "cap", "clear", "close", "complex", "copy", "delete", "imag",
	"len", "make", "max", "min", "new", "panic", "print", "println", "real",
	"recover",
	"bool", "byte", "complex64", "complex128", "error", "float32", "float64",
	"int", "int8", "int16", "int32", "int64", "rune", "string",
	"uint", "uint8", "uint16", "uint32", "uint64", "uintptr", "any",
	"true", "false", "nil", "iota",
}
