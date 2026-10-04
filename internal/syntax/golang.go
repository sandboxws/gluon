package syntax

import (
	"go/scanner"
	"go/token"
	"strings"
	"unicode/utf8"
)

// raw is one Scan result with its extent worked out. Roles need a token of
// lookahead — `len` is a builtin in len(x) and a name in var len int — so
// classification is a second pass over these rather than a decision taken
// while scanning.
type raw struct {
	off, end int
	tok      token.Token
	lit      string
}

func goTokens(src string) []Token {
	if src == "" {
		return nil
	}
	fset := token.NewFileSet()
	file := fset.AddFile("", fset.Base(), len(src))

	var s scanner.Scanner
	// A nil error handler, unlike internal/session.IsIncomplete's. That one
	// reads the messages to decide whether to keep reading; this one has
	// nothing to decide. Half the lines a REPL scans are incomplete by design,
	// and an error here is harmless: an unterminated string is painted as a
	// string to the end of the input, which is what it is.
	s.Init(file, []byte(src), nil, scanner.ScanComments)

	raws := make([]raw, 0, len(src)/4+8)
	prevEnd := 0
	// Scan always advances and always terminates at EOF, but this loop draws
	// the input line on every keystroke and a hang there is the one failure a
	// user cannot escape. The cap costs one comparison.
	for i := 0; i <= len(src)+2; i++ {
		pos, tok, lit := s.Scan()
		if tok == token.EOF {
			break
		}
		off := file.Offset(pos)
		end := off + extent(src, off, tok, lit)
		if off < prevEnd || end > len(src) {
			// go/scanner documents this: when a newline falls inside a
			// /*...*/ comment, the artificial semicolon is synthesized after
			// the COMMENT token but positioned at that newline — inside the
			// comment already emitted. Keeping a zero-width entry preserves
			// the lookahead sequence; Paint drops it.
			raws = append(raws, raw{off: off, end: off, tok: tok, lit: lit})
			continue
		}
		raws = append(raws, raw{off: off, end: end, tok: tok, lit: lit})
		prevEnd = end
	}

	toks := make([]Token, 0, len(raws))
	for i := range raws {
		if raws[i].end <= raws[i].off {
			continue
		}
		r := goRole(i, raws)
		if r == RoleNone {
			continue
		}
		toks = append(toks, Token{Start: raws[i].off, End: raws[i].end, Role: r})
	}
	return toks
}

// extent is how many bytes of src the token at off occupies.
//
// Never len(lit) alone. Three of the scanner's cases lie about it, each for its
// own reason, and each is verified against $GOROOT/src/go/scanner/scanner.go:
//
//   - ILLEGAL: Scan sets lit = string(s.ch), and next() sets s.ch to
//     utf8.RuneError after consuming ONE byte of invalid UTF-8 — three bytes
//     reported for one consumed.
//   - COMMENT, and a raw STRING: scanComment and scanRawString both call
//     stripCR on what they return, so a comment ending "\r\n" is one byte
//     longer than its literal.
//   - SEMICOLON with lit "\n": inserted by the scanner, and not in the source.
//
// tok.String() is no better: for ILLEGAL it is the seven-byte word "ILLEGAL",
// and for a token go/token grows later it is "token(N)". So every case is
// spelled out and the default returns zero — a token type Go adds in a future
// release is a missing colour, and never a swallowed byte.
func extent(src string, off int, tok token.Token, lit string) int {
	if off < 0 || off >= len(src) {
		return 0
	}
	switch tok {
	case token.SEMICOLON:
		if lit == ";" {
			return 1
		}
		return 0 // inserted; there is no ';' here to paint
	case token.ILLEGAL:
		// Exactly what next() consumed: one rune, or one byte when that rune
		// is invalid.
		_, w := utf8.DecodeRuneInString(src[off:])
		return w
	case token.COMMENT:
		return commentExtent(src, off, lit)
	case token.STRING:
		if src[off] == '`' {
			return rawStringExtent(src, off)
		}
		return len(lit) // scanString strips nothing
	}
	switch {
	case tok.IsLiteral(): // IDENT, INT, FLOAT, IMAG, CHAR — STRING handled above
		return len(lit)
	case tok.IsKeyword(): // Scan's contract: lit is the keyword
		return len(lit)
	case tok.IsOperator(): // operators, delimiters, and TILDE
		return len(tok.String())
	}
	return 0
}

