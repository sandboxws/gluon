// A trimmed stand-in for $GOROOT/src/go/types, holding one call of each shape
// the real files use. It is not compiled — Gates parses it — so it needs only
// to parse, not to make sense.

package types

func (check *Checker) example() {
	check.verifyVersionf(pos, go1_27, "generic method")
	check.verifyVersionf(pos, go1_27, "use of promoted field %s in struct literal of type %s", a, b)
	check.versionErrorf(lit, go1_13, "binary literal")
	check.allowVersion(go1_14)
	_ = check.verifyVersionf(pos, go1_27, "generic method")
}

// The shape that a selector-only match misses: allowVersion arrives as a
// function parameter, which is how range-over-func is gated.
func rangeKeyVal(allowVersion func(goVersion) bool) {
	if !allowVersion(go1_23) {
		return
	}
	if !allowVersion(go1_22) {
		return
	}
}

// Not a gate: no version argument.
func other(check *Checker) { check.errorf(pos, "not a gate") }
