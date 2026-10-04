package theme

import (
	"math"
	"slices"
	"strconv"
	"strings"
)

// Relative luminance, and the two questions gluon asks with it.
//
// gluon paints foregrounds and never a background, so nothing here decides what
// a colour looks like — the terminal does. What luminance answers is narrower
// and entirely about honesty: whether a theme file's own claim about the ground
// it was drawn for is borne out by the colours in it, and whether a colour an
// editor theme offered for a role is one that would disappear into that ground.

// Luminance is the WCAG relative luminance of a #rrggbb colour, 0 for black and
// 1 for white. The second result is false for anything that is not a hex
// triple — an ANSI slot, None — because those resolve against a palette gluon
// does not have and cannot be reasoned about here.
func Luminance(v string) (float64, bool) {
	hex := normaliseHex(v)
	if hex == "" {
		return 0, false
	}
	channel := func(i int) float64 {
		n, err := strconv.ParseUint(hex[1+i*2:3+i*2], 16, 8)
		if err != nil {
			return 0
		}
		c := float64(n) / 255
		if c <= 0.04045 {
			return c / 12.92
		}
		return math.Pow((c+0.055)/1.055, 2.4)
	}
	return 0.2126*channel(0) + 0.7152*channel(1) + 0.0722*channel(2), true
}

// groundOf is the appearance a background colour implies.
//
// The midpoint is 0.18 rather than 0.5 because luminance is perceptual and
// backgrounds are not evenly spread across it: an editor background is either
// near-black or near-white, and everything real sits far from this line.
// Solarized Dark's #002B36 is 0.02 and Solarized Light's #FDF6E3 is 0.93.
func groundOf(background string) string {
	l, ok := Luminance(background)
	if !ok {
		return ""
	}
	if l > 0.18 {
		return Light
	}
	return Dark
}

// appearanceOf reads a source theme's own word for its ground. VS Code writes
// `"type": "dark"` and its high-contrast variants; an IntelliJ scheme writes
// `parent_scheme="Darcula"`, whose counterpart is the confusingly-named
// "Default". Anything else is not an answer, and the caller falls back to the
// background.
func appearanceOf(s string) string {
	switch t := strings.ToLower(strings.TrimSpace(s)); {
	case t == "":
		return ""
	case strings.Contains(t, "light"):
		return Light
	case strings.Contains(t, "dark"), strings.Contains(t, "black"), t == "darcula":
		return Dark
	case t == "default":
		return Light
	}
	return ""
}

// sameColour reports whether two colours are the same, or so close that a
// terminal would not tell them apart. The tolerance is per channel and small:
// this is here to catch a role that resolved to the theme's own background,
// which is usually literally equal and occasionally a shade off.
func sameColour(a, b string) bool {
	x, y := normaliseHex(a), normaliseHex(b)
	if x == "" || y == "" {
		return false
	}
	if x == y {
		return true
	}
	for i := 0; i < 3; i++ {
		p, err1 := strconv.ParseInt(x[1+i*2:3+i*2], 16, 32)
		q, err2 := strconv.ParseInt(y[1+i*2:3+i*2], 16, 32)
		if err1 != nil || err2 != nil {
			return false
		}
		if p-q > 8 || q-p > 8 {
			return false
		}
	}
	return true
}

// roleBounds is how faint a role is allowed to be on the ground its theme
// declared. Grouped by what a role is *for*: `border` is drawn to be looked
// past and may be faint, a comment recedes, and everything that carries meaning
// has to read.
//
// These are not a contrast standard. gluon does not know the real background —
// the terminal owns it — so this is a floor under "can this be seen at all",
// loose enough that a theme's own judgement is never second-guessed and tight
// enough to catch a role that resolved to nothing.
var roleBounds = []struct {
	roles []string
	// light is the most luminous a colour may be against white, dark the least
	// against black.
	light, dark float64
}{
	{[]string{"keyword", "string", "type", "number", "builtin", "ident",
		"punctuation", "error", "prompt", "note", "search"}, 0.50, 0.12},
	{[]string{"comment", "annotation", "dim"}, 0.65, 0.05},
	{[]string{"border"}, 0.85, 0.02},
}

// Readable reports whether a colour can be seen at all in a role, on the ground
// a theme says it was drawn for.
//
// True for anything it cannot judge: an ANSI slot, None, an appearance of
// Either or none at all. Those resolve against a palette this package does not
// have, or on a ground nobody declared, and a guess would be worse than no
// answer.
//
// It is used twice, which is the point of it being here rather than in a test.
// `gluon theme import` drops a role that fails and keeps gluon's own, so a
// converted theme cannot ship a role that vanishes; the built-in test applies
// it to the files themselves, which are hand-finished after conversion and
// could otherwise reintroduce exactly what the importer refused.
func Readable(role, appearance, colour string) bool {
	if appearance != Dark && appearance != Light {
		return true
	}
	l, ok := Luminance(colour)
	if !ok {
		return true
	}
	for _, b := range roleBounds {
		if !slices.Contains(b.roles, role) {
			continue
		}
		if appearance == Light {
			return l <= b.light
		}
		return l >= b.dark
	}
	return true
}