// commentExtent measures from the source, because scanComment strips carriage
// returns out of the literal it hands back.
func commentExtent(src string, off int, lit string) int {
	if off+1 >= len(src) || src[off] != '/' {
		return len(lit) // not a shape scanComment produces; trust the literal
	}
	if src[off+1] == '/' {
		if i := strings.IndexByte(src[off:], '\n'); i >= 0 {
			return i // the newline itself is not part of a //-comment
		}
		return len(src) - off
	}
	if i := strings.Index(src[off+2:], "*/"); i >= 0 {
		return 2 + i + 2
	}
	return len(src) - off // unterminated: the rest of the input is comment
}

func rawStringExtent(src string, off int) int {
	if i := strings.IndexByte(src[off+1:], '`'); i >= 0 {
		return 1 + i + 1
	}
	return len(src) - off // unterminated, which a half-typed line often is
}

func goRole(i int, raws []raw) Role {
	r := raws[i]
	switch {
	case r.tok == token.COMMENT:
		return RoleComment
	case r.tok == token.STRING, r.tok == token.CHAR:
		return RoleString
	case r.tok == token.INT, r.tok == token.FLOAT, r.tok == token.IMAG:
		return RoleNumber
	case r.tok.IsKeyword():
		return RoleKeyword
	case r.tok == token.IDENT:
		if predeclared[r.lit] {
			return RoleType
		}
		if builtinFuncs[r.lit] && callFollows(raws, i) {
			return RoleBuiltin
		}
		return RoleIdent
	case r.tok.IsOperator():
		return RolePunct
	}
	return RoleNone // ILLEGAL and anything new: already wrong, do not decorate it
}

// callFollows is the one token of lookahead the builtin rule needs. `len` is a
// builtin in `len(x)` and an ordinary name in `var len int` — the difference is
// the next token and nothing else.
//
// Comments are skipped, because `len /*n*/ (x)` is a call. A semicolon is not,
// real or inserted: `len` at the end of a line is not calling whatever the next
// line opens with.
func callFollows(raws []raw, i int) bool {
	for j := i + 1; j < len(raws); j++ {
		if raws[j].tok == token.COMMENT {
			continue
		}
		return raws[j].tok == token.LPAREN
	}
	return false
}

// predeclared are the names the universe block binds.
//
// Highlighting is lexical by construction: a session that shadows `string` with
// a variable still gets it painted as a type. Asking go/types instead would
// make the highlighter depend on a session — `:doc -src strings.Builder` is not
// in one — and invariant 5 says the type checker is never an authority. A
// cosmetic lie a reader can see straight through is the cheaper of the two.
var predeclared = map[string]bool{
	// types
	"bool": true, "byte": true, "complex64": true, "complex128": true,
	"error": true, "float32": true, "float64": true, "int": true,
	"int8": true, "int16": true, "int32": true, "int64": true,
	"rune": true, "string": true, "uint": true, "uint8": true,
	"uint16": true, "uint32": true, "uint64": true, "uintptr": true,
	"any": true, "comparable": true,
	// Constants. They are here rather than in a role of their own because
	// gluon's palette has no `constant`, and a predeclared name reads as one
	// class to someone scanning a screen. Moving these four to RoleKeyword
	// later is a second map and one line in goRole.
	"true": true, "false": true, "iota": true, "nil": true,
}

// builtinFuncs are the predeclared functions, painted only where they are
// called. Go 1.21 added clear, min and max.
var builtinFuncs = map[string]bool{
	"append": true, "cap": true, "clear": true, "close": true, "complex": true,
	"copy": true, "delete": true, "imag": true, "len": true, "make": true,
	"max": true, "min": true, "new": true, "panic": true, "print": true,
	"println": true, "real": true, "recover": true,
}
